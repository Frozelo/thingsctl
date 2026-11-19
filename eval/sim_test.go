//go:build eval

package eval

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Frozelo/thingsctl/internal/cli"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func TestSimRoundTrip(t *testing.T) {
	seed := filepath.Join(t.TempDir(), "seed.sqlite")
	if err := BuildSeed(seed, "../testdata/schema.sql", thingsdb.DateOf(time.Now())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THINGS_DB", seed)
	cli.Simulate = (&Sim{Path: seed}).Apply
	defer func() { cli.Simulate = nil }()
	calls := []string{
		`{"name":"add","arguments":{"title":"Order tiles","in":"Kitchen renovation","deadline":"+5d","checklist":"a; b"}}`,
		`{"name":"update","arguments":{"id":"Trec","tags":"waiting","tags_mode":"add"}}`,
		`{"name":"update","arguments":{"id":"pay electricity bill","status":"done"}}`,
		`{"name":"batch","arguments":{"json":"[{\"type\":\"to-do\",\"attributes\":{\"title\":\"Water plants\",\"when\":\"evening\"}}]"}}`,
		`{"name":"trash","arguments":{"id":"Tjug"}}`,
		`{"name":"read","arguments":{"project":"Kitchen renovation"}}`,
		`{"name":"read","arguments":{"view":"today"}}`,
		`{"name":"read","arguments":{"tag":"waiting"}}`,
		`{"name":"read","arguments":{"view":"trash"}}`,
	}
	var in strings.Builder
	in.WriteString(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}` + "\n")
	for i, c := range calls {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": json.RawMessage(c)})
		in.Write(append(b, '\n'))
	}
	cli.Stdin = strings.NewReader(in.String())
	var out, errb strings.Builder
	if code := cli.Main([]string{"mcp", "--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")[1:]
	var texts []string
	for _, l := range lines {
		var m struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		json.Unmarshal([]byte(l), &m)
		texts = append(texts, m.Result.Content[0].Text)
		t.Logf("%s\n", m.Result.Content[0].Text)
	}
	want := []string{"Order tiles", "#home #waiting", "done:", "new rows seen: 1", "Learn to juggle", "Order tiles", "Water plants", "Collect receipts", "Learn to juggle"}
	for i, w := range want {
		if !strings.Contains(texts[i], w) {
			t.Errorf("call %d: %q missing %q", i, texts[i], w)
		}
	}
	if strings.Contains(texts[6], "Pay electricity bill") {
		t.Error("completed item still in Today")
	}
}
