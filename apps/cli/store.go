package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// SessionToken is the persisted Calyx session credential.
type SessionToken struct {
	Token     string    `json:"session_token"`
	ExpiresAt time.Time `json:"expires_at"` // RFC3339 via encoding/json
}

// TokenStore abstracts session-token persistence so the file backend can later be
// swapped for an OS keyring backend without touching call sites.
type TokenStore interface {
	Save(tok SessionToken) error
	Load() (SessionToken, error) // ErrNoToken when nothing is stored
	Delete() error               // idempotent; nil when absent
}

// ErrNoToken distinguishes "not authenticated" from real I/O / decode failures.
var ErrNoToken = errors.New("no session token stored")

// sessionFilePath resolves the on-disk location of the session file:
// <base>/calyx/session.json, where <base> is CALYX_CONFIG_DIR when set, else the
// OS per-user config dir (os.UserConfigDir). The calyx subdir is always appended.
func sessionFilePath() (string, error) {
	base := os.Getenv("CALYX_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("could not determine user config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "calyx", "session.json"), nil
}

// NewTokenStore selects a TokenStore backend from CALYX_TOKEN_STORE: "" or "file"
// (default, implemented), "keyring" (reserved, not implemented this phase), and any
// other value fails fast with an actionable error.
func NewTokenStore() (TokenStore, error) {
	switch os.Getenv("CALYX_TOKEN_STORE") {
	case "", "file":
		path, err := sessionFilePath()
		if err != nil {
			return nil, err
		}
		return &fileStore{path: path}, nil
	case "keyring":
		return nil, errors.New(`keyring backend is not implemented in this phase; use "file"`)
	default:
		store := os.Getenv("CALYX_TOKEN_STORE")
		return nil, fmt.Errorf(`unknown CALYX_TOKEN_STORE %q: valid values are "file" (default) or "keyring"`, store)
	}
}

// fileStore persists the session token as JSON on the local filesystem.
type fileStore struct {
	path string
}

// Save writes the token atomically: it creates the parent dir (0700), writes a
// temp file (0600) in the same dir, then renames it over the target. On any error
// after the temp file is created, the temp file is removed so no partial file is
// left behind.
func (s *fileStore) Save(tok SessionToken) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode session token: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "session-*.json.tmp")
	if err != nil {
		return fmt.Errorf("could not create temp session file: %w", err)
	}
	tmpPath := tmp.Name()

	// From here on, clean up the temp file on any failure.
	cleanup := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return cause
	}

	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("could not set permissions on session file: %w", err))
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("could not write session file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("could not close session file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("could not finalize session file %s: %w", s.path, err)
	}
	return nil
}

// Load reads and decodes the stored token. A missing file returns ErrNoToken; a
// corrupt file or other read error returns a real (non-ErrNoToken) error.
func (s *fileStore) Load() (SessionToken, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return SessionToken{}, ErrNoToken
		}
		return SessionToken{}, fmt.Errorf("could not read session file %s: %w", s.path, err)
	}

	var tok SessionToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return SessionToken{}, fmt.Errorf("session file %s is corrupt: %w", s.path, err)
	}
	return tok, nil
}

// Delete removes the session file. A missing file is treated as success.
func (s *fileStore) Delete() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("could not delete session file %s: %w", s.path, err)
	}
	return nil
}
