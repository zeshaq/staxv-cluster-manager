package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zeshaq/staxv-cluster-manager/internal/db"
	"github.com/zeshaq/staxv-cluster-manager/pkg/auth"
	"github.com/zeshaq/staxv-cluster-manager/pkg/cisco"
)

// NetworkDevicesHandler surfaces /api/network-devices — Cisco IOS /
// IOS-XE switches and routers. Sibling to ServersHandler (Redfish
// BMCs) with an intentionally parallel shape so the frontend can
// render both uniformly, but separate domain: SSH instead of HTTPS,
// VLAN/interface world instead of power/boot.
//
// Phase 1 scope (this file): CRUD + reachability test + GET /health.
// VLAN + interface IP management land in Phase 2/3.
//
// Admin-only. Device creds grant full control of fabric — no
// sub-admin visibility until a device-ACL model exists.
type NetworkDevicesHandler struct {
	store *db.NetworkDeviceStore
}

func NewNetworkDevicesHandler(store *db.NetworkDeviceStore) *NetworkDevicesHandler {
	return &NetworkDevicesHandler{store: store}
}

func (h *NetworkDevicesHandler) Mount(r chi.Router, authMW func(http.Handler) http.Handler) {
	r.Route("/api/network-devices", func(r chi.Router) {
		r.Use(authMW)
		r.Use(auth.RequireAdmin)

		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Delete("/{id}", h.Delete)
		r.Post("/{id}/test", h.Test)
		r.Get("/{id}/health", h.Health)
		r.Post("/{id}/role", h.SetRole)
	})
}

type enrollNetworkDeviceRequest struct {
	Name           string `json:"name"`
	MgmtHost       string `json:"mgmt_host"`
	MgmtPort       int    `json:"mgmt_port,omitempty"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	EnablePassword string `json:"enable_password,omitempty"`
	Platform       string `json:"platform,omitempty"` // "ios" default; "ios-xe" / "nxos" override
	// Role override — optional. Empty string = leave as 'unknown' and
	// let the post-probe autodetect classify it from the model string.
	Role string `json:"role,omitempty"`
}

func (h *NetworkDevicesHandler) List(w http.ResponseWriter, r *http.Request) {
	devs, err := h.store.ListNetworkDevices(r.Context())
	if err != nil {
		slog.Error("network-devices list", "err", err)
		writeError(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// Create enrolls a device. We probe immediately so the admin sees
// reachability on the enroll response, same UX as servers.
//
// Unreachable rows are still persisted — admins often enroll before
// mgmt-network connectivity is fully there, and the /test endpoint
// re-probes on demand.
func (h *NetworkDevicesHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req enrollNetworkDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.MgmtHost = strings.TrimSpace(req.MgmtHost)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" || req.MgmtHost == "" || req.Username == "" || req.Password == "" {
		writeError(w, "name, mgmt_host, username, password are required", http.StatusBadRequest)
		return
	}
	// Role override is optional; validate if supplied. Empty = leave
	// as 'unknown' so the post-probe autodetect classifies it below.
	if req.Role != "" && !cisco.ValidRoles[req.Role] {
		writeError(w, "invalid role: "+req.Role+" (valid: router, switch, l3-switch, firewall, other, unknown)", http.StatusBadRequest)
		return
	}

	dev, err := h.store.CreateNetworkDevice(r.Context(), db.CreateNetworkDeviceArgs{
		Name:           req.Name,
		MgmtHost:       req.MgmtHost,
		MgmtPort:       req.MgmtPort,
		Username:       req.Username,
		Password:       req.Password,
		EnablePassword: req.EnablePassword,
		Platform:       req.Platform,
		Role:           req.Role,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, "a network device with that name already exists", http.StatusConflict)
			return
		}
		slog.Error("network-devices create", "err", err)
		writeError(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	probe := h.probe(r.Context(), req.MgmtHost, req.MgmtPort, req.Username, req.Password, req.EnablePassword)
	if err := h.store.UpdateReachability(r.Context(), dev.ID, probe); err != nil {
		slog.Warn("network-devices create: update reachability", "err", err, "id", dev.ID)
	}
	// Autodetect role from the probed model — only if admin didn't
	// pre-pick one and the probe actually succeeded with a model. Uses
	// SetRoleIfUnknown so manual overrides via POST /role are sticky.
	if probe.OK && probe.Model != "" {
		if auto := cisco.DetectRole(probe.Model); auto != cisco.RoleUnknown {
			if err := h.store.SetRoleIfUnknown(r.Context(), dev.ID, auto); err != nil {
				slog.Warn("network-devices create: autodetect role", "err", err, "id", dev.ID)
			}
		}
	}
	fresh, err := h.store.GetNetworkDevice(r.Context(), dev.ID)
	if err != nil {
		slog.Warn("network-devices create: refetch", "err", err, "id", dev.ID)
		writeJSON(w, http.StatusCreated, dev)
		return
	}
	slog.Info("network device enrolled", "id", fresh.ID, "name", fresh.Name, "status", fresh.Status)
	writeJSON(w, http.StatusCreated, fresh)
}

func (h *NetworkDevicesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	dev, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, dev)
}

func (h *NetworkDevicesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	if err := h.store.DeleteNetworkDevice(r.Context(), id); err != nil {
		slog.Error("network-devices delete", "err", err, "id", id)
		writeError(w, "delete failed", http.StatusInternalServerError)
		return
	}
	slog.Info("network device deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// Test re-probes a device by running `show version` again; same
// pattern as /servers/{id}/test.
func (h *NetworkDevicesHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	dev, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	username, password, enable, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("network-devices test: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return
	}
	probe := h.probe(r.Context(), dev.MgmtHost, dev.MgmtPort, username, password, enable)
	if err := h.store.UpdateReachability(r.Context(), id, probe); err != nil {
		slog.Warn("network-devices test: update reachability", "err", err, "id", id)
	}
	fresh, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		writeError(w, "refetch failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, fresh)
}

// Health runs CPU + memory + env + interfaces in one SSH session.
// Always fresh from the device; no caching — health is "what's true
// right now," not "what it was last probe."
func (h *NetworkDevicesHandler) Health(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	dev, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	username, password, enable, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("network-devices health: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cli, err := cisco.Dial(ctx, dev.MgmtHost, dev.MgmtPort, username, password, enable)
	if err != nil {
		h.writeCiscoErr(w, err, "connect")
		return
	}
	defer cli.Close()

	health, err := cli.GetHealth(ctx)
	if err != nil {
		h.writeCiscoErr(w, err, "health")
		return
	}
	slog.Info("network device health fetched",
		"name", dev.Name,
		"cpu_5s", health.CPU5s,
		"mem_used", health.MemoryUsedBytes,
		"env_sensors", len(health.Env),
		"interfaces", len(health.Interfaces),
	)
	writeJSON(w, http.StatusOK, health)
}

// SetRole is the admin-override path for the operational role
// (router / switch / l3-switch / firewall / other / unknown).
// Once set manually, the enrollment-time autodetect won't overwrite
// it — SetRoleIfUnknown (which runs at enroll) only fires when the
// row is still 'unknown'.
//
// Body: {"role": "<role>"}. Reject unknown values with 400 so the
// UI surfaces a useful error rather than silently accepting garbage.
func (h *NetworkDevicesHandler) SetRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"role\":\"...\"})", http.StatusBadRequest)
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	if !cisco.ValidRoles[req.Role] {
		writeError(w, "invalid role: "+req.Role+" (valid: router, switch, l3-switch, firewall, other, unknown)", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateRole(r.Context(), id, req.Role); err != nil {
		slog.Error("network-devices set role", "err", err, "id", id)
		writeError(w, "update failed", http.StatusInternalServerError)
		return
	}
	fresh, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "refetch failed", http.StatusInternalServerError)
		return
	}
	slog.Info("network device role updated", "id", id, "name", fresh.Name, "role", req.Role)
	writeJSON(w, http.StatusOK, fresh)
}

// probe opens a short-lived SSH session, runs `show version`, and
// classifies the result into a NetworkDeviceProbeResult the store
// can persist. Status ∈ {"unreachable" (TCP/dial), "error" (auth/parse)}.
func (h *NetworkDevicesHandler) probe(
	ctx context.Context, host string, port int, username, password, enable string,
) *db.NetworkDeviceProbeResult {
	// Probe is a slow network op — give it its own deadline so a
	// dead device doesn't tie up the enroll request context.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cli, err := cisco.Dial(ctx, host, port, username, password, enable)
	if err != nil {
		r := &db.NetworkDeviceProbeResult{OK: false, Err: truncateErr(err, 256)}
		var de *cisco.DialError
		if errors.As(err, &de) {
			r.Status = "unreachable"
		} else {
			r.Status = "error"
		}
		return r
	}
	defer cli.Close()

	info, err := cli.Probe(ctx)
	if err != nil {
		return &db.NetworkDeviceProbeResult{
			OK:     false,
			Status: "error",
			Err:    truncateErr(err, 256),
		}
	}
	return &db.NetworkDeviceProbeResult{
		OK:       true,
		Platform: info.Platform,
		Model:    info.Model,
		Version:  info.Version,
		Serial:   info.Serial,
		Hostname: info.Hostname,
		UptimeS:  info.UptimeS,
	}
}

// writeCiscoErr classifies pkg/cisco errors to HTTP statuses:
//
//	*cisco.DialError → 503 (box unreachable / mgmt-LAN issue)
//	*cisco.AuthError → 401 (creds wrong)
//	anything else    → 502 (reached the box but something else went wrong)
func (h *NetworkDevicesHandler) writeCiscoErr(w http.ResponseWriter, err error, ctx string) {
	status := http.StatusBadGateway
	switch {
	case errors.As(err, new(*cisco.DialError)):
		status = http.StatusServiceUnavailable
	case errors.As(err, new(*cisco.AuthError)):
		status = http.StatusUnauthorized
	}
	writeError(w, ctx+": "+err.Error(), status)
}
