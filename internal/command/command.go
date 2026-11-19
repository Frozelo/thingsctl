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
//	mcp:"..."       shorter description for the MCP schema; mcp:"~" none; mcp:"-" hides the field from MCP
//	required:"true" must be given (MCP schema "required")
package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// Read lists a view, searches, or shows one item.
type Read struct {
	View     string `arg:"view" enum:"summary|inbox|today|upcoming|anytime|someday|deadlines|logbook|trash|projects|areas|tags|help" mcp:"~" help:"list to show (default summary; search when -q is set)"`
	ID       string `flag:"id" mcp:"one item in full" help:"show one item in full: to-do, project, area, tag (short or full id)"`
	Q        string `flag:"q" mcp:"search text" help:"search titles and notes (case-insensitive)"`
	Tag      string `flag:"tag" mcp:"~" help:"only items with this tag"`
	Project  string `flag:"project" mcp:"~" help:"only items in this project (id or exact title)"`
	Area     string `flag:"area" mcp:"~" help:"only items in this area (id or exact title)"`
	Modified string `flag:"modified" mcp:"on/after date" help:"only items modified on/after date: YYYY-MM-DD, today, yesterday, -Nd"`
	All      bool   `flag:"all" mcp:"include closed" help:"search also completed and canceled items"`
	Detail   bool   `flag:"detail" mcp:"full detail per item" help:"full detail for every item (notes, checklist, dates)"`
	Limit    int    `flag:"limit" mcp:"~" help:"max items (default 50; logbook and trash 20)"`
	Offset   int    `flag:"offset" mcp:"~" help:"skip the first N items"`
}

// Add creates a to-do (default), project, area or tag.
type Add struct {
	Title     string `arg:"title" required:"true" mcp:"~" help:"title of the new item (required)"`
	Type      string `flag:"type" enum:"todo|project|area|tag" mcp:"~" help:"what to create (default todo)"`
	Notes     string `flag:"notes" mcp:"~" help:"notes text"`
	When      string `flag:"when" mcp:"date|evening|anytime|someday, +@HH:MM reminder" help:"today, tomorrow, evening, anytime, someday, YYYY-MM-DD, +Nd; append @HH:MM for a reminder"`
	Deadline  string `flag:"deadline" mcp:"date" help:"YYYY-MM-DD, today, tomorrow, +Nd"`
	Tags      string `flag:"tags" mcp:"comma-separated" help:"comma-separated existing tags"`
	In        string `flag:"in" mcp:"project/area id or title; tag: parent tag" help:"project or area (id or exact title); for a tag: its parent tag"`
	Heading   string `flag:"heading" mcp:"heading in project" help:"heading title inside the --in project"`
	Checklist string `flag:"checklist" mcp:"items split by ;" help:"checklist items, separated by ; or newlines"`
	Items     string `flag:"items" mcp:"project to-dos split by ;" help:"project only: to-dos to create inside, separated by ; or newlines"`
	DryRun    bool   `flag:"dry-run" mcp:"-" help:"print what would be sent to Things and change nothing"`
}

// Update changes an existing item. Only the given fields change.
type Update struct {
	ID            string `arg:"id" required:"true" mcp:"~" help:"item to change (short or full id; required)"`
	Title         string `flag:"title" mcp:"~" help:"new title"`
	Notes         string `flag:"notes" mcp:"~" help:"notes text (see --notes-mode)"`
	NotesMode     string `flag:"notes-mode" enum:"set|append|prepend" mcp:"~" help:"how --notes applies (default set)"`
	When          string `flag:"when" mcp:"as in add" help:"today, tomorrow, evening, anytime, someday, YYYY-MM-DD, +Nd; append @HH:MM for a reminder"`
	Deadline      string `flag:"deadline" mcp:"date or none" help:"YYYY-MM-DD, today, tomorrow, +Nd, or none to clear"`
	Tags          string `flag:"tags" mcp:"comma-separated" help:"comma-separated tags (see --tags-mode)"`
	TagsMode      string `flag:"tags-mode" enum:"set|add|remove" mcp:"~" help:"how --tags applies (default set)"`
	In            string `flag:"in" mcp:"project/area, inbox; tag: parent tag" help:"move to project or area (id or exact title), or inbox; for a tag: new parent tag"`
	Heading       string `flag:"heading" mcp:"~" help:"move under this heading of the item's project"`
	Checklist     string `flag:"checklist" mcp:"items split by ;" help:"checklist items, separated by ; or newlines (see --checklist-mode)"`
	ChecklistMode string `flag:"checklist-mode" enum:"set|append|prepend" mcp:"~" help:"how --checklist applies (default set)"`
	Status        string `flag:"status" enum:"open|done|canceled" mcp:"~" help:"complete, cancel or reopen"`
	DryRun        bool   `flag:"dry-run" mcp:"-" help:"print what would be sent to Things and change nothing"`
}

// Trash moves a to-do or project to the Trash, or deletes an area or tag.
type Trash struct {
	ID      string `arg:"id" mcp:"~" help:"to-do or project to move to the Trash; areas and tags are deleted permanently"`
	Empty   bool   `flag:"empty" mcp:"empty the Trash" help:"empty the Trash (permanent)"`
	Confirm bool   `flag:"confirm" mcp:"~" help:"required for permanent deletes: --empty, areas, tags"`
	DryRun  bool   `flag:"dry-run" mcp:"-" help:"print what would be sent to Things and change nothing"`
}

// Show opens an item or list in the Things window.
type Show struct {
	Target string `arg:"target" required:"true" mcp:"id or list name" help:"id, or a list: inbox today upcoming anytime someday logbook trash"`
}

// Batch passes a Things JSON array straight to the Things json command.
type Batch struct {
	JSON   string `arg:"json" required:"true" mcp:"~" help:"Things JSON array (see Things URL scheme docs), or - to read stdin"`
	DryRun bool   `flag:"dry-run" mcp:"-" help:"print what would be sent to Things and change nothing"`
}

// Field describes one bindable field of a verb.
type Field struct {
	Name       string
	Positional bool
	Enum       []string
	Help       string
	Kind       reflect.Kind
	Required   bool
	MCPHelp    string // description in the MCP schema
	MCPHidden  bool
	index      int
}

// JSONName is the field's MCP argument name: the flag name with - as _.
func (f Field) JSONName() string { return strings.ReplaceAll(f.Name, "-", "_") }

// Fields returns the bindable fields of a verb struct pointer.
func Fields(v any) []Field {
	t := reflect.TypeOf(v).Elem()
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		f := Field{Help: sf.Tag.Get("help"), Kind: sf.Type.Kind(), index: i,
			Required: sf.Tag.Get("required") == "true"}
		f.MCPHelp = f.Help
		switch m := sf.Tag.Get("mcp"); m {
		case "":
		case "-":
			f.MCPHidden = true
		case "~":
			f.MCPHelp = ""
		default:
			f.MCPHelp = m
		}
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
	return Validate(v)
}

// Validate checks enum fields of a bound verb struct pointer.
func Validate(v any) error {
	rv := reflect.ValueOf(v).Elem()
	for _, f := range Fields(v) {
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

// FromMap binds MCP tool arguments to a verb struct pointer. Keys are
// JSONName()s; hidden fields and names listed in omit are rejected as unknown.
// Numbers and booleans are accepted for string fields, and JSON arrays or
// objects given for a string field are passed on as their JSON text.
func FromMap(v any, args map[string]any, omit ...string) error {
	fields := MCPFields(v, omit...)
	rv := reflect.ValueOf(v).Elem()
	byName := make(map[string]Field, len(fields))
	names := make([]string, len(fields))
	for i, f := range fields {
		byName[f.JSONName()] = f
		names[i] = f.JSONName()
	}
	for k, val := range args {
		f, ok := byName[k]
		if !ok {
			return fmt.Errorf("unknown argument %q; valid: %s", k, strings.Join(names, ", "))
		}
		if val == nil {
			continue
		}
		fv := rv.Field(f.index)
		switch f.Kind {
		case reflect.String:
			switch x := val.(type) {
			case string:
				fv.SetString(x)
			case float64:
				fv.SetString(strconv.FormatFloat(x, 'f', -1, 64))
			case bool:
				fv.SetString(strconv.FormatBool(x))
			default:
				b, err := json.Marshal(x)
				if err != nil {
					return fmt.Errorf("%s: %v", k, err)
				}
				fv.SetString(string(b))
			}
		case reflect.Bool:
			switch x := val.(type) {
			case bool:
				fv.SetBool(x)
			case string:
				b, err := strconv.ParseBool(x)
				if err != nil {
					return fmt.Errorf("%s must be true or false", k)
				}
				fv.SetBool(b)
			default:
				return fmt.Errorf("%s must be true or false", k)
			}
		case reflect.Int:
			var n int64
			switch x := val.(type) {
			case float64:
				if x != float64(int64(x)) {
					return fmt.Errorf("%s must be an integer", k)
				}
				n = int64(x)
			case string:
				var err error
				if n, err = strconv.ParseInt(x, 10, 64); err != nil {
					return fmt.Errorf("%s must be an integer", k)
				}
			default:
				return fmt.Errorf("%s must be an integer", k)
			}
			fv.SetInt(n)
		}
	}
	for _, f := range fields {
		if f.Required && rv.Field(f.index).IsZero() {
			return fmt.Errorf("missing required argument %q", f.JSONName())
		}
	}
	return Validate(v)
}

// MCPFields returns the fields exposed over MCP: not hidden, not in omit.
func MCPFields(v any, omit ...string) []Field {
	var out []Field
	for _, f := range Fields(v) {
		if f.MCPHidden || contains(omit, f.JSONName()) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Schema returns the MCP inputSchema (JSON Schema) of a verb struct pointer.
// Properties keep struct order, so the positional argument comes first.
func Schema(v any, omit ...string) json.RawMessage {
	var props []string
	var required []string
	for _, f := range MCPFields(v, omit...) {
		typ := "string"
		switch f.Kind {
		case reflect.Bool:
			typ = "boolean"
		case reflect.Int:
			typ = "integer"
		}
		p := `{"type":` + quote(typ)
		if len(f.Enum) > 0 {
			p += `,"enum":` + string(mustJSON(f.Enum))
		}
		if f.MCPHelp != "" {
			p += `,"description":` + quote(f.MCPHelp)
		}
		props = append(props, quote(f.JSONName())+":"+p+"}")
		if f.Required {
			required = append(required, f.JSONName())
		}
	}
	s := `{"type":"object","properties":{` + strings.Join(props, ",") + "}"
	if len(required) > 0 {
		s += `,"required":` + string(mustJSON(required))
	}
	return json.RawMessage(s + "}")
}

func quote(s string) string { return string(mustJSON(s)) }

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
