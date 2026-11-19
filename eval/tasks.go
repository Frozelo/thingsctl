//go:build eval

package eval

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// Match accepts one effect.
type Match func(Effect) bool

// Task is one scripted agent task.
type Task struct {
	Name   string
	Prompt string
	// Adds, Updates (by id) and Trashes are the exact set of writes expected.
	Adds    []Match
	Updates map[string]Match
	Trashes []string
	// Answer checks the final reply (read tasks).
	Answer func(string) bool
}

func eqf(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

func has(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, x := range subs {
		if !strings.Contains(s, strings.ToLower(x)) {
			return false
		}
	}
	return true
}

func sameList(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !eqf(a[i], b[i]) {
			return false
		}
	}
	return true
}

func dateForms(d thingsdb.Date) []string {
	t := d.Time(time.Local)
	return []string{d.ISO(), t.Format("01-02"), t.Format("January 2"), t.Format("Jan 2"), t.Format("2 January"), t.Format("2 Jan"), t.Format("1/2")}
}

func anyOf(s string, forms []string) bool {
	for _, f := range forms {
		if strings.Contains(strings.ToLower(s), strings.ToLower(f)) {
			return true
		}
	}
	return false
}

// addedTag accepts adding tag to an item whose tags are existing.
func addedTag(tag string, existing ...string) Match {
	all := append([]string{tag}, existing...)
	slices.Sort(all)
	return func(e Effect) bool {
		if e.Title != "" || e.When != "" || e.Status != "" || e.In != "" || e.Notes != "" {
			return false
		}
		return (e.TagsMode == "add" && slices.Equal(e.Tags, []string{tag})) ||
			(e.TagsMode != "add" && e.TagsMode != "remove" && slices.Equal(e.Tags, all))
	}
}

// Tasks returns the task set for a given today.
func Tasks(today thingsdb.Date) []Task {
	iso := func(n int) string { return today.AddDays(n).ISO() }
	inbox := func(e Effect) bool { return e.In == "" || e.In == "inbox" }
	todo := func(title string, extra func(Effect) bool) Match {
		return func(e Effect) bool { return e.Type == "todo" && eqf(e.Title, title) && extra(e) }
	}
	set := func(e Effect, fields ...string) bool {
		// only the named fields may be set on an update
		got := map[string]bool{"title": e.Title != "", "notes": e.Notes != "", "when": e.When != "",
			"deadline": e.Deadline != "", "tags": len(e.Tags) > 0, "in": e.In != "", "heading": e.Heading != "",
			"checklist": len(e.Checklist) > 0, "status": e.Status != ""}
		for k, v := range got {
			if v && !slices.Contains(fields, k) {
				return false
			}
		}
		return true
	}
	return []Task{
		// --- reads ---
		{Name: "read_today", Prompt: "What's on my Today list? List the titles.",
			Answer: func(s string) bool {
				return has(s, "Write release notes", "Pay electricity bill", "Review PR #42", "Gym")
			}},
		{Name: "read_inbox_count", Prompt: "How many to-dos are in my Inbox? Answer with the number.",
			Answer: func(s string) bool { return regexp.MustCompile(`\b3\b|three`).MatchString(strings.ToLower(s)) }},
		{Name: "read_tag", Prompt: "Which open to-dos are tagged urgent?",
			Answer: func(s string) bool { return has(s, "Review PR #42", "Update budget spreadsheet") }},
		{Name: "read_notes", Prompt: "What does the note on 'Fix leaking tap' say?",
			Answer: func(s string) bool { return has(s, "1/2") }},
		{Name: "read_deadline", Prompt: "When is the Q4 budget project due?",
			Answer: func(s string) bool { return anyOf(s, dateForms(today.AddDays(10))) }},
		{Name: "read_area", Prompt: "Which projects are in my Work area?",
			Answer: func(s string) bool { return has(s, "Website redesign", "Q4 budget") && !has(s, "Kitchen renovation") }},

		// --- single writes ---
		{Name: "add_inbox", Prompt: "Add a to-do 'Buy coffee beans' to my Inbox.",
			Adds: []Match{todo("Buy coffee beans", func(e Effect) bool { return inbox(e) && e.When == "" })}},
		{Name: "add_when_tag", Prompt: "Add a to-do 'Call the bank' for tomorrow, tagged urgent.",
			Adds: []Match{todo("Call the bank", func(e Effect) bool { return e.When == iso(1) && slices.Contains(e.Tags, "urgent") })}},
		{Name: "add_project_deadline", Prompt: "Add 'Order tiles' to the Kitchen renovation project with a deadline 5 days from today.",
			Adds: []Match{todo("Order tiles", func(e Effect) bool { return e.In == ProjKitchen && e.Deadline == iso(5) })}},
		{Name: "add_checklist", Prompt: "Create a to-do 'Pack for Lisbon' with a checklist: passport, charger, sunscreen.",
			Adds: []Match{todo("Pack for Lisbon", func(e Effect) bool { return sameList(e.Checklist, "passport", "charger", "sunscreen") })}},
		{Name: "complete", Prompt: "Mark 'Pay electricity bill' as done.",
			Updates: map[string]Match{TElectric: func(e Effect) bool { return e.Status == "done" && set(e, "status") }}},
		{Name: "cancel", Prompt: "Cancel 'Visit Iceland'.",
			Updates: map[string]Match{TIceland: func(e Effect) bool { return e.Status == "canceled" && set(e, "status") }}},
		{Name: "someday", Prompt: "Move 'Order new monitor' to Someday.",
			Updates: map[string]Match{TMonitor: func(e Effect) bool { return e.When == "someday" && set(e, "when") }}},
		{Name: "evening", Prompt: "Schedule 'Pick paint colors' for this evening.",
			Updates: map[string]Match{TPaint: func(e Effect) bool { return e.When == "evening" && set(e, "when") }}},
		{Name: "reminder", Prompt: fmt.Sprintf("Move 'Dentist appointment' to %s with a reminder at 10:00.", iso(5)),
			Updates: map[string]Match{TDentist: func(e Effect) bool { return e.When == iso(5)+"@10:00" && set(e, "when") }}},
		{Name: "tag_add", Prompt: "Add the tag 'waiting' to 'Collect receipts', keeping its existing tags.",
			Updates: map[string]Match{TReceipts: func(e Effect) bool {
				return set(e, "tags") && ((e.TagsMode == "add" && slices.Equal(e.Tags, []string{"waiting"})) ||
					(e.TagsMode != "add" && e.TagsMode != "remove" && slices.Equal(e.Tags, []string{"home", "waiting"})))
			}}},
		{Name: "notes_append", Prompt: "Add 'Ask about warranty' to the notes of 'Fix leaking tap' without losing what's there.",
			Updates: map[string]Match{TTap: func(e Effect) bool {
				return set(e, "notes") && has(e.Notes, "Ask about warranty") &&
					(e.NotesMode == "append" || has(e.Notes, "1/2"))
			}}},
		{Name: "move_project", Prompt: "Move 'Read article on Go generics' into the Website redesign project.",
			Updates: map[string]Match{TArticle: func(e Effect) bool { return e.In == ProjWebsite && set(e, "in", "heading") }}},
		{Name: "trash", Prompt: "Delete the to-do 'Learn to juggle'.", Trashes: []string{TJuggle}},
		{Name: "new_project", Prompt: "Create a project 'Garden' in my Home area with to-dos 'Buy seeds' and 'Build raised bed'.",
			Adds: []Match{func(e Effect) bool {
				return e.Type == "project" && eqf(e.Title, "Garden") && e.In == AreaHome && sameList(e.Items, "buy seeds", "build raised bed")
			}}},

		// --- multi-item ---
		{Name: "multi_add", Prompt: "Add these to my Inbox: 'Buy stamps', 'Return library books', 'Water plants'.",
			Adds: []Match{
				todo("Buy stamps", func(e Effect) bool { return inbox(e) && e.When == "" }),
				todo("Return library books", func(e Effect) bool { return inbox(e) && e.When == "" }),
				todo("Water plants", func(e Effect) bool { return inbox(e) && e.When == "" }),
			}},
		{Name: "tag_done", Prompt: "Mark every open to-do tagged errand as done.",
			Updates: map[string]Match{
				TMonitor:   func(e Effect) bool { return e.Status == "done" && set(e, "status") },
				TBatteries: func(e Effect) bool { return e.Status == "done" && set(e, "status") },
			}},
		{Name: "inbox_anytime", Prompt: "Move everything in my Inbox to Anytime.",
			Updates: map[string]Match{
				TPlumber: func(e Effect) bool { return e.When == "anytime" && set(e, "when") },
				TFlights: func(e Effect) bool { return e.When == "anytime" && set(e, "when") },
				TArticle: func(e Effect) bool { return e.When == "anytime" && set(e, "when") },
			}},
		{Name: "project_done", Prompt: "Mark every to-do in the Taxes 2026 project as done.",
			Updates: map[string]Match{
				TReceipts: func(e Effect) bool { return e.Status == "done" && set(e, "status") },
				TForms:    func(e Effect) bool { return e.Status == "done" && set(e, "status") },
			}},
		{Name: "project_tag", Prompt: "Tag every to-do in the Kitchen renovation project with urgent, keeping their existing tags.",
			Updates: map[string]Match{
				TPaint:    addedTag("urgent"),
				TTap:      addedTag("urgent", "home"),
				TCabinets: addedTag("urgent"),
			}},
		{Name: "area_someday", Prompt: "Move all open to-dos in my Health area to Someday.",
			Updates: map[string]Match{
				TGym:     func(e Effect) bool { return e.When == "someday" && set(e, "when") },
				TDentist: func(e Effect) bool { return e.When == "someday" && set(e, "when") },
			}},
		{Name: "today_done", Prompt: "Mark everything on my Today list as done.",
			Updates: map[string]Match{
				TNotes:    func(e Effect) bool { return e.Status == "done" && set(e, "status") },
				TElectric: func(e Effect) bool { return e.Status == "done" && set(e, "status") },
				TReviewPR: func(e Effect) bool { return e.Status == "done" && set(e, "status") },
				TGym:      func(e Effect) bool { return e.Status == "done" && set(e, "status") },
			}},
		{Name: "rename_deadline", Prompt: "Rename the Q4 budget project to 'Q4 budget (final)' and remove its deadline.",
			Updates: map[string]Match{ProjBudget: func(e Effect) bool {
				return eqf(e.Title, "Q4 budget (final)") && e.Deadline == "none" && set(e, "title", "deadline")
			}}},
	}
}

// Grade checks the recorded writes and answer against the task.
func (t Task) Grade(n norm, calls []Call, answer string) (bool, string) {
	adds, updates, trashes, _ := n.Effects(calls)
	if t.Answer != nil && !t.Answer(answer) {
		return false, "answer"
	}
	used := make([]bool, len(adds))
next:
	for i, m := range t.Adds {
		for j, e := range adds {
			if !used[j] && m(e) {
				used[j] = true
				continue next
			}
		}
		return false, fmt.Sprintf("missing add #%d; adds=%v", i+1, adds)
	}
	for j, e := range adds {
		if !used[j] {
			return false, "extra add " + e.String()
		}
	}
	for id, m := range t.Updates {
		e, ok := updates[id]
		if !ok {
			return false, "missing update " + id
		}
		if !m(e) {
			return false, "wrong update " + e.String()
		}
	}
	for id, e := range updates {
		if _, ok := t.Updates[id]; !ok {
			return false, "extra update " + e.String()
		}
	}
	slices.Sort(trashes)
	trashes = slices.Compact(trashes)
	want := slices.Clone(t.Trashes)
	slices.Sort(want)
	if !slices.Equal(trashes, want) {
		return false, fmt.Sprintf("trash %v, want %v", trashes, want)
	}
	return true, ""
}
