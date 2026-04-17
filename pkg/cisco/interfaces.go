package cisco

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Interface is the richer per-interface view for the management UI.
// Merges data from `show ip interface brief` (IP, status, protocol,
// method) with descriptions from `show interfaces description`.
//
// IP is the dotted-decimal address only — the mask is **not** included
// because `show ip interface brief` doesn't carry it. We could pull
// mask via a second `show ip interface` pass but the extra traffic
// isn't worth it for the list view; the edit form requires CIDR input
// anyway so the admin specifies mask at write time.
type Interface struct {
	Name        string `json:"name"`        // short or long form, as the device reports
	Description string `json:"description,omitempty"`
	IP          string `json:"ip,omitempty"` // "10.1.1.1" or empty for unassigned
	Method      string `json:"method,omitempty"` // NVRAM / manual / DHCP / unset
	Status      string `json:"status,omitempty"` // "up" / "down" / "administratively down"
	Protocol    string `json:"protocol,omitempty"` // "up" / "down"
}

// InterfaceNameRE validates the admin-typed interface name before we
// send it to the device. IOS accepts: GigabitEthernet0/0/0,
// TenGigabitEthernet1/0, Vlan10, Loopback0, Tunnel1, Port-channel1,
// plus short forms (Gi0/0, Te1/0, Lo0, Vl10). The common shape is
// alphanumeric + slash + dot (subinterfaces like Gi0/0.100) + hyphen
// (Port-channel, Tunnel-TE).
var InterfaceNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9/\.\-]+$`)

// Interfaces lists every L3-capable interface on the device, suitable
// for the IP-management UI. Runs:
//
//  1. `show ip interface brief` — the authoritative list of interface
//     names + current IP + status + protocol.
//  2. `show interfaces description` — descriptions, which we merge
//     in by name.
//
// `show interfaces description` rejected by older IOS images → we
// proceed with just the brief output; descriptions stay empty.
func (c *Client) Interfaces(ctx context.Context) ([]Interface, error) {
	brief, err := c.RunCommand(ctx, "show ip interface brief")
	if err != nil {
		return nil, err
	}
	// Reuse the parser from health.go — same output format.
	rows := parseIPIntBrief(brief)
	out := make([]Interface, 0, len(rows))
	for _, r := range rows {
		out = append(out, Interface{
			Name:     r.Name,
			IP:       r.IP,
			Method:   r.Method,
			Status:   r.Status,
			Protocol: r.Protocol,
		})
	}

	// Descriptions — best-effort. A device that rejects the command
	// just leaves descriptions empty.
	if desc, err := c.RunCommand(ctx, "show interfaces description"); err == nil {
		if ok, _ := HasIOSError(desc); !ok {
			descByName := parseShowInterfacesDescription(desc)
			for i := range out {
				if d, found := descByName[out[i].Name]; found {
					out[i].Description = d
				}
			}
		}
	}
	return out, nil
}

// parseShowInterfacesDescription consumes `show interfaces description`:
//
//	Interface                      Status         Protocol Description
//	Gi0/0                          up             up       Uplink to core
//	Gi0/1                          admin down     down
//	Vl10                           up             up       Data VLAN
//
// Columns are positional; description is the trailing "everything
// after the protocol column" which can include spaces. IOS keeps the
// column widths stable, so we find the column start positions from
// the header line and slice.
//
// Returns a map name → description. Missing descriptions (some rows
// have none) map to empty string and are filtered by the caller.
func parseShowInterfacesDescription(out string) map[string]string {
	result := map[string]string{}
	lines := strings.Split(out, "\n")
	// Find header line to learn column positions.
	descStart := -1
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "Interface") &&
			strings.Contains(ln, "Status") &&
			strings.Contains(ln, "Description") {
			descStart = strings.Index(ln, "Description")
			break
		}
	}
	if descStart < 0 {
		return result
	}
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r ")
		if ln == "" {
			continue
		}
		trim := strings.TrimSpace(ln)
		// Skip header + separator + non-data lines.
		if strings.HasPrefix(trim, "Interface") || strings.HasPrefix(trim, "----") {
			continue
		}
		// Interface name is the first whitespace-delimited field.
		fields := strings.Fields(ln)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		// Description is everything from descStart to EOL, trimmed.
		if len(ln) > descStart {
			desc := strings.TrimSpace(ln[descStart:])
			if desc != "" {
				result[name] = desc
			}
		}
	}
	return result
}

// SetInterfaceIP assigns a CIDR address to the interface, bringing it
// out of shutdown. `cidr` is the admin-supplied "10.1.1.1/24" form;
// we split into IOS's dotted-decimal form for the set command.
//
// Returns before/after `show running-config | section interface <name>`
// snapshots for audit — same contract as CreateVLAN / DeleteVLAN.
func (c *Client) SetInterfaceIP(ctx context.Context, ifaceName, cidr string) (before, after string, err error) {
	ip, mask, err := cidrToIOS(cidr)
	if err != nil {
		return "", "", err
	}
	section := fmt.Sprintf("interface %s", ifaceName)
	before, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{
		fmt.Sprintf("interface %s", ifaceName),
		fmt.Sprintf("ip address %s %s", ip, mask),
		"no shutdown",
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

// ClearInterfaceIP removes the IP from the interface (leaves the
// admin state alone — if it was `no shutdown`, it stays up, just
// with no IP).
func (c *Client) ClearInterfaceIP(ctx context.Context, ifaceName string) (before, after string, err error) {
	section := fmt.Sprintf("interface %s", ifaceName)
	before, err = c.ShowRunningConfig(ctx, section)
	if err != nil {
		return "", "", fmt.Errorf("snapshot before: %w", err)
	}
	lines := []string{
		fmt.Sprintf("interface %s", ifaceName),
		"no ip address",
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

// cidrToIOS splits "10.1.1.1/24" into ("10.1.1.1", "255.255.255.0").
// IPv4-only for now; IPv6 support would add a separate `ipv6 address`
// line and deserves its own code path. Returns a plain error so the
// handler can surface it directly to the admin.
func cidrToIOS(cidr string) (ip, mask string, err error) {
	ipObj, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	v4 := ipObj.To4()
	if v4 == nil {
		return "", "", fmt.Errorf("IPv6 not supported yet: %q", cidr)
	}
	// net.ParseCIDR's ipObj is the host address; ipnet.IP is the
	// network address. Reject 0.0.0.0/0-ish configs, and if the admin
	// typed a network address (host bits zero) flag it — it's usually
	// a typo, but we allow /31 where both ends are valid hosts.
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", "", fmt.Errorf("expected IPv4, got /%d bits", bits)
	}
	if ones == 0 {
		return "", "", fmt.Errorf("refusing to set a /0 interface address")
	}
	return v4.String(), net.IP(ipnet.Mask).String(), nil
}
