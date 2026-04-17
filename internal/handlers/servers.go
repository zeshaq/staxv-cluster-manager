package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zeshaq/staxv-cluster-manager/internal/db"
	"github.com/zeshaq/staxv-cluster-manager/pkg/auth"
	"github.com/zeshaq/staxv-cluster-manager/pkg/redfish"
)

// ServersHandler surfaces physical-server inventory management —
// Redfish (iLO / iDRAC) enrollment and reachability testing.
//
// All routes are admin-only. BMC credentials let the holder power off,
// boot custom media, and attach serial console — effectively root on
// the physical box. No "regular user can see some servers" model for
// v1; add a per-server ACL table if that ever becomes a need.
type ServersHandler struct {
	store *db.ServerStore
}

func NewServersHandler(store *db.ServerStore) *ServersHandler {
	return &ServersHandler{store: store}
}

// Mount attaches /api/servers routes under authMW + RequireAdmin.
func (h *ServersHandler) Mount(r chi.Router, authMW func(http.Handler) http.Handler) {
	r.Route("/api/servers", func(r chi.Router) {
		r.Use(authMW)
		r.Use(auth.RequireAdmin)

		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Delete("/{id}", h.Delete)
		r.Post("/{id}/test", h.Test)
	})
}

type enrollRequest struct {
	Name        string `json:"name"`
	BMCHost     string `json:"bmc_host"`
	BMCPort     int    `json:"bmc_port,omitempty"` // omit → 443
	BMCUsername string `json:"bmc_username"`
	BMCPassword string `json:"bmc_password"`
}

// List returns every enrolled server.
func (h *ServersHandler) List(w http.ResponseWriter, r *http.Request) {
	servers, err := h.store.ListServers(r.Context())
	if err != nil {
		slog.Error("servers list", "err", err)
		writeError(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

// Create enrolls a new server. Immediately probes the BMC with the
// given creds so the admin sees "reachable" / "unreachable" right
// after submitting the form — valuable UX for a slow task (BMCs can
// take 5–10s to respond on first contact).
//
// If the probe fails we STILL persist the row — admins often enroll
// ahead of BMC config completion and rerun /test later. The row just
// carries status='unreachable' or 'error' with the message.
func (h *ServersHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req enrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.BMCHost = strings.TrimSpace(req.BMCHost)
	req.BMCUsername = strings.TrimSpace(req.BMCUsername)
	if req.Name == "" || req.BMCHost == "" || req.BMCUsername == "" || req.BMCPassword == "" {
		writeError(w, "name, bmc_host, bmc_username, bmc_password are required", http.StatusBadRequest)
		return
	}

	srv, err := h.store.CreateServer(r.Context(), db.CreateServerArgs{
		Name:        req.Name,
		BMCHost:     req.BMCHost,
		BMCPort:     req.BMCPort,
		BMCUsername: req.BMCUsername,
		BMCPassword: req.BMCPassword,
	})
	if err != nil {
		// Unique-name violation is the common case. Distinguish it for
		// a kinder error message.
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, "a server with that name already exists", http.StatusConflict)
			return
		}
		slog.Error("servers create", "err", err)
		writeError(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Probe BMC with the creds we just encrypted + saved. Update the
	// reachability columns regardless of outcome.
	probe := h.probe(r.Context(), srv.ID, req.BMCHost, req.BMCPort, req.BMCUsername, req.BMCPassword)
	if err := h.store.UpdateReachability(r.Context(), srv.ID, probe); err != nil {
		slog.Warn("servers create: update reachability", "err", err, "id", srv.ID)
	}

	// Re-read so the response reflects the probe result (status, model,
	// serial, etc.). A little wasteful but trivial; simpler than
	// merging in memory.
	fresh, err := h.store.GetServer(r.Context(), srv.ID)
	if err != nil {
		slog.Warn("servers create: refetch", "err", err, "id", srv.ID)
		writeJSON(w, http.StatusCreated, srv) // fall back to pre-probe row
		return
	}
	slog.Info("server enrolled", "id", fresh.ID, "name", fresh.Name, "status", fresh.Status)
	writeJSON(w, http.StatusCreated, fresh)
}

// Get returns one server.
func (h *ServersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

// Delete removes a server. Idempotent — 204 even if already gone.
func (h *ServersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	if err := h.store.DeleteServer(r.Context(), id); err != nil {
		slog.Error("servers delete", "err", err, "id", id)
		writeError(w, "delete failed", http.StatusInternalServerError)
		return
	}
	slog.Info("server deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// Test re-probes a server's BMC and updates the reachability columns.
// Useful when the admin fixed a network issue or rotated BMC creds
// out-of-band (they'd need to update creds separately, but that's a
// future PATCH endpoint).
func (h *ServersHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	username, password, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("servers test: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return
	}

	probe := h.probe(r.Context(), id, srv.BMCHost, srv.BMCPort, username, password)
	if err := h.store.UpdateReachability(r.Context(), id, probe); err != nil {
		slog.Warn("servers test: update reachability", "err", err, "id", id)
	}
	fresh, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, "refetch failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, fresh)
}

// probe is the single place we dispatch Redfish calls and classify
// the result into a ProbeResult. Handler callers take the result and
// hand it to ServerStore.UpdateReachability.
func (h *ServersHandler) probe(ctx context.Context, _id int64, host string, port int, username, password string) *db.ProbeResult {
	cli := redfish.New(host, port, username, password)
	info, err := cli.Probe(ctx)
	if err == nil && info != nil {
		return &db.ProbeResult{
			OK:           true,
			Manufacturer: info.Manufacturer,
			Model:        info.Model,
			Serial:       info.Serial,
		}
	}
	r := &db.ProbeResult{OK: false, Err: truncateErr(err, 256)}
	switch err.(type) {
	case *redfish.NetError:
		r.Status = "unreachable"
	default:
		// AuthError, HTTPError, JSON decode errors, etc. — not a
		// network failure, but something logically wrong.
		r.Status = "error"
	}
	// A partial success (info populated but err set) falls through as
	// OK=false Status="error" — conservative default.
	return r
}

func truncateErr(err error, max int) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func parseInt64Param(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := chi.URLParam(r, name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, "invalid "+name, http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
