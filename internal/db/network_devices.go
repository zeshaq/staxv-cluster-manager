package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zeshaq/staxv-cluster-manager/pkg/secrets"
)

// NetworkDevice is the in-memory, unencrypted view of a row in
// `network_devices`. Credentials (login + enable) decrypt only when
// the handler needs to dial SSH — otherwise we stay opaque.
//
// Mirror of Server's shape where overlap makes sense; diverges for
// Cisco-specific fields (platform, uptime_s, no power state) so the
// UI can render both kinds of nodes without conditionals.
type NetworkDevice struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	MgmtHost   string     `json:"mgmt_host"`
	MgmtPort   int        `json:"mgmt_port"`
	Username   string     `json:"username"`
	HasEnable  bool       `json:"has_enable"` // derived — never return the password itself
	Platform   string     `json:"platform"`   // "ios" | "ios-xe" | "nxos"
	Status     string     `json:"status"`     // unknown | reachable | unreachable | error
	StatusErr  string     `json:"status_error,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`

	Model    string `json:"model,omitempty"`
	Version  string `json:"version,omitempty"`
	Serial   string `json:"serial,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	UptimeS  int64  `json:"uptime_s,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NetworkDeviceStore is CRUD over network_devices with login +
// enable-secret encryption on save and decrypt on demand.
type NetworkDeviceStore struct {
	db  *DB
	enc *secrets.AEAD
}

func NewNetworkDeviceStore(db *DB, enc *secrets.AEAD) *NetworkDeviceStore {
	return &NetworkDeviceStore{db: db, enc: enc}
}

const networkDeviceColumns = `
	id, name, mgmt_host, mgmt_port, username,
	enable_password_enc IS NOT NULL AS has_enable,
	platform,
	status, status_error, last_seen_at,
	model, version, serial, hostname, uptime_s,
	created_at, updated_at
`

func scanNetworkDevice(row interface{ Scan(...any) error }) (*NetworkDevice, error) {
	d := &NetworkDevice{}
	var (
		statusErr, model, version, serial, hostname sql.NullString
		uptime                                      sql.NullInt64
		lastSeen                                    sql.NullTime
	)
	err := row.Scan(
		&d.ID, &d.Name, &d.MgmtHost, &d.MgmtPort, &d.Username,
		&d.HasEnable,
		&d.Platform,
		&d.Status, &statusErr, &lastSeen,
		&model, &version, &serial, &hostname, &uptime,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.StatusErr = statusErr.String
	d.Model = model.String
	d.Version = version.String
	d.Serial = serial.String
	d.Hostname = hostname.String
	d.UptimeS = uptime.Int64
	if lastSeen.Valid {
		t := lastSeen.Time
		d.LastSeenAt = &t
	}
	return d, nil
}

// CreateNetworkDeviceArgs is the enrollment payload. EnablePassword is
// optional — many modern Cisco deployments put users at priv 15 via
// AAA so no escalation is needed. Empty string → stored as NULL.
type CreateNetworkDeviceArgs struct {
	Name           string
	MgmtHost       string
	MgmtPort       int // 0 → 22
	Username       string
	Password       string // required; encrypted before write
	EnablePassword string // optional; NULL in DB if empty
	Platform       string // empty → "ios" default
}

func (s *NetworkDeviceStore) CreateNetworkDevice(ctx context.Context, a CreateNetworkDeviceArgs) (*NetworkDevice, error) {
	if a.MgmtPort == 0 {
		a.MgmtPort = 22
	}
	if a.Platform == "" {
		a.Platform = "ios"
	}
	encPw, err := s.enc.Encrypt([]byte(a.Password))
	if err != nil {
		return nil, fmt.Errorf("encrypt login password: %w", err)
	}
	var encEnable any // sql.NullBytes isn't a thing; pass nil for NULL.
	if a.EnablePassword != "" {
		b, err := s.enc.Encrypt([]byte(a.EnablePassword))
		if err != nil {
			return nil, fmt.Errorf("encrypt enable password: %w", err)
		}
		encEnable = b
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO network_devices (name, mgmt_host, mgmt_port, username,
		                             password_enc, enable_password_enc,
		                             platform, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?)
	`, a.Name, a.MgmtHost, a.MgmtPort, a.Username, encPw, encEnable, a.Platform, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetNetworkDevice(ctx, id)
}

func (s *NetworkDeviceStore) GetNetworkDevice(ctx context.Context, id int64) (*NetworkDevice, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+networkDeviceColumns+` FROM network_devices WHERE id = ?`, id)
	return scanNetworkDevice(row)
}

// GetCredentials decrypts login + optional enable password. Caller
// must keep these in memory only — never log, never return over HTTP.
func (s *NetworkDeviceStore) GetCredentials(ctx context.Context, id int64) (username, password, enable string, err error) {
	var encPw []byte
	var encEnable []byte
	row := s.db.QueryRowContext(ctx,
		`SELECT username, password_enc, enable_password_enc FROM network_devices WHERE id = ?`, id)
	if err := row.Scan(&username, &encPw, &encEnable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", ErrNotFound
		}
		return "", "", "", err
	}
	plain, err := s.enc.Decrypt(encPw)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt login password: %w", err)
	}
	password = string(plain)
	if len(encEnable) > 0 {
		p, err := s.enc.Decrypt(encEnable)
		if err != nil {
			return "", "", "", fmt.Errorf("decrypt enable password: %w", err)
		}
		enable = string(p)
	}
	return username, password, enable, nil
}

func (s *NetworkDeviceStore) ListNetworkDevices(ctx context.Context) ([]NetworkDevice, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+networkDeviceColumns+` FROM network_devices ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkDevice{}
	for rows.Next() {
		d, err := scanNetworkDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *NetworkDeviceStore) DeleteNetworkDevice(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM network_devices WHERE id = ?`, id)
	return err
}

// NetworkDeviceProbeResult is the output of a pkg/cisco probe. The
// handler fills it after dialing SSH and running `show version`, then
// hands it back here so discovered fields land in the row.
//
// Same OK/Err/Status triple as ProbeResult for servers — keeps the
// handler logic symmetrical.
type NetworkDeviceProbeResult struct {
	OK       bool
	Platform string // detected — overrides admin-supplied if non-empty
	Model    string
	Version  string
	Serial   string
	Hostname string
	UptimeS  int64
	Status   string // "unreachable" (SSH dial/TCP) or "error" (auth/parse) when !OK
	Err      string
}

// UpdateReachability is the network-device analog of ServerStore's
// UpdateReachability. Same COALESCE-on-non-empty pattern so a partial
// probe doesn't overwrite previously-discovered good data.
func (s *NetworkDeviceStore) UpdateReachability(ctx context.Context, id int64, probe *NetworkDeviceProbeResult) error {
	now := time.Now()
	if probe != nil && probe.OK {
		_, err := s.db.ExecContext(ctx, `
			UPDATE network_devices
			SET status       = 'reachable',
			    status_error = NULL,
			    last_seen_at = ?,
			    platform     = COALESCE(NULLIF(?, ''), platform),
			    model        = COALESCE(NULLIF(?, ''), model),
			    version      = COALESCE(NULLIF(?, ''), version),
			    serial       = COALESCE(NULLIF(?, ''), serial),
			    hostname     = COALESCE(NULLIF(?, ''), hostname),
			    uptime_s     = COALESCE(NULLIF(?, 0), uptime_s),
			    updated_at   = ?
			WHERE id = ?
		`,
			now,
			probe.Platform, probe.Model, probe.Version, probe.Serial, probe.Hostname,
			probe.UptimeS,
			now, id,
		)
		return err
	}
	msg := "unknown error"
	status := "error"
	if probe != nil && probe.Err != "" {
		msg = probe.Err
		status = probe.Status
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE network_devices
		SET status = ?, status_error = ?, updated_at = ?
		WHERE id = ?
	`, status, msg, now, id)
	return err
}
