package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// recordRunner records what would be sent to Things and fails every call.
type recordRunner struct{ scripts, urls []string }

func (r *recordRunner) AppleScript(_ context.Context, script string, args []string) (string, error) {
	r.scripts = append(r.scripts, script+"\n"+strings.Join(args, "|"))
	return "", errors.New("things unavailable")
}
func (r *recordRunner) OpenURL(_ context.Context, u string, _ bool) error {
	r.urls = append(r.urls, u)
	return errors.New("things unavailable")
}
func (r *recordRunner) EnsureRunning(context.Context) error { return nil }

type mcpResult struct {
	Text    string
	IsError bool
}

func mcpSession(t *testing.T, flags []string, calls ...string) (tools []string, results []mcpResult, rec *recordRunner) {
	t.Helper()
	path := testfixture.Build(t)
	OpenDB = func() (*thingsdb.DB, error) { return thingsdb.Open(path) }
	Now = testfixture.Now
	rec = &recordRunner{}
	Runner = rec
	lines := []string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	}
	for i, c := range calls {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 2, "method": "tools/call", "params": json.RawMessage(c)})
		lines = append(lines, string(b))
	}
	var out strings.Builder
	if err := runMCP(flags, strings.NewReader(strings.Join(lines, "\n")), &out); err != nil {
		t.Fatal(err)
	}
	resps := strings.Split(strings.TrimSpace(out.String()), "\n")
	var list struct {
		Result struct{ Tools []struct{ Name string } }
	}
	if err := json.Unmarshal([]byte(resps[1]), &list); err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Result.Tools {
		tools = append(tools, tl.Name)
	}
	for _, r := range resps[2:] {
		var m struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			t.Fatal(err)
		}
		results = append(results, mcpResult{m.Result.Content[0].Text, m.Result.IsError})
	}
	return tools, results, rec
}

func TestMCPToolsAndRead(t *testing.T) {
	tools, res, _ := mcpSession(t, nil,
		`{"name":"read","arguments":{"view":"today"}}`,
		`{"name":"read","arguments":{"id":"Ttod"}}`,
		`{"name":"read","arguments":{"q":"milk","limit":1}}`,
		`{"name":"read","arguments":{"view":"nope"}}`,
	)
	if strings.Join(tools, ",") != "read,add,update,trash,batch,show" {
		t.Fatalf("tools: %v", tools)
	}
	wantLines(t, res[0].Text, "today=11-19 today:5\n", "Work Pwor:\n Ttod Draft report #urgent [1/2]\n")
	wantLines(t, res[1].Text, "Draft report")
	wantLines(t, res[2].Text, "search:1")
	if !res[3].IsError || !strings.Contains(res[3].Text, `invalid view "nope"`) {
		t.Errorf("bad view: %+v", res[3])
	}
	if strings.HasSuffix(res[0].Text, "\n") {
		t.Error("trailing newline wastes a token")
	}
}

func TestMCPWritesReachRunner(t *testing.T) {
	_, res, rec := mcpSession(t, nil,
		`{"name":"add","arguments":{"title":"Buy milk","when":"tomorrow"}}`,
		`{"name":"trash","arguments":{"id":"Ttod","confirm":true}}`,
		`{"name":"batch","arguments":{"json":"-"}}`,
		`{"name":"update","arguments":{"title":"x"}}`,
	)
	if !res[0].IsError || !strings.Contains(res[0].Text, "things unavailable") || len(rec.scripts) != 1 ||
		!strings.Contains(rec.scripts[0], "Buy milk") {
		t.Errorf("add: %+v scripts=%v", res[0], rec.scripts)
	}
	if !res[1].IsError || !strings.Contains(res[1].Text, `unknown argument "confirm"`) {
		t.Errorf("confirm must be hidden without --allow-permanent: %+v", res[1])
	}
	if !res[2].IsError || !strings.Contains(res[2].Text, "JSON array itself") {
		t.Errorf("batch stdin: %+v", res[2])
	}
	if !res[3].IsError || !strings.Contains(res[3].Text, `missing required argument "id"`) {
		t.Errorf("update without id: %+v", res[3])
	}
}

func TestMCPReadOnlyAndPermanent(t *testing.T) {
	tools, _, _ := mcpSession(t, []string{"--read-only"})
	if strings.Join(tools, ",") != "read" {
		t.Errorf("read-only tools: %v", tools)
	}
	_, res, _ := mcpSession(t, []string{"--allow-permanent"}, `{"name":"trash","arguments":{"empty":true}}`)
	if strings.Contains(res[0].Text, "unknown argument") {
		t.Errorf("--allow-permanent should expose empty: %+v", res[0])
	}
	t.Setenv("THINGSCTL_READONLY", "1")
	tools, _, _ = mcpSession(t, nil)
	if strings.Join(tools, ",") != "read" {
		t.Errorf("THINGSCTL_READONLY tools: %v", tools)
	}
}
