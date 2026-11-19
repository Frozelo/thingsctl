// Package testfixture builds a synthetic Things database for tests, using the
// real schema (testdata/schema.sql) and made-up rows. It never touches the
// user's database.
package testfixture

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// Today is the fixed "today" of the fixture.
var Today = thingsdb.NewDate(2026, time.November, 19)

// Now is noon on Today in the local zone.
func Now() time.Time { return Today.Time(time.Local).Add(12 * time.Hour) }

// IDs of fixture rows (22 chars, like real Things ids).
const (
	AreaHome   = "Ahome00000000000000000"
	TagHome    = "Ghome00000000000000000"
	TagUrgent  = "Gurgent000000000000000"
	TagErrand  = "Gerrand000000000000000"
	ProjWork   = "Pwork00000000000000000"
	ProjSome   = "Psome00000000000000000"
	ProjDone   = "Pdone00000000000000000"
	Heading1   = "Hphase1000000000000000"
	TodoInbox  = "Tinbox0000000000000000"
	TodoToday  = "Ttoday0000000000000000"
	TodoEve    = "Teve000000000000000000"
	TodoPast   = "Tpast00000000000000000"
	TodoDue    = "Tdue000000000000000000"
	TodoUp     = "Tup0000000000000000000"
	TodoAny    = "Tany000000000000000000"
	TodoHidden = "Thidden00000000000000a"
	TodoSome   = "Tsome00000000000000000"
	TodoDone   = "Tdone00000000000000000"
	TodoCxl    = "Tcxl000000000000000000"
	TodoTrash  = "Ttrash0000000000000000"
	TodoInDone = "Tindone000000000000000"
	RepTmpl    = "Rtmpl00000000000000000"
	RepInst    = "Rinst00000000000000000"
	Check1     = "Cone000000000000000000"
	Check2     = "Ctwo000000000000000000"
)

func schemaPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "schema.sql")
}

// Build writes the fixture database to a temp dir and returns its path.
func Build(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile(schemaPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("schema: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO Meta(key,value) VALUES('databaseVersion','<?xml version="1.0"?><plist version="1.0"><integer>29</integer></plist>')`)
	exec(`INSERT INTO TMSettings(uuid, uriSchemeAuthenticationToken) VALUES('settings','')`)
	exec(`INSERT INTO TMArea(uuid,title,visible,"index") VALUES(?,?,1,1)`, AreaHome, "Home")
	exec(`INSERT INTO TMTag(uuid,title,parent,"index") VALUES(?,?,NULL,1),(?,?,?,2),(?,?,NULL,3)`,
		TagHome, "home", TagUrgent, "urgent", TagHome, TagErrand, "errand list")

	day := func(n int) int64 { return int64(Today.AddDays(n)) }
	ts := float64(Now().Unix())
	type row map[string]any
	task := func(uuid string, r row) {
		t.Helper()
		cols := []string{"uuid", "type", "status", "trashed", "start", "creationDate", "userModificationDate"}
		vals := []any{uuid, 0, 0, 0, 1, ts - 86400, ts}
		for k, v := range r {
			replaced := false
			for i, c := range cols {
				if c == k {
					vals[i], replaced = v, true
				}
			}
			if !replaced {
				cols = append(cols, k)
				vals = append(vals, v)
			}
		}
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
		}
		exec(fmt.Sprintf(`INSERT INTO TMTask(%s) VALUES(%s)`, strings.Join(quoted, ","),
			strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")), vals...)
	}

	task(ProjWork, row{"type": 1, "title": "Work", "index": 1, "openUntrashedLeafActionsCount": 2})
	task(ProjSome, row{"type": 1, "title": "Someday Proj", "start": 2, "area": AreaHome, "index": 2, "openUntrashedLeafActionsCount": 1})
	task(ProjDone, row{"type": 1, "title": "Done Proj", "status": 3, "stopDate": ts - 3600, "index": 3})
	task(Heading1, row{"type": 2, "title": "Phase 1", "project": ProjWork, "index": 1})

	task(TodoInbox, row{"title": "Inbox item", "start": 0, "index": 1})
	task(TodoToday, row{"title": "Draft report", "startDate": day(0), "project": ProjWork, "todayIndex": 1, "index": 2,
		"checklistItemsCount": 2, "openChecklistItemsCount": 1})
	task(TodoEve, row{"title": "Call mom", "startDate": day(0), "startBucket": 1, "reminderTime": int64(thingsdb.NewClock(18, 0)), "todayIndex": 2, "index": 3})
	task(TodoPast, row{"title": "Scheduled past", "start": 2, "startDate": day(-1), "index": 4})
	task(TodoDue, row{"title": "Pay bill", "deadline": day(-1), "index": 5})
	task(TodoUp, row{"title": "Dentist", "start": 2, "startDate": day(3), "reminderTime": int64(thingsdb.NewClock(9, 30)), "area": AreaHome, "index": 6})
	task(TodoAny, row{"title": "Anytime under heading", "heading": Heading1, "notes": "Milk and bread", "index": 7})
	task(TodoHidden, row{"title": "In someday project", "project": ProjSome, "index": 8})
	task(TodoSome, row{"title": "Someday loose", "start": 2, "area": AreaHome, "index": 9})
	task(TodoDone, row{"title": "Finished", "status": 3, "stopDate": ts - 7200, "index": 10})
	task(TodoCxl, row{"title": "Canceled one", "status": 2, "stopDate": ts - 60, "index": 11})
	task(TodoTrash, row{"title": "Trashed", "trashed": 1, "index": 12})
	task(TodoInDone, row{"title": "In done project", "project": ProjDone, "index": 13})
	task(RepTmpl, row{"title": "Daily standup", "start": 2, "rt1_recurrenceRule": []byte{0}, "rt1_nextInstanceStartDate": day(1), "index": 14})
	task(RepInst, row{"title": "Daily standup", "startDate": day(0), "rt1_repeatingTemplate": RepTmpl, "todayIndex": 3, "index": 15})

	exec(`INSERT INTO TMTaskTag(tasks,tags) VALUES(?,?),(?,?),(?,?)`, TodoToday, TagUrgent, TodoAny, TagHome, TodoSome, TagErrand)
	exec(`INSERT INTO TMChecklistItem(uuid,title,status,"index",task) VALUES(?,?,3,1,?),(?,?,0,2,?)`,
		Check1, "outline", TodoToday, Check2, "numbers", TodoToday)
	return path
}

// Open builds the fixture and opens it the way thingsctl does (read-only).
func Open(t testing.TB) *thingsdb.DB {
	t.Helper()
	db, err := thingsdb.Open(Build(t))
	if err != nil {
		t.Fatal(err)
	}
	db.Now = Now
	t.Cleanup(func() { db.Close() })
	return db
}
