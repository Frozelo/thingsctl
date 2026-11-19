//go:build eval

// Package eval compares MCP tool surfaces with a real agent (Codex CLI) on a
// seeded database. Writes are validated by a --dry-run server and applied to
// a per-run copy of the seed by the simulator, so nothing reaches Things.
//
//	go test -tags eval ./eval -run TestEval -timeout 2h -v
//
// Env: EVAL_OUT (dir), EVAL_VARIANTS, EVAL_TASKS (regexp), EVAL_REPS (1),
// EVAL_PAR (4), EVAL_MODEL, EVAL_EFFORT (medium).
//
// EVAL_VARIANTS is a comma list of name=ref:surface. ref is a git commit to
// build the server from, or "." for the working tree. Default:
// tools=.:tools,ops=.:ops.
package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type usage struct {
	Input, Cached, Output, Reasoning int
}

type result struct {
	Surface, Task string
	Rep           int
	OK            bool
	Why           string
	Calls         []Call
	Answer        string
	Usage         usage
	Seconds       float64
	RunErr        string
}

func TestEval(t *testing.T) {
	out := env("EVAL_OUT", t.TempDir())
	out, _ = filepath.Abs(out)
	os.MkdirAll(filepath.Join(out, "runs"), 0o755)
	variants, err := buildVariants(out, env("EVAL_VARIANTS", "tools=.:tools,ops=.:ops"))
	if err != nil {
		t.Fatal(err)
	}
	today := thingsdb.DateOf(time.Now())
	seed := filepath.Join(out, "seed.sqlite")
	if err := BuildSeed(seed, "../testdata/schema.sql", today); err != nil {
		t.Fatal(err)
	}
	db, err := thingsdb.Open(seed)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := db.Snap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := snap.AllIDs()
	snap.Close()
	db.Close()
	titles, _ := taskTitles(seed)
	n := norm{ids: shortid.New(ids), today: today, titles: titles, containers: map[string]string{
		"home": AreaHome, "work": AreaWork, "health": AreaHealth,
		"website redesign": ProjWebsite, "q4 budget": ProjBudget, "kitchen renovation": ProjKitchen,
		"taxes 2026": ProjTaxes, "learn spanish": ProjSpanish,
	}}

	tasks := Tasks(today)
	if f := os.Getenv("EVAL_TASKS"); f != "" {
		re := regexp.MustCompile(f)
		var keep []Task
		for _, tk := range tasks {
			if re.MatchString(tk.Name) {
				keep = append(keep, tk)
			}
		}
		tasks = keep
	}
	var surfaces []string
	for _, v := range variants {
		surfaces = append(surfaces, v.name)
	}
	reps, _ := strconv.Atoi(env("EVAL_REPS", "1"))
	par, _ := strconv.Atoi(env("EVAL_PAR", "4"))

	type job struct {
		task    Task
		variant variant
		rep     int
	}
	var jobs []job
	for rep := 1; rep <= reps; rep++ {
		for _, tk := range tasks {
			for _, v := range variants {
				jobs = append(jobs, job{tk, v, rep})
			}
		}
	}
	results := make([]result, len(jobs))
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := runOne(out, j.variant, seed, j.task, j.rep)
			if r.RunErr == "" {
				r.OK, r.Why = j.task.Grade(n, r.Calls, r.Answer)
			} else {
				r.Why = "run: " + r.RunErr
			}
			results[i] = r
			t.Logf("%-6s %-20s rep%d ok=%-5v in=%d cached=%d out=%d calls=%d %.0fs %s",
				r.Surface, r.Task, r.Rep, r.OK, r.Usage.Input, r.Usage.Cached, r.Usage.Output, len(r.Calls), r.Seconds, r.Why)
		}()
	}
	wg.Wait()
	b, _ := json.MarshalIndent(results, "", " ")
	os.WriteFile(filepath.Join(out, "results.json"), b, 0o644)
	report := summarize(results, surfaces)
	os.WriteFile(filepath.Join(out, "results.md"), []byte(report), 0o644)
	t.Log("\n" + report)
}

const preamble = "You manage the user's Things 3 through the `things` MCP tools. Do the task now, without asking for confirmation, then reply briefly.\n\nTask: "

func runOne(out string, v variant, seed string, tk Task, rep int) result {
	r := result{Surface: v.name, Task: tk.Name, Rep: rep}
	dir := filepath.Join(out, "runs", fmt.Sprintf("%s-%s-%d", v.name, tk.Name, rep))
	os.MkdirAll(dir, 0o755)
	db := filepath.Join(dir, "things.sqlite")
	if b, err := os.ReadFile(seed); err != nil || os.WriteFile(db, b, 0o644) != nil {
		r.RunErr = "copy seed"
		return r
	}
	args := []string{"exec", "--json", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check",
		"-s", "read-only", "--disable", "shell_tool", "--disable", "unified_exec", "-C", dir,
		"-c", "approval_policy=\"never\"",
		"-c", "model_reasoning_effort=\"" + env("EVAL_EFFORT", "medium") + "\"",
		"-c", fmt.Sprintf("mcp_servers.things.command=%q", v.bin),
		"-c", fmt.Sprintf(`mcp_servers.things.args=["--surface",%q]`, v.surface),
		"-c", fmt.Sprintf("mcp_servers.things.env={THINGS_DB=%q}", db),
	}
	if m := os.Getenv("EVAL_MODEL"); m != "" {
		args = append(args, "-m", m)
	}
	args = append(args, preamble+tk.Prompt)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	r.Seconds = time.Since(start).Seconds()
	os.WriteFile(filepath.Join(dir, "events.jsonl"), stdout.Bytes(), 0o644)
	os.WriteFile(filepath.Join(dir, "stderr.txt"), stderr.Bytes(), 0o644)
	if err != nil {
		r.RunErr = err.Error()
	}
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type   string         `json:"type"`
				Text   string         `json:"text"`
				Server string         `json:"server"`
				Tool   string         `json:"tool"`
				Args   map[string]any `json:"arguments"`
				Result *struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"result"`
				Error any `json:"error"`
			} `json:"item"`
			Usage struct {
				Input     int `json:"input_tokens"`
				Cached    int `json:"cached_input_tokens"`
				Output    int `json:"output_tokens"`
				Reasoning int `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "item.completed" && ev.Item.Type == "agent_message":
			r.Answer = ev.Item.Text
		case ev.Type == "item.completed" && ev.Item.Type == "mcp_tool_call":
			c := Call{Tool: ev.Item.Tool, Args: ev.Item.Args, Err: ev.Item.Error != nil}
			if ev.Item.Result != nil && len(ev.Item.Result.Content) > 0 {
				c.Text = ev.Item.Result.Content[0].Text
			}
			r.Calls = append(r.Calls, c)
		case ev.Type == "turn.completed":
			r.Usage.Input += ev.Usage.Input
			r.Usage.Cached += ev.Usage.Cached
			r.Usage.Output += ev.Usage.Output
			r.Usage.Reasoning += ev.Usage.Reasoning
		}
	}
	if r.RunErr == "" && r.Usage.Input == 0 {
		r.RunErr = "no usage reported: " + strings.TrimSpace(lastLine(stderr.String()))
	}
	return r
}

type variant struct{ name, ref, surface, bin string }

// buildVariants builds one simserver per distinct git ref.
func buildVariants(out, spec string) ([]variant, error) {
	var vs []variant
	built := map[string]string{}
	for _, part := range strings.Split(spec, ",") {
		name, rest, ok := strings.Cut(part, "=")
		ref, surface, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 {
			return nil, fmt.Errorf("bad variant %q; want name=ref:surface", part)
		}
		bin, ok := built[ref]
		if !ok {
			src := ".."
			if ref != "." {
				src = filepath.Join(out, "src", ref)
				os.RemoveAll(src)
				os.MkdirAll(src, 0o755)
				cmd := fmt.Sprintf("git -C .. archive %s | tar -x -C %q", ref, src)
				if b, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
					return nil, fmt.Errorf("archive %s: %v\n%s", ref, err, b)
				}
			}
			bin = filepath.Join(out, "bin", "simserver-"+strings.ReplaceAll(ref, ".", "worktree"))
			build := exec.Command("go", "build", "-tags", "eval", "-o", bin, "./eval/simserver")
			build.Dir = src
			build.Env = append(os.Environ(), "GOWORK=off")
			if b, err := build.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("build %s: %v\n%s", ref, err, b)
			}
			built[ref] = bin
		}
		vs = append(vs, variant{name, ref, surface, bin})
	}
	return vs, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func summarize(rs []result, surfaces []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| surface | success | mean input | mean uncached | mean output | mean calls | input per success |\n|---|---|---|---|---|---|---|\n")
	for _, s := range surfaces {
		var ok, n, in, unc, outp, calls int
		for _, r := range rs {
			if r.Surface != s || r.RunErr != "" {
				continue
			}
			n++
			if r.OK {
				ok++
			}
			in += r.Usage.Input
			unc += r.Usage.Input - r.Usage.Cached
			outp += r.Usage.Output
			calls += len(r.Calls)
		}
		if n == 0 {
			continue
		}
		per := "-"
		if ok > 0 {
			per = strconv.Itoa(in / ok)
		}
		fmt.Fprintf(&b, "| %s | %d/%d | %d | %d | %d | %.1f | %s |\n", s, ok, n, in/n, unc/n, outp/n, float64(calls)/float64(n), per)
	}
	fmt.Fprintf(&b, "\n| task |")
	for _, s := range surfaces {
		fmt.Fprintf(&b, " %s |", s)
	}
	fmt.Fprintf(&b, "\n|---|")
	for range surfaces {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	byTask := map[string]map[string][]result{}
	var order []string
	for _, r := range rs {
		if byTask[r.Task] == nil {
			byTask[r.Task] = map[string][]result{}
			order = append(order, r.Task)
		}
		byTask[r.Task][r.Surface] = append(byTask[r.Task][r.Surface], r)
	}
	for _, tk := range order {
		fmt.Fprintf(&b, "| %s |", tk)
		for _, s := range surfaces {
			var cells []string
			for _, r := range byTask[tk][s] {
				mark := "✗"
				if r.OK {
					mark = "✓"
				}
				if r.RunErr != "" {
					mark = "err"
				}
				cells = append(cells, fmt.Sprintf("%s %dc %dk", mark, len(r.Calls), r.Usage.Input/1000))
			}
			fmt.Fprintf(&b, " %s |", strings.Join(cells, ", "))
		}
		b.WriteString("\n")
	}
	var fails []string
	for _, r := range rs {
		if !r.OK {
			fails = append(fails, fmt.Sprintf("- %s/%s rep%d: %s", r.Surface, r.Task, r.Rep, r.Why))
		}
	}
	if len(fails) > 0 {
		b.WriteString("\nfailures:\n" + strings.Join(fails, "\n") + "\n")
	}
	return b.String()
}
