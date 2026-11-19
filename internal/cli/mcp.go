package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/mcp"
	"github.com/Frozelo/thingsctl/internal/write"
)

const mcpUsage = `thingsctl mcp — run as an MCP server on stdio
  --read-only        expose only the read tool (also: THINGSCTL_READONLY=1)
  --allow-permanent  let trash empty the Trash and delete areas and tags
  --dry-run          validate writes but send nothing to Things
  --surface S        tools (default: read add update trash batch show) or
                     ops (experimental: read write trash show help)

example client config:
  {"mcpServers":{"things":{"command":"thingsctl","args":["mcp"]}}}
`

const mcpInstructions = "Things 3 to-do manager on this Mac. Name items by short id or exact title; when the user gives a title, act on it directly without reading first. " +
	"Every write replies with the item's state read back from Things, so there is no need to read it again."

const readDescription = "Read Things 3. No args: list counts. Filters search open items. " +
	"Line: <id> <title> [proj N] @start HH:MM eve !deadline #tag [done/total] ~repeat; '<name> <id>:' heads an indented group. Dates MM-DD; header has today=."

// mcpConfig selects what the MCP server exposes.
type mcpConfig struct {
	readOnly, allowPermanent, dryRun bool
	surface                          string // "tools" or "ops"
}

func runMCP(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cfg mcpConfig
	fs.BoolVar(&cfg.readOnly, "read-only", false, "")
	fs.BoolVar(&cfg.allowPermanent, "allow-permanent", false, "")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "")
	fs.StringVar(&cfg.surface, "surface", "tools", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, mcpUsage)
			return nil
		}
		return fmt.Errorf("%v (see: thingsctl mcp -h)", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see: thingsctl mcp -h)", fs.Arg(0))
	}
	if cfg.surface != "tools" && cfg.surface != "ops" {
		return fmt.Errorf("invalid --surface %q; valid: tools, ops", cfg.surface)
	}
	cfg.readOnly = cfg.readOnly || os.Getenv("THINGSCTL_READONLY") == "1"
	s := &mcp.Server{
		Name:         "thingsctl",
		Version:      Version,
		Instructions: mcpInstructions,
		Tools:        mcpTools(cfg),
	}
	return s.Serve(context.Background(), stdin, stdout)
}

func annotations(readOnly, destructive, idempotent bool) map[string]any {
	a := map[string]any{"openWorldHint": false}
	if readOnly {
		a["readOnlyHint"] = true
	} else {
		a["destructiveHint"] = destructive
		a["idempotentHint"] = idempotent
	}
	return a
}

// mcpTools builds the MCP tools from the same command structs as the CLI.
func mcpTools(cfg mcpConfig) []mcp.Tool {
	tools := []mcp.Tool{{
		Name:        "read",
		Description: readDescription,
		InputSchema: command.Schema(&command.Read{}),
		Annotations: annotations(true, false, true),
		Call: func(ctx context.Context, args map[string]any, w io.Writer) error {
			var r command.Read
			if err := command.FromMap(&r, args); err != nil {
				return err
			}
			return execRead(r, w)
		},
	}}
	if cfg.readOnly {
		return tools
	}
	var trashOmit []string
	trashDesc := "Move a to-do or project to the Trash."
	if cfg.allowPermanent {
		trashDesc = "Move a to-do or project to the Trash. Permanent (confirm=true required): delete an area or tag, or empty=true."
	} else {
		trashOmit = []string{"empty", "confirm"}
	}
	trash := cfg.tool("trash", trashDesc, &command.Trash{}, trashOmit, annotations(false, true, true))
	show := cfg.tool("show", "Open an item or list in the Things window.", &command.Show{}, nil, annotations(true, false, true))
	if cfg.surface == "ops" {
		return append(tools, cfg.opsTool(), trash, show, helpTool(trashOmit))
	}
	return append(tools,
		cfg.tool("add", "Create a to-do (default), project, area or tag. Dates: YYYY-MM-DD, today, tomorrow, +Nd. Replies with the new item as Things saved it.",
			&command.Add{}, nil, annotations(false, false, false)),
		cfg.tool("update", "Change, move, complete, cancel or reopen an item, or with where every matching to-do at once. Only given fields change. Replies with the items as Things saved them.",
			&command.Update{}, nil, annotations(false, false, false)),
		trash,
		cfg.tool("batch", `Bulk create/update. json: Things URL-scheme JSON array, e.g. [{"type":"to-do","attributes":{"title":"x"}}]`,
			&command.Batch{}, nil, annotations(false, false, false)),
		show,
	)
}

// runVerb runs one bound write verb.
func runVerb(ctx context.Context, e *write.Env, v any) error {
	switch c := v.(type) {
	case *command.Add:
		return e.Add(ctx, *c)
	case *command.Update:
		return e.Update(ctx, *c)
	case *command.Trash:
		if c.ID == "" && !c.Empty {
			return errors.New(`missing required argument "id"`)
		}
		return e.Trash(ctx, *c)
	case *command.Batch:
		if c.JSON == "-" {
			return errors.New("pass the JSON array itself in json")
		}
		return e.Batch(ctx, *c, nil)
	case *command.Show:
		return e.Show(ctx, *c)
	}
	return fmt.Errorf("runVerb: %T", v)
}

// exec binds args to a fresh verb struct and runs it. In dry-run mode the
// write is planned and validated, its plan is discarded, and a short
// receipt is printed instead.
func (cfg mcpConfig) exec(ctx context.Context, proto any, omit []string, args map[string]any, w io.Writer) error {
	v := newLike(proto)
	if err := command.FromMap(v, args, omit...); err != nil {
		return err
	}
	if !cfg.dryRun {
		return withEnv(w, func(e *write.Env) error { return runVerb(ctx, e, v) })
	}
	if f := reflect.ValueOf(v).Elem().FieldByName("DryRun"); f.IsValid() {
		f.SetBool(true)
		if err := withEnv(io.Discard, func(e *write.Env) error { return runVerb(ctx, e, v) }); err != nil {
			return err
		}
		f.SetBool(false)
	}
	if Simulate != nil {
		return Simulate(ctx, v, w)
	}
	fmt.Fprint(w, "ok (dry run)")
	return nil
}

// Simulate, when set, receives every validated write of a --dry-run MCP
// server instead of the dry-run receipt. The eval harness uses it to apply
// writes to a copy of a seed database.
var Simulate func(ctx context.Context, verb any, w io.Writer) error

func (cfg mcpConfig) tool(name, desc string, proto any, omit []string, ann map[string]any) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: desc,
		InputSchema: command.Schema(proto, omit...),
		Annotations: ann,
		Call: func(ctx context.Context, args map[string]any, w io.Writer) error {
			return cfg.exec(ctx, proto, omit, args, w)
		},
	}
}

// opsTool is the experimental single write tool: a list of add/update ops
// whose fields are described as compact text instead of JSON Schema.
func (cfg mcpConfig) opsTool() mcp.Tool {
	protos := map[string]any{"add": &command.Add{}, "update": &command.Update{}}
	desc := "Create or change items; ops run in order. Fields (* required): add: " + command.Compact(&command.Add{}) +
		". update: " + command.Compact(&command.Update{}) + ". Value formats: help."
	return mcp.Tool{
		Name:        "write",
		Description: desc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"ops":{"type":"array","items":{"type":"object","properties":{"op":{"type":"string","enum":["add","update"]}},"required":["op"]}}},"required":["ops"]}`),
		Annotations: annotations(false, false, false),
		Call: func(ctx context.Context, args map[string]any, w io.Writer) error {
			ops, ok := args["ops"].([]any)
			if !ok || len(args) != 1 || len(ops) == 0 {
				return errors.New(`expected {"ops":[{"op":"add"|"update",...},...]}`)
			}
			failed := 0
			for i, raw := range ops {
				m, _ := raw.(map[string]any)
				name, _ := m["op"].(string)
				proto, ok := protos[name]
				var err error
				if !ok {
					err = fmt.Errorf(`op must be "add" or "update"`)
				} else {
					fields := make(map[string]any, len(m))
					for k, v := range m {
						if k != "op" {
							fields[k] = v
						}
					}
					var out strings.Builder
					err = cfg.exec(ctx, proto, nil, fields, &out)
					fmt.Fprintln(w, strings.TrimRight(out.String(), "\n"))
				}
				if err != nil {
					failed++
					fmt.Fprintf(w, "op %d: error: %v\n", i+1, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d ops failed", failed, len(ops))
			}
			return nil
		},
	}
}

// helpTool explains the fields and value formats of the ops surface.
func helpTool(trashOmit []string) mcp.Tool {
	verbs := map[string]any{"add": &command.Add{}, "update": &command.Update{}, "trash": &command.Trash{}, "read": &command.Read{}}
	return mcp.Tool{
		Name:        "help",
		Description: "Fields and value formats of a tool.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","enum":["add","update","trash","read"]}},"required":["topic"]}`),
		Annotations: annotations(true, false, true),
		Call: func(ctx context.Context, args map[string]any, w io.Writer) error {
			topic, _ := args["topic"].(string)
			v, ok := verbs[topic]
			if !ok {
				return errors.New("topic must be add, update, trash or read")
			}
			var omit []string
			if topic == "trash" {
				omit = trashOmit
			}
			command.FieldHelp(w, v, omit...)
			return nil
		},
	}
}

// newLike returns a fresh zero value of the same verb struct type.
func newLike(proto any) any { return reflect.New(reflect.TypeOf(proto).Elem()).Interface() }
