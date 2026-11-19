package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/mcp"
	"github.com/Frozelo/thingsctl/internal/write"
)

const mcpUsage = `thingsctl mcp — run as an MCP server on stdio
  --read-only        expose only the read tool (also: THINGSCTL_READONLY=1)
  --allow-permanent  let trash empty the Trash and delete areas and tags

example client config:
  {"mcpServers":{"things":{"command":"thingsctl","args":["mcp"]}}}
`

const mcpInstructions = "Things 3 to-do manager on this Mac. Start with read; pass the short ids it prints to the other tools."

const readDescription = "Read Things 3. No args: list counts. Filters search open items. " +
	"Line: <id> <title> [proj N] @start HH:MM eve !deadline #tag [done/total] ~repeat; '<name> <id>:' heads an indented group. Dates MM-DD; header has today=."

func runMCP(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	readOnly := fs.Bool("read-only", false, "")
	allowPermanent := fs.Bool("allow-permanent", false, "")
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
	s := &mcp.Server{
		Name:         "thingsctl",
		Version:      Version,
		Instructions: mcpInstructions,
		Tools:        mcpTools(*readOnly || os.Getenv("THINGSCTL_READONLY") == "1", *allowPermanent),
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
func mcpTools(readOnly, allowPermanent bool) []mcp.Tool {
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
	if readOnly {
		return tools
	}
	var trashOmit []string
	trashDesc := "Move a to-do or project to the Trash."
	if allowPermanent {
		trashDesc = "Move a to-do or project to the Trash. Permanent (confirm=true required): delete an area or tag, or empty=true."
	} else {
		trashOmit = []string{"empty", "confirm"}
	}
	return append(tools,
		writeTool("add", "Create a to-do (default), project, area or tag. Dates: YYYY-MM-DD, today, tomorrow, +Nd.",
			&command.Add{}, nil, annotations(false, false, false),
			func(ctx context.Context, e *write.Env, v any) error { return e.Add(ctx, *v.(*command.Add)) }),
		writeTool("update", "Change, move, complete, cancel or reopen an item. Only given fields change.",
			&command.Update{}, nil, annotations(false, false, false),
			func(ctx context.Context, e *write.Env, v any) error { return e.Update(ctx, *v.(*command.Update)) }),
		writeTool("trash", trashDesc,
			&command.Trash{}, trashOmit, annotations(false, true, true),
			func(ctx context.Context, e *write.Env, v any) error {
				t := *v.(*command.Trash)
				if t.ID == "" && !t.Empty {
					return errors.New(`missing required argument "id"`)
				}
				return e.Trash(ctx, t)
			}),
		writeTool("batch", `Bulk create/update. json: Things URL-scheme JSON array, e.g. [{"type":"to-do","attributes":{"title":"x"}}]`,
			&command.Batch{}, nil, annotations(false, false, false),
			func(ctx context.Context, e *write.Env, v any) error {
				b := *v.(*command.Batch)
				if b.JSON == "-" {
					return errors.New("pass the JSON array itself in json")
				}
				return e.Batch(ctx, b, nil)
			}),
		writeTool("show", "Open an item or list in the Things window.",
			&command.Show{}, nil, annotations(true, false, true),
			func(ctx context.Context, e *write.Env, v any) error { return e.Show(ctx, *v.(*command.Show)) }),
	)
}

func writeTool(name, desc string, proto any, omit []string, ann map[string]any,
	run func(context.Context, *write.Env, any) error) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: desc,
		InputSchema: command.Schema(proto, omit...),
		Annotations: ann,
		Call: func(ctx context.Context, args map[string]any, w io.Writer) error {
			v := newLike(proto)
			if err := command.FromMap(v, args, omit...); err != nil {
				return err
			}
			return withEnv(w, func(e *write.Env) error { return run(ctx, e, v) })
		},
	}
}

// newLike returns a fresh zero value of the same verb struct type.
func newLike(proto any) any { return reflect.New(reflect.TypeOf(proto).Elem()).Interface() }
