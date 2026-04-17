// Package redfish is a minimal Redfish 1.x client — just enough for
// staxv-cluster-manager to probe a BMC for reachability and pull the
// common system fields (Manufacturer / Model / SerialNumber).
//
// Power control, sensor readings, firmware, and vendor-OEM endpoints
// (HPE iLO, Dell iDRAC, Lenovo XCC extensions) are intentionally not
// here yet — they land as separate additions when needed.
//
// TLS
// ───
// BMCs nearly universally ship with self-signed TLS certs. We
// InsecureSkipVerify=true by default and document the risk. Operators
// who want proper TLS can generate a real cert on their BMC and flip
// a future `[redfish] verify_tls = true` knob.
package redfish

import (
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
type computerSystem struct {
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	SerialNumber string `json:"SerialNumber"`
	SKU          string `json:"SKU"` // Dell uses this; HPE uses SerialNumber
}

// SystemInfo is what a successful Probe returns — the fields the
// cluster-manager UI actually displays.
type SystemInfo struct {
	Manufacturer string
	Model        string
	Serial       string // best-of-SerialNumber/SKU, whichever is set
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
