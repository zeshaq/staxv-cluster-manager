package cisco

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// VLAN is the normalized view of one VLAN as parsed from
// `show vlan brief`. Ports is the flat list of interfaces currently
// in that VLAN (access ports + routed-to-VLAN trunks' native
// membership). Continuation-line membership (IOS wraps port lists
// across multiple lines when they're long) is collapsed into one
// slice.
type VLAN struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Status string   `json:"status"` // active | act/unsup | suspended
	Ports  []string `json:"ports"`
}

// Reserved VLAN IDs IOS owns by default. We don't block creating/
// editing these at the library layer — admin may legitimately touch
// VLAN 1's name — but the handler warns on them.
const (
	VLANIDMin      = 1    // "default" — don't delete, but renaming is fine
	VLANIDMax      = 4094 // 4095 is reserved; 0 is invalid
	VLANIDRsvdMin  = 1002 // 1002-1005 are fddi / token-ring defaults
	VLANIDRsvdMax  = 1005
)

// VLANNameRE — IOS accepts 1-32 chars, alnum + hyphen + underscore.
// Matches what `name <X>` under `vlan N` will accept without
// rejecting as "% Invalid name".
var VLANNameRE = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,32}$`)

// VLANs lists every VLAN the device reports from `show vlan brief`.
// Returns an empty slice (not nil) on devices that don't support
// VLANs (pure routers, some L2-only MPLS edge devices); the parser
// just finds no rows to consume.
func (c *Client) VLANs(ctx context.Context) ([]VLAN, error) {
	out, err := c.RunCommand(ctx, "show vlan brief")
	if err != nil {
		return nil, err
	}
	if ok, _ := HasIOSError(out); ok {
		// Devices that don't do VLANs (e.g. pure ISR without
		// EtherSwitch) reject "show vlan brief" with "% Invalid
		// input". Return empty slice — caller treats it as
		// "no VLANs reported" rather than a hard failure.
		return []VLAN{}, nil
	}
	return parseShowVLANBrief(out), nil
}

// CreateVLAN adds `vlan <id>` with `name <name>` via a config-mode
// session. Returns the before + after `show running-config | section
// vlan` snapshots for audit. On config error mid-sequence, returns
// the snapshots captured so far + the error.
//
// The library doesn't validate VLAN ID / name syntax — that's the
// handler's job; this function is the low-level write.
func (c *Client) CreateVLAN(ctx context.Context, id int, name string) (before, after string, err error) {
	before, err = c.ShowRunningConfig(ctx, "vlan")
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{
		fmt.Sprintf("vlan %d", id),
		fmt.Sprintf("name %s", name),
		"exit", // leave the vlan sub-mode explicitly
	}
	if _, err := c.RunConfigLines(ctx, lines, true); err != nil {
		// Grab the "after" snapshot anyway — shows whether any lines
		// landed before the error, helping the admin diagnose.
		after, _ = c.ShowRunningConfig(ctx, "vlan")
		return before, after, err
	}
	after, err = c.ShowRunningConfig(ctx, "vlan")
	if err != nil {
		return before, "", fmt.Errorf("snapshot after: %w", err)
	}
	return before, after, nil
}

// DeleteVLAN removes a VLAN via `no vlan <id>`. Before/after
// snapshots captured for audit, same as CreateVLAN.
//
// IOS silently ignores `no vlan N` for a non-existent VLAN — no
// error. That's intentional on their part and matches our "idempotent
// delete" convention elsewhere.
func (c *Client) DeleteVLAN(ctx context.Context, id int) (before, after string, err error) {
	before, err = c.ShowRunningConfig(ctx, "vlan")
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{fmt.Sprintf("no vlan %d", id)}
	if _, err := c.RunConfigLines(ctx, lines, true); err != nil {
		after, _ = c.ShowRunningConfig(ctx, "vlan")
		return before, after, err
	}
	after, err = c.ShowRunningConfig(ctx, "vlan")
	if err != nil {
		return before, "", fmt.Errorf("snapshot after: %w", err)
	}
	return before, after, nil
}

// parseShowVLANBrief consumes classic IOS `show vlan brief` output:
//
//	VLAN Name                             Status    Ports
//	---- -------------------------------- --------- -------------------------------
//	1    default                          active    Gi0/1, Gi0/2, Gi0/3, Gi0/4
//	                                                Gi0/5, Gi0/6
//	10   DATA                             active    Gi0/10, Gi0/11
//	1002 fddi-default                     act/unsup
//
// The tricky parts are (a) the port column wrapping across
// continuation lines with no VLAN id, and (b) devices that don't
// emit the separator line consistently. We key off "first column
// is a number" vs "first column is whitespace" to distinguish new
// rows from continuations.
func parseShowVLANBrief(out string) []VLAN {
	vlans := make([]VLAN, 0)
	var current *VLAN

	for _, line := range strings.Split(out, "\n") {
		// Strip CRs defensively; tolerated by the client layer but
		// cheap insurance.
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// Header + separator lines. Check for literal strings that
		// Cisco emits; anything else falls through to data parsing.
		if strings.HasPrefix(line, "VLAN ") || strings.HasPrefix(line, "----") {
			continue
		}

		// A data row starts with a digit in column 0. A continuation
		// line starts with whitespace.
		if line[0] >= '0' && line[0] <= '9' {
			// Finish the previous row.
			if current != nil {
				vlans = append(vlans, *current)
			}
			// Parse: "<id> <name> <status> [<ports>]". Name can't
			// contain whitespace (IOS validates this on `name`) so
			// Fields works.
			fields := strings.Fields(line)
			if len(fields) < 3 {
				current = nil
				continue
			}
			id, err := strconv.Atoi(fields[0])
			if err != nil {
				current = nil
				continue
			}
			current = &VLAN{
				ID:     id,
				Name:   fields[1],
				Status: fields[2],
			}
			if len(fields) > 3 {
				current.Ports = parsePortList(strings.Join(fields[3:], " "))
			}
		} else if current != nil {
			// Continuation — add any listed ports to the current
			// VLAN. Only ports wrap; other fields are single-line.
			current.Ports = append(current.Ports, parsePortList(line)...)
		}
	}
	if current != nil {
		vlans = append(vlans, *current)
	}
	return vlans
}

// parsePortList splits a port column value like:
//
//	Gi0/1, Gi0/2, Gi0/3, Gi0/4
//
// into ["Gi0/1", "Gi0/2", "Gi0/3", "Gi0/4"]. Whitespace and commas
// are both separators, so we don't care about the commas explicitly.
func parsePortList(s string) []string {
	// Replace commas with spaces, then Fields.
	s = strings.ReplaceAll(s, ",", " ")
	out := make([]string, 0)
	for _, f := range strings.Fields(s) {
		out = append(out, f)
	}
	return out
}
