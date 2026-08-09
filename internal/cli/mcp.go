package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// An MCP server, so an agent can operate ratline without shelling out and parsing prose.
//
// Two decisions shape this.
//
// It is read-only unless told otherwise. An agent with a root tool and no gate is a very
// efficient way to delete a server, and "the model will be careful" is not a security
// control. Mutating tools are absent from tools/list entirely without --allow-mutations —
// absent rather than present-and-refusing, because a tool an agent can see is a tool it
// will try, and a refusal it can retry is an invitation to find a way around it.
//
// The protocol is implemented here rather than pulled in. It is JSON-RPC 2.0 over stdio
// with four methods; a dependency for that would cost more than it saves, and the binary
// has to stay static and dependency-light for the same reason the SQLite driver is pure
// Go. The same reasoning as the markdown renderer on the docs site.

const mcpProtocolVersion = "2025-06-18"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpTool is one tool as the protocol describes it.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	// argv turns the call's arguments into a ratline command line. Nothing else in this
	// file builds one, so every tool goes through the same construction and the same
	// allowlist of commands.
	argv    func(args map[string]any) ([]string, error)
	mutates bool
}

// mcpTools is the surface an agent gets. Deliberately small.
//
// Not generated from the schema, unlike the interactive menu: exposing all hundred
// commands would include `user delete --purge` and `db drop`, and a curated list is the
// difference between an agent that can deploy and an agent that can destroy. The schema is
// still how an agent learns the rest — it can read it and tell a human what to run.
func mcpTools() []mcpTool {
	str := func(m map[string]any, key string) string {
		if v, ok := m[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	need := func(m map[string]any, key string) (string, error) {
		if v := str(m, key); v != "" {
			return v, nil
		}
		return "", rlerr.Usagef("%s is required", key)
	}
	object := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	domain := map[string]any{"type": "string", "description": "the site's domain"}

	return []mcpTool{
		{
			Name: "ratline_status",
			Description: "Everything on this server on one screen: tenants, sites and their " +
				"runtimes, certificates near expiry, and a count of problems. Read-only. " +
				"Start here — it is the cheapest way to learn what exists.",
			InputSchema: object(map[string]any{}),
			argv:        func(map[string]any) ([]string, error) { return []string{"status"}, nil },
		},
		{
			Name: "ratline_schema",
			Description: "The full command surface as JSON: every command, every flag with " +
				"its type and whether it is required, the exit codes, and the shape of the " +
				"JSON envelope. Read this before constructing any command you are unsure of, " +
				"rather than guessing a flag name.",
			InputSchema: object(map[string]any{}),
			argv:        func(map[string]any) ([]string, error) { return []string{"schema"}, nil },
		},
		{
			Name:        "ratline_site_list",
			Description: "Every site, its owner, runtime, whether it is running, and its TLS state. Read-only.",
			InputSchema: object(map[string]any{}),
			argv:        func(map[string]any) ([]string, error) { return []string{"site", "list"}, nil },
		},
		{
			Name:        "ratline_site_show",
			Description: "One site in full: paths, unit, socket or port, environment keys (values masked), certificate. Read-only.",
			InputSchema: object(map[string]any{"domain": domain}, "domain"),
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				return []string{"site", "show", d}, err
			},
		},
		{
			Name: "ratline_site_troubleshoot",
			Description: "Diagnose one site: checks the unit, the listener, the application's " +
				"answer and nginx end to end, in dependency order, and names the first thing " +
				"that is wrong. Read-only. Use this before reading logs.",
			InputSchema: object(map[string]any{"domain": domain}, "domain"),
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				return []string{"site", "troubleshoot", d}, err
			},
		},
		{
			Name:        "ratline_site_logs",
			Description: "Recent log lines for one site. Read-only. Secrets are redacted.",
			InputSchema: object(map[string]any{
				"domain": domain,
				"lines":  map[string]any{"type": "integer", "description": "how many lines, default 50"},
			}, "domain"),
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				argv := []string{"site", "logs", d}
				if n, ok := a["lines"].(float64); ok && n > 0 {
					argv = append(argv, "--lines", fmt.Sprintf("%d", int(n)))
				}
				return argv, err
			},
		},
		{
			Name:        "ratline_doctor",
			Description: "Every health check on the server, reporting only what is wrong. Read-only.",
			InputSchema: object(map[string]any{}),
			argv:        func(map[string]any) ([]string, error) { return []string{"doctor"}, nil },
		},
		{
			Name: "ratline_site_jobs",
			Description: "A site's scheduled jobs and long-running workers, with their " +
				"schedules and whether each is enabled. Read-only. These are the part of a " +
				"site least likely to be noticed when they stop.",
			InputSchema: object(map[string]any{"domain": domain}, "domain"),
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				// `site units`, not `site cron list`: the description promises workers
				// too, and the cron spelling filters to kind=job — a worker was invisible
				// to exactly the audience this tool serves.
				return []string{"site", "units", d}, err
			},
		},
		{
			Name:        "ratline_db_list",
			Description: "Databases ratline provisioned. Read-only. No password is ever stored or returned.",
			InputSchema: object(map[string]any{}),
			argv:        func(map[string]any) ([]string, error) { return []string{"db", "list"}, nil },
		},
		{
			Name: "ratline_explain",
			Description: "Long-form documentation on one topic — layout, sockets, deploys, tls, " +
				"ssh, databases, state, safety, diagnose, limits, node, python, static. Read-only. " +
				"Call with no topic for the list.",
			InputSchema: object(map[string]any{
				"topic": map[string]any{"type": "string", "description": "the topic name, or empty for the list"},
			}),
			argv: func(a map[string]any) ([]string, error) {
				if t := str(a, "topic"); t != "" {
					return []string{"explain", t}, nil
				}
				return []string{"explain"}, nil
			},
		},

		// ---- mutating, and hidden unless --allow-mutations ----
		{
			Name: "ratline_site_deploy",
			Description: "Deploy a site: install dependencies, build, restart, and wait for a " +
				"real HTTP response before reporting success. A step that fails leaves the " +
				"previous version serving. Pass dry_run first and read the plan.",
			InputSchema: object(map[string]any{
				"domain":  domain,
				"install": map[string]any{"type": "boolean", "description": "reinstall dependencies"},
				"build":   map[string]any{"type": "boolean", "description": "run the build command"},
				"dry_run": map[string]any{"type": "boolean", "description": "print every mutation without making it"},
			}, "domain"),
			mutates: true,
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				argv := []string{"site", "deploy", d, "--restart"}
				if b, _ := a["install"].(bool); b {
					argv = append(argv, "--install")
				}
				if b, _ := a["build"].(bool); b {
					argv = append(argv, "--build")
				}
				if b, _ := a["dry_run"].(bool); b {
					argv = append(argv, "--dry-run")
				}
				return argv, err
			},
		},
		{
			Name: "ratline_site_restart",
			Description: "Restart one site's service and wait for it to answer. " +
				"The narrowest useful mutation: it changes no configuration and no code.",
			InputSchema: object(map[string]any{"domain": domain}, "domain"),
			mutates:     true,
			argv: func(a map[string]any) ([]string, error) {
				d, err := need(a, "domain")
				return []string{"site", "restart", d}, err
			},
		},
	}
}

// runMCP serves the protocol on stdin and stdout until stdin closes.
func (g *Globals) runMCP(ctx context.Context, allowMutations bool) error {
	tools := mcpTools()
	visible := make([]mcpTool, 0, len(tools))
	for _, t := range tools {
		if t.mutates && !allowMutations {
			continue
		}
		visible = append(visible, t)
	}
	byName := map[string]mcpTool{}
	for _, t := range visible {
		byName[t.Name] = t
	}

	// stdout is the transport, so nothing else may write to it. The logger is already on
	// stderr; this is the reminder that it has to stay there.
	out := json.NewEncoder(g.Stdout)
	out.SetEscapeHTML(false)

	g.Log.Info("mcp server ready", "tools", len(visible), "mutations", allowMutations)

	reader := bufio.NewReaderSize(g.Stdin, 1<<20)
	dec := json.NewDecoder(reader)
	for {
		var req mcpRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return rlerr.Wrap(err, rlerr.CodeUsage, "reading a JSON-RPC message")
		}
		resp := g.handleMCP(ctx, req, visible, byName)
		if resp == nil {
			continue // a notification: no id, no reply
		}
		if err := out.Encode(resp); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "writing a JSON-RPC reply")
		}
	}
}

func (g *Globals) handleMCP(ctx context.Context, req mcpRequest, visible []mcpTool, byName map[string]mcpTool) *mcpResponse {
	reply := func(result any) *mcpResponse {
		return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	fail := func(code int, msg string) *mcpResponse {
		return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		return reply(map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ratline", "version": buildinfo.Version},
			"instructions": "ratline provisions users, sites, certificates and databases on this " +
				"server. Call ratline_status first to see what exists, and ratline_schema when " +
				"you need a command this tool set does not expose — it is the authoritative list " +
				"of flags, so do not guess one. Every result is the JSON envelope " +
				"{ok, command, version, data}; on failure it carries an error code and a hint " +
				"naming what to change. Deployments are safe to retry: a failed step leaves the " +
				"previous version serving.",
		})

	case "notifications/initialized":
		return nil

	case "tools/list":
		list := make([]map[string]any, 0, len(visible))
		for _, t := range visible {
			list = append(list, map[string]any{
				"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
			})
		}
		return reply(map[string]any{"tools": list})

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(-32602, "the parameters could not be read: "+err.Error())
		}
		tool, ok := byName[params.Name]
		if !ok {
			// Naming why, because the commonest case is an agent reaching for a mutating
			// tool on a read-only server and needing to be told that rather than that the
			// tool does not exist.
			for _, t := range mcpTools() {
				if t.Name == params.Name && t.mutates {
					return fail(-32601, params.Name+" is a mutating tool and this server is "+
						"read-only. Start it with --allow-mutations to enable it.")
				}
			}
			return fail(-32601, "no such tool: "+params.Name)
		}
		argv, err := tool.argv(params.Arguments)
		if err != nil {
			return fail(-32602, err.Error())
		}
		text, isErr := g.runForMCP(ctx, argv)
		return reply(map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		})

	case "ping":
		return reply(map[string]any{})
	}
	return fail(-32601, "unsupported method: "+req.Method)
}

// runForMCP executes one ratline command and returns its JSON output.
//
// A fresh root command with --json forced, so a tool result is always the envelope an
// agent can parse rather than a table meant for a terminal. Errors come back as content
// with isError set rather than as protocol errors: the envelope carries the exit code and
// the hint, which is what the agent needs to decide what to do next, and a JSON-RPC error
// would throw that away.
func (g *Globals) runForMCP(ctx context.Context, argv []string) (string, bool) {
	var buf strings.Builder
	sub := &Globals{
		Stdout: &buf, Stderr: g.Stderr, Stdin: strings.NewReader(""),
		JSON: true, NoInput: true,
		Start: g.Start, Argv: argv,
	}
	root := NewRootCommand(sub)
	args := append(argv, "--json", "--no-input")
	// The server's --config must reach the tool call, or every tool would read the
	// default path while the server the operator configured answers about nothing. As
	// a flag rather than a field: parsing the sub-command's flags writes the flag's
	// default over anything pre-set on sub.
	if g.ConfigPath != "" {
		args = append(args, "--config", g.ConfigPath)
	}
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(g.Stderr)

	g.Log.Info("mcp tool call", "argv", strings.Join(argv, " "))
	err := root.ExecuteContext(ctx)
	if err != nil {
		// The command layer writes the failure envelope itself when --json is set; if it
		// did not get that far, synthesise one so the agent still receives structured
		// output rather than a bare string.
		if buf.Len() == 0 {
			code := rlerr.CodeOf(err)
			payload, _ := json.MarshalIndent(map[string]any{
				"ok": false, "command": strings.Join(argv, " "), "version": buildinfo.Version,
				"error": map[string]any{
					"code": int(code), "name": code.Name(),
					"message": err.Error(), "hint": rlerr.Hint(err),
				},
			}, "", "  ")
			return string(payload), true
		}
		return buf.String(), true
	}
	return buf.String(), false
}

func newMCPCommand(g *Globals) *cobra.Command {
	var allowMutations bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve ratline to an AI agent over MCP on stdin and stdout",
		Args:  cobra.NoArgs,
		Long: "Speaks the Model Context Protocol over stdio, so an agent can read the state of\n" +
			"this server and — with --allow-mutations — deploy to it, without shelling out and\n" +
			"parsing output meant for a person.\n\n" +
			"Read-only by default. The mutating tools are not listed at all unless you ask for\n" +
			"them: a tool an agent can see is a tool it will eventually try, and a refusal it\n" +
			"can retry is an invitation to find a way around it.\n\n" +
			"Every call is written to the audit log with the arguments it was given, and every\n" +
			"result is the same JSON envelope the CLI produces, so an agent reads exit codes\n" +
			"and hints rather than guessing from prose.",
		Example: "  # in an MCP client's configuration\n" +
			"  ratline mcp\n" +
			"  ratline mcp --allow-mutations   # adds site deploy and site restart",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if allowMutations {
				g.Log.Warn("this MCP server may change the server",
					"tools", "site deploy, site restart",
					"note", "every call is audited; ask the agent to dry-run first")
			}
			return g.runMCP(cmd.Context(), allowMutations)
		},
	}
	cmd.Flags().BoolVar(&allowMutations, "allow-mutations", false,
		"Also expose the tools that change this server (site deploy, site restart)")
	// Serving the protocol is not itself privileged: each tool re-enters the command tree
	// and the command it runs checks for root on its own. That keeps the failure specific
	// — "site deploy must run as root" rather than a server that will not start — and lets
	// an agent read the schema and the documentation without any privilege at all.
	return NonRoot(cmd)
}
