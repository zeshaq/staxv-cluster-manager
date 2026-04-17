// Package config loads staxv-cluster-manager's TOML config.
//
// Narrower than staxv-hypervisor's config: no [libvirt] (CM talks gRPC
// to hypervisors, not libvirtd directly). Fleet-specific sections —
// hypervisor enrollment, gRPC TLS — will land here as those features
// arrive. [isos] lives here because OS-install media is a CM-level
// resource (bound to physical servers, not per-hypervisor tenants).
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server struct {
		Addr   string `toml:"addr"`   // e.g. ":5002" — coexists with staxv-hypervisor on :5001
		Secure bool   `toml:"secure"` // behind TLS? sets cookie Secure flag
	} `toml:"server"`

	Log struct {
		Level string `toml:"level"`
	} `toml:"log"`

	DB struct {
		Path string `toml:"path"`
	} `toml:"db"`

	Auth struct {
		Backend    string        `toml:"backend"`     // "db" | "pam"
		SecretPath string        `toml:"secret_path"` // 32-byte HS256 secret
		TTL        time.Duration `toml:"ttl"`
		PAMService string        `toml:"pam_service"` // /etc/pam.d/<name>
	} `toml:"auth"`

	Secrets struct {
		KeyPath string `toml:"key_path"` // 32-byte AES-256 key
	} `toml:"secrets"`

	ISOs struct {
		// Path is the root directory for uploaded / downloaded ISO files.
		// Created on first boot if missing. BMCs fetch ISOs by HTTP URL
		// via Virtual Media Insert, so this directory's contents are
		// what gets served at /iso/{id}/{filename}.
		Path string `toml:"path"`

		// MaxUploadGB caps a single multipart upload. 20 GB covers the
		// largest distro ISO (Windows Server + optional bits) with
		// headroom. Set to 0 to inherit the default.
		MaxUploadGB int `toml:"max_upload_gb"`

		// DownloadTimeout caps URL-import download time. Fast mirrors
		// do a typical 4-8 GB Linux ISO in ~1-2 min on LAN; keeping a
		// generous ceiling lets slow mirrors and WAN fetches complete.
		DownloadTimeout time.Duration `toml:"download_timeout"`
	} `toml:"isos"`
}

// Load reads the TOML file at path and fills in defaults. Missing file
// → defaults only (good for first-run dev).
func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.applyDefaults()

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	cfg.applyDefaults() // re-fill any zero values the TOML left blank
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		// :5002 keeps CM out of hypervisor's :5001 and vm-manager's :5000
		// during parallel run on the same host.
		c.Server.Addr = ":5002"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.DB.Path == "" {
		c.DB.Path = "./tmp/staxv-cm.db"
	}
	if c.Auth.SecretPath == "" {
		c.Auth.SecretPath = "./tmp/jwt.key"
	}
	if c.Auth.TTL == 0 {
		c.Auth.TTL = 24 * time.Hour
	}
	if c.Auth.Backend == "" {
		c.Auth.Backend = "db"
	}
	if c.Auth.PAMService == "" {
		// Distinct PAM service name from hypervisor's so admin can have
		// different stacks per service on the same host.
		c.Auth.PAMService = "staxv-cluster-manager"
	}
	if c.Secrets.KeyPath == "" {
		c.Secrets.KeyPath = "./tmp/settings.key"
	}
	if c.ISOs.Path == "" {
		// ./tmp/isos in dev; prod deployments override with
		// /var/lib/staxv-cluster-manager/isos.
		c.ISOs.Path = "./tmp/isos"
	}
	if c.ISOs.MaxUploadGB == 0 {
		c.ISOs.MaxUploadGB = 20
	}
	if c.ISOs.DownloadTimeout == 0 {
		// 2 hours — ESXi ISOs from VMware's mirror can crawl at ~10 MB/s
		// on a cold CDN; don't kill the download mid-flight just because
		// the upstream is slow.
		c.ISOs.DownloadTimeout = 2 * time.Hour
	}
}
