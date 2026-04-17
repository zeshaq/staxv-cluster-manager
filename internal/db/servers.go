package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zeshaq/staxv-cluster-manager/pkg/secrets"
)

// Server is the unencrypted, in-memory view of a row. Credentials are
// only decrypted when callers need to dial Redfish.
type Server struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	BMCHost      string     `json:"bmc_host"`
	BMCPort      int        `json:"bmc_port"`
	BMCUsername  string     `json:"bmc_username"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Model        string     `json:"model,omitempty"`
	Serial       string     `json:"serial,omitempty"`
	Status       string     `json:"status"` // unknown | reachable | unreachable | error
	StatusError  string     `json:"status_error,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`

	// Extended detail — populated on probe. Omitted from JSON when
	// zero so the list endpoint stays compact; the detail page cares
	// about these.
	PowerState  string `json:"power_state,omitempty"`
	Health      string `json:"health,omitempty"`
	BIOSVersion string `json:"bios_version,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	CPUCount    int    `json:"cpu_count,omitempty"`
	MemoryGB    int    `json:"memory_gb,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ServerStore is CRUD over the servers table with BMC-password
// encryption on save and decryption on demand.
type ServerStore struct {
	db  *DB
	enc *secrets.AEAD
}

func NewServerStore(db *DB, enc *secrets.AEAD) *ServerStore {
	return &ServerStore{db: db, enc: enc}
}

const serverColumns = `
	id, name, bmc_host, bmc_port, bmc_username,
	manufacturer, model, serial,
	status, status_error, last_seen_at,
	power_state, health, bios_version, hostname, cpu_count, memory_gb,
	created_at, updated_at
`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	s := &Server{}
	var mfr, model, serial, statusErr sql.NullString
	var powerState, health, biosVersion, hostname sql.NullString
	var cpuCount, memoryGB sql.NullInt64
	var lastSeen sql.NullTime
	err := row.Scan(
		&s.ID, &s.Name, &s.BMCHost, &s.BMCPort, &s.BMCUsername,
		&mfr, &model, &serial,
		&s.Status, &statusErr, &lastSeen,
		&powerState, &health, &biosVersion, &hostname, &cpuCount, &memoryGB,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Manufacturer = mfr.String
	s.Model = model.String
	s.Serial = serial.String
	s.StatusError = statusErr.String
	s.PowerState = powerState.String
	s.Health = health.String
	s.BIOSVersion = biosVersion.String
	s.Hostname = hostname.String
	s.CPUCount = int(cpuCount.Int64)
	s.MemoryGB = int(memoryGB.Int64)
	if lastSeen.Valid {
		t := lastSeen.Time
		s.LastSeenAt = &t
	}
	return s, nil
}

// CreateServerArgs is the input for enrollment.
type CreateServerArgs struct {
	Name        string
	BMCHost     string
	BMCPort     int // 0 → 443
	BMCUsername string
	BMCPassword string // plaintext; encrypted before storage
}

// CreateServer inserts a new row with the BMC password encrypted.
// Status defaults to "unknown"; the handler is expected to call
// UpdateReachability right after a fresh Redfish probe.
func (s *ServerStore) CreateServer(ctx context.Context, a CreateServerArgs) (*Server, error) {
	if a.BMCPort == 0 {
		a.BMCPort = 443
	}
	encPw, err := s.enc.Encrypt([]byte(a.BMCPassword))
	if err != nil {
		return nil, fmt.Errorf("encrypt bmc password: %w", err)
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (name, bmc_host, bmc_port, bmc_username, bmc_password_enc,
		                     status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'unknown', ?, ?)
	`, a.Name, a.BMCHost, a.BMCPort, a.BMCUsername, encPw, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetServer(ctx, id)
}

// GetServer returns a single row by id.
func (s *ServerStore) GetServer(ctx context.Context, id int64) (*Server, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+serverColumns+` FROM servers WHERE id = ?`, id)
	return scanServer(row)
}

// GetCredentials returns the decrypted BMC password for id. Caller is
// responsible for keeping this in memory only — never log it, never
// return it over HTTP.
func (s *ServerStore) GetCredentials(ctx context.Context, id int64) (username, password string, err error) {
	var encPw []byte
	row := s.db.QueryRowContext(ctx,
		`SELECT bmc_username, bmc_password_enc FROM servers WHERE id = ?`, id)
	if err := row.Scan(&username, &encPw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", err
	}
	plain, err := s.enc.Decrypt(encPw)
	if err != nil {
		return "", "", fmt.Errorf("decrypt bmc password: %w", err)
	}
	return username, string(plain), nil
}

// ListServers returns all rows, newest-first. Admin-only from the
// handler side; no per-user scoping here.
func (s *ServerStore) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+serverColumns+` FROM servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *srv)
	}
	return out, rows.Err()
}

// DeleteServer removes the row. Idempotent: no error if already gone.
func (s *ServerStore) DeleteServer(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	return err
}

// UpdateReachability is called after a Redfish probe (enroll or /test).
// err=nil → mark reachable and stamp last_seen_at + discovered fields;
// err!=nil → mark unreachable (or "error" for non-network faults), no
// last_seen_at update.
//
// Discovered string fields (manufacturer/model/serial/etc.) are only
// written when non-empty — a half-complete probe doesn't clobber
// previously-known values. Numeric fields (cpu_count/memory_gb)
// follow the same COALESCE pattern using NULLIF on 0.
//
// power_state is the exception — we always overwrite it, because
// "On → Off" is a legitimate transition that matters more than
// preserving a stale value.
func (s *ServerStore) UpdateReachability(ctx context.Context, id int64, probe *ProbeResult) error {
	now := time.Now()
	if probe != nil && probe.OK {
		_, err := s.db.ExecContext(ctx, `
			UPDATE servers
			SET status       = 'reachable',
			    status_error = NULL,
			    last_seen_at = ?,
			    manufacturer = COALESCE(NULLIF(?, ''), manufacturer),
			    model        = COALESCE(NULLIF(?, ''), model),
			    serial       = COALESCE(NULLIF(?, ''), serial),
			    power_state  = NULLIF(?, ''),
			    health       = COALESCE(NULLIF(?, ''), health),
			    bios_version = COALESCE(NULLIF(?, ''), bios_version),
			    hostname     = COALESCE(NULLIF(?, ''), hostname),
			    cpu_count    = COALESCE(NULLIF(?, 0), cpu_count),
			    memory_gb    = COALESCE(NULLIF(?, 0), memory_gb),
			    updated_at   = ?
			WHERE id = ?
		`,
			now,
			probe.Manufacturer, probe.Model, probe.Serial,
			probe.PowerState, probe.Health, probe.BIOSVersion, probe.Hostname,
			probe.CPUCount, probe.MemoryGB,
			now, id,
		)
		return err
	}
	msg := "unknown error"
	status := "error"
	if probe != nil && probe.Err != "" {
		msg = probe.Err
		status = probe.Status // "unreachable" or "error"
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE servers
		SET status = ?, status_error = ?, updated_at = ?
		WHERE id = ?
	`, status, msg, now, id)
	return err
}

// ProbeResult is what the Redfish client returns after trying to
// reach a server. Kept in the db package to avoid a circular import
// back to the handler layer.
type ProbeResult struct {
	OK           bool   // reached and valid Redfish response
	Manufacturer string // discovered from /Systems
	Model        string
	Serial       string
	PowerState   string // "On" | "Off" | "PoweringOn" | "PoweringOff" | ""
	Health       string // "OK" | "Warning" | "Critical" | ""
	BIOSVersion  string
	Hostname     string
	CPUCount     int
	MemoryGB     int
	Status       string // "unreachable" (network/TLS) or "error" (auth/invalid response) when !OK
	Err          string // one-line error message
}
