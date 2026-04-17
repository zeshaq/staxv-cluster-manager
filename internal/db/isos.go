package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ISO is the in-memory view of one row in `isos`. Status transitions:
//
//	uploading/downloading → ready | error
//
// The file on disk matches Filename under the library root. Size / SHA256
// are populated when status becomes 'ready'; before that they're zero /
// empty respectively.
type ISO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
	OSType      string `json:"os_type"`
	OSVersion   string `json:"os_version,omitempty"`
	Description string `json:"description,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	Status      string `json:"status"` // uploading | downloading | ready | error
	Error       string `json:"error,omitempty"`
	UploadedBy  *int64 `json:"uploaded_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ISOStore is CRUD over the isos table. No secrets to encrypt here —
// everything is already admin-visible; bytes on disk aren't encrypted
// either (BMCs couldn't decrypt them anyway when mounting via Virtual
// Media).
type ISOStore struct {
	db *DB
}

func NewISOStore(db *DB) *ISOStore {
	return &ISOStore{db: db}
}

const isoColumns = `
	id, name, filename, path,
	size_bytes, sha256,
	os_type, os_version, description, source_url,
	status, error, uploaded_by,
	created_at, updated_at
`

func scanISO(row interface{ Scan(...any) error }) (*ISO, error) {
	i := &ISO{}
	var sha, osVer, desc, srcURL, errMsg sql.NullString
	var uploadedBy sql.NullInt64
	err := row.Scan(
		&i.ID, &i.Name, &i.Filename, &i.Path,
		&i.SizeBytes, &sha,
		&i.OSType, &osVer, &desc, &srcURL,
		&i.Status, &errMsg, &uploadedBy,
		&i.CreatedAt, &i.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	i.SHA256 = sha.String
	i.OSVersion = osVer.String
	i.Description = desc.String
	i.SourceURL = srcURL.String
	i.Error = errMsg.String
	if uploadedBy.Valid {
		v := uploadedBy.Int64
		i.UploadedBy = &v
	}
	return i, nil
}

// CreateISOArgs is the full insert payload. Status is mandatory so the
// caller declares whether this is an in-progress upload / download or
// an already-ready row (unusual — only happens on migration imports).
type CreateISOArgs struct {
	Name        string
	Filename    string
	Path        string
	SizeBytes   int64  // may be 0 while in-flight
	SHA256      string // may be "" while in-flight
	OSType      string // required; defaults to "linux" if empty
	OSVersion   string
	Description string
	SourceURL   string // non-empty → status starts as "downloading"
	Status      string // defaults to "uploading" if empty
	UploadedBy  *int64
}

// CreateISO inserts a new row and returns the populated ISO. The
// UNIQUE(filename) constraint prevents duplicate filenames and
// propagates as a plain sqlite error the handler translates to 409.
func (s *ISOStore) CreateISO(ctx context.Context, a CreateISOArgs) (*ISO, error) {
	if a.OSType == "" {
		a.OSType = "linux"
	}
	if a.Status == "" {
		a.Status = "uploading"
	}
	// sql.Null* wrappers so zero values insert as NULL, not empty
	// strings — keeps the schema honest and lets COALESCE updates work.
	var (
		sha         = nullString(a.SHA256)
		osVer       = nullString(a.OSVersion)
		desc        = nullString(a.Description)
		srcURL      = nullString(a.SourceURL)
		uploadedBy  sql.NullInt64
	)
	if a.UploadedBy != nil {
		uploadedBy = sql.NullInt64{Int64: *a.UploadedBy, Valid: true}
	}

	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO isos (
			name, filename, path,
			size_bytes, sha256,
			os_type, os_version, description, source_url,
			status, uploaded_by,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.Name, a.Filename, a.Path,
		a.SizeBytes, sha,
		a.OSType, osVer, desc, srcURL,
		a.Status, uploadedBy,
		now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetISO(ctx, id)
}

// GetISO returns one row by id or ErrNotFound.
func (s *ISOStore) GetISO(ctx context.Context, id int64) (*ISO, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+isoColumns+` FROM isos WHERE id = ?`, id)
	return scanISO(row)
}

// ListISOs returns every row, newest-first. No pagination yet — an
// operator fleet keeps dozens, not thousands, of ISOs; a UI list is fine.
func (s *ISOStore) ListISOs(ctx context.Context) ([]ISO, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+isoColumns+` FROM isos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ISO{}
	for rows.Next() {
		iso, err := scanISO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iso)
	}
	return out, rows.Err()
}

// MarkReady transitions an in-flight ISO to status='ready' with the
// discovered size + sha256. Used by both the synchronous upload path
// (after the file is fsynced) and the async URL-download goroutine.
func (s *ISOStore) MarkReady(ctx context.Context, id int64, size int64, sha256 string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE isos
		SET status = 'ready', error = NULL,
		    size_bytes = ?, sha256 = ?,
		    updated_at = ?
		WHERE id = ?
	`, size, sha256, time.Now(), id)
	return err
}

// MarkError records a failed upload / download. Leaves size/sha256
// untouched so the admin can see how far the transfer got before it
// broke — useful when diagnosing partial downloads.
func (s *ISOStore) MarkError(ctx context.Context, id int64, msg string) error {
	// Cap at 512 chars — some HTTP error bodies are multi-KB JSON from
	// mirror providers, not useful verbatim.
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE isos
		SET status = 'error', error = ?, updated_at = ?
		WHERE id = ?
	`, msg, time.Now(), id)
	return err
}

// DeleteISO removes the row. Idempotent. File cleanup is the handler's
// responsibility — db.go doesn't know about filesystem paths.
func (s *ISOStore) DeleteISO(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM isos WHERE id = ?`, id)
	return err
}

// nullString converts a Go string to sql.NullString, treating "" as NULL.
// Small helper; isos is the first store to use it so lives here until a
// second caller justifies promoting to db.go.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
