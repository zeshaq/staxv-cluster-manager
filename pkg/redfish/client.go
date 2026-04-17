// Package redfish is a minimal Redfish 1.x client — BMC reachability
// probe, common system fields, and power actions (ComputerSystem.Reset).
//
// Sensor readings, firmware, hardware-inventory drill-down, and
// vendor-OEM endpoints (HPE iLO, Dell iDRAC, Lenovo XCC extensions)
// are not here yet — they land as separate additions when needed.
//
// TLS
// ───
// BMCs nearly universally ship with self-signed TLS certs. We
// InsecureSkipVerify=true by default and document the risk. Operators
// who want proper TLS can generate a real cert on their BMC and flip
// a future `[redfish] verify_tls = true` knob.
package redfish

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout caps a single Redfish call. BMCs are slow;
// 15s covers most first-time SSL handshake + page fetch.
const DefaultTimeout = 15 * time.Second

// Client is a BMC-specific Redfish caller. One instance per server.
// Cheap to construct, safe for concurrent use (http.Client is).
type Client struct {
	baseURL  *url.URL
	username string
	password string
	http     *http.Client
}

// New builds a Client pointing at https://<host>:<port>/redfish/v1/.
// username/password are used for HTTP Basic auth — Redfish's required
// auth scheme.
func New(host string, port int, username, password string) *Client {
	if port == 0 {
		port = 443
	}
	u := &url.URL{Scheme: "https", Host: fmt.Sprintf("%s:%d", host, port), Path: "/redfish/v1/"}
	return &Client{
		baseURL:  u,
		username: username,
		password: password,
		http: &http.Client{
			Timeout: DefaultTimeout,
			Transport: &http.Transport{
				// BMC self-signed certs are the norm; document in the
				// package comment above. Not negotiable without operator
				// action.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}
}

// serviceRoot is the tiniest subset of the Redfish root document we
// need — just enough to confirm the endpoint really is Redfish and to
// follow the Systems collection link.
type serviceRoot struct {
	RedfishVersion string `json:"RedfishVersion"`
	Systems        odata  `json:"Systems"`
}

type odata struct {
	ID string `json:"@odata.id"`
}

type systemsCollection struct {
	Members []odata `json:"Members"`
}

// computerSystem is the subset of `ComputerSystem.v1_*_*.ComputerSystem`
// we care about. Other vendors extend this with OEM data; we ignore.
//
// All fields are best-effort: BMCs vary in what they populate. Empty
// strings / zero values are normal and the caller treats them as
// "unknown" (rendered as "—" in the UI).
type computerSystem struct {
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	SerialNumber string `json:"SerialNumber"`
	SKU          string `json:"SKU"` // Dell uses this; HPE uses SerialNumber
	PowerState   string `json:"PowerState"`
	BiosVersion  string `json:"BiosVersion"`
	HostName     string `json:"HostName"`

	Status struct {
		Health string `json:"Health"`
		State  string `json:"State"`
	} `json:"Status"`

	ProcessorSummary struct {
		Count int `json:"Count"`
	} `json:"ProcessorSummary"`

	MemorySummary struct {
		TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
	} `json:"MemorySummary"`
}

// SystemInfo is what a successful Probe returns — the fields the
// cluster-manager UI displays. Zero values mean "BMC didn't populate
// this" rather than an error, and render as "—" upstream.
type SystemInfo struct {
	Manufacturer string
	Model        string
	Serial       string // best-of-SerialNumber/SKU
	PowerState   string // "On" | "Off" | "PoweringOn" | "PoweringOff" | ""
	Health       string // "OK" | "Warning" | "Critical" | ""
	BIOSVersion  string
	Hostname     string
	CPUCount     int
	MemoryGB     int // rounded from TotalSystemMemoryGiB
}

// Probe attempts a reachability check:
//  1. GET /redfish/v1/             — confirms Redfish is listening + creds valid
//  2. GET <Systems.@odata.id>      — locates the Systems collection
//  3. GET <Members[0].@odata.id>   — first ComputerSystem (BMCs usually serve one)
//
// Returns the discovered SystemInfo on full success. Partial success
// (e.g. root works, systems call fails) returns SystemInfo with the
// fields we DID learn and the error — caller decides whether to treat
// it as "reachable" (root OK is enough for the admin) or "error".
//
// Network / TLS / timeout failures surface as their own error types
// so the caller can set status="unreachable" vs "error" appropriately.
func (c *Client) Probe(ctx context.Context) (*SystemInfo, error) {
	var root serviceRoot
	if err := c.getJSON(ctx, c.baseURL.String(), &root); err != nil {
		return nil, fmt.Errorf("service root: %w", err)
	}
	if root.RedfishVersion == "" && root.Systems.ID == "" {
		// Valid 200 but doesn't smell like Redfish. Rare — happens
		// with proxies or mis-pointed URLs.
		return nil, errors.New("endpoint returned 200 but doesn't appear to be a Redfish service")
	}

	// Systems collection URL — relative to host root, NOT the /redfish/v1/
	// base. Redfish returns paths like "/redfish/v1/Systems".
	info := &SystemInfo{}
	if root.Systems.ID == "" {
		return info, nil // alive but no systems list — weirdly empty but not an error
	}
	var coll systemsCollection
	if err := c.getJSON(ctx, c.absURL(root.Systems.ID), &coll); err != nil {
		// Root worked; collection failed — return what we have as a
		// partial success rather than dropping reachability entirely.
		return info, fmt.Errorf("systems collection: %w", err)
	}
	if len(coll.Members) == 0 {
		return info, nil
	}

	var sys computerSystem
	if err := c.getJSON(ctx, c.absURL(coll.Members[0].ID), &sys); err != nil {
		return info, fmt.Errorf("computer system: %w", err)
	}
	info.Manufacturer = sys.Manufacturer
	info.Model = sys.Model
	info.Serial = sys.SerialNumber
	if info.Serial == "" {
		info.Serial = sys.SKU // Dell fallback
	}
	info.PowerState = sys.PowerState
	info.Health = sys.Status.Health
	info.BIOSVersion = sys.BiosVersion
	info.Hostname = sys.HostName
	info.CPUCount = sys.ProcessorSummary.Count
	// Round TotalSystemMemoryGiB to an integer. Some BMCs report
	// fractional GiB (e.g. 1535.875 for 1.5 TiB of DIMMs); integer is
	// plenty for the list/detail UI.
	if sys.MemorySummary.TotalSystemMemoryGiB > 0 {
		info.MemoryGB = int(sys.MemorySummary.TotalSystemMemoryGiB + 0.5)
	}
	return info, nil
}

// absURL resolves a Redfish-relative path ("/redfish/v1/Systems") to
// the full URL on the BMC's host.
func (c *Client) absURL(path string) string {
	u := *c.baseURL
	u.Path = path
	return u.String()
}

// getJSON fetches + decodes a JSON resource with basic auth and the
// context timeout. Maps non-2xx to a typed error so the caller can
// distinguish auth (401), not-found (404), etc.
func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0") // Redfish mandates this on writes; harmless on GETs

	resp, err := c.http.Do(req)
	if err != nil {
		return &NetError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// postJSON sends a JSON body with basic auth, returns an error on
// non-2xx. The Redfish Reset action returns 204 No Content on success
// on most BMCs, but some return 200 + a task reference — we accept
// any 2xx as success and don't parse the response body.
func (c *Client) postJSON(ctx context.Context, rawURL string, body any) error {
	buf := new(bytes.Buffer)
	if body != nil {
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", rawURL, buf)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OData-Version", "4.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return &NetError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &HTTPError{Status: resp.StatusCode, Body: string(respBody)}
	}
	return nil
}

// ResetType values supported by the Redfish spec. We expose the common
// subset upstream; other values are server-side-allowlisted so an
// operator can add uncommon ones like PowerCycle / Nmi without a
// code change.
const (
	ResetOn               = "On"
	ResetForceOff         = "ForceOff"
	ResetGracefulShutdown = "GracefulShutdown"
	ResetGracefulRestart  = "GracefulRestart"
	ResetForceRestart     = "ForceRestart"
	ResetNmi              = "Nmi"
	ResetPowerCycle       = "PowerCycle"
	ResetPushPowerButton  = "PushPowerButton"
)

// PowerAction fires a Redfish ComputerSystem.Reset with the given
// resetType (one of the ResetXxx constants above).
//
// Flow:
//  1. Service root → Systems collection → first system URL.
//  2. GET the system to read its Actions."#ComputerSystem.Reset"
//     target. Some BMCs under-populate Actions; fall back to the
//     spec-compliant path "<system>/Actions/ComputerSystem.Reset".
//  3. If the BMC advertises AllowableValues, validate the requested
//     resetType against that list first so we fail fast with a useful
//     message instead of a cryptic 400 from the BMC.
//  4. POST {"ResetType": <value>} to the target.
//
// BMCs typically return 204 within a second or two; the actual power
// state change (POST, OS boot, ACPI shutdown) takes longer. Callers
// that want fresh state should re-probe after this returns.
func (c *Client) PowerAction(ctx context.Context, resetType string) error {
	if resetType == "" {
		return errors.New("redfish: empty reset type")
	}

	// Walk to the System resource (reuses the same path as Probe).
	var root serviceRoot
	if err := c.getJSON(ctx, c.baseURL.String(), &root); err != nil {
		return fmt.Errorf("service root: %w", err)
	}
	if root.Systems.ID == "" {
		return errors.New("redfish: no Systems collection on this BMC")
	}
	var coll systemsCollection
	if err := c.getJSON(ctx, c.absURL(root.Systems.ID), &coll); err != nil {
		return fmt.Errorf("systems collection: %w", err)
	}
	if len(coll.Members) == 0 {
		return errors.New("redfish: Systems collection is empty")
	}
	systemURL := coll.Members[0].ID

	// Read the system's Actions block. We only need the Reset action
	// target + allowed values, so keep the struct tight.
	var sys struct {
		Actions struct {
			Reset struct {
				Target        string   `json:"target"`
				AllowedValues []string `json:"ResetType@Redfish.AllowableValues"`
			} `json:"#ComputerSystem.Reset"`
		} `json:"Actions"`
	}
	if err := c.getJSON(ctx, c.absURL(systemURL), &sys); err != nil {
		return fmt.Errorf("system for reset: %w", err)
	}

	target := sys.Actions.Reset.Target
	if target == "" {
		// Spec-compliant fallback. Most BMCs honor this even without
		// advertising it.
		target = systemURL + "/Actions/ComputerSystem.Reset"
	}

	// Pre-flight against the BMC's declared allow-list if available —
	// catches "my iLO doesn't support Nmi" locally rather than
	// producing a 400 downstream.
	if len(sys.Actions.Reset.AllowedValues) > 0 {
		supported := false
		for _, v := range sys.Actions.Reset.AllowedValues {
			if v == resetType {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("redfish: BMC does not support ResetType=%q (supported: %v)",
				resetType, sys.Actions.Reset.AllowedValues)
		}
	}

	body := map[string]string{"ResetType": resetType}
	if err := c.postJSON(ctx, c.absURL(target), body); err != nil {
		return fmt.Errorf("reset %s: %w", resetType, err)
	}
	return nil
}

// Typed errors so the handler can map them to HTTP status codes
// cleanly (auth failure → 401 back to the admin; network → 503; etc.).

type NetError struct{ Err error }

func (e *NetError) Error() string { return "network: " + e.Err.Error() }
func (e *NetError) Unwrap() error { return e.Err }

type AuthError struct{ Status int }

func (e *AuthError) Error() string { return fmt.Sprintf("auth: HTTP %d (check username / password)", e.Status) }

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}
