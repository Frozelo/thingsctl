//go:build eval

package eval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
	"github.com/Frozelo/thingsctl/internal/when"
)

// Effect is one successful write, normalized so that equivalent calls on
// either surface (and Things JSON via batch) compare equal.
type Effect struct {
	Verb          string // add, update, trash
	ID            string // full uuid (update, trash)
	Type          string // add: todo, project, area, tag
	Title         string
	Notes         string
	NotesMode     string
	When          string // canonical when.URLValue, or raw if unparsable
	Deadline      string // ISO date or "none"
	Tags          []string
	TagsMode      string
	In            string // container uuid, "inbox", or raw
	Heading       string
	Checklist     []string
	ChecklistMode string
	Items         []string
	Status        string
}

func (e Effect) String() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Call is one MCP tool call as recorded by the client.
type Call struct {
	Tool string
	Args map[string]any
	Text string // result text
	Err  bool   // transport or tool error
}

// norm resolves references against the seed.
type norm struct {
	ids        *shortid.Index
	containers map[string]string // lower title -> uuid
	today      thingsdb.Date
}

func (n norm) id(s string) string {
	if full, err := n.ids.Resolve(strings.TrimSpace(s)); err == nil {
		return full
	}
	return s
}

func (n norm) container(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "inbox") {
		return "inbox"
	}
	if u, ok := n.containers[strings.ToLower(s)]; ok {
		return u
	}
	return n.id(s)
}

func (n norm) when(s string) string {
	if s == "" {
		return ""
	}
	w, err := when.Parse(strings.TrimSpace(s), n.today)
	if err != nil {
		return "raw:" + s
	}
	return w.URLValue()
}

func (n norm) deadline(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return s
	}
	d, err := when.Day(s, n.today)
	if err != nil {
		return "raw:" + s
	}
	return d.ISO()
}

func list(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

func tags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func (n norm) fromAdd(a command.Add) Effect {
	typ := a.Type
	if typ == "" {
		typ = "todo"
	}
	return Effect{Verb: "add", Type: typ, Title: strings.TrimSpace(a.Title), Notes: a.Notes, When: n.when(a.When),
		Deadline: n.deadline(a.Deadline), Tags: tags(a.Tags), In: n.container(a.In), Heading: a.Heading,
		Checklist: list(a.Checklist), Items: list(a.Items)}
}

func (n norm) fromUpdate(u command.Update) Effect {
	return Effect{Verb: "update", ID: n.id(u.ID), Title: strings.TrimSpace(u.Title), Notes: u.Notes, NotesMode: u.NotesMode,
		When: n.when(u.When), Deadline: n.deadline(u.Deadline), Tags: tags(u.Tags), TagsMode: u.TagsMode,
		In: n.container(u.In), Heading: u.Heading, Checklist: list(u.Checklist), ChecklistMode: u.ChecklistMode, Status: u.Status}
}

// failedOps returns which ops (1-based) of a write call failed.
func failedOps(text string) map[int]bool {
	out := map[int]bool{}
	for _, l := range strings.Split(text, "\n") {
		var i int
		if _, err := fmt.Sscanf(l, "op %d: error:", &i); err == nil {
			out[i] = true
		}
	}
	return out
}

func failed(c Call) bool {
	return c.Err || strings.HasPrefix(c.Text, "error:") || strings.Contains(c.Text, "\nerror:")
}

// Effects converts recorded calls into successful write effects.
func (n norm) Effects(calls []Call) (adds []Effect, updates map[string]Effect, trashes []string, writes int) {
	updates = map[string]Effect{}
	addUpdate := func(e Effect) {
		prev, ok := updates[e.ID]
		if ok {
			e = merge(prev, e)
		}
		updates[e.ID] = e
	}
	for _, c := range calls {
		switch c.Tool {
		case "add", "update", "trash", "batch", "write":
			writes++
		}
		switch c.Tool {
		case "add":
			var a command.Add
			if !failed(c) && command.FromMap(&a, c.Args) == nil {
				adds = append(adds, n.fromAdd(a))
			}
		case "update":
			var u command.Update
			if !failed(c) && command.FromMap(&u, c.Args) == nil {
				addUpdate(n.fromUpdate(u))
			}
		case "trash":
			var t command.Trash
			if !failed(c) && command.FromMap(&t, c.Args, "empty", "confirm") == nil {
				trashes = append(trashes, n.id(t.ID))
			}
		case "write":
			bad := failedOps(c.Text)
			ops, _ := c.Args["ops"].([]any)
			for i, raw := range ops {
				m, _ := raw.(map[string]any)
				if bad[i+1] || c.Err || m == nil {
					continue
				}
				fields := map[string]any{}
				for k, v := range m {
					if k != "op" {
						fields[k] = v
					}
				}
				switch m["op"] {
				case "add":
					var a command.Add
					if command.FromMap(&a, fields) == nil {
						adds = append(adds, n.fromAdd(a))
					}
				case "update":
					var u command.Update
					if command.FromMap(&u, fields) == nil {
						addUpdate(n.fromUpdate(u))
					}
				}
			}
		case "batch":
			if failed(c) {
				continue
			}
			raw, _ := c.Args["json"].(string)
			if raw == "" {
				b, _ := json.Marshal(c.Args["json"])
				raw = string(b)
			}
			a, u := n.fromThingsJSON(raw)
			adds = append(adds, a...)
			for _, e := range u {
				addUpdate(e)
			}
		}
	}
	return adds, updates, trashes, writes
}

// merge folds a later update into an earlier one for the same item.
func merge(a, b Effect) Effect {
	if b.Title != "" {
		a.Title = b.Title
	}
	if b.Notes != "" {
		if b.NotesMode == "append" && a.Notes != "" {
			a.Notes += "\n" + b.Notes
		} else {
			a.Notes, a.NotesMode = b.Notes, b.NotesMode
		}
	}
	if b.When != "" {
		a.When = b.When
	}
	if b.Deadline != "" {
		a.Deadline = b.Deadline
	}
	if len(b.Tags) > 0 {
		a.Tags, a.TagsMode = b.Tags, b.TagsMode
	}
	if b.In != "" {
		a.In = b.In
	}
	if b.Heading != "" {
		a.Heading = b.Heading
	}
	if len(b.Checklist) > 0 {
		a.Checklist, a.ChecklistMode = b.Checklist, b.ChecklistMode
	}
	if b.Status != "" {
		a.Status = b.Status
	}
	return a
}

// fromThingsJSON converts the subset of Things JSON that agents use.
func (n norm) fromThingsJSON(raw string) (adds, updates []Effect) {
	var items []struct {
		Type       string         `json:"type"`
		Operation  string         `json:"operation"`
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	}
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil, nil
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	titles := func(v any) []string {
		var out []string
		arr, _ := v.([]any)
		for _, x := range arr {
			m, _ := x.(map[string]any)
			at, _ := m["attributes"].(map[string]any)
			if t := str(at, "title"); t != "" {
				out = append(out, strings.ToLower(strings.TrimSpace(t)))
			}
		}
		if s, ok := v.(string); ok {
			out = list(s)
		}
		return out
	}
	tagList := func(v any) []string {
		switch x := v.(type) {
		case string:
			return tags(x)
		case []any:
			var ss []string
			for _, t := range x {
				if s, ok := t.(string); ok {
					ss = append(ss, s)
				}
			}
			return tags(strings.Join(ss, ","))
		}
		return nil
	}
	for _, it := range items {
		at := it.Attributes
		in := str(at, "list-id")
		if in == "" {
			in = str(at, "list")
		}
		if in == "" {
			in = str(at, "area-id")
		}
		if in == "" {
			in = str(at, "area")
		}
		e := Effect{Title: strings.TrimSpace(str(at, "title")), Notes: str(at, "notes"), When: n.when(str(at, "when")),
			Tags: tagList(at["tags"]), In: n.container(in), Heading: str(at, "heading"),
			Checklist: titles(at["checklist-items"]), Items: titles(at["items"])}
		if d, ok := at["deadline"]; ok {
			if s, _ := d.(string); s == "" {
				e.Deadline = "none"
			} else {
				e.Deadline = n.deadline(s)
			}
		}
		if it.Operation == "update" {
			e.Verb, e.ID = "update", n.id(it.ID)
			if at["completed"] == true {
				e.Status = "done"
			}
			if at["canceled"] == true {
				e.Status = "canceled"
			}
			if t := tagList(at["add-tags"]); len(t) > 0 {
				e.Tags, e.TagsMode = t, "add"
			}
			if s := str(at, "append-notes"); s != "" {
				e.Notes, e.NotesMode = s, "append"
			}
			updates = append(updates, e)
			continue
		}
		e.Verb, e.Type = "add", map[string]string{"to-do": "todo", "project": "project"}[it.Type]
		adds = append(adds, e)
	}
	return adds, updates
}
