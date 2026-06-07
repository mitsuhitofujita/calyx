package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// The spec types below are descriptive metadata for a command. They are consumed
// both by dispatch (via resolve, to route) and by `calyx schema` (via buildSchema,
// to serialize), so the published metadata cannot drift from what actually runs.
// JSON tags double as the wire shape emitted by `calyx schema`.

// argSpec describes a positional argument.
type argSpec struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Repeated bool   `json:"repeated"`
}

// flagSpec describes a flag.
type flagSpec struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	Purpose string `json:"purpose"`
}

// envSpec describes an environment variable a command consumes.
type envSpec struct {
	Name     string `json:"name"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
	Purpose  string `json:"purpose"`
}

// exitCodeSpec documents one exit code and the condition that produces it.
type exitCodeSpec struct {
	Code int    `json:"code"`
	When string `json:"when"`
}

// command is a node in the CLI command tree. Run is nil for a group (a
// non-runnable namespace such as the root, `sample`, or `auth`); a leaf carries a
// non-nil Run. dispatch walks this tree to route; `schema` walks the same tree to
// serialize.
type command struct {
	Name      string
	Summary   string
	Args      []argSpec
	Flags     []flagSpec
	Env       []envSpec
	ExitCodes []exitCodeSpec
	Examples  []string
	Run       func(args []string) error // nil ⇒ group
	Sub       []*command
}

// commandRegistry builds and returns the root of the command tree. It is the
// single declarative source for routing, usage text, and `calyx schema` output.
// Cheap to call; both dispatch and runSchema call it afresh. There is no
// init-time cycle: the schema node references the runSchema function *value*, it
// does not invoke it here.
func commandRegistry() *command {
	// Env vars shared by every backend-touching command. Defining them once keeps
	// their defaults and wording identical across commands.
	backendEnv := envSpec{
		Name:    "CALYX_BACKEND_ADDR",
		Default: defaultBackendAddr,
		Purpose: "backend gRPC address",
	}
	tokenStoreEnv := envSpec{
		Name:    "CALYX_TOKEN_STORE",
		Default: "file",
		Purpose: `session-token store backend: "file" (default) or "keyring"`,
	}
	configDirEnv := envSpec{
		Name:    "CALYX_CONFIG_DIR",
		Purpose: "base dir for the session file (<config-dir>/calyx/session.json); defaults to the OS per-user config dir",
	}

	schema := &command{
		Name:    "schema",
		Summary: "print the CLI command tree and metadata as JSON",
		ExitCodes: []exitCodeSpec{
			{Code: 0, When: "JSON printed"},
			{Code: 1, When: "usage error"},
		},
		Examples: []string{"calyx schema"},
		Run:      runSchema,
	}

	hello := &command{
		Name:    "hello",
		Summary: "greet <name> via the backend",
		Args:    []argSpec{{Name: "name", Required: true}},
		Env:     []envSpec{backendEnv, tokenStoreEnv, configDirEnv},
		ExitCodes: []exitCodeSpec{
			{Code: 0, When: "greeting printed"},
			{Code: 1, When: "usage error or RPC failure"},
		},
		Examples: []string{"calyx sample hello World"},
		Run:      runHello,
	}

	sample := &command{
		Name:    "sample",
		Summary: "sample-service commands (a thin gRPC client of the backend)",
		Sub:     []*command{hello},
	}

	login := &command{
		Name:    "login",
		Summary: "sign in with Google and store a session token",
		Env: []envSpec{
			{Name: "CALYX_GOOGLE_CLIENT_ID", Required: true, Purpose: "Google OAuth client ID (read from .env in the working dir or the environment)"},
			{Name: "CALYX_GOOGLE_CLIENT_SECRET", Required: true, Purpose: "Google OAuth client secret (read from .env in the working dir or the environment)"},
			{Name: "CALYX_OAUTH_REDIRECT_ADDR", Default: defaultRedirectAddr, Purpose: "loopback host:port for the OAuth callback server"},
			tokenStoreEnv,
			configDirEnv,
			backendEnv,
		},
		ExitCodes: []exitCodeSpec{
			{Code: 0, When: "logged in, session token saved"},
			{Code: 1, When: "usage error, missing config, OAuth failure, or backend failure"},
		},
		Examples: []string{"calyx auth login"},
		Run:      runAuthLogin,
	}

	status := &command{
		Name:    "status",
		Summary: "show the current session status",
		Env:     []envSpec{tokenStoreEnv, configDirEnv, backendEnv},
		ExitCodes: []exitCodeSpec{
			{Code: 0, When: "status reported (authenticated or not)"},
			{Code: 1, When: "usage error, misconfigured store, load error, or unreachable backend"},
		},
		Examples: []string{"calyx auth status"},
		Run:      runAuthStatus,
	}

	auth := &command{
		Name:    "auth",
		Summary: "authentication commands (Google sign-in and session status)",
		Sub:     []*command{login, status},
	}

	return &command{
		Name:    "calyx",
		Summary: "Calyx sample CLI: a thin gRPC client of the backend.",
		Flags:   []flagSpec{{Name: "version", Type: "bool", Purpose: "print version information"}},
		ExitCodes: []exitCodeSpec{
			{Code: 0, When: "usage printed (no args) or --version printed"},
			{Code: 1, When: "unknown command"},
		},
		Examples: []string{"calyx --version"},
		Sub:      []*command{schema, sample, auth},
	}
}

// resolve routes args to a runnable leaf, descending through groups by name. It
// returns the leaf and its remaining (unconsumed) args. For a missing or unknown
// subcommand it writes a usage message to stderr and returns errUsage.
//
// resolve does NOT validate leaf argument counts — the leaf handlers still own
// their own arity checks. The registry's Args metadata is therefore descriptive
// (it documents and shapes the schema/usage), while the handler remains the
// enforcer. The no-drift test guarantees command *presence*, not arity.
func (c *command) resolve(args []string) (*command, []string, error) {
	return c.route(nil, args)
}

// route is resolve's recursion, threading the path walked so far (used only for
// usage/error messages).
func (c *command) route(path, args []string) (*command, []string, error) {
	if c.Run != nil {
		return c, args, nil
	}
	if len(args) == 0 {
		c.writeUsage(os.Stderr, path)
		return nil, nil, errUsage
	}
	for _, sub := range c.Sub {
		if sub.Name == args[0] {
			return sub.route(append(path, sub.Name), args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "%s: unknown command %q\n", usageLabel(path), args[0])
	return nil, nil, errUsage
}

// writeUsage writes a registry-derived usage message for c to w. path is c's own
// command path (nil for the root). It prints c's flags, then every runnable leaf
// in c's subtree as "  <full path>[ <args>]   <summary>". Both flag.Usage (on the
// root) and a group invoked without a subcommand use it, so help text stays in
// sync with the registry instead of a hardcoded list.
func (c *command) writeUsage(w io.Writer, path []string) {
	fmt.Fprintf(w, "Usage of %s:\n", usageLabel(path))
	for _, f := range c.Flags {
		fmt.Fprintf(w, "  --%s\t%s\n", f.Name, f.Purpose)
	}
	fmt.Fprintf(w, "\nCommands:\n")

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	c.eachLeaf(path, func(leafPath []string, leaf *command) {
		fmt.Fprintf(tw, "  %s%s\t%s\n", strings.Join(leafPath, " "), argSuffix(leaf.Args), leaf.Summary)
	})
	_ = tw.Flush()
}

// eachLeaf calls fn for every runnable leaf in c's subtree, in definition order,
// passing each leaf's full path (path is c's own path). The slice handed to fn is
// only valid for the duration of the call.
func (c *command) eachLeaf(path []string, fn func(path []string, leaf *command)) {
	if c.Run != nil {
		fn(path, c)
		return
	}
	for _, sub := range c.Sub {
		sub.eachLeaf(append(path, sub.Name), fn)
	}
}

// usageLabel renders a command path as a CLI invocation prefix: nil → "calyx",
// ["auth"] → "calyx auth".
func usageLabel(path []string) string {
	if len(path) == 0 {
		return "calyx"
	}
	return "calyx " + strings.Join(path, " ")
}

// argSuffix renders a leaf's positional args for usage text: " <name>" for a
// required arg, " [name...]" for a repeated one, " [name]" otherwise.
func argSuffix(args []argSpec) string {
	var b strings.Builder
	for _, a := range args {
		switch {
		case a.Repeated:
			fmt.Fprintf(&b, " [%s...]", a.Name)
		case a.Required:
			fmt.Fprintf(&b, " <%s>", a.Name)
		default:
			fmt.Fprintf(&b, " [%s]", a.Name)
		}
	}
	return b.String()
}
