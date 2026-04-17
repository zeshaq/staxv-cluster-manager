package cisco

import "testing"

// Role autodetect tests — tracking what we expect to work for common
// Cisco SKUs. When a device in the field gets misclassified, add its
// model string here and adjust DetectRole to match; the test becomes
// the regression guard.
func TestDetectRole(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		// Routers
		{"ISR4321/K9", RoleRouter},
		{"ISR4431/K9", RoleRouter},
		{"ASR1001-X", RoleRouter},
		{"C8300-1N1S-6T", RoleRouter},
		{"2921/K9", RoleRouter},
		{"RV340", RoleRouter},

		// L3 switches
		{"WS-C3750X-48P", RoleL3Switch},
		{"WS-C3850-48T", RoleL3Switch},
		{"C9300-48P", RoleL3Switch},
		{"C9500-40X", RoleL3Switch},
		{"N9K-C93180YC-EX", RoleL3Switch},
		{"Nexus9000 C9336PQ", RoleL3Switch},

		// L2 switches
		{"WS-C2960-24TT-L", RoleSwitch},
		{"C9200L-48P-4G", RoleSwitch}, // access-only despite C9xxx
		{"SG350-28", RoleSwitch},

		// Firewalls
		{"ASA5506", RoleFirewall},
		{"FPR1120", RoleFirewall},

		// Unknown
		{"", RoleUnknown},
		{"something-weird", RoleUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := DetectRole(tc.model)
			if got != tc.want {
				t.Errorf("DetectRole(%q) = %q; want %q", tc.model, got, tc.want)
			}
		})
	}
}
