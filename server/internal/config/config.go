// Package config reads server settings from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL   string
	DataDir       string // database-independent state: derived files (thumbnails, previews)
	Listen        string
	WebDir        string // built web app to serve; empty serves the API only
	AdminEmail    string // first-run admin, created only when no users exist
	AdminPassword string
	ImageWorkers  int
	VideoWorkers  int
	// The built-in AI worker (photostead-worker builtin): the text service search queries need,
	// and a gentle CPU worker for new items. Empty or missing disables it.
	WorkerCmd string
	// Arguments before the worker's own (PHOTOSTEAD_WORKER_ARGS, split on spaces): the desktop app
	// runs its bundled Python as `python -m photostead_worker`.
	WorkerArgs []string
	// Where the text service listens; search by meaning uses it.
	TextEmbedder string
	// Watch indexed libraries for new files (PHOTOSTEAD_WATCH, on unless "off"). Off for a
	// development server reading a share over the network.
	Watch bool
	// The managed share: where the app reads it, and where the move writes it (on Unraid the
	// array-only view, so a large migration bypasses the cache pool).
	ManagedRoot string
	MoveTarget  string
	// Where existing folders are mounted to be indexed in place, read-only (PHOTOSTEAD_LIBRARIES,
	// /libraries): each folder in it becomes a library, found on its own.
	Mounts string
	// Nightly copies of the database (PHOTOSTEAD_BACKUP_DIR, "off" for none): by default the
	// managed share's backups/database, where the move writes (on Unraid the array, away from the
	// cache pool the database is on). The newest PHOTOSTEAD_BACKUP_KEEP (14) are kept.
	BackupDir  string
	BackupKeep int
	// The server's time zone (PHOTOSTEAD_TIMEZONE, else TZ, else the system's), by name. It's only
	// a default for people who haven't chosen their own zone yet; the web app asks at setup.
	ZoneName string
	Zone     *time.Location
	// The address people reach Photostead at (PHOTOSTEAD_PUBLIC_URL, e.g. https://photos.example.com),
	// for single sign-on's return address. Empty: the address each request came in on.
	PublicURL string
	// The reverse proxies whose X-Forwarded-For is believed, to count wrong pairing codes and
	// passwords by the person sending them (PHOTOSTEAD_TRUSTED_PROXIES; see api.ParseProxies).
	// Empty: loopback and private addresses, but not Tailscale's.
	TrustedProxies string
	// Single sign-on with an OpenID Connect provider (PHOTOSTEAD_OIDC_ISSUER, _CLIENT_ID,
	// _CLIENT_SECRET; _NAME for the button; _NEW_ACCOUNTS=on to make accounts for people the
	// provider knows but Photostead doesn't). Off without an issuer and client id.
	OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCName string
	OIDCNewAccounts                                      bool
	// The map (PHOTOSTEAD_MAP=off turns it off; places are still named, offline). Its MapLibre styles
	// default to OpenFreeMap's; PHOTOSTEAD_MAP_STYLE_LIGHT and _DARK choose others, and
	// PHOTOSTEAD_MAP_HOSTS lists the other https origins those styles load tiles and fonts from.
	MapOff                      bool
	MapLight, MapDark, MapHosts string
	// The most a phone sends in one request, in MiB (PHOTOSTEAD_UPLOAD_PIECE), for a reverse proxy
	// that caps a request's size. 0: a file goes whole, but in 64 MiB pieces through Cloudflare.
	UploadPieceMiB int
	// A demo server, such as the one App Review pairs with (PHOTOSTEAD_DISCARD_UPLOADS=on): every
	// upload is answered as if filed and then thrown away, so nothing anyone sends is kept.
	DiscardUploads bool
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("PHOTOSTEAD_DATABASE_URL"),
		DataDir:          os.Getenv("PHOTOSTEAD_DATA_DIR"),
		Listen:           env("PHOTOSTEAD_LISTEN", "127.0.0.1:4800"),
		WebDir:           os.Getenv("PHOTOSTEAD_WEB_DIR"),
		AdminEmail:       os.Getenv("PHOTOSTEAD_ADMIN_EMAIL"),
		AdminPassword:    os.Getenv("PHOTOSTEAD_ADMIN_PASSWORD"),
		ImageWorkers:     envInt("PHOTOSTEAD_IMAGE_WORKERS", 8),
		VideoWorkers:     envInt("PHOTOSTEAD_VIDEO_WORKERS", 2),
		ManagedRoot:      env("PHOTOSTEAD_MANAGED_ROOT", "/photos"),
		MoveTarget:       os.Getenv("PHOTOSTEAD_MOVE_TARGET"),
		Mounts:           env("PHOTOSTEAD_LIBRARIES", "/libraries"),
		BackupDir:        os.Getenv("PHOTOSTEAD_BACKUP_DIR"),
		BackupKeep:       envInt("PHOTOSTEAD_BACKUP_KEEP", 14),
		Watch:            os.Getenv("PHOTOSTEAD_WATCH") != "off",
		WorkerCmd:        env("PHOTOSTEAD_WORKER_CMD", "/opt/photostead-worker/bin/photostead-worker"),
		WorkerArgs:       strings.Fields(os.Getenv("PHOTOSTEAD_WORKER_ARGS")),
		TextEmbedder:     env("PHOTOSTEAD_TEXT_EMBEDDER", "http://127.0.0.1:4801"),
		PublicURL:        strings.TrimRight(os.Getenv("PHOTOSTEAD_PUBLIC_URL"), "/"),
		TrustedProxies:   os.Getenv("PHOTOSTEAD_TRUSTED_PROXIES"),
		OIDCIssuer:       os.Getenv("PHOTOSTEAD_OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("PHOTOSTEAD_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("PHOTOSTEAD_OIDC_CLIENT_SECRET"),
		OIDCName:         env("PHOTOSTEAD_OIDC_NAME", "single sign-on"),
		OIDCNewAccounts:  os.Getenv("PHOTOSTEAD_OIDC_NEW_ACCOUNTS") == "on",
		MapOff:           os.Getenv("PHOTOSTEAD_MAP") == "off",
		MapLight:         os.Getenv("PHOTOSTEAD_MAP_STYLE_LIGHT"),
		MapDark:          os.Getenv("PHOTOSTEAD_MAP_STYLE_DARK"),
		MapHosts:         os.Getenv("PHOTOSTEAD_MAP_HOSTS"),
		UploadPieceMiB:   envInt("PHOTOSTEAD_UPLOAD_PIECE", 0),
		DiscardUploads:   os.Getenv("PHOTOSTEAD_DISCARD_UPLOADS") == "on",
	}
	c.ZoneName = serverZone()
	z, err := time.LoadLocation(c.ZoneName)
	if err != nil {
		return c, fmt.Errorf("time zone %q: %w", c.ZoneName, err)
	}
	c.Zone = z
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("PHOTOSTEAD_DATABASE_URL is not set")
	}
	if c.DataDir == "" {
		return c, fmt.Errorf("PHOTOSTEAD_DATA_DIR is not set")
	}
	c.DataDir = filepath.Clean(c.DataDir)
	if c.MoveTarget == "" {
		c.MoveTarget = c.ManagedRoot
	}
	switch c.BackupDir {
	case "":
		c.BackupDir = filepath.Join(c.MoveTarget, "backups", "database")
	case "off":
		c.BackupDir = ""
	}
	return c, nil
}

// serverZone names the machine's time zone: PHOTOSTEAD_TIMEZONE, TZ (Unraid passes its own to
// containers), or /etc/localtime. UTC only if the machine truly has no zone configured.
func serverZone() string {
	for _, k := range []string{"PHOTOSTEAD_TIMEZONE", "TZ"} {
		if v := strings.TrimPrefix(os.Getenv(k), ":"); v != "" && v != "Local" {
			if _, err := time.LoadLocation(v); err == nil {
				return v
			}
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			name := target[i+len("zoneinfo/"):]
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return "UTC"
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
