// Command staxv-cluster-manager is the central control plane for a
// fleet of staxv-hypervisor nodes — the "vCenter analog."
//
// Subcommands:
//
//	staxv-cluster-manager serve     — HTTP server
//	staxv-cluster-manager useradd   — admin stub (DB or PAM-linked)
//	staxv-cluster-manager migrate   — run SQLite migrations
//	staxv-cluster-manager version   — print version
//
// Scaffold note: the real product features (fleet enrollment, gRPC
// client to hypervisors, Redfish, scheduler, SRE, migration orchestration)
// all land on top of this base. The scaffold itself only boots an
// auth/settings/dashboard service — mirrors staxv-hypervisor's v0.1.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/zeshaq/staxv-cluster-manager/internal/config"
	"github.com/zeshaq/staxv-cluster-manager/internal/db"
	"github.com/zeshaq/staxv-cluster-manager/internal/handlers"
	"github.com/zeshaq/staxv-cluster-manager/internal/isolib"
	"github.com/zeshaq/staxv-cluster-manager/internal/webui"
	"github.com/zeshaq/staxv-cluster-manager/pkg/auth"
	"github.com/zeshaq/staxv-cluster-manager/pkg/pamauth"
	"github.com/zeshaq/staxv-cluster-manager/pkg/secrets"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "serve":
		cmdServe(args)
	case "useradd":
		cmdUseradd(args)
	case "migrate":
		cmdMigrate(args)
	case "version", "-v", "--version":
		fmt.Println("staxv-cluster-manager", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `staxv-cluster-manager — fleet control plane for staxv-hypervisor nodes

Usage:
  staxv-cluster-manager <command> [flags]

Commands:
  serve      Run the HTTP server
  useradd    Create an admin user (DB or --link-existing for PAM)
  migrate    Apply pending SQLite migrations
  version    Print version
  help       Show this help

Use "staxv-cluster-manager <command> -h" for command-specific flags.

Docs:   .claude/memory/
Issues: https://github.com/zeshaq/staxv-cluster-manager/issues
`)
}

// -----------------------------------------------------------------------
// serve
// -----------------------------------------------------------------------

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "/etc/staxv-cluster-manager/config.toml", "path to TOML config file")
	addrOverride := fs.String("addr", "", "override [server].addr from config (e.g. :5002)")
	_ = fs.Parse(args)

	cfg := mustLoadConfig(*configPath)
	if *addrOverride != "" {
		cfg.Server.Addr = *addrOverride
	}
	initLogger(cfg.Log.Level)

	slog.Info("starting",
		"component", "staxv-cluster-manager",
		"version", version,
		"addr", cfg.Server.Addr,
		"config", *configPath,
		"db", cfg.DB.Path,
		"pid", os.Getpid(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.Open(ctx, cfg.DB.Path)
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	secret, err := auth.LoadOrCreateSecret(cfg.Auth.SecretPath)
	if err != nil {
		slog.Error("load/create jwt secret", "err", err, "path", cfg.Auth.SecretPath)
		os.Exit(1)
	}
	signer := auth.NewSigner(secret, cfg.Auth.TTL)
	authMW := auth.Middleware(signer, store)

	// Select credential verifier — db (default) or pam, same pattern
	// as hypervisor. PAM lets admins on the CM host use their Linux
	// password for web login.
	var verifier auth.CredentialVerifier
	switch cfg.Auth.Backend {
	case "db":
		verifier = store
	case "pam":
		verifier = pamauth.NewVerifier(cfg.Auth.PAMService, store)
	default:
		slog.Error("unknown [auth] backend", "backend", cfg.Auth.Backend, "valid", "db, pam")
		os.Exit(1)
	}
	slog.Info("auth backend", "backend", cfg.Auth.Backend, "pam_service", cfg.Auth.PAMService)

	encKey, err := secrets.LoadOrCreateKey(cfg.Secrets.KeyPath)
	if err != nil {
		slog.Error("load/create settings key", "err", err, "path", cfg.Secrets.KeyPath)
		os.Exit(1)
	}
	aead, err := secrets.NewAEAD(encKey)
	if err != nil {
		slog.Error("init AEAD", "err", err)
		os.Exit(1)
	}
	settingsStore := db.NewSettingsStore(store, aead)
	serverStore := db.NewServerStore(store, aead)
	isoStore := db.NewISOStore(store)

	// ISO library root. Created 0755 on first boot so fresh clones
	// don't need a provisioning step. In prod, override via config to
	// /var/lib/staxv-cluster-manager/isos on a big filesystem.
	isoLib, err := isolib.New(cfg.ISOs.Path)
	if err != nil {
		slog.Error("init iso library", "err", err, "path", cfg.ISOs.Path)
		os.Exit(1)
	}
	slog.Info("iso library", "path", isoLib.Root(),
		"max_upload_gb", cfg.ISOs.MaxUploadGB,
		"download_timeout", cfg.ISOs.DownloadTimeout,
	)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Default group: 60s timeout covers quick JSON requests. ISO routes
	// sit OUTSIDE this group — multi-GB uploads, background-download
	// kickoffs, and chassis-speed ISO streaming all routinely exceed
	// 60s, and a killed-midstream upload is a worse experience than a
	// slow endpoint locking a goroutine.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(60 * time.Second))

		r.Get("/healthz", healthzHandler)

		authH := handlers.NewAuthHandler(verifier, signer, cfg.Server.Secure)
		authH.Mount(r, authMW)

		settingsH := handlers.NewSettingsHandler(settingsStore)
		settingsH.Mount(r, authMW)

		hostH := handlers.NewHostHandler()
		hostH.Mount(r, authMW)

		// Dashboard — reports metrics for the CM host itself. When fleet
		// features land, there will be a separate /api/fleet/dashboard
		// that aggregates across hypervisors; this one stays for "am I
		// healthy as the control plane?"
		dashH := handlers.NewDashboardHandler()
		dashH.Mount(r, authMW)

		// Physical servers — Redfish (iLO/iDRAC) inventory. Admin-only.
		serversH := handlers.NewServersHandler(serverStore)
		serversH.Mount(r, authMW)
	})

	// ISO library — both authenticated /api/isos/* (admin-only) AND the
	// public /iso/{id}/{filename} serve route (BMCs fetch here for
	// Virtual Media Insert, can't send our session cookie).
	maxUpload := int64(cfg.ISOs.MaxUploadGB) * 1024 * 1024 * 1024
	isosH := handlers.NewISOsHandler(isoStore, isoLib, maxUpload, cfg.ISOs.DownloadTimeout)
	isosH.Mount(r, authMW)

	// Web UI — React app embedded via embed.FS. Empty on fresh clone
	// (placeholder until `make frontend` runs). Registered LAST so the
	// SPA fallback doesn't shadow the specific routes above.
	r.Handle("/*", webui.Handler())

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		slog.Error("listener exited unexpectedly", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","component":"staxv-cluster-manager","version":"` + version + `"}` + "\n"))
}

// -----------------------------------------------------------------------
// useradd
// -----------------------------------------------------------------------

func cmdUseradd(args []string) {
	fs := flag.NewFlagSet("useradd", flag.ExitOnError)
	configPath := fs.String("config", "/etc/staxv-cluster-manager/config.toml", "path to TOML config file")
	username := fs.String("username", "", "username (required)")
	unixName := fs.String("unix-username", "", "Linux account name (default: same as --username)")
	uidStr := fs.String("uid", "", "UID (default: current user's UID, or Linux user's UID with --link-existing)")
	homePath := fs.String("home", "", "home path (default: /home/<unix-username>, or Linux user's home with --link-existing)")
	admin := fs.Bool("admin", false, "grant admin privileges")
	linkExisting := fs.Bool("link-existing", false, "link to existing Linux account (for PAM backend; skips password prompt)")
	_ = fs.Parse(args)

	if *username == "" {
		fmt.Fprintln(os.Stderr, "useradd: --username is required")
		os.Exit(2)
	}
	if *unixName == "" {
		*unixName = *username
	}

	var (
		uid      int
		password string
		adopted  bool
		err      error
	)

	if *linkExisting {
		lu, err := user.Lookup(*unixName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "useradd: --link-existing: Linux account %q not found: %v\n", *unixName, err)
			os.Exit(2)
		}
		n, err := strconv.Atoi(lu.Uid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "useradd: Linux UID %q not numeric: %v\n", lu.Uid, err)
			os.Exit(1)
		}
		uid = n
		if *homePath == "" {
			*homePath = lu.HomeDir
		}
		if *uidStr != "" {
			override, err := strconv.Atoi(*uidStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "useradd: --uid: %v\n", err)
				os.Exit(2)
			}
			if override != n {
				fmt.Fprintf(os.Stderr, "useradd: --uid=%d does not match Linux %s UID=%d\n", override, *unixName, n)
				os.Exit(2)
			}
		}
		adopted = true
	} else {
		uid, err = resolveUID(*uidStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "useradd: %v\n", err)
			os.Exit(2)
		}
		if *homePath == "" {
			*homePath = "/home/" + *unixName
		}
		password = promptPassword()
	}

	cfg := mustLoadConfig(*configPath)
	initLogger(cfg.Log.Level)

	ctx := context.Background()
	store, err := db.Open(ctx, cfg.DB.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "useradd: open db: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	u, err := store.CreateUser(ctx, db.CreateUserArgs{
		Username:     *username,
		Password:     password,
		UnixUsername: *unixName,
		UnixUID:      uid,
		HomePath:     *homePath,
		StaxvDir:     *homePath + "/.staxv-cm",
		IsAdmin:      *admin,
		Adopted:      adopted,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "useradd: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("created user %q (id=%d, uid=%d, admin=%t, adopted=%t)\n",
		u.Username, u.ID, u.UnixUID, u.IsAdmin, u.Adopted)
	if *linkExisting {
		fmt.Printf("  → linked to Linux account %q; authenticates via PAM (requires [auth] backend=\"pam\")\n", u.UnixUsername)
	} else {
		fmt.Println()
		fmt.Println("NOTE: scaffold useradd — DB row only. Fleet-level provisioning (propagating")
		fmt.Println("      users to hypervisors via gRPC) lands when fleet features do.")
	}
}

func resolveUID(s string) (int, error) {
	if s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("--uid: %w", err)
		}
		return n, nil
	}
	u, err := user.Current()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

func promptPassword() string {
	fd := int(os.Stdin.Fd())
	var raw []byte
	var err error
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "password: ")
		raw, err = term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
	} else {
		reader := bufio.NewReader(os.Stdin)
		var line string
		line, err = reader.ReadString('\n')
		raw = []byte(strings.TrimRight(line, "\r\n"))
	}
	if err != nil && err.Error() != "EOF" {
		fmt.Fprintf(os.Stderr, "read password: %v\n", err)
		os.Exit(1)
	}
	pw := strings.TrimSpace(string(raw))
	if pw == "" {
		fmt.Fprintln(os.Stderr, "password cannot be empty")
		os.Exit(2)
	}
	return pw
}

// -----------------------------------------------------------------------
// migrate
// -----------------------------------------------------------------------

func cmdMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	configPath := fs.String("config", "/etc/staxv-cluster-manager/config.toml", "path to TOML config file")
	_ = fs.Parse(args)

	cfg := mustLoadConfig(*configPath)
	initLogger(cfg.Log.Level)

	ctx := context.Background()
	store, err := db.Open(ctx, cfg.DB.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	fmt.Println("migrations up to date")
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

func mustLoadConfig(path string) *config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func initLogger(level string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})))
}
