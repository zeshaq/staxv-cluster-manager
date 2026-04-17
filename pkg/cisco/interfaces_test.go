package cisco

import (
	"reflect"
	"testing"
)

func TestParseShowInterfacesDescription(t *testing.T) {
	input := `Interface                      Status         Protocol Description
Gi0/0                          up             up       Uplink to core
Gi0/1                          admin down     down
Gi0/2                          up             up       Rack 1 TOR
Vl10                           up             up       Data VLAN
Lo0                            up             up
`
	got := parseShowInterfacesDescription(input)
	want := map[string]string{
		"Gi0/0": "Uplink to core",
		"Gi0/2": "Rack 1 TOR",
		"Vl10":  "Data VLAN",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseShowInterfacesDescription mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseShowInterfacesDescriptionEmpty(t *testing.T) {
	// Unfamiliar device that rejects the command — we get an IOS
	// error string instead of the table. Parser should return an
	// empty map, not panic.
	cases := []string{
		"",
		"% Invalid input detected at '^' marker.\n",
		"some random non-table garbage",
	}
	for _, tc := range cases {
		got := parseShowInterfacesDescription(tc)
		if len(got) != 0 {
			t.Errorf("expected empty on %q, got %#v", tc, got)
		}
	}
}

func TestCIDRToIOS(t *testing.T) {
	cases := []struct {
		cidr    string
		ip      string
		mask    string
		wantErr bool
	}{
		{"10.1.1.1/24", "10.1.1.1", "255.255.255.0", false},
		{"192.168.0.1/16", "192.168.0.1", "255.255.0.0", false},
		{"172.16.0.1/12", "172.16.0.1", "255.240.0.0", false},
		{"10.0.0.1/31", "10.0.0.1", "255.255.255.254", false},
		{"10.0.0.0/32", "10.0.0.0", "255.255.255.255", false},

		// Bad shapes
		{"10.1.1.1", "", "", true},           // no mask
		{"10.1.1.1/33", "", "", true},        // mask too big
		{"not-an-ip/24", "", "", true},       // invalid IP
		{"::1/128", "", "", true},            // IPv6 not supported
		{"0.0.0.0/0", "", "", true},          // refuse default route
	}

	for _, tc := range cases {
		ip, mask, err := cidrToIOS(tc.cidr)
		if tc.wantErr {
			if err == nil {
				t.Errorf("cidrToIOS(%q) expected error, got (%q, %q)", tc.cidr, ip, mask)
			}
			continue
		}
		if err != nil {
			t.Errorf("cidrToIOS(%q) unexpected error: %v", tc.cidr, err)
			continue
		}
		if ip != tc.ip || mask != tc.mask {
			t.Errorf("cidrToIOS(%q) = (%q, %q); want (%q, %q)", tc.cidr, ip, mask, tc.ip, tc.mask)
		}
	}
}

func TestInterfaceNameRE(t *testing.T) {
	ok := []string{
		"GigabitEthernet0/0",
		"Gi0/1",
		"TenGigabitEthernet1/0/1",
		"Te1/0/1",
		"Gi0/0.100",       // subinterface
		"Vlan10", "Vl10",
		"Loopback0", "Lo0",
		"Tunnel1", "Tu1",
		"Port-channel1", "Po1",
	}
	bad := []string{
		"",
		"0/0",         // must start with a letter
		"Gi 0/0",      // no spaces
		"Gi0/0;reboot", // no shell injection characters
		"Gi0/0\n",
	}
	for _, n := range ok {
		if !InterfaceNameRE.MatchString(n) {
			t.Errorf("expected %q to be valid", n)
		}
	}
	for _, n := range bad {
		if InterfaceNameRE.MatchString(n) {
			t.Errorf("expected %q to be INvalid", n)
		}
	}
}
