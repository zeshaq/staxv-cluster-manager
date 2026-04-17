// hardware.go — hardware-inventory drill-down for a ComputerSystem.
//
// Each method walks Systems/{id}/{Processors,Memory,Storage,EthernetInterfaces}
// and returns the per-member detail. These are separate from Probe()
// because they're only needed when the admin actually expands the
// "Hardware Inventory" section on the detail page — saving 4–20 GETs
// per server load otherwise.
//
// Vendor quirks
// ─────────────
// - HPE iLO populates most fields cleanly.
// - Dell iDRAC sometimes returns blanks for Processor.Model and uses
//   "@Redfish.Settings" side-tables for Memory details.
// - Older BMCs (pre-Redfish 1.4) may not expose Storage at all.
//
// We treat every field as best-effort. A missing field renders as "—"
// upstream rather than an error — the admin just won't see that datum
// on that box. Only a complete transport/auth/JSON failure bubbles up.

package redfish

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Processor is one CPU socket (not a core or a thread).
type Processor struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Model          string `json:"model"`
	Manufacturer   string `json:"manufacturer"`
	ProcessorType  string `json:"processor_type"` // "CPU" / "GPU" / "FPGA" / "Accelerator"
	InstructionSet string `json:"instruction_set"`
	TotalCores     int    `json:"total_cores"`
	TotalThreads   int    `json:"total_threads"`
	MaxSpeedMHz    int    `json:"max_speed_mhz"`
	Health         string `json:"health"`
	State          string `json:"state"`
}

// DIMM is one memory module slot. Absent = empty slot; Present+no CapacityMiB
// = slot known to exist but no DIMM installed (BMCs vary).
type DIMM struct {
	ID                string `json:"id"`
	Name              string `json:"name"`                // often "DIMM 1" etc.
	CapacityMiB       int    `json:"capacity_mib"`        // 0 = empty slot
	MemoryDeviceType  string `json:"memory_device_type"`  // "DDR4" / "DDR5" / "DDR3"
	Manufacturer      string `json:"manufacturer"`
	PartNumber        string `json:"part_number"`
	SerialNumber      string `json:"serial_number"`
	OperatingSpeedMHz int    `json:"operating_speed_mhz"`
	DataWidthBits     int    `json:"data_width_bits"`
	RankCount         int    `json:"rank_count"`
	Health            string `json:"health"`
	State             string `json:"state"`
}

// Drive is one physical disk. Reached via Storage controllers, not a
// flat list — BMCs expose disks nested under their controller.
type Drive struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Model           string `json:"model"`
	Manufacturer    string `json:"manufacturer"`
	SerialNumber    string `json:"serial_number"`
	MediaType       string `json:"media_type"`       // "HDD" / "SSD"
	Protocol        string `json:"protocol"`         // "SAS" / "SATA" / "NVMe"
	CapacityBytes   int64  `json:"capacity_bytes"`
	RotationSpeedRPM int   `json:"rotation_speed_rpm,omitempty"`
	Health          string `json:"health"`
	State           string `json:"state"`
	Controller      string `json:"controller"` // friendly parent controller name
}

// NIC is one ethernet interface (physical adapter + MAC).
type NIC struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	MACAddress  string   `json:"mac_address"`
	SpeedMbps   int      `json:"speed_mbps"`
	LinkStatus  string   `json:"link_status"`   // "LinkUp" / "LinkDown" / "NoLink"
	IPv4        []string `json:"ipv4,omitempty"`
	Health      string   `json:"health"`
	State       string   `json:"state"`
}

// Hardware is the combined inventory returned to the API layer. Each
// slice may be nil + a per-collection error — a BMC that rejects the
// Storage call still yields valid Processors/Memory/NICs.
type Hardware struct {
	Processors []Processor `json:"processors"`
	Memory     []DIMM      `json:"memory"`
	Drives     []Drive     `json:"drives"`
	NICs       []NIC       `json:"nics"`

	// Per-collection errors. Non-nil means that specific collection
	// failed; the field above is zero-length. Admin UI renders each
	// section independently and shows its own error on failure.
	ProcessorsErr string `json:"processors_error,omitempty"`
	MemoryErr     string `json:"memory_error,omitempty"`
	DrivesErr     string `json:"drives_error,omitempty"`
	NICsErr       string `json:"nics_error,omitempty"`
}

// Processors returns every CPU/GPU/accelerator socket on the system.
// Fetches /Processors collection, then each member in parallel — a
// 2-socket box is 3 round-trips (collection + 2 members) and runs in
// roughly one member-GET's latency.
func (c *Client) Processors(ctx context.Context) ([]Processor, error) {
	systemURL, err := c.firstSystemURL(ctx)
	if err != nil {
		return nil, err
	}
	if systemURL == "" {
		return nil, nil
	}

	var coll collection
	if err := c.getJSON(ctx, c.absURL(systemURL+"/Processors"), &coll); err != nil {
		return nil, fmt.Errorf("processors collection: %w", err)
	}

	type rawProcessor struct {
		ID             string `json:"Id"`
		Name           string `json:"Name"`
		Model          string `json:"Model"`
		Manufacturer   string `json:"Manufacturer"`
		ProcessorType  string `json:"ProcessorType"`
		InstructionSet string `json:"InstructionSet"`
		TotalCores     int    `json:"TotalCores"`
		TotalThreads   int    `json:"TotalThreads"`
		MaxSpeedMHz    int    `json:"MaxSpeedMHz"`
		Status         struct {
			Health string `json:"Health"`
			State  string `json:"State"`
		} `json:"Status"`
	}

	out := make([]Processor, len(coll.Members))
	fetchMembers(ctx, c, coll.Members, func(i int, path string) {
		var p rawProcessor
		if err := c.getJSON(ctx, c.absURL(path), &p); err != nil {
			// Single-member failure: keep an obviously-partial entry
			// rather than dropping. Admin sees the blank row and the
			// per-collection error above.
			out[i] = Processor{ID: lastSeg(path)}
			return
		}
		out[i] = Processor{
			ID: p.ID, Name: p.Name, Model: strings.TrimSpace(p.Model),
			Manufacturer: p.Manufacturer, ProcessorType: p.ProcessorType,
			InstructionSet: p.InstructionSet,
			TotalCores: p.TotalCores, TotalThreads: p.TotalThreads,
			MaxSpeedMHz: p.MaxSpeedMHz,
			Health: p.Status.Health, State: p.Status.State,
		}
	})
	return out, nil
}

// Memory returns every DIMM slot on the system — populated and empty.
// Empty slots show CapacityMiB=0; the UI can filter or render them
// as "slot N: empty".
func (c *Client) Memory(ctx context.Context) ([]DIMM, error) {
	systemURL, err := c.firstSystemURL(ctx)
	if err != nil {
		return nil, err
	}
	if systemURL == "" {
		return nil, nil
	}

	var coll collection
	if err := c.getJSON(ctx, c.absURL(systemURL+"/Memory"), &coll); err != nil {
		return nil, fmt.Errorf("memory collection: %w", err)
	}

	type rawMemory struct {
		ID                string `json:"Id"`
		Name              string `json:"Name"`
		CapacityMiB       int    `json:"CapacityMiB"`
		MemoryDeviceType  string `json:"MemoryDeviceType"`
		Manufacturer      string `json:"Manufacturer"`
		PartNumber        string `json:"PartNumber"`
		SerialNumber      string `json:"SerialNumber"`
		OperatingSpeedMhz int    `json:"OperatingSpeedMhz"`
		DataWidthBits     int    `json:"DataWidthBits"`
		RankCount         int    `json:"RankCount"`
		Status            struct {
			Health string `json:"Health"`
			State  string `json:"State"`
		} `json:"Status"`
	}

	out := make([]DIMM, len(coll.Members))
	fetchMembers(ctx, c, coll.Members, func(i int, path string) {
		var m rawMemory
		if err := c.getJSON(ctx, c.absURL(path), &m); err != nil {
			out[i] = DIMM{ID: lastSeg(path)}
			return
		}
		out[i] = DIMM{
			ID: m.ID, Name: m.Name, CapacityMiB: m.CapacityMiB,
			MemoryDeviceType:  m.MemoryDeviceType,
			Manufacturer:      strings.TrimSpace(m.Manufacturer),
			PartNumber:        strings.TrimSpace(m.PartNumber),
			SerialNumber:      strings.TrimSpace(m.SerialNumber),
			OperatingSpeedMHz: m.OperatingSpeedMhz,
			DataWidthBits:     m.DataWidthBits,
			RankCount:         m.RankCount,
			Health:            m.Status.Health,
			State:             m.Status.State,
		}
	})
	return out, nil
}

// Drives returns every physical disk across every storage controller.
// Storage controllers are an extra layer: /Storage → per-controller →
// Drives[] of @odata.id refs. We flatten into one list but stash the
// parent controller name for display.
func (c *Client) Drives(ctx context.Context) ([]Drive, error) {
	systemURL, err := c.firstSystemURL(ctx)
	if err != nil {
		return nil, err
	}
	if systemURL == "" {
		return nil, nil
	}

	var storageColl collection
	if err := c.getJSON(ctx, c.absURL(systemURL+"/Storage"), &storageColl); err != nil {
		return nil, fmt.Errorf("storage collection: %w", err)
	}

	// Each controller → list of Drive refs. Fetch controllers in parallel
	// first to get the Drives arrays, then fetch drives in parallel.
	type rawStorage struct {
		ID     string  `json:"Id"`
		Name   string  `json:"Name"`
		Drives []odata `json:"Drives"`
	}

	controllers := make([]rawStorage, len(storageColl.Members))
	fetchMembers(ctx, c, storageColl.Members, func(i int, path string) {
		var s rawStorage
		if err := c.getJSON(ctx, c.absURL(path), &s); err != nil {
			return
		}
		controllers[i] = s
	})

	// Flatten: (controller-name, drive-path) pairs.
	type pending struct {
		controller string
		path       string
	}
	var queue []pending
	for _, ctrl := range controllers {
		ctrlName := ctrl.Name
		if ctrlName == "" {
			ctrlName = ctrl.ID
		}
		for _, d := range ctrl.Drives {
			if d.ID == "" {
				continue
			}
			queue = append(queue, pending{controller: ctrlName, path: d.ID})
		}
	}

	type rawDrive struct {
		ID               string `json:"Id"`
		Name             string `json:"Name"`
		Model            string `json:"Model"`
		Manufacturer     string `json:"Manufacturer"`
		SerialNumber     string `json:"SerialNumber"`
		MediaType        string `json:"MediaType"`
		Protocol         string `json:"Protocol"`
		CapacityBytes    int64  `json:"CapacityBytes"`
		RotationSpeedRPM int    `json:"RotationSpeedRPM"`
		Status           struct {
			Health string `json:"Health"`
			State  string `json:"State"`
		} `json:"Status"`
	}

	out := make([]Drive, len(queue))
	var wg sync.WaitGroup
	for i, q := range queue {
		wg.Add(1)
		go func(i int, q pending) {
			defer wg.Done()
			var d rawDrive
			if err := c.getJSON(ctx, c.absURL(q.path), &d); err != nil {
				out[i] = Drive{ID: lastSeg(q.path), Controller: q.controller}
				return
			}
			out[i] = Drive{
				ID: d.ID, Name: d.Name, Model: strings.TrimSpace(d.Model),
				Manufacturer: strings.TrimSpace(d.Manufacturer),
				SerialNumber: strings.TrimSpace(d.SerialNumber),
				MediaType:    d.MediaType, Protocol: d.Protocol,
				CapacityBytes:    d.CapacityBytes,
				RotationSpeedRPM: d.RotationSpeedRPM,
				Health:           d.Status.Health, State: d.Status.State,
				Controller: q.controller,
			}
		}(i, q)
	}
	wg.Wait()
	return out, nil
}

// NetworkInterfaces returns every EthernetInterface on the system.
// Not "NetworkInterfaces" (Redfish naming schism — NetworkInterfaces
// describes higher-level Ethernet/FibreChannel adapters; EthernetInterfaces
// are the actual NICs with MACs and IPs we want for the UI).
func (c *Client) NetworkInterfaces(ctx context.Context) ([]NIC, error) {
	systemURL, err := c.firstSystemURL(ctx)
	if err != nil {
		return nil, err
	}
	if systemURL == "" {
		return nil, nil
	}

	var coll collection
	if err := c.getJSON(ctx, c.absURL(systemURL+"/EthernetInterfaces"), &coll); err != nil {
		return nil, fmt.Errorf("ethernet interfaces collection: %w", err)
	}

	type rawIPv4 struct {
		Address string `json:"Address"`
	}
	type rawNIC struct {
		ID            string    `json:"Id"`
		Name          string    `json:"Name"`
		MACAddress    string    `json:"MACAddress"`
		SpeedMbps     int       `json:"SpeedMbps"`
		LinkStatus    string    `json:"LinkStatus"`
		IPv4Addresses []rawIPv4 `json:"IPv4Addresses"`
		Status        struct {
			Health string `json:"Health"`
			State  string `json:"State"`
		} `json:"Status"`
	}

	out := make([]NIC, len(coll.Members))
	fetchMembers(ctx, c, coll.Members, func(i int, path string) {
		var n rawNIC
		if err := c.getJSON(ctx, c.absURL(path), &n); err != nil {
			out[i] = NIC{ID: lastSeg(path)}
			return
		}
		var ips []string
		for _, a := range n.IPv4Addresses {
			if a.Address != "" {
				ips = append(ips, a.Address)
			}
		}
		out[i] = NIC{
			ID: n.ID, Name: n.Name, MACAddress: n.MACAddress,
			SpeedMbps: n.SpeedMbps, LinkStatus: n.LinkStatus,
			IPv4:   ips,
			Health: n.Status.Health, State: n.Status.State,
		}
	})
	return out, nil
}

// fetchMembers runs fn for each @odata.id in parallel with a tight
// bounded fan-out. BMCs don't love 30+ concurrent requests — some start
// dropping connections. Cap at 8 in-flight.
func fetchMembers(ctx context.Context, c *Client, members []odata, fn func(i int, path string)) {
	const maxInFlight = 8
	sem := make(chan struct{}, maxInFlight)
	var wg sync.WaitGroup
	for i, m := range members {
		if m.ID == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i, path)
		}(i, m.ID)
	}
	wg.Wait()
	_ = ctx // retained for future cancellation-aware fan-out
}

// lastSeg returns the last path segment (e.g. "/redfish/v1/Systems/1/Processors/3" → "3").
// Used as an ID fallback when a single-member fetch fails but we still
// want to surface "something was here".
func lastSeg(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return path
}
