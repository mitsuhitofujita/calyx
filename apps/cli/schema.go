package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// schemaVersion is the version of the `calyx schema` output shape. Bump it only
// on a breaking change to the JSON structure.
const schemaVersion = "1"

// cliSchema is the top-level shape emitted by `calyx schema`: a stable header
// plus a flat list of every command (each carrying its full path).
type cliSchema struct {
	SchemaVersion string          `json:"schema_version"`
	CLI           cliInfo         `json:"cli"`
	Commands      []commandSchema `json:"commands"`
}

// cliInfo identifies the CLI itself.
type cliInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// commandSchema is the serialized form of one command node. Slices are always
// emitted (never null) so consumers can index them unconditionally.
type commandSchema struct {
	Path      []string       `json:"path"`
	Summary   string         `json:"summary"`
	Group     bool           `json:"group,omitempty"`
	Args      []argSpec      `json:"args"`
	Flags     []flagSpec     `json:"flags"`
	Env       []envSpec      `json:"env"`
	ExitCodes []exitCodeSpec `json:"exit_codes"`
	Examples  []string       `json:"examples"`
}

// buildSchema flattens the registry depth-first into a cliSchema. Each node
// carries its full path ([] for the root); a group (Run == nil) is marked
// Group:true; nil slices are normalized to [] so the JSON never contains null.
// Pure (no I/O), so tests can call it directly.
func buildSchema(root *command) cliSchema {
	s := cliSchema{
		SchemaVersion: schemaVersion,
		CLI:           cliInfo{Name: "calyx", Version: version},
	}

	var walk func(c *command, path []string)
	walk = func(c *command, path []string) {
		s.Commands = append(s.Commands, commandSchema{
			Path:      nonNil(path),
			Summary:   c.Summary,
			Group:     c.Run == nil,
			Args:      nonNil(c.Args),
			Flags:     nonNil(c.Flags),
			Env:       nonNil(c.Env),
			ExitCodes: nonNil(c.ExitCodes),
			Examples:  nonNil(c.Examples),
		})
		for _, sub := range c.Sub {
			// Fresh slice per child so sibling paths never alias.
			childPath := append(append([]string{}, path...), sub.Name)
			walk(sub, childPath)
		}
	}
	walk(root, nil)
	return s
}

// schemaJSON marshals the full CLI schema as pretty-printed (2-space) JSON. Pure.
func schemaJSON() ([]byte, error) {
	return json.MarshalIndent(buildSchema(commandRegistry()), "", "  ")
}

// runSchema prints the CLI schema as JSON to stdout and exits 0. Extra args are a
// usage error (matching the other handlers' errUsage convention).
func runSchema(args []string) error {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx schema")
		return errUsage
	}
	data, err := schemaJSON()
	if err != nil {
		return fmt.Errorf("could not encode schema: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// nonNil replaces a nil slice with an empty one so JSON marshals [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
