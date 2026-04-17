package cisco

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseShowVLANBrief(t *testing.T) {
	// Representative Catalyst 2960 output — trailing default VLANs
	// intentionally included so the parser tolerates them.
	input := `VLAN Name                             Status    Ports
---- -------------------------------- --------- -------------------------------
1    default                          active    Gi0/1, Gi0/2, Gi0/3, Gi0/4
                                                Gi0/5, Gi0/6
10   DATA                             active    Gi0/10, Gi0/11
20   VOICE                            active
1002 fddi-default                     act/unsup
1003 token-ring-default               act/unsup
1004 fddinet-default                  act/unsup
1005 trnet-default                    act/unsup
`
	got := parseShowVLANBrief(input)
	want := []VLAN{
		{ID: 1, Name: "default", Status: "active",
			Ports: []string{"Gi0/1", "Gi0/2", "Gi0/3", "Gi0/4", "Gi0/5", "Gi0/6"}},
		{ID: 10, Name: "DATA", Status: "active",
			Ports: []string{"Gi0/10", "Gi0/11"}},
		{ID: 20, Name: "VOICE", Status: "active", Ports: nil},
		{ID: 1002, Name: "fddi-default", Status: "act/unsup", Ports: nil},
		{ID: 1003, Name: "token-ring-default", Status: "act/unsup", Ports: nil},
		{ID: 1004, Name: "fddinet-default", Status: "act/unsup", Ports: nil},
		{ID: 1005, Name: "trnet-default", Status: "act/unsup", Ports: nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseShowVLANBrief mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseShowVLANBriefEmpty(t *testing.T) {
	if got := parseShowVLANBrief(""); len(got) != 0 {
		t.Errorf("empty input → %#v, want empty", got)
	}
	// Devices without VLAN support return "% Invalid input detected"
	// — the library caller (VLANs()) short-circuits before getting
	// here, but test the parser tolerates junk.
	if got := parseShowVLANBrief("% Invalid input detected at '^' marker."); len(got) != 0 {
		t.Errorf("error string → %#v, want empty", got)
	}
}

func TestHasIOSError(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"VLAN 100 created\n", false},
		{"% Invalid input detected at '^' marker.\n", true},
		{"  ^\n% Ambiguous command:  \"show runn\"\n", true},
		{"Error: something went wrong", true},
		{"normal output with a % symbol in it", false},
	}
	for _, tc := range cases {
		got, _ := HasIOSError(tc.in)
		if got != tc.wantErr {
			t.Errorf("HasIOSError(%q) = %v, want %v", tc.in, got, tc.wantErr)
		}
	}
}

func TestVLANNameRE(t *testing.T) {
	ok := []string{"DATA", "voice_10", "mgmt-10", "A", "0123456789012345678901234567890A"} // 32 chars
	bad := []string{"", "name with space", "name!", strings.Repeat("A", 33), "has/slash"}

	for _, n := range ok {
		if !VLANNameRE.MatchString(n) {
			t.Errorf("expected %q to be valid", n)
		}
	}
	for _, n := range bad {
		if VLANNameRE.MatchString(n) {
			t.Errorf("expected %q to be INvalid", n)
		}
	}
}
