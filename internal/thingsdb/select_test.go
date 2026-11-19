package thingsdb_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Frozelo/thingsctl/internal/shortid"
	f "github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func TestSelect(t *testing.T) {
	db, err := thingsdb.Open(f.Build(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Now = f.Now
	snap, _ := db.Snap(context.Background())
	defer snap.Close()
	all, _ := snap.AllIDs()
	ids := shortid.New(all)
	got := func(where string) []string {
		t.Helper()
		ts, err := snap.Select(ids, where)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		var out []string
		for _, x := range ts {
			out = append(out, x.UUID)
		}
		slices.Sort(out)
		return out
	}
	for where, want := range map[string][]string{
		"inbox":            {f.TodoInbox},
		"INBOX":            {f.TodoInbox},
		"tag:urgent":       {f.TodoToday},
		"tag:#errand_list": {f.TodoSome},
		"project:Work":     {f.TodoToday, f.TodoAny},
		"project:Pwor":     {f.TodoToday, f.TodoAny},
		"area:home":        {f.TodoUp, f.TodoSome, f.TodoHidden}, // project to-dos too
		"q:milk":           {f.TodoAny},
		"upcoming":         {f.TodoUp}, // never the repeating template
	} {
		w := slices.Clone(want)
		slices.Sort(w)
		if g := got(where); !slices.Equal(g, w) {
			t.Errorf("%s: got %v, want %v", where, g, w)
		}
	}
	for where, want := range map[string]string{
		"everything":       "invalid where",
		"tag:":             "needs a value",
		"project:Pay bill": "not a project",
		"area:Work":        "not an area",
		"color:red":        "invalid where",
	} {
		if _, err := snap.Select(ids, where); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", where, err, want)
		}
	}
}
