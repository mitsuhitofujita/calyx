package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden file when `go test -update` is passed.
var update = flag.Bool("update", false, "update golden files")

// pathKey joins a command path into a stable map key ("" for the root).
func pathKey(path []string) string { return strings.Join(path, "/") }

// TestSchema_CoversRegistry is the no-drift guarantee: the set of command paths
// the registry can dispatch must equal the set `schema` emits — no missing, no
// extra, no duplicates. Adding a command (or a descriptor) without the other
// fails here.
func TestSchema_CoversRegistry(t *testing.T) {
	// Independently enumerate every node path in the registry.
	want := map[string]bool{}
	var walk func(c *command, path []string)
	walk = func(c *command, path []string) {
		want[pathKey(path)] = true
		for _, sub := range c.Sub {
			walk(sub, append(append([]string{}, path...), sub.Name))
		}
	}
	walk(commandRegistry(), nil)

	got := map[string]bool{}
	for _, cmd := range buildSchema(commandRegistry()).Commands {
		key := pathKey(cmd.Path)
		if got[key] {
			t.Errorf("duplicate command path in schema: %q", key)
		}
		got[key] = true
	}

	for key := range want {
		if !got[key] {
			t.Errorf("schema is missing command path %q present in the registry", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("schema has command path %q absent from the registry", key)
		}
	}
}

// TestSchemaJSON_Valid round-trips the emitted JSON and checks the stable header,
// the expected command paths, and that slices serialize as [] (never null).
func TestSchemaJSON_Valid(t *testing.T) {
	data, err := schemaJSON()
	if err != nil {
		t.Fatalf("schemaJSON: %v", err)
	}

	if bytes.Contains(data, []byte("null")) {
		t.Errorf("schema JSON contains null; slices must serialize as []\n%s", data)
	}

	var s cliSchema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	if s.SchemaVersion != schemaVersion {
		t.Errorf("schema_version = %q, want %q", s.SchemaVersion, schemaVersion)
	}
	if s.CLI.Name != "calyx" {
		t.Errorf("cli.name = %q, want %q", s.CLI.Name, "calyx")
	}
	if s.CLI.Version != version {
		t.Errorf("cli.version = %q, want %q", s.CLI.Version, version)
	}

	have := map[string]bool{}
	for _, c := range s.Commands {
		have[pathKey(c.Path)] = true
	}
	for _, p := range [][]string{
		{}, // root
		{"schema"},
		{"sample", "hello"},
		{"auth", "login"},
		{"auth", "status"},
	} {
		if !have[pathKey(p)] {
			t.Errorf("schema missing expected path %v", p)
		}
	}
}

// TestRunSchema_RejectsExtraArgs confirms `calyx schema extra` is a usage error.
func TestRunSchema_RejectsExtraArgs(t *testing.T) {
	if err := runSchema([]string{"x"}); !errors.Is(err, errUsage) {
		t.Fatalf("runSchema with extra args = %v, want errUsage", err)
	}
}

// TestSchema_Golden is the full-shape guard: it compares the pretty JSON against
// a committed golden file, catching unintended env/exit-code/wording changes.
// Regenerate with: go test ./apps/cli -run TestSchema_Golden -update
func TestSchema_Golden(t *testing.T) {
	data, err := schemaJSON()
	if err != nil {
		t.Fatalf("schemaJSON: %v", err)
	}

	golden := filepath.Join("testdata", "schema.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (regenerate with `go test ./apps/cli -run TestSchema_Golden -update`): %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(data)) {
		t.Errorf("schema JSON differs from golden; re-run with -update if intended\n--- got ---\n%s", data)
	}
}
