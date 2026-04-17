package cisco

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// OSPF support — enough to stand up a small P2P mesh between lab
// routers: create a process with a router-id, attach interfaces in
// an area, optionally flip to network type point-to-point so the
// neighbors form cleanly on /30 or /31 links.
//
// Scope (shipped):
//   - Process list + create + delete (pid + router-id).
//   - Per-interface attach with optional network type.
//   - Neighbor read-only view (show ip ospf neighbor).
//
// Scope (deferred):
//   - Cost / hello / dead / priority tuning.
//   - Authentication (MD5 / SHA / key-chain).
//   - Passive-interface management (we respect existing config, don't
//     touch passive state from the UI).
//   - Area-level config (stub, NSSA, virtual-links).
//   - LSDB viewer.

// OSPFProcess is one enabled OSPF process on the device. PID is the
// IOS process-id integer (1-65535), RouterID the dotted-decimal
//32-bit router identifier. Areas is the derived set of areas seen
// across configured interfaces — NOT the full list of areas declared
// in the process, which would need `show ip ospf` parsing. Good
// enough for the overview table.
type OSPFProcess struct {
	PID      int      `json:"pid"`
	RouterID string   `json:"router_id,omitempty"`
	Areas    []string `json:"areas,omitempty"`
}

// OSPFInterface is one row from `show ip ospf interface brief`.
// Covers the fields the UI needs to show what's attached + what
// state it's in.
type OSPFInterface struct {
	Name        string `json:"name"`
	PID         int    `json:"pid"`
	Area        string `json:"area"`
	IPCIDR      string `json:"ip_cidr,omitempty"`
	Cost        int    `json:"cost,omitempty"`
	State       string `json:"state,omitempty"`        // P2P | DR | BDR | LOOP | DROTHER | DOWN | …
	NeighborsFC string `json:"neighbors_fc,omitempty"` // "1/1" = full/adj-count
	// NetworkType isn't in `show ip ospf interface brief` — we
	// augment by parsing the per-interface running-config when
	// the admin edits a row. Left empty from the list view.
	NetworkType string `json:"network_type,omitempty"`
}

// OSPFNeighbor is one row from `show ip ospf neighbor`.
type OSPFNeighbor struct {
	NeighborID string `json:"neighbor_id"`
	Priority   int    `json:"priority"`
	State      string `json:"state"` // FULL/DR, FULL/BDR, 2WAY, INIT, DOWN
	DeadTime   string `json:"dead_time,omitempty"`
	Address    string `json:"address,omitempty"`
	Interface  string `json:"interface,omitempty"`
}

// OSPFState is the bundle returned by one GET /ospf call — three
// independent queries, rendered together so the UI has a complete
// picture in one fetch.
type OSPFState struct {
	Processes  []OSPFProcess   `json:"processes"`
	Interfaces []OSPFInterface `json:"interfaces"`
	Neighbors  []OSPFNeighbor  `json:"neighbors"`

	// Per-block errors so a partial failure is surfaced without the
	// whole response being a 500. Matches the pattern in health.go.
	ProcessesErr  string `json:"processes_error,omitempty"`
	InterfacesErr string `json:"interfaces_error,omitempty"`
	NeighborsErr  string `json:"neighbors_error,omitempty"`
}

// GetOSPFState runs:
//
//   - `show running-config | section ^router ospf` → process list
//     with router-ids (uses the config view because `show ip ospf`
//     output varies wildly by IOS version and has no clean machine-
//     parseable header).
//   - `show ip ospf interface brief` → interface attachments.
//   - `show ip ospf neighbor` → neighbor table.
//
// Runs sequentially — SSH sessions aren't safe for concurrent writes
// on the same channel. Total ~2-3s on a warm link.
func (c *Client) GetOSPFState(ctx context.Context) (*OSPFState, error) {
	out := &OSPFState{}

	if s, err := c.ShowRunningConfig(ctx, "^router ospf"); err != nil {
		out.ProcessesErr = err.Error()
	} else {
		out.Processes = parseRunningConfigRouterOSPF(s)
	}

	if s, err := c.RunCommand(ctx, "show ip ospf interface brief"); err != nil {
		out.InterfacesErr = err.Error()
	} else if ok, marker := HasIOSError(s); ok {
		// Device without OSPF config or older IOS without `brief`
		// subcommand → empty, not a hard error.
		_ = marker
		out.Interfaces = []OSPFInterface{}
	} else {
		out.Interfaces = parseShowIPOSPFInterfaceBrief(s)
	}
	// Back-fill Areas on each Process by scanning interface attachments.
	for i := range out.Processes {
		out.Processes[i].Areas = uniqueAreasForPID(out.Interfaces, out.Processes[i].PID)
	}

	if s, err := c.RunCommand(ctx, "show ip ospf neighbor"); err != nil {
		out.NeighborsErr = err.Error()
	} else if ok, _ := HasIOSError(s); !ok {
		out.Neighbors = parseShowIPOSPFNeighbor(s)
	} else {
		out.Neighbors = []OSPFNeighbor{}
	}

	return out, nil
}

// ─── Parsers ─────────────────────────────────────────────────────

var reRouterOSPFHeader = regexp.MustCompile(`^router ospf (\d+)\s*$`)
var reRouterID = regexp.MustCompile(`^\s*router-id\s+(\S+)\s*$`)

// parseRunningConfigRouterOSPF consumes `show running-config | section
// ^router ospf` which looks like:
//
//	router ospf 1
//	 router-id 1.1.1.1
//	 log-adjacency-changes
//	 passive-interface default
//	 no passive-interface GigabitEthernet0/0
//	!
//	router ospf 2
//	 router-id 2.2.2.2
//	!
//
// Returns a list with PID + RouterID. Everything else in each block
// is ignored at this level — the handler's concern is "is OSPF N
// configured, and what's its router-id".
func parseRunningConfigRouterOSPF(out string) []OSPFProcess {
	procs := make([]OSPFProcess, 0)
	var current *OSPFProcess
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reRouterOSPFHeader.FindStringSubmatch(line); len(m) == 2 {
			// Finish prior block.
			if current != nil {
				procs = append(procs, *current)
			}
			pid, _ := strconv.Atoi(m[1])
			current = &OSPFProcess{PID: pid}
			continue
		}
		if current != nil {
			if m := reRouterID.FindStringSubmatch(line); len(m) == 2 {
				current.RouterID = m[1]
				continue
			}
			// End of block — bare `!` or a line that starts without
			// whitespace (means we've left the router-ospf section).
			if line == "!" || (len(line) > 0 && line[0] != ' ' && line[0] != '\t') {
				procs = append(procs, *current)
				current = nil
			}
		}
	}
	if current != nil {
		procs = append(procs, *current)
	}
	return procs
}

// uniqueAreasForPID returns the set of areas seen across the
// interfaces attached to a given OSPF pid, sorted for deterministic
// output.
func uniqueAreasForPID(ifs []OSPFInterface, pid int) []string {
	seen := map[string]bool{}
	for _, i := range ifs {
		if i.PID == pid && i.Area != "" {
			seen[i.Area] = true
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	// Insertion order is map-random; sort for UI stability.
	sortStrings(out)
	return out
}

// sortStrings — cheap sort.Strings replacement; avoids importing
// "sort" just for this one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// `show ip ospf interface brief`:
//
//	Interface    PID   Area            IP Address/Mask    Cost  State Nbrs F/C
//	Gi0/0        1     0               10.0.0.1/30        1     P2P   1/1
//	Gi0/1        1     0.0.0.5         10.0.0.5/30        1     DR    1/1
//	Lo0          1     0               1.1.1.1/32         1     LOOP  0/0
//
// Columns are whitespace-separated; Area can be numeric ("0") or
// dotted-decimal ("0.0.0.5"). Neighbors column is last and
// contains a slash.
func parseShowIPOSPFInterfaceBrief(out string) []OSPFInterface {
	rows := make([]OSPFInterface, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		// Skip header + any other non-data lines.
		if strings.HasPrefix(trim, "Interface") || strings.HasPrefix(trim, "----") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 6 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cost, _ := strconv.Atoi(fields[4])
		row := OSPFInterface{
			Name:   fields[0],
			PID:    pid,
			Area:   fields[2],
			IPCIDR: fields[3],
			Cost:   cost,
			State:  fields[5],
		}
		if len(fields) >= 7 {
			row.NeighborsFC = fields[6]
		}
		rows = append(rows, row)
	}
	return rows
}

// `show ip ospf neighbor`:
//
//	Neighbor ID     Pri   State           Dead Time   Address         Interface
//	2.2.2.2           1   FULL/BDR        00:00:35    10.0.0.2        GigabitEthernet0/0
//	3.3.3.3           1   FULL/DR         00:00:38    10.0.0.6        GigabitEthernet0/1
//
// Column 3 (State) can include '/' (e.g. FULL/DR). All columns are
// whitespace-free except the header, so strings.Fields is fine.
func parseShowIPOSPFNeighbor(out string) []OSPFNeighbor {
	rows := make([]OSPFNeighbor, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "Neighbor ID") || strings.HasPrefix(trim, "----") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 6 {
			continue
		}
		pri, _ := strconv.Atoi(fields[1])
		rows = append(rows, OSPFNeighbor{
			NeighborID: fields[0],
			Priority:   pri,
			State:      fields[2],
			DeadTime:   fields[3],
			Address:    fields[4],
			Interface:  fields[5],
		})
	}
	return rows
}

// ─── Writes ──────────────────────────────────────────────────────

// CreateOrUpdateOSPFProcess enables `router ospf {pid}` with the
// given router-id. Idempotent: re-running overwrites the router-id.
// Returns before/after `show run | section router ospf {pid}`
// snapshots for audit.
func (c *Client) CreateOrUpdateOSPFProcess(ctx context.Context, pid int, routerID string) (before, after string, err error) {
	if err := validateRouterID(routerID); err != nil {
		return "", "", err
	}
	section := fmt.Sprintf("^router ospf %d$", pid)
	before, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{
		fmt.Sprintf("router ospf %d", pid),
		fmt.Sprintf("router-id %s", routerID),
		"exit",
	}
	if _, err := c.RunConfigLines(ctx, lines, true); err != nil {
		after, _ = c.ShowRunningConfig(ctx, section)
		return before, after, err
	}
	after, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return before, "", fmt.Errorf("snapshot after: %w", err)
	}
	return before, after, nil
}

// DeleteOSPFProcess removes the process entirely. Drops all
// interface attachments for this pid as a side effect (IOS
// automatically cleans those up when `no router ospf N` fires).
func (c *Client) DeleteOSPFProcess(ctx context.Context, pid int) (before, after string, err error) {
	section := fmt.Sprintf("^router ospf %d$", pid)
	before, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{fmt.Sprintf("no router ospf %d", pid)}
	if _, err := c.RunConfigLines(ctx, lines, true); err != nil {
		after, _ = c.ShowRunningConfig(ctx, section)
		return before, after, err
	}
	after, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return before, "", fmt.Errorf("snapshot after: %w", err)
	}
	return before, after, nil
}

// SetOSPFInterface is the full-state set for one interface's OSPF
// membership. Call with pid=0 to clear OSPF from the interface.
// networkType is "point-to-point" / "broadcast" / "non-broadcast"
// etc. — empty string = don't set (leave whatever was there).
//
// Internally queries current per-interface running-config so we
// know which (pid, area) pair to `no ip ospf` when transitioning.
// Without that query the admin would have to always remember their
// old assignment to unconfigure cleanly.
//
// Returns before/after `show run | section interface <name>`
// snapshots for audit.
func (c *Client) SetOSPFInterface(ctx context.Context, ifaceName string, pid int, area, networkType string) (before, after string, err error) {
	section := fmt.Sprintf("interface %s", ifaceName)
	before, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}

	// Parse current OSPF lines out of the before-snapshot so we can
	// emit the right `no ip ospf` cleanup when (pid, area) changes.
	curPID, curArea, curNet := parseInterfaceOSPFFromConfig(before)

	var lines []string
	lines = append(lines, fmt.Sprintf("interface %s", ifaceName))

	// Clear path — or a change of pid/area needs the old `no`.
	if pid == 0 && curPID > 0 {
		lines = append(lines, fmt.Sprintf("no ip ospf %d area %s", curPID, curArea))
	} else if pid > 0 && curPID > 0 && (curPID != pid || curArea != area) {
		// Switching pid or area — unbind old, bind new. IOS tolerates
		// just `ip ospf <new> area <new>` without the prior `no`, but
		// being explicit avoids double-registration edge cases.
		lines = append(lines, fmt.Sprintf("no ip ospf %d area %s", curPID, curArea))
	}

	// Apply new membership.
	if pid > 0 {
		lines = append(lines, fmt.Sprintf("ip ospf %d area %s", pid, area))
	}

	// Network type. Empty networkType with pid>0 = leave as-is (don't
	// touch). Empty networkType with pid==0 (clear path) = also clear
	// the network type to return interface to default.
	switch {
	case networkType != "":
		lines = append(lines, fmt.Sprintf("ip ospf network %s", networkType))
	case pid == 0 && curNet != "":
		lines = append(lines, "no ip ospf network")
	}

	lines = append(lines, "exit")

	if _, err := c.RunConfigLines(ctx, lines, true); err != nil {
		after, _ = c.ShowRunningConfig(ctx, section)
		return before, after, err
	}
	after, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return before, "", fmt.Errorf("snapshot after: %w", err)
	}
	return before, after, nil
}

var (
	reIfaceIPOSPF     = regexp.MustCompile(`^\s*ip ospf (\d+) area (\S+)\s*$`)
	reIfaceOSPFNetTyp = regexp.MustCompile(`^\s*ip ospf network (\S+)\s*$`)
)

// parseInterfaceOSPFFromConfig extracts the current (pid, area,
// network-type) triple from a per-interface running-config block.
// Missing fields → zero / empty string.
func parseInterfaceOSPFFromConfig(cfg string) (pid int, area, networkType string) {
	for _, line := range strings.Split(cfg, "\n") {
		if m := reIfaceIPOSPF.FindStringSubmatch(line); len(m) == 3 {
			pid, _ = strconv.Atoi(m[1])
			area = m[2]
			continue
		}
		if m := reIfaceOSPFNetTyp.FindStringSubmatch(line); len(m) == 2 {
			networkType = m[1]
		}
	}
	return
}

// validateRouterID checks that the admin-typed router-id parses as
// an IPv4 address. IOS accepts any 32-bit value in dotted-decimal
// form regardless of whether it's a "real" routable address; our
// validation matches that — we just want to catch typos like
// "1.1.1" or "one.one.one.one".
func validateRouterID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("router-id is required")
	}
	ip := net.ParseIP(id)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("router-id must be dotted-decimal IPv4 (e.g. 1.1.1.1): %q", id)
	}
	return nil
}

// OSPFNetworkTypes is the allow-list of `ip ospf network <type>`
// values we let the admin pick. Matches the IOS CLI set.
var OSPFNetworkTypes = map[string]bool{
	"":                     true, // "leave unspecified"
	"point-to-point":       true,
	"point-to-multipoint":  true,
	"broadcast":            true,
	"non-broadcast":        true,
}
