// Package cli implements the thingsctl command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/render"
	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsapp"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
	"github.com/Frozelo/thingsctl/internal/when"
	"github.com/Frozelo/thingsctl/internal/write"
)

// Version is set at build time via -ldflags "-X .../internal/cli.Version=...".
var Version = "dev"

// Now is the clock; tests override it.
var Now = time.Now

// OpenDB opens the Things database; tests override it.
var OpenDB = thingsdb.OpenDefault

// Runner talks to the Things app; tests override it.
var Runner thingsapp.Runner = thingsapp.System{}

// Stdin feeds `batch -`; tests override it.
var Stdin io.Reader = os.Stdin

// ConfirmBudget is how long writes wait for Things to record a change.
var ConfirmBudget = 1500 * time.Millisecond

const usageText = `thingsctl — token-lean CLI for Things 3 (unofficial)

usage:
  thingsctl read [view] [flags]   read lists, search, or one item (--id)
  thingsctl add <title> [flags]   create a to-do, project, area or tag
  thingsctl update <id> [flags]   change fields, move, complete, cancel, reopen
  thingsctl trash <id>            move to Trash (areas/tags: --confirm; --empty --confirm)
  thingsctl show <id|list>        open in the Things window
  thingsctl batch <json|->        send a Things JSON array (bulk create/update)
  thingsctl mcp [flags]           run as an MCP server on stdio (see: thingsctl mcp -h)
  thingsctl <verb> -h             flags of a verb; thingsctl read help: output format
  thingsctl version

set THINGSCTL_READONLY=1 to disable all writes.

views: summary inbox today upcoming anytime someday deadlines logbook trash projects areas tags
`

// Main runs thingsctl and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	var err error
	switch args[0] {
	case "read":
		err = runRead(args[1:], stdout)
	case "add":
		var a command.Add
		err = runWrite("add", "create a to-do, project, area or tag", &a, args[1:], stdout, func(e *write.Env) error {
			return e.Add(context.Background(), a)
		})
	case "update":
		var u command.Update
		err = runWrite("update", "change an item; only given fields change", &u, args[1:], stdout, func(e *write.Env) error {
			return e.Update(context.Background(), u)
		})
	case "trash":
		var t command.Trash
		err = runWrite("trash", "move to Trash, delete an area/tag, or empty the Trash", &t, args[1:], stdout, func(e *write.Env) error {
			return e.Trash(context.Background(), t)
		})
	case "show":
		var sh command.Show
		err = runWrite("show", "open an item or list in Things", &sh, args[1:], stdout, func(e *write.Env) error {
			return e.Show(context.Background(), sh)
		})
	case "batch":
		var b command.Batch
		err = runWrite("batch", "send a Things JSON array to the json command", &b, args[1:], stdout, func(e *write.Env) error {
			return e.Batch(context.Background(), b, Stdin)
		})
	case "mcp":
		err = runMCP(args[1:], Stdin, stdout)
	case "version", "--version":
		fmt.Fprintln(stdout, "thingsctl", Version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n%s", args[0], usageText)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// runWrite parses a write verb and runs it with a write.Env.
func runWrite(verb, summary string, v any, args []string, w io.Writer, run func(*write.Env) error) error {
	if err := command.Parse(verb, v, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			command.Usage(w, verb, summary, v)
			return nil
		}
		return err
	}
	return withEnv(w, run)
}

// withEnv opens the database and runs a write with a fresh write.Env.
func withEnv(w io.Writer, run func(*write.Env) error) error {
	db, err := OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	db.Now = Now
	return run(&write.Env{DB: db, Runner: Runner, Out: w, Budget: ConfirmBudget,
		ReadOnly: os.Getenv("THINGSCTL_READONLY") == "1"})
}

const readSummary = "read lists, search, or show one item"

const legend = `output format:
  <id> <title> [proj N] @when [HH:MM] eve !deadline #tag [done/total] ~rep done:|canceled:<date>
  "<project or area> <id>:" starts a group; its items are indented one space; loose items come first
  dates: MM-DD in the current year, otherwise YYYY-MM-DD; the header states today=MM-DD
  ids: shortest unique prefix (case-sensitive); full ids work too
  [proj N] project with N open to-dos; ~rep repeating; eve this evening; tags use _ for spaces
`

func runRead(args []string, w io.Writer) error {
	var r command.Read
	if err := command.Parse("read", &r, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			command.Usage(w, "read", readSummary, &r)
			return nil
		}
		return err
	}
	return execRead(r, w)
}

// execRead runs a parsed read.
func execRead(r command.Read, w io.Writer) error {
	if r.View == "help" {
		command.Usage(w, "read", readSummary, &r)
		fmt.Fprint(w, legend)
		return nil
	}
	if r.Limit < 0 || r.Offset < 0 {
		return errors.New("--limit and --offset must be >= 0")
	}

	db, err := OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	db.Now = Now
	snap, err := db.Snap(context.Background())
	if err != nil {
		return err
	}
	defer snap.Close()
	all, err := snap.AllIDs()
	if err != nil {
		return err
	}
	c := render.Ctx{Today: snap.Today(), IDs: shortid.New(all)}

	if r.ID != "" {
		return showOne(w, snap, c, r.ID)
	}
	view := r.View
	filtered := r.Q != "" || r.Tag != "" || r.Project != "" || r.Area != "" || r.Modified != ""
	if view == "" {
		view = "summary"
		if filtered || r.All {
			view = "search"
		}
	}
	switch view {
	case "summary":
		return summary(w, snap, c)
	case "areas":
		return areas(w, snap, c)
	case "tags":
		return tags(w, snap, c)
	}

	base := thingsdb.View(view)
	opts := render.LineOpts{When: true}
	switch view {
	case "search":
		base = thingsdb.ViewOpen
		if r.All {
			base = thingsdb.ViewAll
			opts.Status = true
		}
	case "today", "anytime":
		opts.When = false
	case "logbook", "trash":
		opts.Status = true
	case "projects":
		opts.InProject = true
	}
	tasks, err := snap.Tasks(base)
	if err != nil {
		return err
	}
	match, err := filter(r, c, snap)
	if err != nil {
		return err
	}
	kept := tasks[:0]
	for _, t := range tasks {
		if match(t) {
			kept = append(kept, t)
		}
	}
	tasks = kept

	limit := r.Limit
	if limit == 0 {
		limit = 50
		if view == "logbook" || view == "trash" {
			limit = 20
		}
	}
	from := min(r.Offset, len(tasks))
	to := min(from+limit, len(tasks))
	c.Header(w, view, len(tasks), from, to)
	page := tasks[from:to]
	if r.Detail {
		for i, t := range page {
			full, err := snap.Task(t.UUID)
			if err != nil {
				return err
			}
			if i > 0 {
				fmt.Fprintln(w)
			}
			c.Detail(w, full)
		}
	} else if view == "today" {
		var day, evening []thingsdb.Task
		for _, t := range page {
			if t.Evening {
				evening = append(evening, t)
			} else {
				day = append(day, t)
			}
		}
		c.Grouped(w, day, opts)
		if len(evening) > 0 {
			fmt.Fprintln(w, "evening:")
			var b strings.Builder
			c.Grouped(&b, evening, opts)
			for _, l := range strings.SplitAfter(b.String(), "\n") {
				if l != "" {
					fmt.Fprint(w, " "+l)
				}
			}
		}
	} else if view == "logbook" || view == "trash" || view == "upcoming" || view == "deadlines" || view == "inbox" {
		// Chronological lists: keep order, show the parent inline instead of grouping.
		for _, t := range page {
			line := c.Line(t, opts)
			if p := parentRef(t, c); p != "" {
				line += " in:" + p
			}
			fmt.Fprintln(w, line)
		}
	} else {
		c.Grouped(w, page, opts)
	}
	c.Footer(w, len(tasks), to)
	return nil
}

func parentRef(t thingsdb.Task, c render.Ctx) string {
	switch {
	case t.ProjectID != "" && t.Kind != thingsdb.KindProject:
		return c.IDs.Short(t.ProjectID)
	case t.AreaID != "":
		return c.IDs.Short(t.AreaID)
	}
	return ""
}

// filter builds the predicate for --q, --tag, --project, --area, --modified.
func filter(r command.Read, c render.Ctx, snap *thingsdb.Snap) (func(thingsdb.Task) bool, error) {
	var preds []func(thingsdb.Task) bool
	if q := strings.ToLower(strings.TrimSpace(r.Q)); q != "" {
		preds = append(preds, func(t thingsdb.Task) bool {
			return strings.Contains(strings.ToLower(t.Title), q) || strings.Contains(strings.ToLower(t.Notes), q)
		})
	}
	if r.Tag != "" {
		want := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(r.Tag, "#"), "_", " "))
		preds = append(preds, func(t thingsdb.Task) bool {
			for _, tag := range t.Tags {
				if strings.ToLower(tag) == want {
					return true
				}
			}
			return false
		})
	}
	if r.Project != "" {
		id := resolveQuiet(c, r.Project)
		preds = append(preds, func(t thingsdb.Task) bool {
			return (id != "" && t.ProjectID == id) || strings.EqualFold(t.ProjectTitle, r.Project)
		})
	}
	if r.Area != "" {
		id := resolveQuiet(c, r.Area)
		preds = append(preds, func(t thingsdb.Task) bool {
			return (id != "" && t.AreaID == id) || strings.EqualFold(t.AreaTitle, r.Area)
		})
	}
	if r.Modified != "" {
		d, err := when.Day(r.Modified, c.Today)
		if err != nil {
			return nil, err
		}
		since := d.Time(snap.Location())
		preds = append(preds, func(t thingsdb.Task) bool { return !t.Modified.Before(since) })
	}
	return func(t thingsdb.Task) bool {
		for _, p := range preds {
			if !p(t) {
				return false
			}
		}
		return true
	}, nil
}

func resolveQuiet(c render.Ctx, s string) string {
	id, err := c.IDs.Resolve(s)
	if err != nil {
		return ""
	}
	return id
}

func summary(w io.Writer, snap *thingsdb.Snap, c render.Ctx) error {
	fmt.Fprintf(w, "today=%s", c.Date(c.Today))
	for _, v := range []thingsdb.View{thingsdb.ViewInbox, thingsdb.ViewToday, thingsdb.ViewUpcoming,
		thingsdb.ViewAnytime, thingsdb.ViewSomeday, thingsdb.ViewDeadlines, thingsdb.ViewProjects} {
		ts, err := snap.Tasks(v)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, " %s:%d", v, len(ts))
		if v == thingsdb.ViewDeadlines {
			overdue := 0
			for _, t := range ts {
				if t.Deadline < c.Today {
					overdue++
				}
			}
			if overdue > 0 {
				fmt.Fprintf(w, " overdue:%d", overdue)
			}
		}
	}
	as, err := snap.Areas()
	if err != nil {
		return err
	}
	tg, err := snap.Tags()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, " areas:%d tags:%d\n", len(as), len(tg))
	return nil
}

func areas(w io.Writer, snap *thingsdb.Snap, c render.Ctx) error {
	as, err := snap.Areas()
	if err != nil {
		return err
	}
	c.Header(w, "areas", len(as), 0, len(as))
	for _, a := range as {
		fmt.Fprintf(w, "%s %s projects:%d todos:%d\n", c.IDs.Short(a.UUID), a.Title, a.OpenProjects, a.OpenTodos)
	}
	return nil
}

func tags(w io.Writer, snap *thingsdb.Snap, c render.Ctx) error {
	tg, err := snap.Tags()
	if err != nil {
		return err
	}
	byID := map[string]thingsdb.Tag{}
	for _, t := range tg {
		byID[t.UUID] = t
	}
	c.Header(w, "tags", len(tg), 0, len(tg))
	for _, t := range tg {
		path := render.TagName(t.Title)
		for p, seen := t.ParentID, 0; p != "" && seen < 10; seen++ {
			parent, ok := byID[p]
			if !ok {
				break
			}
			path = render.TagName(parent.Title) + "/" + path
			p = parent.ParentID
		}
		fmt.Fprintf(w, "%s #%s open:%d\n", c.IDs.Short(t.UUID), path, t.OpenTasks)
	}
	return nil
}

func showOne(w io.Writer, snap *thingsdb.Snap, c render.Ctx, ref string) error {
	id, err := c.IDs.Resolve(ref)
	if err != nil {
		return err
	}
	kind, err := snap.KindOf(id)
	if err != nil {
		return err
	}
	switch kind {
	case thingsdb.ObjChecklist:
		owner, err := snap.ChecklistOwner(id)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "checklist item of %s:\n", c.IDs.Short(owner))
		id = owner
		fallthrough
	case thingsdb.ObjTask:
		t, err := snap.Task(id)
		if err != nil {
			return err
		}
		c.Detail(w, t)
		if t.Kind != thingsdb.KindProject {
			return nil
		}
		items, err := snap.ProjectTasks(id)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		fmt.Fprintln(w, "items:")
		heading := ""
		for _, it := range items {
			if it.HeadingID != heading {
				heading = it.HeadingID
				fmt.Fprintf(w, "%s:\n", it.HeadingTitle)
			}
			indent := ""
			if heading != "" {
				indent = " "
			}
			fmt.Fprintln(w, indent+c.Line(it, render.LineOpts{When: true}))
		}
		return nil
	case thingsdb.ObjArea:
		as, err := snap.Areas()
		if err != nil {
			return err
		}
		for _, a := range as {
			if a.UUID == id {
				fmt.Fprintf(w, "%s area %s projects:%d todos:%d\n", c.IDs.Short(a.UUID), a.Title, a.OpenProjects, a.OpenTodos)
			}
		}
		items, err := snap.AreaItems(id)
		if err != nil {
			return err
		}
		for _, it := range items {
			fmt.Fprintln(w, c.Line(it, render.LineOpts{When: true}))
		}
		return nil
	case thingsdb.ObjTag:
		tg, err := snap.Tags()
		if err != nil {
			return err
		}
		for _, t := range tg {
			if t.UUID == id {
				fmt.Fprintf(w, "%s tag #%s open:%d\n", c.IDs.Short(t.UUID), render.TagName(t.Title), t.OpenTasks)
			}
		}
		items, err := snap.TaggedTasks(id)
		if err != nil {
			return err
		}
		c.Grouped(w, items, render.LineOpts{When: true})
		return nil
	}
	return fmt.Errorf("unsupported item kind %s", kind)
}
