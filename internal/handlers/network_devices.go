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
		r.Get("/{id}/vlans", h.ListVLANs)
		r.Post("/{id}/vlans", h.CreateVLAN)
		r.Delete("/{id}/vlans/{vlan_id}", h.DeleteVLAN)
		r.Get("/{id}/interfaces", h.ListInterfaces)
		r.Post("/{id}/interface-ip", h.SetInterfaceIP)
		r.Get("/{id}/ospf", h.GetOSPF)
		r.Post("/{id}/ospf/processes", h.UpsertOSPFProcess)
		r.Delete("/{id}/ospf/processes/{pid}", h.DeleteOSPFProcess)
		r.Post("/{id}/ospf/interface", h.SetOSPFInterface)
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

// ─── VLAN management ─────────────────────────────────────────────
//
// Read-only list + single-VLAN create + single-VLAN delete. Bulk
// ops (range syntax, batched CSV imports) are deferred. Role-gated
// at the UI layer — handlers accept the call on any device, because
// refusing to list VLANs on a router that might legitimately have
// them would be wrong; but writes are admin-scoped via the outer
// auth middleware so accidental damage on a misconfigured role is
// the admin's own mistake, not ours to prevent.

// ListVLANs returns the current `show vlan brief` output parsed into
// [{id, name, status, ports[]}]. Empty slice when the device doesn't
// support VLANs (e.g. pure routers — we don't 404, we return []).
func (h *NetworkDevicesHandler) ListVLANs(w http.ResponseWriter, r *http.Request) {
	cli, _, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()
	vlans, err := cli.VLANs(r.Context())
	if err != nil {
		h.writeCiscoErr(w, err, "list vlans")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vlans": vlans})
}

// createVLANRequest is the enroll payload for a new VLAN.
type createVLANRequest struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CreateVLAN runs `vlan <id> / name <N> / end / wr mem`. Rejects
// invalid ID / name at the handler layer so the BMC doesn't have to
// say "% Invalid name" for us.
//
// Returns the updated list of VLANs so the UI can re-render without
// a separate round trip. Also returns before/after running-config
// snapshots — useful for admin audit and "did it actually do
// anything" confirmation.
func (h *NetworkDevicesHandler) CreateVLAN(w http.ResponseWriter, r *http.Request) {
	var req createVLANRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"id\":N,\"name\":\"...\"})", http.StatusBadRequest)
		return
	}
	if req.ID < cisco.VLANIDMin || req.ID > cisco.VLANIDMax {
		writeError(w, fmt.Sprintf("vlan id %d out of range (valid: %d-%d)", req.ID, cisco.VLANIDMin, cisco.VLANIDMax), http.StatusBadRequest)
		return
	}
	if req.ID >= cisco.VLANIDRsvdMin && req.ID <= cisco.VLANIDRsvdMax {
		writeError(w, fmt.Sprintf("vlan id %d is reserved for legacy protocols (FDDI / Token Ring); pick another", req.ID), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if !cisco.VLANNameRE.MatchString(name) {
		writeError(w, "invalid name (1-32 chars, alphanumeric + hyphen + underscore only)", http.StatusBadRequest)
		return
	}

	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	before, after, err := cli.CreateVLAN(r.Context(), req.ID, name)
	if err != nil {
		slog.Warn("vlan create failed",
			"device", devName, "vlan_id", req.ID, "name", name,
			"err", err, "config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "create vlan")
		return
	}
	slog.Info("vlan created",
		"device", devName, "vlan_id", req.ID, "name", name,
		"config_before", before, "config_after", after)

	vlans, _ := cli.VLANs(r.Context())
	writeJSON(w, http.StatusCreated, map[string]any{
		"vlans":         vlans,
		"config_before": before,
		"config_after":  after,
	})
}

// DeleteVLAN runs `no vlan <id> / end / wr mem`. Idempotent — IOS
// silently tolerates deleting a non-existent VLAN, matching our
// "delete is always 204" convention.
func (h *NetworkDevicesHandler) DeleteVLAN(w http.ResponseWriter, r *http.Request) {
	vlanIDStr := chi.URLParam(r, "vlan_id")
	vlanID, err := strconv.Atoi(vlanIDStr)
	if err != nil || vlanID < cisco.VLANIDMin || vlanID > cisco.VLANIDMax {
		writeError(w, fmt.Sprintf("invalid vlan id %q", vlanIDStr), http.StatusBadRequest)
		return
	}
	// VLAN 1 is the IOS default — deleting it is rejected by the device
	// but we double-check here for a friendlier error than a stacktrace
	// through the Cisco reply.
	if vlanID == 1 {
		writeError(w, "vlan 1 is the IOS default and cannot be deleted", http.StatusBadRequest)
		return
	}

	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	before, after, err := cli.DeleteVLAN(r.Context(), vlanID)
	if err != nil {
		slog.Warn("vlan delete failed",
			"device", devName, "vlan_id", vlanID,
			"err", err, "config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "delete vlan")
		return
	}
	slog.Info("vlan deleted",
		"device", devName, "vlan_id", vlanID,
		"config_before", before, "config_after", after)

	vlans, _ := cli.VLANs(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"vlans":         vlans,
		"config_before": before,
		"config_after":  after,
	})
}

// ─── Interface IP management ─────────────────────────────────────
//
// List + single-interface IP set/clear. The edit path carries a
// CIDR string (e.g. "10.1.1.1/24") so we get mask + host address in
// one field; backend splits into IOS's `A.B.C.D M.M.M.M` form.
//
// As with VLANs, both writes return before/after running-config
// snapshots (scoped to `section interface <name>`) for audit.

// ListInterfaces merges `show ip interface brief` with
// `show interfaces description`. Returns what the admin needs to
// edit IPs: name, description, current IP, status, protocol.
func (h *NetworkDevicesHandler) ListInterfaces(w http.ResponseWriter, r *http.Request) {
	cli, _, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()
	ifaces, err := cli.Interfaces(r.Context())
	if err != nil {
		h.writeCiscoErr(w, err, "list interfaces")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": ifaces})
}

// setInterfaceIPRequest is the payload for a single-interface IP
// edit. `ip` in CIDR form ("10.1.1.1/24"), or empty string to clear.
type setInterfaceIPRequest struct {
	Name string `json:"name"`
	IP   string `json:"ip"` // empty = clear
}

// SetInterfaceIP handles both the set and clear paths — discriminated
// by whether IP is empty. Same pattern as the VLAN writes: validate,
// dial, run config lines, return {interfaces, config_before,
// config_after}.
func (h *NetworkDevicesHandler) SetInterfaceIP(w http.ResponseWriter, r *http.Request) {
	var req setInterfaceIPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"name\":\"...\",\"ip\":\"a.b.c.d/N\"})", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.IP = strings.TrimSpace(req.IP)
	if !cisco.InterfaceNameRE.MatchString(req.Name) {
		writeError(w, "invalid interface name (letters, digits, /, ., -)", http.StatusBadRequest)
		return
	}

	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	var before, after string
	var err error
	if req.IP == "" {
		before, after, err = cli.ClearInterfaceIP(r.Context(), req.Name)
	} else {
		before, after, err = cli.SetInterfaceIP(r.Context(), req.Name, req.IP)
	}
	if err != nil {
		slog.Warn("interface IP update failed",
			"device", devName, "iface", req.Name, "ip", req.IP, "err", err,
			"config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "update interface IP")
		return
	}
	slog.Info("interface IP updated",
		"device", devName, "iface", req.Name, "ip", req.IP,
		"config_before", before, "config_after", after)

	ifaces, _ := cli.Interfaces(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces":    ifaces,
		"config_before": before,
		"config_after":  after,
	})
}

// ─── OSPF ────────────────────────────────────────────────────────
//
// Process list + create + delete + per-interface attach with
// optional network type (the "point-to-point" admins use for /30
// WAN links so neighbors form without DR/BDR election). Neighbor
// table is read-only observation.

// GetOSPF runs all three OSPF queries (show run section + show ip
// ospf interface brief + show ip ospf neighbor) and returns one
// bundled response. Per-block errors surface via the *_error fields
// so partial failure is visible without failing the whole call.
func (h *NetworkDevicesHandler) GetOSPF(w http.ResponseWriter, r *http.Request) {
	cli, _, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()
	state, err := cli.GetOSPFState(r.Context())
	if err != nil {
		h.writeCiscoErr(w, err, "ospf state")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// upsertOSPFProcessRequest is the payload for enabling or updating
// an OSPF process. Idempotent — re-running with a new router-id
// overwrites the old one.
type upsertOSPFProcessRequest struct {
	PID      int    `json:"pid"`
	RouterID string `json:"router_id"`
}

// UpsertOSPFProcess creates or updates `router ospf <pid>` with the
// given router-id. Useful for both first-time enable and for rotating
// a router-id.
func (h *NetworkDevicesHandler) UpsertOSPFProcess(w http.ResponseWriter, r *http.Request) {
	var req upsertOSPFProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body (expected {\"pid\":N,\"router_id\":\"...\"})", http.StatusBadRequest)
		return
	}
	if req.PID < 1 || req.PID > 65535 {
		writeError(w, "pid must be 1-65535", http.StatusBadRequest)
		return
	}
	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	before, after, err := cli.CreateOrUpdateOSPFProcess(r.Context(), req.PID, req.RouterID)
	if err != nil {
		slog.Warn("ospf process upsert failed",
			"device", devName, "pid", req.PID, "router_id", req.RouterID,
			"err", err, "config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "upsert ospf process")
		return
	}
	slog.Info("ospf process upserted",
		"device", devName, "pid", req.PID, "router_id", req.RouterID,
		"config_before", before, "config_after", after)

	state, _ := cli.GetOSPFState(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"state":         state,
		"config_before": before,
		"config_after":  after,
	})
}

// DeleteOSPFProcess removes `router ospf <pid>`. IOS cleans up
// interface attachments for this pid automatically on process
// removal. Idempotent — deleting a non-existent process is fine.
func (h *NetworkDevicesHandler) DeleteOSPFProcess(w http.ResponseWriter, r *http.Request) {
	pidStr := chi.URLParam(r, "pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid < 1 || pid > 65535 {
		writeError(w, fmt.Sprintf("invalid pid %q", pidStr), http.StatusBadRequest)
		return
	}
	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	before, after, err := cli.DeleteOSPFProcess(r.Context(), pid)
	if err != nil {
		slog.Warn("ospf process delete failed",
			"device", devName, "pid", pid,
			"err", err, "config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "delete ospf process")
		return
	}
	slog.Info("ospf process deleted",
		"device", devName, "pid", pid,
		"config_before", before, "config_after", after)

	state, _ := cli.GetOSPFState(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"state":         state,
		"config_before": before,
		"config_after":  after,
	})
}

// setOSPFInterfaceRequest is the payload for per-interface OSPF
// attach / detach. pid=0 clears. network_type is optional; empty
// = don't touch (unless clearing, in which case we also reset).
type setOSPFInterfaceRequest struct {
	Name        string `json:"name"`
	PID         int    `json:"pid"`   // 0 = clear
	Area        string `json:"area"`  // required when pid > 0
	NetworkType string `json:"network_type,omitempty"`
}

func (h *NetworkDevicesHandler) SetOSPFInterface(w http.ResponseWriter, r *http.Request) {
	var req setOSPFInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Area = strings.TrimSpace(req.Area)
	req.NetworkType = strings.TrimSpace(req.NetworkType)

	if !cisco.InterfaceNameRE.MatchString(req.Name) {
		writeError(w, "invalid interface name", http.StatusBadRequest)
		return
	}
	if req.PID < 0 || req.PID > 65535 {
		writeError(w, "pid must be 0 (clear) or 1-65535", http.StatusBadRequest)
		return
	}
	if req.PID > 0 && req.Area == "" {
		writeError(w, "area is required when pid > 0", http.StatusBadRequest)
		return
	}
	if !cisco.OSPFNetworkTypes[req.NetworkType] {
		writeError(w, "invalid network_type (valid: point-to-point, point-to-multipoint, broadcast, non-broadcast, or empty)", http.StatusBadRequest)
		return
	}

	cli, devName, ok := h.clientForDevice(w, r)
	if !ok {
		return
	}
	defer cli.Close()

	before, after, err := cli.SetOSPFInterface(r.Context(), req.Name, req.PID, req.Area, req.NetworkType)
	if err != nil {
		slog.Warn("ospf interface set failed",
			"device", devName, "iface", req.Name, "pid", req.PID, "area", req.Area, "network_type", req.NetworkType,
			"err", err, "config_before", before, "config_after", after)
		h.writeCiscoErr(w, err, "set ospf interface")
		return
	}
	slog.Info("ospf interface set",
		"device", devName, "iface", req.Name, "pid", req.PID, "area", req.Area, "network_type", req.NetworkType,
		"config_before", before, "config_after", after)

	state, _ := cli.GetOSPFState(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"state":         state,
		"config_before": before,
		"config_after":  after,
	})
}

// clientForDevice — the network-device equivalent of ServersHandler's
// clientForServer. Resolves id param → row → decrypted creds →
// SSH-dialed Client. Writes HTTP error on failure and returns
// ok=false so the caller returns immediately.
//
// Caller MUST defer cli.Close() on success.
func (h *NetworkDevicesHandler) clientForDevice(w http.ResponseWriter, r *http.Request) (*cisco.Client, string, bool) {
	id, okID := parseInt64Param(w, r, "id")
	if !okID {
		return nil, "", false
	}
	dev, err := h.store.GetNetworkDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return nil, "", false
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return nil, "", false
	}
	username, password, enable, err := h.store.GetCredentials(r.Context(), id)
	if err != nil {
		slog.Error("clientForDevice: decrypt creds", "err", err, "id", id)
		writeError(w, "credentials unavailable", http.StatusInternalServerError)
		return nil, "", false
	}
	// r.Context() already carries the per-request timeout from
	// middleware.Timeout (60s). That's comfortable headroom for a
	// `show vlan brief` + 3-line config + `wr mem` on a warm SSH
	// connection. For an operation that routinely needs more, move
	// the handler outside the middleware.Timeout group (the pattern
	// we used for ISO uploads).
	cli, err := cisco.Dial(r.Context(), dev.MgmtHost, dev.MgmtPort, username, password, enable)
	if err != nil {
		h.writeCiscoErr(w, err, "connect")
		return nil, "", false
	}
	return cli, dev.Name, true
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
