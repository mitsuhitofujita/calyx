package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newFileStore returns a fileStore rooted at an isolated temp config dir. Setting
// CALYX_CONFIG_DIR keeps each test's session file separate and away from the real
// user config dir.
func newFileStore(t *testing.T) *fileStore {
	t.Helper()
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
	path, err := sessionFilePath()
	if err != nil {
		t.Fatalf("sessionFilePath: %v", err)
	}
	return &fileStore{path: path}
}

func sampleToken() SessionToken {
	return SessionToken{
		Token:     "header.payload.signature",
		ExpiresAt: time.Date(2026, 6, 6, 12, 34, 56, 0, time.UTC),
	}
}

func TestFileStore_SaveLoadRoundTrip(t *testing.T) {
	s := newFileStore(t)
	want := sampleToken()

	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Token != want.Token {
		t.Errorf("Token = %q, want %q", got.Token, want.Token)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestFileStore_FilePermissions0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not enforced on Windows")
	}
	s := newFileStore(t)
	if err := s.Save(sampleToken()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
}

func TestFileStore_DirPermissions0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not enforced on Windows")
	}
	s := newFileStore(t)
	if err := s.Save(sampleToken()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Dir(s.path))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}
}

func TestFileStore_LoadNotFoundBeforeSave(t *testing.T) {
	s := newFileStore(t)
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Errorf("Load error = %v, want ErrNoToken", err)
	}
}

func TestFileStore_DeleteRemovesFile(t *testing.T) {
	s := newFileStore(t)
	if err := s.Save(sampleToken()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after Delete, stat err = %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Errorf("Load after Delete = %v, want ErrNoToken", err)
	}
}

func TestFileStore_DeleteIdempotent(t *testing.T) {
	s := newFileStore(t)
	if err := s.Delete(); err != nil {
		t.Errorf("Delete on fresh store = %v, want nil", err)
	}
}

func TestFileStore_LoadCorruptFile(t *testing.T) {
	s := newFileStore(t)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := s.Load()
	if err == nil {
		t.Fatal("Load on corrupt file returned nil error")
	}
	if errors.Is(err, ErrNoToken) {
		t.Errorf("Load on corrupt file = ErrNoToken, want a real decode error")
	}
}

func TestFileStore_SaveOverwrites(t *testing.T) {
	s := newFileStore(t)
	if err := s.Save(sampleToken()); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := SessionToken{Token: "second.jwt.value", ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.Save(second); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Token != second.Token {
		t.Errorf("Token = %q, want %q (second Save should win)", got.Token, second.Token)
	}
	if !got.ExpiresAt.Equal(second.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, second.ExpiresAt)
	}
}

func TestNewTokenStore_Selector(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantFile   bool
		wantErrSub string
	}{
		{name: "default empty", value: "", wantFile: true},
		{name: "file", value: "file", wantFile: true},
		{name: "keyring reserved", value: "keyring", wantErrSub: "not implemented"},
		{name: "unknown value", value: "bogus", wantErrSub: "unknown CALYX_TOKEN_STORE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
			t.Setenv("CALYX_TOKEN_STORE", tc.value)

			store, err := NewTokenStore()
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("NewTokenStore(%q) = nil error, want error containing %q", tc.value, tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewTokenStore(%q): %v", tc.value, err)
			}
			if _, ok := store.(*fileStore); !ok {
				t.Errorf("store type = %T, want *fileStore", store)
			}
		})
	}
}

func TestSessionFilePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CALYX_CONFIG_DIR", tmp)
	got, err := sessionFilePath()
	if err != nil {
		t.Fatalf("sessionFilePath: %v", err)
	}
	want := filepath.Join(tmp, "calyx", "session.json")
	if got != want {
		t.Errorf("sessionFilePath = %q, want %q", got, want)
	}
}
