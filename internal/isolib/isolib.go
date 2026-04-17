// Package isolib is the filesystem layer for the ISO library —
// streaming saves from multipart forms, URL imports with SHA256
// computed on the fly, safe removes, basic validation.
// It doesn't touch the DB or HTTP; the handler wires those.
//
// Cluster-manager scope: ISOs only (.iso extension). Hypervisor's sister
// package accepts qcow2/raw/img too — those are hypervisor-side install
// media formats, not BMC-mountable bare-metal install media.
package isolib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Errors the handler maps to HTTP statuses. Keep them package-level so
// callers can errors.Is without string-matching.
var (
	ErrBadFilename = errors.New("isolib: filename contains disallowed characters")
	ErrBadFormat   = errors.New("isolib: only .iso files are supported")
	ErrExists      = errors.New("isolib: file already exists (delete existing first)")
	ErrOutsideRoot = errors.New("isolib: path is outside the library root")
)

// Library is the handle a handler uses. Instantiated once in main.go
// per root directory. Safe for concurrent use — the individual file
// creates are atomic thanks to O_EXCL, and removes are per-path.
type Library struct {
	root string
}

// New constructs a Library rooted at absolute dir. Creates the
// directory (0755) if missing so first-boot doesn't need a separate
// provisioning step.
func New(root string) (*Library, error) {
	if root == "" {
		return nil, fmt.Errorf("isolib: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("isolib: resolve %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("isolib: mkdir %s: %w", abs, err)
	}
	return &Library{root: abs}, nil
}

// Root returns the library's root directory — for logging and for the
// /iso/{id}/{filename} serve route's path-allow-list check.
func (l *Library) Root() string { return l.root }

// SaveResult describes a completed save (upload or URL import).
type SaveResult struct {
	Filename string // basename only
	Path     string // full path on disk
	Size     int64
	SHA256   string // hex-encoded, lowercase
}

// SaveUpload streams a multipart.Part to disk with integrity hashing
// on the fly. Enforces .iso extension + safe basename. Caller is
// responsible for wrapping r.Body in http.MaxBytesReader upstream so
// a client can't fill the disk.
//
// On any failure the partial file is removed — caller doesn't clean up.
// Uses O_EXCL so re-uploading the same name 409s instead of silently
// overwriting a previously-referenced file.
func (l *Library) SaveUpload(part *multipart.Part, destName string) (*SaveResult, error) {
	name := destName
	if name == "" {
		name = part.FileName()
	}
	name, err := l.sanitizeName(name)
	if err != nil {
		return nil, err
	}

	dst := filepath.Join(l.root, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrExists
		}
		return nil, fmt.Errorf("isolib: open %s: %w", dst, err)
	}

	h := sha256.New()
	// io.MultiWriter fans the bytes into both the file and the hasher
	// without an extra pass over disk.
	n, copyErr := io.Copy(io.MultiWriter(f, h), part)
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: copy to %s: %w", dst, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: close %s: %w", dst, closeErr)
	}

	return &SaveResult{
		Filename: name,
		Path:     dst,
		Size:     n,
		SHA256:   hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// Download fetches an ISO from url, streaming to disk while hashing.
// Follows HTTP redirects (needed for most distro download mirrors).
// Context cancellation stops the in-flight download cleanly and removes
// the partial file.
//
// destName is the target basename; must pass the same extension check
// as SaveUpload. Caller typically derives it from the URL or user
// input and validates before calling.
func (l *Library) Download(ctx context.Context, url, destName string) (*SaveResult, error) {
	name, err := l.sanitizeName(destName)
	if err != nil {
		return nil, err
	}

	dst := filepath.Join(l.root, name)
	// Same O_EXCL discipline as upload.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrExists
		}
		return nil, fmt.Errorf("isolib: open %s: %w", dst, err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: build request: %w", err)
	}
	// Many mirrors serve a polite agent check; anonymous download isn't
	// blocked for UAs that look like a real browser / tool. curl's UA
	// works everywhere we've seen.
	req.Header.Set("User-Agent", "staxv-cluster-manager/iso-import")

	// http.DefaultClient follows up to 10 redirects — distro URLs often
	// redirect through a mirror selector, so this is required.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = f.Close()
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: HTTP %d fetching %s", resp.StatusCode, url)
	}

	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(dst)
		// Context cancellation surfaces as context.Canceled wrapped by
		// io.Copy — let the caller distinguish via errors.Is.
		return nil, fmt.Errorf("isolib: copy %s → %s: %w", url, dst, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return nil, fmt.Errorf("isolib: close %s: %w", dst, closeErr)
	}

	return &SaveResult{
		Filename: name,
		Path:     dst,
		Size:     n,
		SHA256:   hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// Remove deletes a file, but only if it lives inside the library root
// (defense-in-depth against a tampered-DB path row telling us to
// unlink /etc/passwd). Idempotent — returns nil if the file is
// already gone.
func (l *Library) Remove(path string) error {
	if err := l.withinRoot(path); err != nil {
		return err
	}
	if err := os.Remove(filepath.Clean(path)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("isolib: remove %s: %w", path, err)
	}
	return nil
}

// Open returns a *os.File for the given path, after verifying it lives
// inside the library root. Handler uses this to stream the ISO back to
// a BMC's Virtual Media client.
func (l *Library) Open(path string) (*os.File, error) {
	if err := l.withinRoot(path); err != nil {
		return nil, err
	}
	return os.Open(filepath.Clean(path))
}

// sanitizeName validates a user-supplied filename, returning the
// cleaned basename or an error. Only .iso files are permitted;
// path separators are rejected outright (no traversal even into the
// library root's subdirs — library is flat).
func (l *Library) sanitizeName(raw string) (string, error) {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", ErrBadFilename
	}
	if !strings.EqualFold(filepath.Ext(name), ".iso") {
		return "", ErrBadFormat
	}
	return name, nil
}

// withinRoot checks that a cleaned path has the library root as a
// prefix. Use before any filesystem operation that takes a path from
// the DB — the row could have been tampered with externally.
func (l *Library) withinRoot(path string) error {
	clean := filepath.Clean(path)
	root := filepath.Clean(l.root) + string(filepath.Separator)
	if !strings.HasPrefix(clean, root) {
		return fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	return nil
}
