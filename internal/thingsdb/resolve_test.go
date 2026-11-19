package thingsdb_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Frozelo/thingsctl/internal/shortid"
	f "github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func TestResolveItem(t *testing.T) {
	path := f.Build(t)
	rw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(`INSERT INTO TMTask(uuid,type,status,trashed,start,title) VALUES
		('Xru0000000000000000000',0,0,0,1,'Купить молоко'),
		('Xdone00000000000000000',0,3,0,1,'draft report')`); err != nil {
		t.Fatal(err)
	}
	rw.Close()
	db, _ := thingsdb.Open(path)
	defer db.Close()
	snap, _ := db.Snap(context.Background())
	defer snap.Close()
	all, _ := snap.AllIDs()
	ids := shortid.New(all)

	for ref, want := range map[string]string{
		f.TodoToday:     f.TodoToday,
		"Ttod":          f.TodoToday,
		"DRAFT REPORT":  f.TodoToday, // the open one wins over the done one
		"  Pay bill ":   f.TodoDue,
		"купить МОЛОКО": "Xru0000000000000000000",
		"Finished":      f.TodoDone,
		"errand list":   f.TagErrand,
		"Work":          f.ProjWork,
	} {
		got, err := snap.ResolveItem(ids, ref)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", ref, got, err, want)
		}
	}
	for ref, want := range map[string]string{
		"Daily standup": `2 items are titled "Daily standup"`,
		"Home":          `2 items are titled "Home"`, // area and tag: never guess
		"Trashed":       `no item with id or title "Trashed"`,
		"Nope":          `no item with id or title "Nope"`,
		"T":             "ambiguous",
	} {
		_, err := snap.ResolveItem(ids, ref)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", ref, err, want)
		}
	}
}
