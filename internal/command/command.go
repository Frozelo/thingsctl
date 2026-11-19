// Package command is the single source of truth for thingsctl's grammar.
// Each verb is a struct whose tags drive both the CLI flags and (later) the
// MCP tool schema, so the two front-ends can never drift apart.
//
// Tags:
//
//	arg:"name"      positional argument (at most one per verb)
//	flag:"name"     CLI flag / MCP field name
//	enum:"a|b|c"    allowed values
//	help:"..."      one-line description
package command

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Read lists a view, searches, or shows one item.
type Read struct {
	View     string `arg:"view" enum:"summary|inbox|today|upcoming|anytime|someday|deadlines|logbook|trash|projects|areas|tags|help" help:"list to show (default summary; search when -q is set)"`
	ID       string `flag:"id" help:"show one item in full: to-do, project, area, tag (short or full id)"`
	Q        string `flag:"q" help:"search titles and notes (case-insensitive)"`
	Tag      string `flag:"tag" help:"only items with this tag"`
	Project  string `flag:"project" help:"only items in this project (id or exact title)"`
	Area     string `flag:"area" help:"only items in this area (id or exact title)"`
	Modified string `flag:"modified" help:"only items modified on/after date: YYYY-MM-DD, today, yesterday, -Nd"`
	All      bool   `flag:"all" help:"search also completed and canceled items"`
	Detail   bool   `flag:"detail" help:"full detail for every item (notes, checklist, dates)"`
	Limit    int    `flag:"limit" help:"max items (default 50; logbook and trash 20)"`
	Offset   int    `flag:"offset" help:"skip the first N items"`
}

// Add creates a to-do (default), project, area or tag.
type Add struct {
	Title     string `arg:"title" help:"title of the new item (required)"`
	Type      string `flag:"type" enum:"todo|project|area|tag" help:"what to create (default todo)"`
	Notes     string `flag:"notes" help:"notes text"`
	When      string `flag:"when" help:"today, tomorrow, evening, anytime, someday, YYYY-MM-DD, +Nd; append @HH:MM for a reminder"`
	Deadline  string `flag:"deadline" help:"YYYY-MM-DD, today, tomorrow, +Nd"`
	Tags      string `flag:"tags" help:"comma-separated existing tags"`
	In        string `flag:"in" help:"project or area (id or exact title); for a tag: its parent tag"`
	Heading   string `flag:"heading" help:"heading title inside the --in project"`
	Checklist string `flag:"checklist" help:"checklist items, separated by ; or newlines"`
	Items     string `flag:"items" help:"project only: to-dos to create inside, separated by ; or newlines"`
	DryRun    bool   `flag:"dry-run" help:"print what would be sent to Things and change nothing"`
}

// Update changes an existing item. Only the given fields change.
type Update struct {
	ID            string `arg:"id" help:"item to change (short or full id; required)"`
	Title         string `flag:"title" help:"new title"`
	Notes         string `flag:"notes" help:"notes text (see --notes-mode)"`
	NotesMode     string `flag:"notes-mode" enum:"set|append|prepend" help:"how --notes applies (default set)"`
	When          string `flag:"when" help:"today, tomorrow, evening, anytime, someday, YYYY-MM-DD, +Nd; append @HH:MM for a reminder"`
	Deadline      string `flag:"deadline" help:"YYYY-MM-DD, today, tomorrow, +Nd, or none to clear"`
	Tags          string `flag:"tags" help:"comma-separated tags (see --tags-mode)"`
	TagsMode      string `flag:"tags-mode" enum:"set|add|remove" help:"how --tags applies (default set)"`
	In            string `flag:"in" help:"move to project or area (id or exact title), or inbox; for a tag: new parent tag"`
	Heading       string `flag:"heading" help:"move under this heading of the item's project"`
	Checklist     string `flag:"checklist" help:"checklist items, separated by ; or newlines (see --checklist-mode)"`
	ChecklistMode string `flag:"checklist-mode" enum:"set|append|prepend" help:"how --checklist applies (default set)"`
	Status        string `flag:"status" enum:"open|done|canceled" help:"complete, cancel or reopen"`
	DryRun        bool   `flag:"dry-run" help:"print what would be sent to Things and change nothing"`
}

// Trash moves a to-do or project to the Trash, or deletes an area or tag.
type Trash struct {
	ID      string `arg:"id" help:"to-do or project to move to the Trash; areas and tags are deleted permanently"`
	Empty   bool   `flag:"empty" help:"empty the Trash (permanent)"`
	Confirm bool   `flag:"confirm" help:"required for permanent deletes: --empty, areas, tags"`
	DryRun  bool   `flag:"dry-run" help:"print what would be sent to Things and change nothing"`
}

// Show opens an item or list in the Things window.
type Show struct {
	Target string `arg:"target" help:"id, or a list: inbox today upcoming anytime someday logbook trash"`
}

// Batch passes a Things JSON array straight to the Things json command.
type Batch struct {
	JSON   string `arg:"json" help:"Things JSON array (see Things URL scheme docs), or - to read stdin"`
	DryRun bool   `flag:"dry-run" help:"print what would be sent to Things and change nothing"`
}

// Field describes one bindable field of a verb.
type Field struct {
	Name       string
	Positional bool
	Enum       []string
	Help       string
	Kind       reflect.Kind
	index      int
}

// Fields returns the bindable fields of a verb struct pointer.
func Fields(v any) []Field {
	t := reflect.TypeOf(v).Elem()
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		f := Field{Help: sf.Tag.Get("help"), Kind: sf.Type.Kind(), index: i}
		if n := sf.Tag.Get("arg"); n != "" {
			f.Name, f.Positional = n, true
		} else if n := sf.Tag.Get("flag"); n != "" {
			f.Name = n
		} else {
			continue
		}
		if e := sf.Tag.Get("enum"); e != "" {
			f.Enum = strings.Split(e, "|")
		}
		out = append(out, f)
	}
	return out
}

// Parse binds CLI args to a verb struct pointer. Flags may appear before or
// after the positional argument. Enum values are validated.
func Parse(verb string, v any, args []string) error {
	fields := Fields(v)
	rv := reflect.ValueOf(v).Elem()
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var positional *Field
	for i := range fields {
		f := &fields[i]
		if f.Positional {
			positional = f
			continue
		}
		p := rv.Field(f.index).Addr().Interface()
		switch ptr := p.(type) {
		case *string:
			fs.StringVar(ptr, f.Name, "", f.Help)
		case *bool:
			fs.BoolVar(ptr, f.Name, false, f.Help)
		case *int:
			fs.IntVar(ptr, f.Name, 0, f.Help)
		default:
			return fmt.Errorf("unsupported field type for %s", f.Name)
		}
	}
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return usageError(verb, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
	if len(rest) > 1 || (len(rest) == 1 && positional == nil) {
		return fmt.Errorf("unexpected argument %q (see: thingsctl %s -h)", rest[len(rest)-1], verb)
	}
	if len(rest) == 1 {
		rv.Field(positional.index).SetString(rest[0])
	}
	for _, f := range fields {
		if len(f.Enum) == 0 {
			continue
		}
		val := rv.Field(f.index).String()
		if val != "" && !contains(f.Enum, val) {
			return fmt.Errorf("invalid %s %q; valid: %s", f.Name, val, strings.Join(f.Enum, ", "))
		}
	}
	return nil
}

func usageError(verb string, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return flag.ErrHelp
	}
	return fmt.Errorf("%v (see: thingsctl %s -h)", err, verb)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Usage writes compact help for a verb.
func Usage(w io.Writer, verb, summary string, v any) {
	fmt.Fprintf(w, "thingsctl %s — %s\n", verb, summary)
	for _, f := range Fields(v) {
		name := "--" + f.Name
		if f.Positional {
			name = "<" + f.Name + ">"
		} else if f.Kind == reflect.Int {
			name += " N"
		} else if f.Kind == reflect.String {
			name += " S"
		}
		line := fmt.Sprintf("  %-14s %s", name, f.Help)
		if len(f.Enum) > 0 {
			line += ": " + strings.Join(f.Enum, " ")
		}
		fmt.Fprintln(w, line)
	}
}
