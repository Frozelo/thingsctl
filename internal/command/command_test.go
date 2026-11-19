package command

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFromMapBindsAndCoerces(t *testing.T) {
	var u Update
	err := FromMap(&u, map[string]any{
		"id": "a3f9", "notes": "x", "notes_mode": "append", "tags": []any{"a", "b"}, "title": 42.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "a3f9" || u.NotesMode != "append" || u.Title != "42" || u.Tags != `["a","b"]` {
		t.Fatalf("got %+v", u)
	}

	var r Read
	if err := FromMap(&r, map[string]any{"limit": 5.0, "all": "true", "view": nil}); err != nil {
		t.Fatal(err)
	}
	if r.Limit != 5 || !r.All {
		t.Fatalf("got %+v", r)
	}
}

func TestFromMapErrors(t *testing.T) {
	cases := []struct {
		v    any
		args map[string]any
		omit []string
		want string
	}{
		{&Add{}, map[string]any{}, nil, `missing required argument "title"`},
		{&Add{}, map[string]any{"title": "x", "dry_run": true}, nil, `unknown argument "dry_run"`},
		{&Add{}, map[string]any{"title": "x", "type": "folder"}, nil, `invalid type "folder"`},
		{&Read{}, map[string]any{"limit": 1.5}, nil, "limit must be an integer"},
		{&Read{}, map[string]any{"all": 1.0}, nil, "all must be true or false"},
		{&Trash{}, map[string]any{"id": "x", "confirm": true}, []string{"empty", "confirm"}, `unknown argument "confirm"; valid: id`},
	}
	for _, c := range cases {
		err := FromMap(c.v, c.args, c.omit...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%T %v: got %v, want %q", c.v, c.args, err, c.want)
		}
	}
}

func TestSchema(t *testing.T) {
	raw := Schema(&Update{})
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	if s.Type != "object" || len(s.Required) != 1 || s.Required[0] != "id" {
		t.Fatalf("got %s", raw)
	}
	if _, ok := s.Properties["dry_run"]; ok {
		t.Error("dry_run must be hidden from MCP")
	}
	if !strings.HasPrefix(string(raw), `{"type":"object","properties":{"id":`) {
		t.Errorf("positional argument must come first: %s", raw)
	}
	if !strings.Contains(string(raw), `"notes_mode":{"type":"string","enum":["set","append","prepend"]}`) {
		t.Errorf("enum or name mapping wrong: %s", raw)
	}
	if strings.Contains(string(Schema(&Trash{}, "empty", "confirm")), "confirm") {
		t.Error("omitted field still in schema")
	}
}
