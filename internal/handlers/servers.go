package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

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
	// isoStore lets mount/boot-from-iso look up the ISO row so we can
	// enforce status=ready and construct the BMC-facing serve URL using
	// the stored filename. Kept optional (nil = ISO features disabled)
	// for future deployments that might run servers-only.
	isoStore *db.ISOStore
}

func NewServersHandler(store *db.ServerStore, isoStore *db.ISOStore) *ServersHandler {
	return &ServersHandler{store: store, isoStore: isoStore}
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
		r.Post("/{id}/power", h.Power)
		r.Get("/{id}/hardware", h.Hardware)
		r.Get("/{id}/health", h.Health)
		r.Get("/{id}/virtual-media", h.VirtualMedia)
		r.Post("/{id}/mount-iso", h.MountISO)
		r.Post("/{id}/eject-iso", h.EjectISO)
		r.Post("/{id}/boot-from-iso", h.BootFromISO)
	})
}

// powerActionMap translates UI-level action names to Redfish ResetType
// values. Kept here (handler) rather than in pkg/redfish so the client
// stays a thin Redfish wrapper and policy (which actions the UI knows
// about) lives with the API surface.
var powerActionMap = map[string]string{
	"on":            redfish.ResetOn,
	"shutdown":      redfish.ResetGracefulShutdown,
	"reboot":        redfish.ResetGracefulRestart,
	"force_off":     redfish.ResetForceOff,
	"force_reboot":  redfish.ResetForceRestart,
	"power_cycle":   redfish.ResetPowerCycle,
	"nmi":           redfish.ResetNmi,
	"push_button":   redfish.ResetPushPowerButton,
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

// Power issues a Redfish ComputerSystem.Reset via the BMC. The request
// body is {"action": "<name>"} where name is one of the keys in
// powerActionMap. On success, we re-probe so the returned Server row
// carries the fresh power_state — users hit Power On and expect to
// see "PoweringOn" or "On" within the same round-trip.
//
// Synchronous-and-slow is deliberate. A 202 + background model would
// be nicer for UX but adds complexity (task table, polling); keep the
// contract simple until we have real users complaining.
func (h *ServersHandler) Power(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"action\":\"...\"})", http.StatusBadRequest)
		return
	}
	resetType, valid := powerActionMap[req.Action]
	if !valid {
		valids := make([]string, 0, len(powerActionMap))
		for k := range powerActionMap {
			valids = append(valids, k)
		}
		writeError(w, "unknown action: "+req.Action+" (valid: "+strings.Join(valids, ", ")+")", http.StatusBadRequest)
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
		slog.Error("power: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return
	}

	cli := redfish.New(srv.BMCHost, srv.BMCPort, username, password)
	if err := cli.PowerAction(r.Context(), resetType); err != nil {
		slog.Warn("power action failed", "id", id, "action", req.Action, "err", err)
		// BMC errors surface as 400 — most are "already in that state"
		// or "operation not permitted", which are user-fixable.
		status := http.StatusBadRequest
		if errors.As(err, new(*redfish.NetError)) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, "power "+req.Action+" failed: "+err.Error(), status)
		return
	}

	// Re-probe so the response carries the fresh power_state. BMCs
	// report transitional states ("PoweringOn") which is the HONEST
	// answer right after a Reset — the UI shows that rather than
	// pretending the action completed instantly.
	probe := h.probe(r.Context(), id, srv.BMCHost, srv.BMCPort, username, password)
	if err := h.store.UpdateReachability(r.Context(), id, probe); err != nil {
		slog.Warn("power: post-action update reachability", "err", err, "id", id)
	}
	fresh, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		// Degraded success — action went through, refetch failed.
		// Return the pre-action row rather than an error; the client
		// can call /test manually.
		slog.Warn("power: refetch after action", "err", err, "id", id)
		writeJSON(w, http.StatusOK, srv)
		return
	}

	slog.Info("power action",
		"id", id, "name", srv.Name, "action", req.Action, "reset_type", resetType,
		"power_state_after", fresh.PowerState,
	)
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
			PowerState:   info.PowerState,
			Health:       info.Health,
			BIOSVersion:  info.BIOSVersion,
			Hostname:     info.Hostname,
			CPUCount:     info.CPUCount,
			MemoryGB:     info.MemoryGB,
		}
	}
	r := &db.ProbeResult{OK: false, Err: truncateErr(err, 256)}
	// errors.As walks the %w wrap chain (the redfish package surfaces
	// typed errors inside fmt.Errorf("service root: %w", err)).
	var netErr *redfish.NetError
	if errors.As(err, &netErr) {
		r.Status = "unreachable"
	} else {
		// AuthError, HTTPError, JSON decode errors — reached the box
		// but something's logically wrong.
		r.Status = "error"
	}
	// A partial success (info populated but err set) falls through as
	// OK=false Status="error" — conservative default.
	return r
}

// Hardware returns the full hardware-inventory drill-down for one
// server — CPUs, DIMMs, drives, NICs. Each collection is fetched in
// parallel; per-collection errors are surfaced inside the JSON so a
// BMC that exposes some collections but not others still yields a
// useful response instead of an all-or-nothing 500.
//
// First-time cost on a populous box (2 sockets, 24 DIMMs, 6 drives,
// 4 NICs) is ~2s against a warm BMC; 5-10s on cold iLO. UI lazy-loads
// on expand so initial paint stays fast.
func (h *ServersHandler) Hardware(w http.ResponseWriter, r *http.Request) {
	cli, srvName, ok := h.clientForServer(w, r)
	if !ok {
		return
	}

	// Four independent collections. Run them concurrently — each one
	// makes its own firstSystemURL call, so there's 4× the service-root
	// GET traffic, but those are cheap and the BMC TCP pool reuses
	// connections. Saves ~4-8s of wall time vs. serial.
	var (
		hw redfish.Hardware
		wg sync.WaitGroup
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		procs, err := cli.Processors(r.Context())
		hw.Processors = procs
		if err != nil {
			hw.ProcessorsErr = err.Error()
		}
	}()
	go func() {
		defer wg.Done()
		mem, err := cli.Memory(r.Context())
		hw.Memory = mem
		if err != nil {
			hw.MemoryErr = err.Error()
		}
	}()
	go func() {
		defer wg.Done()
		drives, err := cli.Drives(r.Context())
		hw.Drives = drives
		if err != nil {
			hw.DrivesErr = err.Error()
		}
	}()
	go func() {
		defer wg.Done()
		nics, err := cli.NetworkInterfaces(r.Context())
		hw.NICs = nics
		if err != nil {
			hw.NICsErr = err.Error()
		}
	}()
	wg.Wait()

	slog.Info("server hardware fetched",
		"name", srvName,
		"cpus", len(hw.Processors),
		"dimms", len(hw.Memory),
		"drives", len(hw.Drives),
		"nics", len(hw.NICs),
	)
	writeJSON(w, http.StatusOK, hw)
}

// Health returns thermal + power sensor readings. Two independent GETs
// (chassis-level /Thermal and /Power), run in parallel. Same per-block
// error convention as Hardware.
func (h *ServersHandler) Health(w http.ResponseWriter, r *http.Request) {
	cli, srvName, ok := h.clientForServer(w, r)
	if !ok {
		return
	}

	var (
		out redfish.Health
		wg  sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		fans, temps, err := cli.Thermal(r.Context())
		out.Fans = fans
		out.Temperatures = temps
		if err != nil {
			out.ThermalErr = err.Error()
		}
	}()
	go func() {
		defer wg.Done()
		psus, consumption, err := cli.Power(r.Context())
		out.PSUs = psus
		out.Power = consumption
		if err != nil {
			out.PowerErr = err.Error()
		}
	}()
	wg.Wait()

	slog.Info("server health fetched",
		"name", srvName,
		"fans", len(out.Fans),
		"temps", len(out.Temperatures),
		"psus", len(out.PSUs),
		"watts", out.Power.ConsumedWatts,
	)
	writeJSON(w, http.StatusOK, out)
}

// clientForServer is the shared "fetch row + creds → redfish.Client"
// helper used by Hardware/Health. Writes the HTTP response on failure
// and returns ok=false so the caller returns immediately.
func (h *ServersHandler) clientForServer(w http.ResponseWriter, r *http.Request) (cli *redfish.Client, name string, ok bool) {
	id, okID := parseInt64Param(w, r, "id")
	if !okID {
		return nil, "", false
	}
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return nil, "", false
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return nil, "", false
	}
	username, password, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("inventory: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return nil, "", false
	}
	return redfish.New(srv.BMCHost, srv.BMCPort, username, password), srv.Name, true
}

// VirtualMedia lists the BMC's virtual-media slots and their current
// state (inserted image, connected_via, media types). Used by the UI
// to render the slot picker and by the admin to confirm what's mounted.
func (h *ServersHandler) VirtualMedia(w http.ResponseWriter, r *http.Request) {
	cli, _, ok := h.clientForServer(w, r)
	if !ok {
		return
	}
	slots, err := cli.VirtualMedia(r.Context())
	if err != nil {
		slog.Warn("virtual media list failed", "err", err)
		h.writeRedfishErr(w, err, "list virtual media")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

// MountISO attaches an ISO from our library to a BMC virtual-media slot.
// Body: {"iso_id": 42, "slot": "2"} (slot optional → auto-pick CD/DVD).
//
// The BMC fetches the ISO from our /iso/{id}/{filename} serve route
// (no auth — document-mandated; see isos.md). The URL uses the same
// scheme/host the admin's browser hit, under the assumption the admin
// and BMC share a reachable path to the CM. Cross-network deployments
// will need an [isos] serve_base_url override, deferred to follow-up.
func (h *ServersHandler) MountISO(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		ISOID int64  `json:"iso_id"`
		Slot  string `json:"slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"iso_id\":N,\"slot\":\"?\"})", http.StatusBadRequest)
		return
	}
	if req.ISOID == 0 {
		writeError(w, "iso_id is required", http.StatusBadRequest)
		return
	}

	iso, imageURL, err := h.resolveISO(r, req.ISOID)
	if err != nil {
		h.writeISOLookupErr(w, err)
		return
	}

	cli, srvName, ok := h.clientForServerID(w, r, id)
	if !ok {
		return
	}
	if err := cli.InsertMedia(r.Context(), req.Slot, imageURL); err != nil {
		slog.Warn("insert media failed", "server", srvName, "iso", iso.Filename, "err", err)
		h.writeRedfishErr(w, err, "mount ISO")
		return
	}

	slog.Info("iso mounted",
		"server_id", id, "server", srvName,
		"iso_id", iso.ID, "iso", iso.Filename,
		"slot", req.Slot, "url", imageURL,
	)
	// Re-list so the response carries the fresh slot state (Inserted=true,
	// Image=<our url>). Cheap — same round trip the UI would do anyway.
	slots, _ := cli.VirtualMedia(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

// EjectISO dismounts whatever's on the named slot. Body: {"slot": "2"}.
// Slot is required — unlike mount we don't auto-pick, since "eject the
// first slot with something in it" is ambiguous when multiple are
// mounted.
func (h *ServersHandler) EjectISO(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slot string `json:"slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"slot\":\"...\"})", http.StatusBadRequest)
		return
	}
	if req.Slot == "" {
		writeError(w, "slot is required", http.StatusBadRequest)
		return
	}
	cli, srvName, ok := h.clientForServer(w, r)
	if !ok {
		return
	}
	if err := cli.EjectMedia(r.Context(), req.Slot); err != nil {
		slog.Warn("eject media failed", "server", srvName, "slot", req.Slot, "err", err)
		h.writeRedfishErr(w, err, "eject ISO")
		return
	}
	slog.Info("iso ejected", "server", srvName, "slot", req.Slot)
	slots, _ := cli.VirtualMedia(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

// BootFromISO is the full "install this OS" convenience: mount + one-
// shot boot override + reset. The three BMC calls run serially — each
// depends on the prior. No rollback on failure; if Insert succeeded but
// Boot override failed, the admin sees "mounted, boot override failed"
// in the error and can retry / eject manually.
//
// Body: {"iso_id": 42, "slot": "2"} (slot optional).
//
// Power handling: if the system is Off we send "On" (boot from the
// newly-inserted virtual CD); otherwise "ForceRestart" (reset is the
// reliable path — a halted OS ignores GracefulRestart).
func (h *ServersHandler) BootFromISO(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		ISOID int64  `json:"iso_id"`
		Slot  string `json:"slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"iso_id\":N,\"slot\":\"?\"})", http.StatusBadRequest)
		return
	}
	if req.ISOID == 0 {
		writeError(w, "iso_id is required", http.StatusBadRequest)
		return
	}

	iso, imageURL, err := h.resolveISO(r, req.ISOID)
	if err != nil {
		h.writeISOLookupErr(w, err)
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
		slog.Error("boot-from-iso: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return
	}
	cli := redfish.New(srv.BMCHost, srv.BMCPort, username, password)

	// Step 1: mount.
	if err := cli.InsertMedia(r.Context(), req.Slot, imageURL); err != nil {
		slog.Warn("boot-from-iso: insert failed", "server", srv.Name, "err", err)
		h.writeRedfishErr(w, err, "mount ISO")
		return
	}
	// Step 2: boot override. One-shot, reverts after next boot.
	if err := cli.SetBootOverride(r.Context(), "Cd", "Once"); err != nil {
		slog.Warn("boot-from-iso: boot override failed", "server", srv.Name, "err", err)
		// Leave the media mounted — admin might want to retry boot
		// override separately rather than re-mount.
		h.writeRedfishErr(w, err, "set boot override (ISO mounted — eject manually if aborting)")
		return
	}
	// Step 3: reset. Pick ResetType based on current power state.
	resetType := redfish.ResetForceRestart
	if srv.PowerState == "Off" {
		resetType = redfish.ResetOn
	}
	if err := cli.PowerAction(r.Context(), resetType); err != nil {
		slog.Warn("boot-from-iso: reset failed", "server", srv.Name, "reset_type", resetType, "err", err)
		h.writeRedfishErr(w, err, "restart (ISO mounted + boot override set — power on manually)")
		return
	}

	slog.Info("boot-from-iso triggered",
		"server_id", id, "server", srv.Name,
		"iso_id", iso.ID, "iso", iso.Filename,
		"slot", req.Slot, "reset_type", resetType, "url", imageURL,
	)

	// Refresh power_state — same pattern as Power handler.
	probe := h.probe(r.Context(), id, srv.BMCHost, srv.BMCPort, username, password)
	if err := h.store.UpdateReachability(r.Context(), id, probe); err != nil {
		slog.Warn("boot-from-iso: update reachability", "err", err, "id", id)
	}
	fresh, _ := h.store.GetServer(r.Context(), id)
	if fresh == nil {
		fresh = srv
	}
	writeJSON(w, http.StatusOK, fresh)
}

// resolveISO loads the ISO row, validates status=ready, and builds the
// absolute BMC-facing serve URL from the request's Host + scheme. The
// URL points at /iso/{id}/{filename} — the public (no-auth) route BMCs
// fetch from.
//
// Scheme: honors X-Forwarded-Proto when present (reverse-proxy-safe),
// falls back to https when the request itself was TLS, else http.
// Host: incoming request's Host header. Same assumption as the "Copy
// URL" button on the ISOs page — admin's browser and the BMC share a
// path to the CM. Cross-network deployments will need a config knob.
func (h *ServersHandler) resolveISO(r *http.Request, isoID int64) (*db.ISO, string, error) {
	if h.isoStore == nil {
		return nil, "", errors.New("ISO store not wired")
	}
	iso, err := h.isoStore.GetISO(r.Context(), isoID)
	if err != nil {
		return nil, "", err
	}
	if iso.Status != "ready" {
		return nil, "", fmt.Errorf("iso %d is %s (must be ready)", iso.ID, iso.Status)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	url := fmt.Sprintf("%s://%s/iso/%d/%s", scheme, host, iso.ID, iso.Filename)
	return iso, url, nil
}

// writeISOLookupErr maps resolveISO's failure modes to HTTP statuses.
func (h *ServersHandler) writeISOLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, "iso not found", http.StatusNotFound)
		return
	}
	// Status-not-ready and "store not wired" surface as 409 and 500
	// respectively; the string prefix differentiates.
	writeError(w, err.Error(), http.StatusConflict)
}

// writeRedfishErr maps BMC errors to HTTP statuses — network failure
// → 503 (try again, fix the path), everything else (auth, 4xx, 5xx
// from the BMC) → 400 so the admin sees the actual message.
func (h *ServersHandler) writeRedfishErr(w http.ResponseWriter, err error, ctx string) {
	status := http.StatusBadRequest
	if errors.As(err, new(*redfish.NetError)) {
		status = http.StatusServiceUnavailable
	}
	writeError(w, ctx+": "+err.Error(), status)
}

// clientForServerID is clientForServer's sibling when the id was
// already parsed by the caller — avoids re-parsing the URL param.
func (h *ServersHandler) clientForServerID(w http.ResponseWriter, r *http.Request, id int64) (*redfish.Client, string, bool) {
	srv, err := h.store.GetServer(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return nil, "", false
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return nil, "", false
	}
	username, password, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("clientForServerID: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return nil, "", false
	}
	return redfish.New(srv.BMCHost, srv.BMCPort, username, password), srv.Name, true
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
