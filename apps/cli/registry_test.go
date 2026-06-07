package main

import (
	"errors"
	"reflect"
	"testing"
)

// TestResolve exercises routing without invoking the network handlers: it checks
// which leaf each argument vector resolves to (and the unconsumed args), and that
// missing/unknown subcommands are usage errors.
func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantLeaf string   // leaf command Name; "" ⇒ expect errUsage
		wantRest []string // unconsumed args handed to the leaf
	}{
		{name: "sample hello with name", args: []string{"sample", "hello", "World"}, wantLeaf: "hello", wantRest: []string{"World"}},
		{name: "auth login", args: []string{"auth", "login"}, wantLeaf: "login", wantRest: []string{}},
		{name: "auth status", args: []string{"auth", "status"}, wantLeaf: "status", wantRest: []string{}},
		{name: "schema", args: []string{"schema"}, wantLeaf: "schema", wantRest: []string{}},
		{name: "unknown root command", args: []string{"bogus"}},
		{name: "sample without subcommand", args: []string{"sample"}},
		{name: "sample unknown subcommand", args: []string{"sample", "x"}},
		{name: "auth without subcommand", args: []string{"auth"}},
		{name: "auth unknown subcommand", args: []string{"auth", "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, rest, err := commandRegistry().resolve(tt.args)

			if tt.wantLeaf == "" {
				if !errors.Is(err, errUsage) {
					t.Fatalf("resolve(%v) error = %v, want errUsage", tt.args, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolve(%v) error = %v, want nil", tt.args, err)
			}
			if cmd.Name != tt.wantLeaf {
				t.Errorf("resolve(%v) leaf = %q, want %q", tt.args, cmd.Name, tt.wantLeaf)
			}
			if cmd.Run == nil {
				t.Errorf("resolve(%v) returned a group, want a runnable leaf", tt.args)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("resolve(%v) rest = %v, want %v", tt.args, rest, tt.wantRest)
			}
		})
	}
}

// TestArgSuffix documents how positional args render in usage text.
func TestArgSuffix(t *testing.T) {
	tests := []struct {
		name string
		args []argSpec
		want string
	}{
		{name: "none", args: nil, want: ""},
		{name: "required", args: []argSpec{{Name: "name", Required: true}}, want: " <name>"},
		{name: "repeated", args: []argSpec{{Name: "path", Repeated: true}}, want: " [path...]"},
		{name: "optional", args: []argSpec{{Name: "name"}}, want: " [name]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := argSuffix(tt.args); got != tt.want {
				t.Errorf("argSuffix(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
