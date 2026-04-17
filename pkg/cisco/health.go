package cisco

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Health bundles the live operational readings we pull from a device:
// CPU, memory, environment sensors, and a per-interface brief.
//
// Each block has its own error field so a BMC-like partial-success
// still yields useful data. The handler returns the whole struct;
// frontend colors empty blocks with a small error banner.
type Health struct {
	CPU5s   int `json:"cpu_5sec_percent"`   // 0-100; 0 means "not parsed"
	CPU1m   int `json:"cpu_1min_percent"`
	CPU5m   int `json:"cpu_5min_percent"`
	CPUErr  string `json:"cpu_error,omitempty"`

	MemoryUsedBytes  uint64 `json:"memory_used_bytes"`
	MemoryFreeBytes  uint64 `json:"memory_free_bytes"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes"`
	MemoryPoolName   string `json:"memory_pool,omitempty"` // "Processor"
	MemoryErr        string `json:"memory_error,omitempty"`

	Env    []EnvSensor `json:"env,omitempty"`
	EnvErr string      `json:"env_error,omitempty"`

	Interfaces    []InterfaceBrief `json:"interfaces,omitempty"`
	InterfacesErr string           `json:"interfaces_error,omitempty"`
}

// EnvSensor is a line from `show environment all` / `show env` —
// temperature, fan, or PSU. Cisco's output is wildly
// device-dependent; we flatten into a generic {name, reading, state}.
type EnvSensor struct {
	Kind    string `json:"kind"`              // "temperature" | "fan" | "power"
	Name    string `json:"name"`
	Reading string `json:"reading,omitempty"` // raw string; units vary by box
	State   string `json:"state,omitempty"`   // "OK", "Warn", "Bad", "Normal", ...
}

// InterfaceBrief is one row from `show ip interface brief`. Fields
// are the columns Cisco docs promise everywhere — names may wrap but
// we parse them out of fixed-width positions, which Cisco keeps
// stable.
type InterfaceBrief struct {
	Name     string `json:"name"`
	IP       string `json:"ip,omitempty"`       // "unassigned" → ""
	OK       string `json:"ok,omitempty"`        // "YES"/"NO"
	Method   string `json:"method,omitempty"`    // "NVRAM", "manual", "DHCP", ...
	Status   string `json:"status,omitempty"`    // "up", "administratively down", "down"
	Protocol string `json:"protocol,omitempty"`  // "up", "down"
}

// GetHealth runs CPU + memory + environment + interfaces in sequence.
// Serialized because SSH sessions are not safe for concurrent writes —
// one command at a time. Each step's error stays local to that block.
//
// Total wall-time ~2-4s on a warm mgmt LAN connection.
func (c *Client) GetHealth(ctx context.Context) (*Health, error) {
	var h Health
	// NOTE: `sync` imported but unused yet — reserved for when we
	// split into concurrent sessions per command (Phase 2+). Keeps
	// the import stable so diff-reviewers see the intent.
	_ = sync.Mutex{}

	if out, err := c.RunCommand(ctx, "show processes cpu sorted 5sec"); err != nil {
		h.CPUErr = err.Error()
	} else {
		c5, m1, m5 := parseCPU(out)
		h.CPU5s, h.CPU1m, h.CPU5m = c5, m1, m5
	}

	if out, err := c.RunCommand(ctx, "show memory statistics"); err != nil {
		h.MemoryErr = err.Error()
	} else {
		pool, used, free := parseMemoryStats(out)
		h.MemoryPoolName = pool
		h.MemoryUsedBytes = used
		h.MemoryFreeBytes = free
		h.MemoryTotalBytes = used + free
	}

	// `show environment all` is the IOS command; NX-OS uses the same.
	// Many small switches don't implement it — treat a rejected
	// command as "no sensors" rather than a fatal error.
	if out, err := c.RunCommand(ctx, "show environment all"); err != nil {
		h.EnvErr = err.Error()
	} else if !looksLikeRejection(out) {
		h.Env = parseEnvironment(out)
	}

	if out, err := c.RunCommand(ctx, "show ip interface brief"); err != nil {
		h.InterfacesErr = err.Error()
	} else {
		h.Interfaces = parseIPIntBrief(out)
	}

	return &h, nil
}

// ─── parsers ─────────────────────────────────────────────────────

// `show processes cpu [sorted 5sec]` header line on IOS:
//
//	CPU utilization for five seconds: 1%/0%; one minute: 2%; five minutes: 1%
//
// NX-OS prints a similar line.
var reCPU = regexp.MustCompile(
	`five seconds:\s*(\d+)%(?:/\d+%)?;\s*one minute:\s*(\d+)%;\s*five minutes:\s*(\d+)%`)

func parseCPU(out string) (c5, m1, m5 int) {
	if m := reCPU.FindStringSubmatch(out); len(m) == 4 {
		c5, _ = strconv.Atoi(m[1])
		m1, _ = strconv.Atoi(m[2])
		m5, _ = strconv.Atoi(m[3])
	}
	return
}

// `show memory statistics`:
//
//	          Head    Total(b)     Used(b)     Free(b)   Lowest(b)  Largest(b)
//	Processor ...     526782976    178934756   347848220 ...
//	      I/O ...      67108864     20480396    46628468 ...
//
// We pick the "Processor" row; that's the one users care about. Fields
// are whitespace-separated and positionally stable.
var reMemProc = regexp.MustCompile(`(?m)^Processor\s+\S+\s+(\d+)\s+(\d+)\s+(\d+)`)

func parseMemoryStats(out string) (pool string, used, free uint64) {
	if m := reMemProc.FindStringSubmatch(out); len(m) == 4 {
		// total, used, free — columns.
		used, _ = strconv.ParseUint(m[2], 10, 64)
		free, _ = strconv.ParseUint(m[3], 10, 64)
		pool = "Processor"
	}
	return
}

// looksLikeRejection covers Cisco's "% Invalid input detected" which
// we treat as "this device doesn't support that command" rather than
// a hard failure.
func looksLikeRejection(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "% invalid input") ||
		strings.Contains(s, "% unknown command") ||
		strings.Contains(s, "% ambiguous command")
}

// parseEnvironment is a loose, section-aware scan of `show environment
// all` output. Cisco emits wildly different formats per device family,
// so we look for a few well-known shapes:
//
//	Temperature readings:                    →  Kind=temperature
//	  <name>: <reading> [State]
//	Fan readings:                            →  Kind=fan
//	  <name>: <reading> [State]
//	Power supplies:                          →  Kind=power
//
// Devices that don't emit these headers return [] — caller renders
// an empty block with no error.
func parseEnvironment(out string) []EnvSensor {
	var sensors []EnvSensor
	section := ""
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if trim == "" {
			continue
		}
		low := strings.ToLower(trim)
		switch {
		case strings.HasPrefix(low, "temperature"):
			section = "temperature"
			continue
		case strings.Contains(low, "fan"):
			if strings.HasSuffix(strings.TrimRight(low, ":"), "fan") ||
				strings.HasSuffix(strings.TrimRight(low, ":"), "fans") ||
				strings.Contains(low, "fan status") ||
				strings.Contains(low, "fan readings") {
				section = "fan"
				continue
			}
		case strings.HasPrefix(low, "power") || strings.Contains(low, "power supply"):
			section = "power"
			continue
		}
		if section == "" {
			continue
		}
		// Attempt a "name: reading ... [State]" split.
		if name, rest, ok := splitFirst(trim, ":"); ok {
			name = strings.TrimSpace(name)
			rest = strings.TrimSpace(rest)
			reading, state := splitLastState(rest)
			sensors = append(sensors, EnvSensor{
				Kind:    section,
				Name:    name,
				Reading: reading,
				State:   state,
			})
		}
	}
	return sensors
}

func splitFirst(s, sep string) (head, tail string, ok bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// splitLastState separates a trailing bracketed state (e.g. "42 C [OK]")
// from the reading. If no brackets, everything is reading.
func splitLastState(s string) (reading, state string) {
	if i := strings.LastIndex(s, "["); i > 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1 : j])
		}
	}
	// Sometimes the state word appears bare at the end ("...  OK" / "...  Normal").
	fields := strings.Fields(s)
	if len(fields) > 1 {
		last := fields[len(fields)-1]
		upper := strings.ToUpper(last)
		if upper == "OK" || upper == "NORMAL" || upper == "WARN" || upper == "BAD" || upper == "FAULT" {
			return strings.TrimSpace(strings.Join(fields[:len(fields)-1], " ")), last
		}
	}
	return s, ""
}

// `show ip interface brief` output:
//
//	Interface              IP-Address      OK? Method Status                Protocol
//	GigabitEthernet0/0     unassigned      YES NVRAM  administratively down down
//	GigabitEthernet0/1     10.1.1.1        YES manual up                    up
//	Vlan10                 192.168.10.1    YES NVRAM  up                    up
//
// Columns are whitespace-separated, but Status can contain spaces
// ("administratively down") so we anchor from the right for Protocol
// and the Status column gets whatever's left.
func parseIPIntBrief(out string) []InterfaceBrief {
	var rows []InterfaceBrief
	for _, ln := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(ln)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "Interface") { // header
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 6 {
			continue
		}
		// Protocol is the last field; Status is fields[4..len-1].
		protocol := fields[len(fields)-1]
		name := fields[0]
		ip := fields[1]
		if ip == "unassigned" {
			ip = ""
		}
		ok := fields[2]
		method := fields[3]
		status := strings.Join(fields[4:len(fields)-1], " ")
		rows = append(rows, InterfaceBrief{
			Name:     name,
			IP:       ip,
			OK:       ok,
			Method:   method,
			Status:   status,
			Protocol: protocol,
		})
	}
	return rows
}
