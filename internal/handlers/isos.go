package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zeshaq/staxv-cluster-manager/internal/db"
	"github.com/zeshaq/staxv-cluster-manager/internal/isolib"
	"github.com/zeshaq/staxv-cluster-manager/pkg/auth"
)

// ISOsHandler serves /api/isos — the ISO library used for bare-metal OS
// install via BMC Virtual Media.
//
// Two upload paths:
//  1. POST /api/isos/upload  — multipart, synchronous (small .iso files
//     push via the browser in ~seconds at LAN speeds).
//  2. POST /api/isos/import  — body {"name":..., "url":...}, background
//     goroutine streams the download. Immediately returns the
//     row with status='downloading' so the UI can poll.
//
// Plus the public serve route /iso/{id}/{filename} — NO auth, served
// on the same listener so BMCs on the management network can fetch
// ISOs for Virtual Media Insert. The route is under /iso/ (not
// /api/isos/) so the auth middleware can be scoped cleanly.
//
// All write routes require admin. OS install is a root-equivalent
// operation; non-admins don't need to upload or delete bootable media.
type ISOsHandler struct {
	store *db.ISOStore
	lib   *isolib.Library

	// downloadTimeout caps each URL-import goroutine. Set from config.
	downloadTimeout time.Duration

	// maxUploadBytes caps a single multipart upload. Set from config.
	maxUploadBytes int64

	// In-flight URL-import cancel funcs, keyed by ISO id. Populated when
	// a goroutine starts, removed on exit. Delete handler calls these
	// so removing a downloading ISO actually aborts the HTTP fetch
	// rather than wasting bandwidth for hours.
	activeMu       sync.Mutex
	activeDownloads map[int64]context.CancelFunc
}

// NewISOsHandler constructs the handler. maxUploadBytes + downloadTimeout
// come from [isos] config; the handler itself doesn't touch config.
func NewISOsHandler(store *db.ISOStore, lib *isolib.Library, maxUploadBytes int64, downloadTimeout time.Duration) *ISOsHandler {
	return &ISOsHandler{
		store:           store,
		lib:             lib,
		downloadTimeout: downloadTimeout,
		maxUploadBytes:  maxUploadBytes,
		activeDownloads: make(map[int64]context.CancelFunc),
	}
}

// Mount attaches both the authenticated /api/isos/* routes and the
// public /iso/{id}/{filename} serve route.
//
// Two route groups because BMC Virtual Media clients can't send our
// session cookie. If we later need tokenized URLs, they'd go here too.
func (h *ISOsHandler) Mount(r chi.Router, authMW func(http.Handler) http.Handler) {
	r.Route("/api/isos", func(r chi.Router) {
		r.Use(authMW)
		r.Use(auth.RequireAdmin)
		r.Get("/", h.List)
		r.Get("/{id}", h.Get)
		r.Post("/upload", h.Upload)
		r.Post("/import", h.ImportByURL)
		r.Delete("/{id}", h.Delete)
	})
	// Public — BMC fetches here. No auth; protect via network
	// segmentation (BMC management LAN only) in prod deployments.
	r.Get("/iso/{id}/{filename}", h.Serve)
}

// List returns every ISO, newest-first.
func (h *ISOsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListISOs(r.Context())
	if err != nil {
		slog.Error("isos list", "err", err)
		writeError(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"isos": items})
}

// Get returns one ISO.
func (h *ISOsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}
	iso, err := h.store.GetISO(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, iso)
}

// Upload streams a multipart upload directly to disk.
//
// Form fields:
//   file        — required, the .iso (only this part is streamed)
//   name        — optional, display name; defaults to filename
//   os_type     — optional, defaults to "linux"
//   os_version  — optional
//   description — optional
//
// Because we use MultipartReader (not ParseMultipartForm), we walk the
// parts in order — metadata fields MUST come before the file field in
// the form, which the frontend enforces. This keeps the whole upload
// streaming; no 20 GB buffer in RAM.
func (h *ISOsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromCtx(r.Context())

	// Cap total request size. Overrun surfaces as io.ErrUnexpectedEOF
	// on the Part read.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes+1024*1024) // +1 MB slack for headers

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, "expected multipart/form-data", http.StatusBadRequest)
		return
	}

	var (
		displayName string
		osType      string
		osVersion   string
		description string
	)

	for {
		part, err := reader.NextPart()
		if err != nil {
			if errors.Is(err, http.ErrNotMultipart) {
				writeError(w, "not a multipart form", http.StatusBadRequest)
				return
			}
			writeError(w, "no file part in form (ensure metadata fields precede 'file')", http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" {
			// Metadata field — read up to a small cap and capture it.
			val, err := readSmallFormField(part, 4*1024)
			_ = part.Close()
			if err != nil {
				writeError(w, "form field too large: "+part.FormName(), http.StatusBadRequest)
				return
			}
			switch part.FormName() {
			case "name":
				displayName = val
			case "os_type":
				osType = val
			case "os_version":
				osVersion = val
			case "description":
				description = val
			}
			continue
		}

		// This is the file part — stream it.
		filename := strings.TrimSpace(part.FileName())
		res, err := h.lib.SaveUpload(part, filename)
		_ = part.Close()
		if err != nil {
			h.writeISOLibErr(w, err)
			return
		}

		if displayName == "" {
			displayName = res.Filename
		}
		uid := u.ID
		iso, err := h.store.CreateISO(r.Context(), db.CreateISOArgs{
			Name:        displayName,
			Filename:    res.Filename,
			Path:        res.Path,
			SizeBytes:   res.Size,
			SHA256:      res.SHA256,
			OSType:      osType,
			OSVersion:   osVersion,
			Description: description,
			Status:      "ready",
			UploadedBy:  &uid,
		})
		if err != nil {
			// File is on disk but DB row failed — clean up so we don't
			// leave orphan files whose existence blocks a re-upload.
			_ = h.lib.Remove(res.Path)
			if strings.Contains(err.Error(), "UNIQUE") {
				writeError(w, "an ISO with that filename already exists", http.StatusConflict)
				return
			}
			slog.Error("isos upload: db insert", "err", err, "path", res.Path)
			writeError(w, "registration failed", http.StatusInternalServerError)
			return
		}

		slog.Info("iso uploaded",
			"id", iso.ID, "name", iso.Name, "filename", iso.Filename,
			"size", iso.SizeBytes, "sha256", iso.SHA256, "by_user", u.ID,
		)
		writeJSON(w, http.StatusCreated, iso)
		return
	}
}

// ImportByURL inserts a row with status='downloading' and kicks off a
// background goroutine to fetch the file. Admin polls GET /api/isos/{id}
// to see status transition to 'ready' or 'error'.
//
// Body: {"name":..., "url":..., "os_type":..., "os_version":..., "description":..., "filename":"optional-override"}
//
// The goroutine uses context.Background + configured timeout so it
// survives the HTTP request ending. Admin-initiated cancellation lands
// via DELETE, which triggers the activeDownloads CancelFunc.
func (h *ISOsHandler) ImportByURL(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromCtx(r.Context())

	var req struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Filename    string `json:"filename"` // optional override
		OSType      string `json:"os_type"`
		OSVersion   string `json:"os_version"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		writeError(w, "url is required", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, "url must be http(s)://...", http.StatusBadRequest)
		return
	}

	// Derive filename from override / URL path. The library layer
	// validates the .iso extension.
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = path.Base(parsed.Path)
	}
	if filename == "" || filename == "/" || filename == "." {
		writeError(w, "cannot derive filename from URL; supply 'filename'", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(path.Ext(filename), ".iso") {
		writeError(w, "only .iso imports are supported", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = filename
	}

	uid := u.ID
	iso, err := h.store.CreateISO(r.Context(), db.CreateISOArgs{
		Name:        req.Name,
		Filename:    filename,
		Path:        path.Join(h.lib.Root(), filename), // declared path; real write happens in goroutine
		OSType:      req.OSType,
		OSVersion:   req.OSVersion,
		Description: req.Description,
		SourceURL:   req.URL,
		Status:      "downloading",
		UploadedBy:  &uid,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, "an ISO with that filename already exists", http.StatusConflict)
			return
		}
		slog.Error("isos import: db insert", "err", err)
		writeError(w, "registration failed", http.StatusInternalServerError)
		return
	}

	// Kick off the download. Context lives independent of the HTTP
	// request — the admin's browser closes immediately, but the fetch
	// continues until completion / cancellation / timeout.
	dlCtx, cancel := context.WithTimeout(context.Background(), h.downloadTimeout)
	h.activeMu.Lock()
	h.activeDownloads[iso.ID] = cancel
	h.activeMu.Unlock()

	go h.runDownload(dlCtx, iso.ID, req.URL, filename)

	slog.Info("iso import started",
		"id", iso.ID, "url", req.URL, "filename", filename, "by_user", u.ID,
	)
	writeJSON(w, http.StatusAccepted, iso)
}

// runDownload is the goroutine body for URL imports. Runs outside any
// request context. Updates the DB row at the end, regardless of
// success / failure / cancellation.
func (h *ISOsHandler) runDownload(ctx context.Context, id int64, url, filename string) {
	start := time.Now()
	defer func() {
		// Always clear the activeDownloads entry so we don't leak cancel
		// funcs if a goroutine panics (the library / net lib rarely
		// panic, but belt & braces).
		h.activeMu.Lock()
		delete(h.activeDownloads, id)
		h.activeMu.Unlock()
	}()

	res, err := h.lib.Download(ctx, url, filename)
	// Use a fresh context for the DB updates — the download context
	// might be cancelled, but we still need to record the outcome.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	if err != nil {
		// If the download was cancelled via DELETE, the row is already
		// gone — MarkError returns nil on a missing row (UPDATE affects
		// 0 rows, which isn't an error in SQL). Double-check by
		// fetching first to suppress a redundant log line.
		if _, getErr := h.store.GetISO(dbCtx, id); errors.Is(getErr, db.ErrNotFound) {
			slog.Info("iso import cancelled", "id", id, "url", url)
			return
		}
		slog.Warn("iso import failed", "id", id, "url", url, "err", err, "elapsed", time.Since(start))
		if mErr := h.store.MarkError(dbCtx, id, err.Error()); mErr != nil {
			slog.Error("iso import: mark error failed", "id", id, "err", mErr)
		}
		return
	}

	// Success — but the row might have been deleted mid-download. In
	// that case, clean up the file we just wrote.
	current, getErr := h.store.GetISO(dbCtx, id)
	if errors.Is(getErr, db.ErrNotFound) {
		slog.Info("iso import finished after delete; cleaning file", "path", res.Path)
		_ = h.lib.Remove(res.Path)
		return
	}
	if getErr != nil {
		slog.Error("iso import: refetch after success", "id", id, "err", getErr)
		return
	}
	_ = current
	if err := h.store.MarkReady(dbCtx, id, res.Size, res.SHA256); err != nil {
		slog.Error("iso import: mark ready failed", "id", id, "err", err)
		return
	}

	slog.Info("iso import complete",
		"id", id, "filename", filename, "size", res.Size, "sha256", res.SHA256,
		"elapsed", time.Since(start),
	)
}

// Delete removes the row and (by default) the on-disk file. If a URL
// import is in progress for this id, cancels the download first so the
// partial file goes away cleanly.
func (h *ISOsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(w, r, "id")
	if !ok {
		return
	}

	iso, err := h.store.GetISO(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
		writeError(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	// Cancel any in-flight download. The goroutine will see the row
	// gone and remove its partial file.
	h.activeMu.Lock()
	if cancel, ok := h.activeDownloads[id]; ok {
		cancel()
		delete(h.activeDownloads, id)
	}
	h.activeMu.Unlock()

	deleteFile := r.URL.Query().Get("delete_file") != "false"

	// Row first. Same reasoning as hypervisor's images handler: a
	// dangling file is recoverable (sudo rm); a dangling row breaks
	// the UI list.
	if err := h.store.DeleteISO(r.Context(), id); err != nil {
		slog.Error("isos delete: db", "err", err, "id", id)
		writeError(w, "delete failed", http.StatusInternalServerError)
		return
	}
	if deleteFile && iso.Status != "downloading" {
		// 'downloading' rows have no committed file yet (the goroutine
		// either already cleaned up on its way out or will when it sees
		// the row gone). Attempting Remove here races with the
		// goroutine; safer to let the goroutine handle it.
		if err := h.lib.Remove(iso.Path); err != nil {
			slog.Warn("isos delete: file remove", "err", err, "path", iso.Path)
		}
	}

	slog.Info("iso deleted", "id", id, "filename", iso.Filename, "removed_file", deleteFile)
	w.WriteHeader(http.StatusNoContent)
}

// Serve streams a ready ISO by id+filename. No auth — BMC Virtual Media
// clients can't authenticate with our session cookie. The filename in
// the path must match the DB row, so an adversary can't probe IDs to
// enumerate files (they'd need to guess the filename too, and anyway
// the route is expected to sit on a network-segmented BMC LAN).
//
// Serves with Content-Type: application/octet-stream. http.ServeContent
// handles Range requests for BMCs that resume partial mounts.
func (h *ISOsHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	filename := chi.URLParam(r, "filename")

	iso, err := h.store.GetISO(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Filename must match — prevents a single id bypass from leaking
	// the full catalog.
	if iso.Filename != filename {
		http.NotFound(w, r)
		return
	}
	if iso.Status != "ready" {
		http.Error(w, fmt.Sprintf("iso %d not ready (status=%s)", id, iso.Status), http.StatusConflict)
		return
	}

	f, err := h.lib.Open(iso.Path)
	if err != nil {
		slog.Error("iso serve: open", "err", err, "id", id, "path", iso.Path)
		http.Error(w, "file unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "stat failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, iso.Filename))
	http.ServeContent(w, r, iso.Filename, stat.ModTime(), f)
}

// writeISOLibErr maps isolib errors to HTTP responses. Kept here
// (not in isolib) so isolib stays HTTP-agnostic.
func (h *ISOsHandler) writeISOLibErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, isolib.ErrBadFilename):
		writeError(w, "invalid filename", http.StatusBadRequest)
	case errors.Is(err, isolib.ErrBadFormat):
		writeError(w, "only .iso files are supported", http.StatusBadRequest)
	case errors.Is(err, isolib.ErrExists):
		writeError(w, "an ISO with that filename already exists", http.StatusConflict)
	default:
		slog.Error("isolib error", "err", err)
		writeError(w, "upload failed: "+err.Error(), http.StatusInternalServerError)
	}
}

// readSmallFormField reads a metadata form field with a size cap.
// Form fields (name, os_type, etc.) are tiny; reject anything suspicious
// so a misuse can't bloat memory.
func readSmallFormField(part interface{ Read([]byte) (int, error) }, maxBytes int) (string, error) {
	buf := make([]byte, maxBytes+1)
	total := 0
	for total <= maxBytes {
		n, err := part.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}
	if total > maxBytes {
		return "", errors.New("form field exceeds cap")
	}
	return strings.TrimSpace(string(buf[:total])), nil
}
