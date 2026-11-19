// Package write turns thingsctl write verbs into AppleScript or Things URL
// commands, runs them, and confirms each change by reading the database back.
//
// Routing: AppleScript is preferred (no auth token, no focus change, returns
// ids). The URL scheme is used only for what AppleScript cannot do: evening,
// reminders, headings, checklists, and bulk JSON.
package write

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/render"
	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsapp"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
	"github.com/Frozelo/thingsctl/internal/when"
)

// Env holds what write commands need.
type Env struct {
	DB       *thingsdb.DB
	Runner   thingsapp.Runner
	Out      io.Writer
	Budget   time.Duration // how long to wait for Things to record a change
	ReadOnly bool

	echo Echo // what the receipt repeats besides the item line
}

// Echo marks fields a receipt shows in full because the item line cannot.
type Echo struct {
	Notes, Checklist, NoDeadline bool
}

// EchoFor returns what the receipt of a write command should repeat.
func EchoFor(cmd any) Echo {
	switch c := cmd.(type) {
	case *command.Add:
		return Echo{Checklist: c.Checklist != ""}
	case *command.Update:
		return Echo{Notes: c.Notes != "", Checklist: c.Checklist != "", NoDeadline: c.Deadline == "none"}
	}
	return Echo{}
}

// ErrReadOnly is returned for writes in read-only mode.
var ErrReadOnly = errors.New("read-only mode (THINGSCTL_READONLY=1): writes are disabled")

// plan is a planned write: an AppleScript and/or URLs, plus how to confirm it.
type plan struct {
	script     *thingsapp.Script
	urls       []*thingsapp.URL
	foreground bool
	confirm    func(ctx context.Context, t0 time.Time, scriptOut string) string
	// target is the existing item a script edits; on a script error its
	// current state is reported, since AppleScript may have applied part of it.
	target string
}

func (e *Env) world(ctx context.Context, fn func(*world) (plan, error)) (plan, error) {
	s, err := e.DB.Snap(ctx)
	if err != nil {
		return plan{}, err
	}
	defer s.Close() // never hold a read transaction while Things writes
	w, err := loadWorld(s)
	if err != nil {
		return plan{}, err
	}
	return fn(w)
}

func (e *Env) execute(ctx context.Context, p plan, dry bool) error {
	if dry {
		if p.script != nil {
			fmt.Fprintf(e.Out, "dry-run applescript:\n%s", p.script.Source())
			if args := p.script.Args(); len(args) > 0 {
				b, _ := json.Marshal(args)
				fmt.Fprintf(e.Out, "argv: %s\n", b)
			}
		}
		for _, u := range p.urls {
			fmt.Fprintf(e.Out, "dry-run url: %s\n", u.Redacted())
		}
		return nil
	}
	if e.ReadOnly {
		return ErrReadOnly
	}
	t0 := time.Now()
	var out string
	if p.script != nil {
		if err := e.Runner.EnsureRunning(ctx); err != nil {
			return err
		}
		var err error
		if out, err = e.Runner.AppleScript(ctx, p.script.Source(), p.script.Args()); err != nil {
			if p.target != "" {
				return fmt.Errorf("%w\nThings may have applied part of the change; now: %s", err, e.line(ctx, p.target))
			}
			return err
		}
	}
	for i, u := range p.urls {
		if i > 0 {
			time.Sleep(2 * time.Second) // Things accepts ~250 items per 10s
		}
		if err := e.Runner.OpenURL(ctx, u.String(), !p.foreground); err != nil {
			return err
		}
	}
	if p.confirm != nil {
		fmt.Fprintln(e.Out, p.confirm(ctx, t0, out))
	}
	return nil
}

// wait polls fresh snapshots until cond holds or the budget runs out.
func (e *Env) wait(ctx context.Context, cond func(*thingsdb.Snap) (bool, error)) bool {
	deadline := time.Now().Add(e.Budget)
	for {
		s, err := e.DB.Snap(ctx)
		if err == nil {
			ok, cerr := cond(s)
			s.Close()
			if cerr == nil && ok {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// line renders a task receipt with fresh short ids.
func (e *Env) line(ctx context.Context, id string) string { return Receipt(ctx, e.DB, id, e.echo) }

// Receipt renders the state of a task after a write, with fresh short ids:
// its line including where it now lives, plus the fields echo asks for.
func Receipt(ctx context.Context, db *thingsdb.DB, id string, echo Echo) string {
	s, err := db.Snap(ctx)
	if err != nil {
		return id
	}
	defer s.Close()
	all, _ := s.AllIDs()
	c := render.Ctx{Today: s.Today(), IDs: shortid.New(all)}
	t, err := s.Task(id)
	if err != nil {
		return c.IDs.Short(id)
	}
	l := c.Line(t, render.LineOpts{When: true, Status: true, Where: true, Parent: true})
	if t.Trashed {
		l += " trashed"
	}
	if echo.NoDeadline && t.Deadline.IsZero() {
		l += " no-deadline"
	}
	if echo.Notes {
		l += "\nnotes: " + clip(t.Notes, 300)
	}
	if echo.Checklist {
		items := make([]string, len(t.Checklist))
		for i, it := range t.Checklist {
			items[i] = it.Title
		}
		l += "\nchecklist: " + clip(strings.Join(items, "; "), 300)
	}
	return l
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (e *Env) short(ctx context.Context, id string) string {
	s, err := e.DB.Snap(ctx)
	if err != nil {
		return id
	}
	defer s.Close()
	all, _ := s.AllIDs()
	return shortid.New(all).Short(id)
}

func unconfirmed(ref string) string {
	return fmt.Sprintf("sent %s unconfirmed: Things did not record the change in time; check with: thingsctl read --id %s", ref, ref)
}

// confirmTask waits until the task satisfies check, then prints its line.
func (e *Env) confirmTask(id string, t0 time.Time, check func(thingsdb.Task) bool) func(context.Context, time.Time, string) string {
	return func(ctx context.Context, _ time.Time, _ string) string {
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			t, err := s.Task(id)
			if err != nil {
				return false, nil
			}
			return check(t), nil
		})
		if !ok {
			return unconfirmed(e.short(ctx, id))
		}
		return "ok " + e.line(ctx, id)
	}
}

func modifiedSince(t0 time.Time) func(thingsdb.Task) bool {
	return func(t thingsdb.Task) bool { return !t.Modified.Before(t0.Add(-time.Second)) }
}

// confirmCreated finds the item a URL command created (the URL scheme returns
// no id without a callback app). It matches kind, title and parent, and never
// guesses: zero or several matches are reported as unconfirmed.
func (e *Env) confirmCreated(kind thingsdb.Kind, title, parent string) func(context.Context, time.Time, string) string {
	return func(ctx context.Context, t0 time.Time, _ string) string {
		var matches []string
		e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			ts, err := s.CreatedSince(t0.Add(-time.Second))
			if err != nil {
				return false, err
			}
			matches = matches[:0]
			for _, t := range ts {
				if t.Kind == kind && t.Title == title && (parent == "" || t.ProjectID == parent || t.AreaID == parent) {
					matches = append(matches, t.UUID)
				}
			}
			return len(matches) == 1, nil
		})
		switch len(matches) {
		case 1:
			return "ok " + e.line(ctx, matches[0])
		case 0:
			return "sent unconfirmed: Things did not record a new item in time; check with: thingsctl read -q " + quote(title)
		}
		var c []string
		for _, m := range matches {
			c = append(c, e.short(ctx, m))
		}
		return "sent unconfirmed: several new items match: " + strings.Join(c, ", ")
	}
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t\"'") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// ---- add -------------------------------------------------------------------

// Add creates an item.
func (e *Env) Add(ctx context.Context, a command.Add) error {
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("title is required: thingsctl add \"<title>\" [flags]")
	}
	e.echo = EchoFor(&a)
	p, err := e.world(ctx, func(w *world) (plan, error) {
		switch a.Type {
		case "", "todo":
			return e.addTodo(w, a)
		case "project":
			return e.addProject(w, a)
		case "area":
			return e.addArea(w, a)
		case "tag":
			return e.addTag(w, a)
		}
		return plan{}, fmt.Errorf("unknown type %q", a.Type)
	})
	if err != nil {
		return err
	}
	return e.execute(ctx, p, a.DryRun)
}

type fields struct {
	when     *when.When
	deadline thingsdb.Date
	tags     []string
	hasTags  bool
}

func parseFields(w *world, whenS, deadlineS, tagsS string) (fields, error) {
	var f fields
	if whenS != "" {
		wv, err := when.Parse(whenS, w.today)
		if err != nil {
			return f, err
		}
		f.when = &wv
	}
	if deadlineS != "" && !strings.EqualFold(deadlineS, "none") {
		d, err := when.Day(deadlineS, w.today)
		if err != nil {
			return f, err
		}
		f.deadline = d
	}
	if tagsS != "" {
		tags, err := w.resolveTags(tagsS)
		if err != nil {
			return f, err
		}
		f.tags, f.hasTags = tags, true
	}
	return f, nil
}

// Built-in list ids; list names are localized ("Someday" is "Когда-нибудь"
// in Russian), so scripts must address lists by id.
const (
	listInbox   = `list id "TMInboxListSource"`
	listAnytime = `list id "TMNextListSource"`
	listSomeday = `list id "TMSomedayListSource"`
)

// scheduleLines applies a when value to AppleScript variable t.
func scheduleLines(s *thingsapp.Script, wv *when.When) {
	switch wv.Kind {
	case when.KindAnytime:
		s.Line("move t to " + listAnytime)
	case when.KindSomeday:
		s.Line("move t to " + listSomeday)
	default:
		s.Date("wd", wv.Date.Year(), int(wv.Date.Month()), wv.Date.Day())
		s.Line("schedule t for wd")
	}
}

func deadlineLines(s *thingsapp.Script, d thingsdb.Date, clear bool) {
	switch {
	case clear:
		s.Line("delete due date of t") // "set … to missing value" fails with -1700
	case !d.IsZero():
		s.Date("dl", d.Year(), int(d.Month()), d.Day())
		s.Line("set due date of t to dl")
	}
}

func (e *Env) addTodo(w *world, a command.Add) (plan, error) {
	if a.Items != "" {
		return plan{}, errors.New("--items is for projects; use --checklist for a to-do")
	}
	f, err := parseFields(w, a.When, a.Deadline, a.Tags)
	if err != nil {
		return plan{}, err
	}
	var in container
	if a.In != "" && !strings.EqualFold(a.In, "inbox") { // new to-dos land in the Inbox anyway
		if in, err = w.resolveContainer(a.In, false); err != nil {
			return plan{}, err
		}
	}
	if a.Heading != "" && in.kind != "project" {
		return plan{}, errors.New("--heading needs --in <project>")
	}
	checklist := splitList(a.Checklist)
	if len(checklist) > 100 {
		return plan{}, errors.New("Things accepts at most 100 checklist items")
	}

	if len(checklist) > 0 || a.Heading != "" || (f.when != nil && f.when.NeedsURL()) {
		u := &thingsapp.URL{Command: "add"}
		u.Set("title", a.Title)
		if a.Notes != "" {
			u.Set("notes", a.Notes)
		}
		if f.when != nil {
			u.Set("when", f.when.URLValue())
		}
		if !f.deadline.IsZero() {
			u.Set("deadline", f.deadline.ISO())
		}
		if f.hasTags {
			u.Set("tags", strings.Join(f.tags, ","))
		}
		if in.id != "" {
			u.Set("list-id", in.id)
		}
		if a.Heading != "" {
			u.Set("heading", a.Heading)
		}
		if len(checklist) > 0 {
			u.Set("checklist-items", strings.Join(checklist, "\n"))
		}
		u.Set("reveal", "false")
		return plan{urls: []*thingsapp.URL{u}, confirm: e.confirmCreated(thingsdb.KindTodo, a.Title, in.id)}, nil
	}

	s := &thingsapp.Script{}
	props := "name:" + s.Arg(a.Title)
	if a.Notes != "" {
		props += ", notes:" + s.Arg(a.Notes)
	}
	loc := ""
	switch in.kind {
	case "project":
		loc = " at beginning of project id " + s.Arg(in.id)
	case "area":
		loc = " at beginning of area id " + s.Arg(in.id)
	}
	s.Line("set t to make new to do with properties {%s}%s", props, loc)
	if f.hasTags {
		s.Line("set tag names of t to %s", s.Arg(strings.Join(f.tags, ", ")))
	}
	deadlineLines(s, f.deadline, false)
	if f.when != nil {
		scheduleLines(s, f.when)
	}
	s.Line("return id of t")
	return plan{script: s, confirm: e.confirmReturnedID()}, nil
}

// confirmReturnedID confirms an AppleScript create that returned the new id.
func (e *Env) confirmReturnedID() func(context.Context, time.Time, string) string {
	return func(ctx context.Context, t0 time.Time, out string) string {
		id := strings.TrimSpace(out)
		if id == "" {
			return "sent unconfirmed: Things returned no id"
		}
		return e.confirmTask(id, t0, func(thingsdb.Task) bool { return true })(ctx, t0, out)
	}
}

func (e *Env) addProject(w *world, a command.Add) (plan, error) {
	if a.Checklist != "" || a.Heading != "" {
		return plan{}, errors.New("projects have no checklist or heading; use --items for to-dos inside")
	}
	f, err := parseFields(w, a.When, a.Deadline, a.Tags)
	if err != nil {
		return plan{}, err
	}
	var in container
	if a.In != "" {
		if in, err = w.resolveContainer(a.In, false); err != nil {
			return plan{}, err
		}
		if in.kind != "area" {
			return plan{}, errors.New("a project can only be placed --in an area")
		}
	}
	items := splitList(a.Items)
	if len(items) > 0 || (f.when != nil && f.when.NeedsURL()) {
		u := &thingsapp.URL{Command: "add-project"}
		u.Set("title", a.Title)
		if a.Notes != "" {
			u.Set("notes", a.Notes)
		}
		if f.when != nil {
			u.Set("when", f.when.URLValue())
		}
		if !f.deadline.IsZero() {
			u.Set("deadline", f.deadline.ISO())
		}
		if f.hasTags {
			u.Set("tags", strings.Join(f.tags, ","))
		}
		if in.id != "" {
			u.Set("area-id", in.id)
		}
		if len(items) > 0 {
			u.Set("to-dos", strings.Join(items, "\n"))
		}
		u.Set("reveal", "false")
		return plan{urls: []*thingsapp.URL{u}, confirm: e.confirmCreated(thingsdb.KindProject, a.Title, in.id)}, nil
	}
	s := &thingsapp.Script{}
	props := "name:" + s.Arg(a.Title)
	if a.Notes != "" {
		props += ", notes:" + s.Arg(a.Notes)
	}
	s.Line("set t to make new project with properties {%s}", props)
	if in.id != "" {
		s.Line("set area of t to area id %s", s.Arg(in.id))
	}
	if f.hasTags {
		s.Line("set tag names of t to %s", s.Arg(strings.Join(f.tags, ", ")))
	}
	deadlineLines(s, f.deadline, false)
	if f.when != nil {
		scheduleLines(s, f.when)
	}
	s.Line("return id of t")
	return plan{script: s, confirm: e.confirmReturnedID()}, nil
}

func (e *Env) addArea(w *world, a command.Add) (plan, error) {
	if a.When != "" || a.Deadline != "" || a.Checklist != "" || a.Items != "" || a.Heading != "" || a.In != "" || a.Notes != "" {
		return plan{}, errors.New("an area takes only a title (and --tags)")
	}
	s := &thingsapp.Script{}
	s.Line("set t to make new area with properties {name:%s}", s.Arg(a.Title))
	if a.Tags != "" {
		tags, err := w.resolveTags(a.Tags)
		if err != nil {
			return plan{}, err
		}
		s.Line("set tag names of t to %s", s.Arg(strings.Join(tags, ", ")))
	}
	s.Line("return id of t")
	title := a.Title
	return plan{script: s, confirm: func(ctx context.Context, _ time.Time, out string) string {
		id := strings.TrimSpace(out)
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			k, err := s.KindOf(id)
			return err == nil && k == thingsdb.ObjArea, nil
		})
		if !ok {
			return unconfirmed(id)
		}
		return fmt.Sprintf("ok %s area %s", e.short(ctx, id), title)
	}}, nil
}

func (e *Env) addTag(w *world, a command.Add) (plan, error) {
	if a.When != "" || a.Deadline != "" || a.Checklist != "" || a.Items != "" || a.Heading != "" || a.Notes != "" || a.Tags != "" {
		return plan{}, errors.New("a tag takes only a name and optionally --in <parent tag>")
	}
	if _, exists := w.tag(a.Title); exists {
		return plan{}, fmt.Errorf("tag %q already exists", a.Title)
	}
	s := &thingsapp.Script{}
	s.Line("set t to make new tag with properties {name:%s}", s.Arg(a.Title))
	if a.In != "" {
		parent, err := w.resolveTagRef(a.In)
		if err != nil {
			return plan{}, err
		}
		s.Line("set parent tag of t to tag %s", s.Arg(parent.Title))
	}
	s.Line("return id of t")
	name := a.Title
	return plan{script: s, confirm: func(ctx context.Context, _ time.Time, out string) string {
		id := strings.TrimSpace(out)
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			k, err := s.KindOf(id)
			return err == nil && k == thingsdb.ObjTag, nil
		})
		if !ok {
			return unconfirmed(id)
		}
		return fmt.Sprintf("ok %s #%s", e.short(ctx, id), render.TagName(name))
	}}, nil
}

// ---- update ----------------------------------------------------------------

// Update changes an item.
func (e *Env) Update(ctx context.Context, u command.Update) error {
	if u.ID == "" {
		return errors.New("id is required: thingsctl update <id> [flags]")
	}
	e.echo = EchoFor(&u)
	p, err := e.world(ctx, func(w *world) (plan, error) {
		id, err := w.ids.Resolve(u.ID)
		if err != nil {
			return plan{}, err
		}
		kind, err := w.snap.KindOf(id)
		if err != nil {
			return plan{}, err
		}
		switch kind {
		case thingsdb.ObjTask:
			return e.updateTask(w, id, u)
		case thingsdb.ObjArea:
			return e.updateArea(w, id, u)
		case thingsdb.ObjTag:
			return e.updateTag(w, id, u)
		}
		return plan{}, errors.New("checklist items cannot be edited one by one; replace them with: thingsctl update <task> --checklist ...")
	})
	if err != nil {
		return err
	}
	return e.execute(ctx, p, u.DryRun)
}

func (e *Env) updateTask(w *world, id string, u command.Update) (plan, error) {
	t, err := w.snap.Task(id)
	if err != nil {
		return plan{}, err
	}
	switch {
	case t.Kind == thingsdb.KindHeading:
		return plan{}, errors.New("headings cannot be edited through Things' automation APIs")
	case t.RepeatingTemplate:
		return plan{}, errors.New("this is a repeating template; it is read-only here — edit the repeat rule in Things")
	case t.RepeatingInstance && (u.When != "" || (u.Deadline != "")):
		return plan{}, errors.New("when and deadline of a repeating to-do are locked by Things")
	case t.Kind == thingsdb.KindProject && (u.Checklist != "" || u.Heading != ""):
		return plan{}, errors.New("projects have no checklist or heading")
	}
	f, err := parseFields(w, u.When, u.Deadline, "")
	if err != nil {
		return plan{}, err
	}
	clearDeadline := strings.EqualFold(u.Deadline, "none")

	// Final tag set, computed from current tags so every mode becomes "set".
	var tags []string
	tagsChanged := u.Tags != ""
	if tagsChanged {
		given, err := w.resolveTags(u.Tags)
		if err != nil {
			return plan{}, err
		}
		tags = applyTags(t.Tags, given, u.TagsMode)
	}

	var in container
	if u.In != "" {
		if in, err = w.resolveContainer(u.In, t.Kind == thingsdb.KindTodo); err != nil {
			return plan{}, err
		}
		if t.Kind == thingsdb.KindProject && in.kind != "area" {
			return plan{}, errors.New("a project can only move --in an area")
		}
	}
	checklist := splitList(u.Checklist)
	if len(checklist) > 100 {
		return plan{}, errors.New("Things accepts at most 100 checklist items")
	}
	if u.Title == "" && u.Notes == "" && u.When == "" && u.Deadline == "" && !tagsChanged && u.In == "" &&
		u.Heading == "" && u.Checklist == "" && u.Status == "" {
		return plan{}, errors.New("nothing to change: pass at least one field (see: thingsctl update -h)")
	}

	needURL := u.Heading != "" || len(checklist) > 0 || (f.when != nil && f.when.NeedsURL())
	if needURL {
		if t.RepeatingInstance && u.Status != "" {
			return plan{}, errors.New("change the status of a repeating to-do separately (without heading/checklist/evening/reminder)")
		}
		if in.kind == "inbox" {
			return plan{}, errors.New("moving to the inbox cannot be combined with heading/checklist/evening/reminder")
		}
		if w.token == "" {
			return plan{}, errNeedToken
		}
		cmd := "update"
		if t.Kind == thingsdb.KindProject {
			cmd = "update-project"
		}
		url := &thingsapp.URL{Command: cmd}
		url.Set("id", id)
		url.Set("auth-token", w.token)
		if u.Title != "" {
			url.Set("title", u.Title)
		}
		if u.Notes != "" {
			key := map[string]string{"": "notes", "set": "notes", "append": "append-notes", "prepend": "prepend-notes"}[u.NotesMode]
			url.Set(key, u.Notes)
		}
		if f.when != nil {
			url.Set("when", f.when.URLValue())
		}
		if clearDeadline {
			url.Set("deadline", "")
		} else if !f.deadline.IsZero() {
			url.Set("deadline", f.deadline.ISO())
		}
		if tagsChanged {
			url.Set("tags", strings.Join(tags, ","))
		}
		if in.id != "" {
			if t.Kind == thingsdb.KindProject {
				url.Set("area-id", in.id)
			} else {
				url.Set("list-id", in.id)
			}
		}
		if u.Heading != "" {
			url.Set("heading", u.Heading)
		}
		if len(checklist) > 0 {
			key := map[string]string{"": "checklist-items", "set": "checklist-items", "append": "append-checklist-items", "prepend": "prepend-checklist-items"}[u.ChecklistMode]
			url.Set(key, strings.Join(checklist, "\n"))
		}
		switch u.Status {
		case "done":
			url.Set("completed", "true")
		case "canceled":
			url.Set("canceled", "true")
		case "open":
			url.Set("completed", "false")
		}
		url.Set("reveal", "false")
		return plan{urls: []*thingsapp.URL{url}, confirm: e.confirmTask(id, time.Now(), modifiedSince(time.Now()))}, nil
	}

	s := &thingsapp.Script{}
	ref := "to do"
	if t.Kind == thingsdb.KindProject {
		ref = "project"
	}
	s.Line("set t to %s id %s", ref, s.Arg(id))
	if u.Title != "" {
		s.Line("set name of t to %s", s.Arg(u.Title))
	}
	if u.Notes != "" {
		notes := u.Notes
		cur := strings.TrimRight(t.Notes, "\n")
		switch u.NotesMode {
		case "append":
			if cur != "" {
				notes = cur + "\n" + u.Notes
			}
		case "prepend":
			if cur != "" {
				notes = u.Notes + "\n" + cur
			}
		}
		s.Line("set notes of t to %s", s.Arg(notes))
	}
	if tagsChanged {
		s.Line("set tag names of t to %s", s.Arg(strings.Join(tags, ", ")))
	}
	deadlineLines(s, f.deadline, clearDeadline)
	switch in.kind {
	case "inbox":
		s.Line("move t to " + listInbox)
	case "project":
		s.Line("set project of t to project id %s", s.Arg(in.id))
	case "area":
		s.Line("set area of t to area id %s", s.Arg(in.id))
	}
	if f.when != nil {
		scheduleLines(s, f.when)
	}
	switch u.Status {
	case "done":
		s.Line("set status of t to completed")
	case "canceled":
		s.Line("set status of t to canceled")
	case "open":
		s.Line("set status of t to open")
	}
	s.Line("return id of t")
	want := map[string]thingsdb.Status{"done": thingsdb.StatusDone, "canceled": thingsdb.StatusCanceled, "open": thingsdb.StatusOpen}
	status := u.Status
	return plan{script: s, target: id, confirm: func(ctx context.Context, t0 time.Time, out string) string {
		return e.confirmTask(id, t0, func(t thingsdb.Task) bool {
			if status != "" && t.Status != want[status] {
				return false
			}
			return modifiedSince(t0)(t)
		})(ctx, t0, out)
	}}, nil
}

// applyTags merges the given tags into the current ones per mode.
func applyTags(cur, given []string, mode string) []string {
	switch mode {
	case "add":
		out := append([]string(nil), cur...)
		for _, g := range given {
			if !containsFold(out, g) {
				out = append(out, g)
			}
		}
		return out
	case "remove":
		var out []string
		for _, c := range cur {
			if !containsFold(given, c) {
				out = append(out, c)
			}
		}
		return out
	}
	return given
}

func containsFold(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (e *Env) updateArea(w *world, id string, u command.Update) (plan, error) {
	if u.Title == "" || u.Notes != "" || u.When != "" || u.Deadline != "" || u.Tags != "" || u.In != "" ||
		u.Heading != "" || u.Checklist != "" || u.Status != "" {
		return plan{}, errors.New("an area can only be renamed: thingsctl update <area> --title ...")
	}
	s := &thingsapp.Script{}
	s.Line("set name of area id %s to %s", s.Arg(id), s.Arg(u.Title))
	title := u.Title
	return plan{script: s, confirm: func(ctx context.Context, _ time.Time, _ string) string {
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			as, err := s.Areas()
			for _, a := range as {
				if a.UUID == id && a.Title == title {
					return true, err
				}
			}
			return false, err
		})
		if !ok {
			return unconfirmed(e.short(ctx, id))
		}
		return fmt.Sprintf("ok %s area %s", e.short(ctx, id), title)
	}}, nil
}

func (e *Env) updateTag(w *world, id string, u command.Update) (plan, error) {
	if u.Notes != "" || u.When != "" || u.Deadline != "" || u.Tags != "" || u.Heading != "" || u.Checklist != "" || u.Status != "" {
		return plan{}, errors.New("a tag can only be renamed (--title) or re-parented (--in <tag>)")
	}
	if u.Title == "" && u.In == "" {
		return plan{}, errors.New("nothing to change: pass --title or --in")
	}
	var cur thingsdb.Tag
	for _, t := range w.tags {
		if t.UUID == id {
			cur = t
		}
	}
	s := &thingsapp.Script{}
	s.Line("set t to tag %s", s.Arg(cur.Title))
	if u.In != "" {
		parent, err := w.resolveTagRef(u.In)
		if err != nil {
			return plan{}, err
		}
		if parent.UUID == id {
			return plan{}, errors.New("a tag cannot be its own parent")
		}
		s.Line("set parent tag of t to tag %s", s.Arg(parent.Title))
	}
	if u.Title != "" {
		if other, ok := w.tag(u.Title); ok && other.UUID != id {
			return plan{}, fmt.Errorf("tag %q already exists", u.Title)
		}
		s.Line("set name of t to %s", s.Arg(u.Title))
	}
	name := cur.Title
	if u.Title != "" {
		name = u.Title
	}
	return plan{script: s, confirm: func(ctx context.Context, _ time.Time, _ string) string {
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			ts, err := s.Tags()
			for _, t := range ts {
				if t.UUID == id && t.Title == name {
					return true, err
				}
			}
			return false, err
		})
		if !ok {
			return unconfirmed(e.short(ctx, id))
		}
		return fmt.Sprintf("ok %s #%s", e.short(ctx, id), render.TagName(name))
	}}, nil
}

// ---- trash -----------------------------------------------------------------

// Trash moves to-dos/projects to the Trash, deletes areas/tags, or empties the Trash.
func (e *Env) Trash(ctx context.Context, t command.Trash) error {
	if t.Empty {
		if t.ID != "" {
			return errors.New("use either <id> or --empty")
		}
		if !t.Confirm {
			return errors.New("emptying the Trash is permanent; repeat with --confirm")
		}
		s := &thingsapp.Script{}
		s.Line("empty trash")
		return e.execute(ctx, plan{script: s, confirm: func(ctx context.Context, _ time.Time, _ string) string {
			ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
				n, err := s.TrashCount()
				return n == 0, err
			})
			if !ok {
				return "sent unconfirmed: the Trash is not empty yet"
			}
			return "ok trash emptied"
		}}, t.DryRun)
	}
	if t.ID == "" {
		return errors.New("id is required: thingsctl trash <id> (or --empty --confirm)")
	}
	p, err := e.world(ctx, func(w *world) (plan, error) {
		id, err := w.ids.Resolve(t.ID)
		if err != nil {
			return plan{}, err
		}
		kind, err := w.snap.KindOf(id)
		if err != nil {
			return plan{}, err
		}
		s := &thingsapp.Script{}
		switch kind {
		case thingsdb.ObjTask:
			task, err := w.snap.Task(id)
			if err != nil {
				return plan{}, err
			}
			if task.Kind == thingsdb.KindHeading {
				return plan{}, errors.New("headings cannot be deleted through Things' automation APIs")
			}
			if task.Trashed {
				return plan{}, errors.New("already in the Trash")
			}
			ref := "to do"
			if task.Kind == thingsdb.KindProject {
				ref = "project"
			}
			s.Line("delete %s id %s", ref, s.Arg(id))
			return plan{script: s, confirm: e.confirmTask(id, time.Now(), func(t thingsdb.Task) bool { return t.Trashed })}, nil
		case thingsdb.ObjArea:
			if !t.Confirm {
				return plan{}, errors.New("deleting an area is permanent (its items move to the Trash); repeat with --confirm")
			}
			s.Line("delete area id %s", s.Arg(id))
			return plan{script: s, confirm: e.confirmGone(id, "area")}, nil
		case thingsdb.ObjTag:
			if !t.Confirm {
				return plan{}, errors.New("deleting a tag is permanent; repeat with --confirm")
			}
			name := ""
			for _, tg := range w.tags {
				if tg.UUID == id {
					name = tg.Title
				}
			}
			s.Line("delete tag %s", s.Arg(name))
			return plan{script: s, confirm: e.confirmGone(id, "tag")}, nil
		}
		return plan{}, errors.New("checklist items cannot be deleted one by one; replace them with: thingsctl update <task> --checklist ...")
	})
	if err != nil {
		return err
	}
	return e.execute(ctx, p, t.DryRun)
}

func (e *Env) confirmGone(id, what string) func(context.Context, time.Time, string) string {
	return func(ctx context.Context, _ time.Time, _ string) string {
		ok := e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			_, err := s.KindOf(id)
			return errors.Is(err, thingsdb.ErrNoObject), nil
		})
		if !ok {
			return unconfirmed(id)
		}
		return fmt.Sprintf("ok deleted %s %s", what, id)
	}
}

// ---- show ------------------------------------------------------------------

var showLists = map[string]bool{"inbox": true, "today": true, "upcoming": true, "anytime": true,
	"someday": true, "logbook": true, "trash": true, "deadlines": true, "tomorrow": true}

// Show brings Things to the front showing an item or list.
func (e *Env) Show(ctx context.Context, sh command.Show) error {
	target := strings.TrimSpace(sh.Target)
	if target == "" {
		return errors.New("target is required: thingsctl show <id|inbox|today|...>")
	}
	id := strings.ToLower(target)
	if !showLists[id] {
		s, err := e.DB.Snap(ctx)
		if err != nil {
			return err
		}
		all, err := s.AllIDs()
		s.Close()
		if err != nil {
			return err
		}
		if id, err = shortid.New(all).Resolve(target); err != nil {
			return err
		}
	}
	u := &thingsapp.URL{Command: "show"}
	u.Set("id", id)
	if err := e.Runner.OpenURL(ctx, u.String(), false); err != nil {
		return err
	}
	fmt.Fprintln(e.Out, "ok shown "+target)
	return nil
}

// ---- batch -----------------------------------------------------------------

const batchChunk = 50

// Batch sends a Things JSON array through the json URL command, in chunks.
func (e *Env) Batch(ctx context.Context, b command.Batch, stdin io.Reader) error {
	raw := b.JSON
	if raw == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		raw = string(data)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return fmt.Errorf("batch needs a JSON array of Things objects: %v", err)
	}
	if len(items) == 0 {
		return errors.New("batch: empty array")
	}
	needsToken := false
	for i, it := range items {
		if _, ok := it["type"]; !ok {
			return fmt.Errorf("batch item %d has no \"type\"", i)
		}
		if op, _ := it["operation"].(string); op == "update" {
			needsToken = true
		}
	}
	var token string
	if needsToken {
		s, err := e.DB.Snap(ctx)
		if err != nil {
			return err
		}
		token, err = s.Token()
		s.Close()
		if err != nil {
			return err
		}
		if token == "" {
			return errNeedToken
		}
	}
	var urls []*thingsapp.URL
	for i := 0; i < len(items); i += batchChunk {
		chunk, err := json.Marshal(items[i:min(i+batchChunk, len(items))])
		if err != nil {
			return err
		}
		u := &thingsapp.URL{Command: "json"}
		u.Set("data", string(chunk))
		if needsToken {
			u.Set("auth-token", token)
		}
		u.Set("reveal", "false")
		urls = append(urls, u)
	}
	n := len(items)
	return e.execute(ctx, plan{urls: urls, confirm: func(ctx context.Context, t0 time.Time, _ string) string {
		var created []thingsdb.Task
		e.wait(ctx, func(s *thingsdb.Snap) (bool, error) {
			var err error
			created, err = s.CreatedSince(t0.Add(-time.Second))
			return false, err // wait the full budget: items may trickle in
		})
		var ids []string
		for i, t := range created {
			if i == 10 {
				ids = append(ids, fmt.Sprintf("+%d more", len(created)-10))
				break
			}
			ids = append(ids, e.short(ctx, t.UUID))
		}
		return fmt.Sprintf("sent %d items in %d calls; new rows seen: %d %s", n, len(urls), len(created), strings.Join(ids, " "))
	}}, b.DryRun)
}
