package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Paths are where things are: the bundled programs, and this person's data and logs.
type Paths struct {
	Runtime string // runtime/: postgres/, bin/, python/, web/
	Data    string // the database, the server's thumbnails and models, settings
	Logs    string
}

// Config is what's kept between runs, in <data>/desktop.json.
type Config struct {
	Library   string `json:"library"`   // where originals go (uploads, phone backups)
	Port      int    `json:"port"`      // the web app's
	KeepAwake bool   `json:"keepAwake"` // stop the computer sleeping while Photostead runs (on power)
	// Windows only: Postgres listens on localhost there, so it has passwords (made on first run).
	DBPort        int    `json:"dbPort,omitempty"`
	DBPassword    string `json:"dbPassword,omitempty"`
	SuperPassword string `json:"superPassword,omitempty"`
}

func loadConfig(p Paths) (*Config, error) {
	cfg := &Config{Library: defaultLibrary(), Port: 4800, KeepAwake: true, DBPort: 54329}
	b, err := os.ReadFile(filepath.Join(p.Data, "desktop.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	}
	if cfg.DBPassword == "" {
		cfg.DBPassword = secret()
	}
	if cfg.SuperPassword == "" {
		cfg.SuperPassword = secret()
	}
	return cfg, nil
}

func (c *Config) save(p Paths) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(p.Data, "desktop.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(p.Data, "desktop.json"))
}

func secret() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
