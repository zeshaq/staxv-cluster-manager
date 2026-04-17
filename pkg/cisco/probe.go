package cisco

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// DeviceInfo is what Probe returns on success — the subset of
// `show version` output cluster-manager cares about. Empty strings
// mean "couldn't parse it from this device's output variant"; the
// caller treats empties as "unknown" and renders "—".
type DeviceInfo struct {
	Hostname string
	Model    string // e.g. "WS-C3750X-48P", "ISR4321/K9"
	Version  string // IOS version string, e.g. "15.2(4)E7"
	Serial   string
	Platform string // "ios" | "ios-xe"
	UptimeS  int64  // seconds since boot
}

// Probe runs `show version` and extracts device identity. A single
// call per enrollment + /test.
//
// The output varies by platform (IOS vs IOS-XE vs NX-OS) but the set
// of fields we want — hostname, model, version, serial, uptime — are
// universally present in some form. Parsing is regex-based + forgiving:
// missing fields stay empty rather than failing the probe.
func (c *Client) Probe(ctx context.Context) (*DeviceInfo, error) {
	out, err := c.RunCommand(ctx, "show version")
	if err != nil {
		return nil, err
	}
	info := &DeviceInfo{
		Hostname: c.Hostname(), // prompt-derived; may be empty
		Platform: detectPlatform(out),
		Version:  parseIOSVersion(out),
		Model:    parseModel(out),
		Serial:   parseSerial(out),
		UptimeS:  parseUptime(out),
	}
	// `show version` sometimes includes the hostname explicitly — use
	// it if the prompt-derived one was empty.
	if info.Hostname == "" {
		info.Hostname = parseShowVerHostname(out)
	}
	return info, nil
}

// ─── parsers ─────────────────────────────────────────────────────

// detectPlatform scans for banner strings that distinguish IOS-XE from
// classic IOS. NX-OS shows `Cisco Nexus Operating System`.
func detectPlatform(out string) string {
	switch {
	case strings.Contains(out, "Nexus Operating System"), strings.Contains(out, "NX-OS"):
		return "nxos"
	case strings.Contains(out, "IOS-XE"), strings.Contains(out, "IOS XE Software"):
		return "ios-xe"
	case strings.Contains(out, "IOS Software"), strings.Contains(out, "Cisco IOS Software"):
		return "ios"
	}
	return ""
}

// parseIOSVersion grabs the version from lines like:
//
//	Cisco IOS Software, C3750E Software (C3750E-UNIVERSALK9-M), Version 15.2(4)E7, RELEASE SOFTWARE (fc2)
//	Cisco IOS-XE software, Copyright (c) 2005-2020 by cisco Systems, Inc.
//	Cisco IOS XE Software, Version 16.12.4
var reVersion = regexp.MustCompile(`(?i)Version\s+(\S+?)[,\s]`)

func parseIOSVersion(out string) string {
	if m := reVersion.FindStringSubmatch(out); len(m) > 1 {
		return strings.TrimRight(m[1], ",")
	}
	return ""
}

// parseModel — IOS and IOS-XE put the model on a "cisco <MODEL>" line
// several paragraphs in. Classic IOS: "cisco WS-C3750X-48P (PowerPC405) processor".
// IOS-XE: "cisco ISR4321/K9 (1RU) processor".
var reModel = regexp.MustCompile(`(?m)^cisco\s+(\S+)\s`)

func parseModel(out string) string {
	if m := reModel.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	return ""
}

// parseSerial — newer IOS prints "System serial number            : FCZ1822E07K".
// Older gear prints "Processor board ID FCZ1822E07K".
var (
	reSerialColon = regexp.MustCompile(`(?i)System serial number\s*:\s*(\S+)`)
	reSerialBoard = regexp.MustCompile(`(?i)Processor board ID\s+(\S+)`)
)

func parseSerial(out string) string {
	if m := reSerialColon.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	if m := reSerialBoard.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	return ""
}

// parseUptime scans the "uptime is …" line and sums it to seconds.
// Example variants:
//
//	R1 uptime is 4 weeks, 2 days, 1 hour, 52 minutes
//	sw1 uptime is 1 year, 20 weeks, 3 days, 5 hours, 17 minutes
//	sw2 uptime is 14 minutes
//
// Missing components default to zero.
var reUptime = regexp.MustCompile(`(?i)uptime is (.+)`)

func parseUptime(out string) int64 {
	m := reUptime.FindStringSubmatch(out)
	if len(m) < 2 {
		return 0
	}
	// Trim at the end of line — some BMCs append more on the same line.
	line := m[1]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return parseUptimeExpr(line)
}

// parseUptimeExpr breaks "1 year, 20 weeks, 3 days, 5 hours, 17 minutes"
// into a total seconds count. Accepts singular + plural forms; ignores
// anything it can't interpret.
func parseUptimeExpr(s string) int64 {
	var total int64
	// Split on comma, then each token is "<N> <unit>[s]".
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		fields := strings.Fields(tok)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		unit := strings.ToLower(strings.TrimRight(fields[1], "s"))
		switch unit {
		case "year":
			total += n * 365 * 24 * 3600
		case "week":
			total += n * 7 * 24 * 3600
		case "day":
			total += n * 24 * 3600
		case "hour":
			total += n * 3600
		case "minute":
			total += n * 60
		case "second":
			total += n
		}
	}
	return total
}

// parseShowVerHostname pulls hostname out of "<name> uptime is …"
// — helpful when the prompt didn't reveal it (e.g. we entered on a
// '%' pseudo-banner because of a warning).
var reShowVerHost = regexp.MustCompile(`(?m)^(\S+)\s+uptime is `)

func parseShowVerHostname(out string) string {
	if m := reShowVerHost.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	return ""
}
