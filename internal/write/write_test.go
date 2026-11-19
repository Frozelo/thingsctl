package write

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Frozelo/thingsctl/internal/command"
	f "github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// fakeThings records calls and simulates Things by editing the fixture DB.
type fakeThings struct {
	t       *testing.T
	rw      *sql.DB
	scripts []string
	args    [][]string
	urls    []string
	inert   bool // do not simulate changes (for "unconfirmed" paths)
}

const newID = "Nnew00000000000000000x"

func (k *fakeThings) exec(q string, a ...any) {
	k.t.Helper()
	if _, err := k.rw.Exec(q, a...); err != nil {
		k.t.Fatalf("fake exec %s: %v", q, err)
	}
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func (k *fakeThings) AppleScript(_ context.Context, script string, args []string) (string, error) {
	k.scripts, k.args = append(k.scripts, script), append(k.args, args)
	if k.inert {
		return newID, nil
	}
	switch {
	case strings.Contains(script, "make new to do"):
		k.exec(`INSERT INTO TMTask(uuid,type,status,trashed,start,title,creationDate,userModificationDate) VALUES(?,0,0,0,0,?,?,?)`, newID, args[0], now(), now())
		return newID, nil
	case strings.Contains(script, "make new area"):
		k.exec(`INSERT INTO TMArea(uuid,title,"index") VALUES(?,?,9)`, newID, args[0])
		return newID, nil
	case strings.Contains(script, "delete to do"), strings.Contains(script, "delete project"):
		k.exec(`UPDATE TMTask SET trashed=1, userModificationDate=? WHERE uuid=?`, now(), args[0])
	case strings.Contains(script, "set status of t to completed"):
		k.exec(`UPDATE TMTask SET status=3, stopDate=?, userModificationDate=? WHERE uuid=?`, now(), now(), args[0])
	case strings.HasPrefix(strings.Split(script, "\n")[2], "set t to "):
		k.exec(`UPDATE TMTask SET userModificationDate=? WHERE uuid=?`, now(), args[0])
	}
	return "", nil
}

func (k *fakeThings) OpenURL(_ context.Context, u string, background bool) error {
	k.urls = append(k.urls, u)
	if k.inert {
		return nil
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	switch pu.Host + pu.Path {
	case "/add", "add":
		k.exec(`INSERT INTO TMTask(uuid,type,status,trashed,start,title,project,creationDate,userModificationDate) VALUES(?,0,0,0,1,?,?,?,?)`,
			newID, q.Get("title"), nullable(q.Get("list-id")), now(), now())
	case "/update", "update":
		k.exec(`UPDATE TMTask SET userModificationDate=? WHERE uuid=?`, now(), q.Get("id"))
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (k *fakeThings) EnsureRunning(context.Context) error { return nil }

func setup(t *testing.T) (*Env, *fakeThings, *bytes.Buffer) {
	t.Helper()
	path := f.Build(t)
	rw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rw.Close() })
	db, err := thingsdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Now = f.Now
	fake := &fakeThings{t: t, rw: rw}
	var out bytes.Buffer
	return &Env{DB: db, Runner: fake, Out: &out, Budget: 300 * time.Millisecond}, fake, &out
}

var ctx = context.Background()

func TestAddSimpleUsesAppleScriptAndArgv(t *testing.T) {
	e, fk, out := setup(t)
	title := `He said "hi"; do shell script "rm -rf ~"`
	if err := e.Add(ctx, command.Add{Title: title, In: "Work", Tags: "home", Deadline: "+3d", When: "tomorrow"}); err != nil {
		t.Fatal(err)
	}
	if len(fk.scripts) != 1 || len(fk.urls) != 0 {
		t.Fatalf("want one AppleScript, got %d scripts %d urls", len(fk.scripts), len(fk.urls))
	}
	src := fk.scripts[0]
	if strings.Contains(src, "rm -rf") {
		t.Fatal("user text leaked into AppleScript source; it must travel as argv")
	}
	for _, want := range []string{
		"make new to do with properties {name:(item 1 of argv)} at beginning of project id (item 2 of argv)",
		"set tag names of t to (item 3 of argv)",
		"set due date of t to dl", "set year of wd to 2026", "set day of wd to 20", "schedule t for wd", "return id of t",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("script missing %q:\n%s", want, src)
		}
	}
	if got := fk.args[0]; got[0] != title || got[1] != f.ProjWork || got[2] != "home" {
		t.Errorf("argv = %q", got)
	}
	if !strings.HasPrefix(out.String(), "ok Nnew ") {
		t.Errorf("receipt = %q", out.String())
	}
}

func TestAddWithChecklistUsesURLAndFindsNewID(t *testing.T) {
	e, fk, out := setup(t)
	if err := e.Add(ctx, command.Add{Title: "Pack bag", In: "Pwor", Heading: "Phase 1", Checklist: "socks; charger"}); err != nil {
		t.Fatal(err)
	}
	if len(fk.urls) != 1 || len(fk.scripts) != 0 {
		t.Fatalf("want one URL, got %v", fk.urls)
	}
	u := fk.urls[0]
	for _, want := range []string{"things:///add?title=Pack%20bag", "list-id=" + f.ProjWork, "heading=Phase%201", "checklist-items=socks%0Acharger", "reveal=false"} {
		if !strings.Contains(u, want) {
			t.Errorf("url missing %q: %s", want, u)
		}
	}
	if !strings.HasPrefix(out.String(), "ok Nnew Pack bag") {
		t.Errorf("receipt = %q", out.String())
	}
}

func TestAddValidation(t *testing.T) {
	e, _, _ := setup(t)
	cases := map[string]command.Add{
		"unknown tags":         {Title: "x", Tags: "nope"},
		"--heading needs --in": {Title: "x", Heading: "h"},
		"no open project":      {Title: "x", In: "Nowhere"},
		"title is required":    {Title: " "},
		"--items is for":       {Title: "x", Items: "a;b"},
		"invalid when":         {Title: "x", When: "next week"},
	}
	for want, a := range cases {
		if err := e.Add(ctx, a); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", a, err, want)
		}
	}
}

func TestUpdateStatusDone(t *testing.T) {
	e, fk, out := setup(t)
	if err := e.Update(ctx, command.Update{ID: "Ttod", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fk.scripts[0], "set t to to do id (item 1 of argv)") || !strings.Contains(fk.scripts[0], "set status of t to completed") {
		t.Errorf("script:\n%s", fk.scripts[0])
	}
	if !strings.Contains(out.String(), "ok Ttod Draft report") || !strings.Contains(out.String(), "done:") {
		t.Errorf("receipt = %q", out.String())
	}
}

func TestUpdateComputesNotesAndTags(t *testing.T) {
	e, fk, _ := setup(t)
	if err := e.Update(ctx, command.Update{ID: "Tany", Notes: "eggs", NotesMode: "append", Tags: "urgent", TagsMode: "add"}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(fk.args[0], "|")
	if !strings.Contains(args, "Milk and bread\neggs") || !strings.Contains(args, "home, urgent") {
		t.Errorf("argv = %q", fk.args[0])
	}
	if err := e.Update(ctx, command.Update{ID: "Ttod", Tags: "urgent", TagsMode: "remove"}); err != nil {
		t.Fatal(err)
	}
	if got := fk.args[1]; got[len(got)-1] != "" {
		t.Errorf("remove last tag should set empty tag names, argv = %q", got)
	}
}

func TestUpdateUsesLocaleIndependentListsAndClearsDeadline(t *testing.T) {
	e, fk, _ := setup(t)
	for when, want := range map[string]string{"someday": `move t to list id "TMSomedayListSource"`, "anytime": `move t to list id "TMNextListSource"`} {
		fk.scripts = nil
		if err := e.Update(ctx, command.Update{ID: "Tany", When: when}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fk.scripts[0], want) {
			t.Errorf("when %s: script missing %q", when, want)
		}
	}
	fk.scripts = nil
	if err := e.Update(ctx, command.Update{ID: "Tdue", Deadline: "none", In: "inbox"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete due date of t", `move t to list id "TMInboxListSource"`} {
		if !strings.Contains(fk.scripts[0], want) {
			t.Errorf("script missing %q:\n%s", want, fk.scripts[0])
		}
	}
}

func TestUpdateRepeatingRules(t *testing.T) {
	e, _, _ := setup(t)
	if err := e.Update(ctx, command.Update{ID: "Rtmp", Title: "x"}); err == nil || !strings.Contains(err.Error(), "repeating template") {
		t.Errorf("template: %v", err)
	}
	if err := e.Update(ctx, command.Update{ID: "Rins", When: "tomorrow"}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("instance when: %v", err)
	}
	if err := e.Update(ctx, command.Update{ID: "Rins", Title: "Standup"}); err != nil {
		t.Errorf("instance title must be allowed: %v", err)
	}
}

func TestUpdateURLNeedsTokenAndRedacts(t *testing.T) {
	e, fk, out := setup(t)
	err := e.Update(ctx, command.Update{ID: "Ttod", Checklist: "a;b", ChecklistMode: "append"})
	if !errors.Is(err, errNeedToken) {
		t.Fatalf("want errNeedToken, got %v", err)
	}
	fk.exec(`UPDATE TMSettings SET uriSchemeAuthenticationToken='SECRET123'`)
	if err := e.Update(ctx, command.Update{ID: "Ttod", Checklist: "a;b", ChecklistMode: "append", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "SECRET123") || !strings.Contains(out.String(), "auth-token=%2A%2A%2A") {
		t.Errorf("dry-run must redact the token: %s", out.String())
	}
	if len(fk.urls) != 0 {
		t.Error("dry-run must not call Things")
	}
	out.Reset()
	if err := e.Update(ctx, command.Update{ID: "Ttod", When: "evening"}); err != nil {
		t.Fatal(err)
	}
	if u := fk.urls[0]; !strings.Contains(u, "things:///update?id="+f.TodoToday+"&auth-token=SECRET123&when=evening") {
		t.Errorf("url = %s", u)
	}
	if !strings.HasPrefix(out.String(), "ok Ttod") {
		t.Errorf("receipt = %q", out.String())
	}
}

func TestTrash(t *testing.T) {
	e, fk, out := setup(t)
	if err := e.Trash(ctx, command.Trash{ID: "Tinb"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fk.scripts[0], "delete to do id (item 1 of argv)") || !strings.HasSuffix(strings.TrimSpace(out.String()), "trashed") {
		t.Errorf("script %q receipt %q", fk.scripts[0], out.String())
	}
	for _, c := range []command.Trash{{ID: "Ahom"}, {ID: "Ghom"}, {Empty: true}} {
		if err := e.Trash(ctx, c); err == nil || !strings.Contains(err.Error(), "--confirm") {
			t.Errorf("%+v must need --confirm: %v", c, err)
		}
	}
}

func TestUnconfirmedAndReadOnly(t *testing.T) {
	e, fk, out := setup(t)
	fk.inert = true
	if err := e.Update(ctx, command.Update{ID: "Ttod", Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "sent Ttod unconfirmed") {
		t.Errorf("receipt = %q", out.String())
	}
	e.ReadOnly = true
	if err := e.Add(ctx, command.Add{Title: "x"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("read-only: %v", err)
	}
}

func TestBatch(t *testing.T) {
	e, fk, out := setup(t)
	if err := e.Batch(ctx, command.Batch{JSON: `[{"type":"to-do","operation":"update","id":"x","attributes":{}}]`}, nil); !errors.Is(err, errNeedToken) {
		t.Errorf("update op needs token: %v", err)
	}
	if err := e.Batch(ctx, command.Batch{JSON: `{"type":"to-do"}`}, nil); err == nil {
		t.Error("non-array must fail")
	}
	if err := e.Batch(ctx, command.Batch{JSON: "-", DryRun: true}, strings.NewReader(`[{"type":"to-do","attributes":{"title":"a b"}}]`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry-run url: things:///json?data=%5B%7B") || len(fk.urls) != 0 {
		t.Errorf("dry-run = %q", out.String())
	}
}
