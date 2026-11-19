//go:build eval

package eval

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func testNorm(t *testing.T) (norm, map[string]Task) {
	today := thingsdb.NewDate(2026, 11, 19)
	seed := filepath.Join(t.TempDir(), "seed.sqlite")
	if err := BuildSeed(seed, "../testdata/schema.sql", today); err != nil {
		t.Fatal(err)
	}
	db, _ := thingsdb.Open(seed)
	defer db.Close()
	snap, _ := db.Snap(context.Background())
	ids, _ := snap.AllIDs()
	snap.Close()
	tasks := map[string]Task{}
	for _, tk := range Tasks(today) {
		tasks[tk.Name] = tk
	}
	return norm{ids: shortid.New(ids), today: today, containers: map[string]string{"home": AreaHome, "kitchen renovation": ProjKitchen}}, tasks
}

func TestGrader(t *testing.T) {
	n, tasks := testNorm(t)
	ok := "ok (dry run)"
	cases := []struct {
		task  string
		calls []Call
		want  bool
	}{
		{"add_project_deadline", []Call{{Tool: "add", Args: map[string]any{"title": "Order tiles", "in": "Kitchen renovation", "deadline": "+5d"}, Text: ok}}, true},
		{"add_project_deadline", []Call{{Tool: "add", Args: map[string]any{"title": "Order tiles", "in": "Pkit", "deadline": "2026-11-24"}, Text: ok}}, true},
		{"add_project_deadline", []Call{{Tool: "add", Args: map[string]any{"title": "Order tiles", "deadline": "+5d"}, Text: ok}}, false},
		{"add_project_deadline", []Call{
			{Tool: "add", Args: map[string]any{"title": "Order tiles", "in": "Kitchen", "deadline": "+5d"}, Text: "error: no project"},
			{Tool: "write", Args: map[string]any{"ops": []any{map[string]any{"op": "add", "title": "Order tiles", "in": "Pkit", "deadline": "+5d"}}}, Text: ok}}, true},
		{"multi_add", []Call{{Tool: "write", Args: map[string]any{"ops": []any{
			map[string]any{"op": "add", "title": "Buy stamps"}, map[string]any{"op": "add", "title": "Return library books"},
			map[string]any{"op": "add", "title": "Water plants"}}}, Text: ok + "\n" + ok + "\n" + ok}}, true},
		{"multi_add", []Call{{Tool: "batch", Args: map[string]any{"json": `[{"type":"to-do","attributes":{"title":"Buy stamps"}},{"type":"to-do","attributes":{"title":"Return library books"}},{"type":"to-do","attributes":{"title":"Water plants"}}]`}, Text: ok}}, true},
		{"multi_add", []Call{{Tool: "write", Args: map[string]any{"ops": []any{
			map[string]any{"op": "add", "title": "Buy stamps"}, map[string]any{"op": "add", "title": "Return library books", "tags": "x"},
			map[string]any{"op": "add", "title": "Water plants"}}}, Text: ok + "\n\nop 2: error: unknown tag\n" + ok + "\nerror: 1 of 3 ops failed"}}, false},
		{"rename_deadline", []Call{
			{Tool: "update", Args: map[string]any{"id": "Pbud", "title": "Q4 budget (final)"}, Text: ok},
			{Tool: "update", Args: map[string]any{"id": "Pbud", "deadline": "none"}, Text: ok}}, true},
		{"tag_add", []Call{{Tool: "update", Args: map[string]any{"id": "Trec", "tags": "waiting", "tags_mode": "add"}, Text: ok}}, true},
		{"tag_add", []Call{{Tool: "update", Args: map[string]any{"id": "Trec", "tags": "waiting"}, Text: ok}}, false},
		{"tag_add", []Call{{Tool: "update", Args: map[string]any{"id": "Trec", "tags": "home, waiting"}, Text: ok}}, true},
		{"reminder", []Call{{Tool: "update", Args: map[string]any{"id": "Tden", "when": "2026-11-24@10:00"}, Text: ok}}, true},
		{"tag_done", []Call{{Tool: "update", Args: map[string]any{"id": "Tmon", "status": "done"}, Text: ok}}, false},
		{"trash", []Call{{Tool: "trash", Args: map[string]any{"id": "Tjug"}, Text: ok}}, true},
		{"new_project", []Call{{Tool: "add", Args: map[string]any{"title": "Garden", "type": "project", "in": "Home", "items": "Buy seeds; Build raised bed"}, Text: ok}}, true},
	}
	for i, c := range cases {
		got, why := tasks[c.task].Grade(n, c.calls, "")
		if got != c.want {
			t.Errorf("case %d %s: got %v (%s), want %v", i, c.task, got, why, c.want)
		}
	}
}
