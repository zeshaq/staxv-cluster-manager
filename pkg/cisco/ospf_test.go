package cisco

import (
	"reflect"
	"testing"
)

func TestParseRunningConfigRouterOSPF(t *testing.T) {
	input := `router ospf 1
 router-id 1.1.1.1
 log-adjacency-changes
 passive-interface default
 no passive-interface GigabitEthernet0/0
!
router ospf 10
 router-id 10.0.0.1
!
`
	got := parseRunningConfigRouterOSPF(input)
	want := []OSPFProcess{
		{PID: 1, RouterID: "1.1.1.1"},
		{PID: 10, RouterID: "10.0.0.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseShowIPOSPFInterfaceBrief(t *testing.T) {
	input := `Interface    PID   Area            IP Address/Mask    Cost  State Nbrs F/C
Gi0/0        1     0               10.0.0.1/30        1     P2P   1/1
Gi0/1        1     0               10.0.0.5/30        1     DR    1/1
Lo0          1     0.0.0.0         1.1.1.1/32         1     LOOP  0/0
Gi0/2        10    5               192.168.1.1/24     10    DROTHER 0/1
`
	got := parseShowIPOSPFInterfaceBrief(input)
	want := []OSPFInterface{
		{Name: "Gi0/0", PID: 1, Area: "0", IPCIDR: "10.0.0.1/30", Cost: 1, State: "P2P", NeighborsFC: "1/1"},
		{Name: "Gi0/1", PID: 1, Area: "0", IPCIDR: "10.0.0.5/30", Cost: 1, State: "DR", NeighborsFC: "1/1"},
		{Name: "Lo0", PID: 1, Area: "0.0.0.0", IPCIDR: "1.1.1.1/32", Cost: 1, State: "LOOP", NeighborsFC: "0/0"},
		{Name: "Gi0/2", PID: 10, Area: "5", IPCIDR: "192.168.1.1/24", Cost: 10, State: "DROTHER", NeighborsFC: "0/1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseShowIPOSPFNeighbor(t *testing.T) {
	input := `Neighbor ID     Pri   State           Dead Time   Address         Interface
2.2.2.2           1   FULL/BDR        00:00:35    10.0.0.2        GigabitEthernet0/0
3.3.3.3           1   FULL/DR         00:00:38    10.0.0.6        GigabitEthernet0/1
`
	got := parseShowIPOSPFNeighbor(input)
	want := []OSPFNeighbor{
		{NeighborID: "2.2.2.2", Priority: 1, State: "FULL/BDR", DeadTime: "00:00:35", Address: "10.0.0.2", Interface: "GigabitEthernet0/0"},
		{NeighborID: "3.3.3.3", Priority: 1, State: "FULL/DR", DeadTime: "00:00:38", Address: "10.0.0.6", Interface: "GigabitEthernet0/1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseInterfaceOSPFFromConfig(t *testing.T) {
	cases := []struct {
		name     string
		cfg      string
		wantPID  int
		wantArea string
		wantNet  string
	}{
		{
			name: "full config",
			cfg: `interface GigabitEthernet0/0
 description WAN link
 ip address 10.0.0.1 255.255.255.252
 ip ospf 1 area 0
 ip ospf network point-to-point
`,
			wantPID: 1, wantArea: "0", wantNet: "point-to-point",
		},
		{
			name: "membership only",
			cfg: `interface GigabitEthernet0/1
 ip ospf 10 area 5
`,
			wantPID: 10, wantArea: "5", wantNet: "",
		},
		{
			name:    "no ospf",
			cfg:     "interface Loopback0\n ip address 1.1.1.1 255.255.255.255\n",
			wantPID: 0, wantArea: "", wantNet: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pid, area, net := parseInterfaceOSPFFromConfig(tc.cfg)
			if pid != tc.wantPID || area != tc.wantArea || net != tc.wantNet {
				t.Errorf("got (pid=%d, area=%q, net=%q); want (pid=%d, area=%q, net=%q)",
					pid, area, net, tc.wantPID, tc.wantArea, tc.wantNet)
			}
		})
	}
}

func TestValidateRouterID(t *testing.T) {
	ok := []string{"1.1.1.1", "10.0.0.1", "255.255.255.255", "0.0.0.1"}
	bad := []string{"", "1.1.1", "1.1.1.1.1", "not-an-ip", "::1", "256.0.0.1"}

	for _, id := range ok {
		if err := validateRouterID(id); err != nil {
			t.Errorf("expected %q valid, got %v", id, err)
		}
	}
	for _, id := range bad {
		if err := validateRouterID(id); err == nil {
			t.Errorf("expected %q INvalid", id)
		}
	}
}

func TestUniqueAreasForPID(t *testing.T) {
	ifs := []OSPFInterface{
		{Name: "Gi0/0", PID: 1, Area: "0"},
		{Name: "Gi0/1", PID: 1, Area: "0"},
		{Name: "Gi0/2", PID: 1, Area: "5"},
		{Name: "Gi0/3", PID: 2, Area: "10"},
	}
	if got := uniqueAreasForPID(ifs, 1); !reflect.DeepEqual(got, []string{"0", "5"}) {
		t.Errorf("pid 1 → %v", got)
	}
	if got := uniqueAreasForPID(ifs, 2); !reflect.DeepEqual(got, []string{"10"}) {
		t.Errorf("pid 2 → %v", got)
	}
	if got := uniqueAreasForPID(ifs, 99); len(got) != 0 {
		t.Errorf("pid 99 → %v, want empty", got)
	}
}
