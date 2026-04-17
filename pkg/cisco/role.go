package cisco

import (
	"regexp"
	"strings"
)

// reISRClassic matches the 1900 / 2900 / 3900 Integrated Services
// Router (ISR G2) series. Cisco prints these as "1921", "2921/K9",
// "3945", etc. — a plain HasPrefix("1900") won't match because
// there's no zero. Anchoring on regex keeps it tight.
var reISRClassic = regexp.MustCompile(`^[123]9\d{2}(/K9)?$`)

// Role is the operational class of a network device, used to scope
// which features make sense in the UI (VLAN editor on switches /
// L3-switches; routing-table viewer on routers / L3-switches; etc.).
//
// Values match the strings the DB + frontend use. Kept as strings
// rather than an iota enum because they cross the JSON boundary —
// a Go int doesn't help the React side understand the semantics.
const (
	RoleRouter    = "router"
	RoleSwitch    = "switch"
	RoleL3Switch  = "l3-switch"
	RoleFirewall  = "firewall"
	RoleOther     = "other"
	RoleUnknown   = "unknown"
)

// ValidRoles is the allow-list the handler checks admin-supplied
// role overrides against.
var ValidRoles = map[string]bool{
	RoleRouter:   true,
	RoleSwitch:   true,
	RoleL3Switch: true,
	RoleFirewall: true,
	RoleOther:    true,
	RoleUnknown:  true,
}

// DetectRole guesses the role from the device's model string (the
// one that parseModel extracts from `show version`). Best-effort
// pattern match — returns RoleUnknown when the model doesn't look
// like anything in our catalog. Admin can always override via
// POST /role.
//
// Heuristics are biased toward the classic Cisco product lines we
// expect to see in a lab / small-enterprise fleet. Not exhaustive —
// new Cisco SKUs ship every year. Extending this is a matter of
// adding a prefix check + a comment pointing to the SKU's datasheet.
func DetectRole(model string) string {
	m := strings.ToUpper(strings.TrimSpace(model))
	if m == "" {
		return RoleUnknown
	}

	// Firewalls — ASA / Firepower. We don't support their CLI yet but
	// classifying them now lets the UI hide switch/router features.
	switch {
	case strings.HasPrefix(m, "ASA"),
		strings.HasPrefix(m, "FPR"),
		strings.HasPrefix(m, "FIREPOWER"):
		return RoleFirewall
	}

	// Pure routers — ISR (integrated services), ASR (aggregation
	// services), C8xxx (Catalyst 8000 SD-WAN/edge routers), 1900/2900/
	// 3900 classic ISR series, RV (small-business routers).
	switch {
	case strings.HasPrefix(m, "ISR"),
		strings.HasPrefix(m, "ASR"),
		strings.HasPrefix(m, "C8"),
		reISRClassic.MatchString(m),
		strings.HasPrefix(m, "RV"):
		return RoleRouter
	}

	// L3 switches — Catalyst 3xxx, 4xxx, 6xxx, 9300/9400/9500, Nexus.
	// These routinely speak OSPF/BGP + SVIs; VLAN AND routing UI both
	// apply.
	switch {
	case strings.HasPrefix(m, "C3"),    // C3750X, C3850, C3650 (some)
		strings.HasPrefix(m, "WS-C3"),   // WS-C3750X-48P etc.
		strings.HasPrefix(m, "C4"),      // Catalyst 4500 series
		strings.HasPrefix(m, "C6"),      // Catalyst 6500 series
		strings.HasPrefix(m, "C9300"), strings.HasPrefix(m, "C9400"),
		strings.HasPrefix(m, "C9500"), strings.HasPrefix(m, "C9600"),
		strings.HasPrefix(m, "NEXUS"),
		strings.HasPrefix(m, "N3K"), strings.HasPrefix(m, "N5K"),
		strings.HasPrefix(m, "N7K"), strings.HasPrefix(m, "N9K"):
		return RoleL3Switch
	}

	// Pure L2 access — Catalyst 2960, 9200 / 9200L (access-layer only),
	// WS-C29xx. Also matches SG / SF small-business switches.
	switch {
	case strings.HasPrefix(m, "C2960"),
		strings.HasPrefix(m, "WS-C29"),
		strings.HasPrefix(m, "C9200"), // 9200 and 9200L are access-only
		strings.HasPrefix(m, "SG"), strings.HasPrefix(m, "SF"):
		return RoleSwitch
	}

	return RoleUnknown
}
