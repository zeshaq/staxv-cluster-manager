// Virtual Media — BMC-side ISO mounting for bare-metal OS install.
//
// Redfish VirtualMedia lives under /Managers/{id}/VirtualMedia/{slot}.
// A BMC typically exposes several slots — Floppy, USBStick, CD, DVD —
// each with distinct MediaTypes. For OS-install ISOs we want a slot
// that advertises CD or DVD in its MediaTypes list; helpers below
// auto-pick the first CD/DVD-capable slot when the caller doesn't
// specify one.
//
// Boot-from-ISO is a three-step dance:
//
//  1. InsertMedia on a CD/DVD slot with the ISO URL.
//  2. PATCH the System's Boot.BootSourceOverrideTarget=Cd + Enabled=Once.
//  3. ComputerSystem.Reset (ForceRestart or On, depending on PowerState).
//
// Each step is exposed separately so the UI can show status between
// steps (mount, then "boot override set, restarting…") rather than one
// opaque 30-second request.

package redfish

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// VMSlot is our normalized view of a Redfish VirtualMedia resource —
// one "slot" on a BMC. A BMC exposes several (Floppy, USB, CD/DVD);
// we surface just the fields the handler/UI need.
type VMSlot struct {
	// ID from the Redfish resource (@odata.id basename). Stable per-BMC
	// identifier we pass back to mount/eject.
	ID string `json:"id"`

	// Name is the human-readable name the BMC reports. iLO calls them
	// "iLO Virtual Media CD Device", iDRAC "Virtual CD", etc.
	Name string `json:"name"`

	// MediaTypes is the raw list the BMC advertises — one or more of
	// "Floppy", "USBStick", "CD", "DVD". The UI filters on this to show
	// only ISO-capable slots in the "Boot from ISO" picker.
	MediaTypes []string `json:"media_types"`

	// Inserted and Image together describe the mount state. When
	// Inserted=true, Image is the URL the BMC is currently pulling from.
	Inserted bool   `json:"inserted"`
	Image    string `json:"image,omitempty"`

	// ConnectedVia — Redfish enum: "NotConnected" | "URI" | "Applet" |
	// "OEM". For HTTP-URL mounts from our serve route this is "URI".
	ConnectedVia string `json:"connected_via,omitempty"`

	// WriteProtected — BMCs that allow R/W virtual media surface this.
	// We always request WriteProtected=true in Insert; a BMC reporting
	// false here means it's exposing a writable virtual disk we don't
	// use.
	WriteProtected bool `json:"write_protected"`

	// insertTarget / ejectTarget are the action URLs we POST to. Kept
	// lowercased so they don't leak into JSON responses — internal
	// routing only.
	insertTarget string `json:"-"`
	ejectTarget  string `json:"-"`
}

// SupportsISO returns true when at least one of MediaTypes is CD or
// DVD (or the legacy "CDROM" some older BMCs report). These are the
// slots that accept an ISO URL via InsertMedia.
func (s *VMSlot) SupportsISO() bool {
	for _, m := range s.MediaTypes {
		switch strings.ToUpper(m) {
		case "CD", "DVD", "CDROM":
			return true
		}
	}
	return false
}

// vmResource is the raw Redfish resource under /VirtualMedia/{slot}.
// Captured as a private decode target; we project into VMSlot above.
type vmResource struct {
	ID             string   `json:"Id"`
	Name           string   `json:"Name"`
	MediaTypes     []string `json:"MediaTypes"`
	Image          string   `json:"Image"`
	Inserted       bool     `json:"Inserted"`
	ConnectedVia   string   `json:"ConnectedVia"`
	WriteProtected bool     `json:"WriteProtected"`

	Actions struct {
		Insert struct {
			Target string `json:"target"`
		} `json:"#VirtualMedia.InsertMedia"`
		Eject struct {
			Target string `json:"target"`
		} `json:"#VirtualMedia.EjectMedia"`
	} `json:"Actions"`
}

// VirtualMedia lists every virtual-media slot exposed by the BMC.
// Returns an empty slice (not nil) when the BMC has no Managers or the
// Managers collection is empty — distinct-from-error for the caller.
//
// One GET for the Manager + one for the VirtualMedia collection + one
// per slot. Typical BMCs expose 2-4 slots; whole call is sub-second
// warm, a few seconds cold.
func (c *Client) VirtualMedia(ctx context.Context) ([]VMSlot, error) {
	managerURL, err := c.firstManagerURL(ctx)
	if err != nil {
		return nil, err
	}
	if managerURL == "" {
		return []VMSlot{}, nil
	}

	// Walk the Manager's VirtualMedia link. Most BMCs expose it at
	// {manager}/VirtualMedia; we read it from the Manager doc to stay
	// honest to whatever the BMC publishes.
	var mgr struct {
		VirtualMedia odata `json:"VirtualMedia"`
	}
	if err := c.getJSON(ctx, c.absURL(managerURL), &mgr); err != nil {
		return nil, fmt.Errorf("manager: %w", err)
	}
	if mgr.VirtualMedia.ID == "" {
		return []VMSlot{}, nil
	}

	var coll collection
	if err := c.getJSON(ctx, c.absURL(mgr.VirtualMedia.ID), &coll); err != nil {
		return nil, fmt.Errorf("virtual media collection: %w", err)
	}

	out := make([]VMSlot, 0, len(coll.Members))
	for _, m := range coll.Members {
		memberURL := strings.TrimRight(m.ID, "/")
		var vm vmResource
		if err := c.getJSON(ctx, c.absURL(memberURL), &vm); err != nil {
			// Skip a single misbehaving slot rather than failing the
			// whole list — enumeration is best-effort.
			continue
		}
		id := vm.ID
		if id == "" {
			// Derive from the URL basename if the BMC omits Id.
			id = memberURL[strings.LastIndex(memberURL, "/")+1:]
		}
		out = append(out, VMSlot{
			ID:             id,
			Name:           vm.Name,
			MediaTypes:     vm.MediaTypes,
			Inserted:       vm.Inserted,
			Image:          vm.Image,
			ConnectedVia:   vm.ConnectedVia,
			WriteProtected: vm.WriteProtected,
			insertTarget:   vm.Actions.Insert.Target,
			ejectTarget:    vm.Actions.Eject.Target,
		})
	}
	return out, nil
}

// findVMSlot returns the raw resource (with action targets) for a given
// slot id — the public slice from VirtualMedia() omits action targets
// to keep them out of JSON responses. Re-GETs instead of threading
// private state through the UI.
func (c *Client) findVMSlot(ctx context.Context, wantID string) (*VMSlot, error) {
	slots, err := c.VirtualMedia(ctx)
	if err != nil {
		return nil, err
	}
	// Empty wantID → auto-pick: first CD/DVD-capable slot.
	if wantID == "" {
		for i := range slots {
			if slots[i].SupportsISO() {
				return &slots[i], nil
			}
		}
		return nil, errors.New("redfish: BMC exposes no CD/DVD-capable virtual media slot")
	}
	for i := range slots {
		if slots[i].ID == wantID {
			return &slots[i], nil
		}
	}
	return nil, fmt.Errorf("redfish: virtual media slot %q not found", wantID)
}

// InsertMedia mounts the given HTTP(S) ISO URL on the named slot (or
// auto-picked CD/DVD slot when slot is "").
//
// Body shape the BMC expects:
//
//	{
//	  "Image": "http://cm.example/iso/42/ubuntu.iso",
//	  "TransferProtocolType": "HTTP",   // or HTTPS; optional per spec, required by some iLOs
//	  "Inserted": true,
//	  "WriteProtected": true
//	}
//
// Returns an HTTPError from the BMC on failure — common ones: slot
// already has media (409), URL unreachable from the BMC's network
// (500 with a descriptive body), file not an ISO (varies).
func (c *Client) InsertMedia(ctx context.Context, slot, imageURL string) error {
	if imageURL == "" {
		return errors.New("redfish: empty image URL")
	}
	s, err := c.findVMSlot(ctx, slot)
	if err != nil {
		return err
	}
	target := s.insertTarget
	if target == "" {
		// Spec-compliant fallback — most BMCs honor this URL even when
		// they don't advertise it in Actions.
		target = fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s/Actions/VirtualMedia.InsertMedia",
			firstSegment(s.ID), s.ID)
	}
	proto := "HTTP"
	if strings.HasPrefix(strings.ToLower(imageURL), "https:") {
		proto = "HTTPS"
	}
	body := map[string]any{
		"Image":                imageURL,
		"TransferProtocolType": proto,
		"Inserted":             true,
		"WriteProtected":       true,
	}
	if err := c.postJSON(ctx, c.absURL(target), body); err != nil {
		return fmt.Errorf("insert media: %w", err)
	}
	return nil
}

// EjectMedia dismounts whatever's on the given slot. Empty body POST.
func (c *Client) EjectMedia(ctx context.Context, slot string) error {
	s, err := c.findVMSlot(ctx, slot)
	if err != nil {
		return err
	}
	target := s.ejectTarget
	if target == "" {
		target = fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s/Actions/VirtualMedia.EjectMedia",
			firstSegment(s.ID), s.ID)
	}
	// Redfish spec says the body is empty but some BMCs 400 on a nil
	// body; send an empty object as a safer "no args" signal.
	if err := c.postJSON(ctx, c.absURL(target), map[string]any{}); err != nil {
		return fmt.Errorf("eject media: %w", err)
	}
	return nil
}

// SetBootOverride writes a one-shot boot override to the first System
// so the next restart boots from `target` (typically "Cd" for our
// virtual CD, "Pxe" for network install, "Hdd" etc.).
//
// enabled: "Once" (one boot, then revert), "Continuous" (sticks until
// disabled), or "Disabled" (clear a prior override). "Once" is the
// right choice for OS-install flows — once the installer writes to
// disk and reboots, the BMC reverts to the normal boot order.
func (c *Client) SetBootOverride(ctx context.Context, target, enabled string) error {
	if target == "" || enabled == "" {
		return errors.New("redfish: boot override target/enabled required")
	}
	systemURL, err := c.firstSystemURL(ctx)
	if err != nil {
		return err
	}
	if systemURL == "" {
		return errors.New("redfish: no ComputerSystem on this BMC")
	}
	body := map[string]any{
		"Boot": map[string]any{
			"BootSourceOverrideTarget":  target,
			"BootSourceOverrideEnabled": enabled,
		},
	}
	if err := c.patchJSON(ctx, c.absURL(systemURL), body); err != nil {
		return fmt.Errorf("boot override: %w", err)
	}
	return nil
}

// firstSegment returns the "manager id" inferred from a slot id — used
// only when a BMC failed to surface Actions.target so we have to
// construct the spec-compliant path by hand. If the slot id doesn't
// encode a manager prefix, "1" is a common default (HPE, Dell both
// name their primary manager "1" or "iDRAC.Embedded.1").
func firstSegment(slotID string) string {
	if i := strings.IndexByte(slotID, '/'); i > 0 {
		return slotID[:i]
	}
	return "1"
}
