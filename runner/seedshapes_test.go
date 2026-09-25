package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/harness"
	"github.com/inference-sh/agentprotocol/transcript"
	"github.com/inference-sh/agentprotocol/transcript/all"
)

// Every shape reports through its checks when the agent sends back exactly
// the Context of the hand-built session (what a perfect agent does), and
// every check passes except where the shape itself says otherwise.
func TestSeedShapesPassOnTheirOwnContext(t *testing.T) {
	for _, sh := range seedShapes {
		r := &TestRunner{section: "acp"}
		built := sh.build(r, t.TempDir(), time.Now())
		s := (&transcript.Session{Agent: seedAgent, Entries: built.entries}).Portable()
		body, _ := json.Marshal(s.Context())
		// A request: the context, and the prompt.
		extra := ""
		if sh.name == "file-uri" {
			// An agent that read the file sends its content.
			extra = built.content
		}
		req := []server.LogEntry{{Body: fmt.Appendf(nil, `{"messages":%s,"prompt":%q,"file":%q}`, body, promptText, extra)}}
		built.report(r, shapeRun{name: sh.name, back: s, req: req})
		if len(r.result.Checks) == 0 {
			t.Errorf("%s: no checks", sh.name)
		}
		for _, c := range r.result.Checks {
			if c.Outcome != OutcomePass {
				t.Errorf("%s: %s = %s (%s)", sh.name, c.ID, c.Key(), c.Message)
			}
		}
	}
}

func TestSeedShapesGate(t *testing.T) {
	r := &TestRunner{section: "acp"}
	built := shapeUserAttachments(r, t.TempDir(), time.Now())
	built.report(r, shapeRun{name: "user-attachments", failed: "url-scheme", failMsg: "URL scheme must be http"})
	var got []string
	for _, c := range r.result.Checks {
		got = append(got, c.ID+"="+c.Key())
	}
	want := "acp/seedkinds.user-attachments.image=finding:request-failed:url-scheme acp/seedkinds.user-attachments.pdf=finding:request-failed:url-scheme"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v", got)
	}
}

func TestCountCallsEachFormOnce(t *testing.T) {
	bodies := []string{
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"read","input":{"p":"x-F"}},{"type":"tool_use","id":"a","name":"read","input":{"p":"x-F"}}]}]}`,
		`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{\"p\":\"x-F\"}"}},{"id":"b","type":"function","function":{"name":"read","arguments":"{\"p\":\"x-F\"}"}}]}]}`,
		`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"read","args":{"p":"x-F"}}},{"functionCall":{"name":"read","args":{"p":"x-F"}}}]}]}`,
		`{"input":[{"type":"function_call","name":"read","arguments":"{\"p\":\"x-F\"}"},{"type":"function_call","name":"read","arguments":"{\"p\":\"x-F\"}"}],"tools":[{"name":"read","input":"x-F"}]}`,
	}
	for _, b := range bodies {
		if n, _ := countCalls([]server.LogEntry{{Body: []byte(b)}}, "x-F"); n != 2 {
			t.Errorf("%d calls in %s", n, b)
		}
	}
	if n, text := countCalls([]server.LogEntry{{Body: []byte(`{"messages":[{"content":"x-F then x-F"}]}`)}}, "x-F"); n != 0 || text != 2 {
		t.Errorf("text: %d calls, %d mentions", n, text)
	}
}

func TestShapePDF(t *testing.T) {
	b := shapePDF("hello")
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) || !bytes.Contains(b, []byte("(hello) Tj")) {
		t.Errorf("pdf: %s", b)
	}
	i := bytes.LastIndex(b, []byte("startxref\n"))
	var off int
	if _, err := fmt.Sscan(string(b[i+len("startxref\n"):]), &off); err != nil || !bytes.HasPrefix(b[off:], []byte("xref\n")) {
		t.Errorf("startxref %d does not point at xref", off)
	}
}

// HARNESS_SEEDKINDS_CODECS=1 go test ./runner -run SeedShapesCodecs -v
//
// The codec half of the seed shapes for every agent, with no agent: each
// shape written and read back, reported as if the agent sent everything its
// codec kept.
func TestSeedShapesCodecs(t *testing.T) {
	if os.Getenv("HARNESS_SEEDKINDS_CODECS") == "" {
		t.Skip("set HARNESS_SEEDKINDS_CODECS=1")
	}
	var names []string
	for n := range harness.All {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, sh := range seedShapes {
			home := t.TempDir()
			cwd := filepath.Join(home, "test-repo")
			os.MkdirAll(cwd, 0o755)
			st, ok, err := all.Open(name, home)
			if !ok || err != nil {
				break
			}
			r := &TestRunner{harness: harness.All[name], section: "codec"}
			built := sh.build(r, cwd, time.Now().UTC())
			s := &transcript.Session{Agent: seedAgent, CWD: cwd, Created: time.Now(), Updated: time.Now(), Entries: built.entries}
			id, err := st.Write(context.Background(), s)
			if err != nil {
				t.Logf("%-9s %-45s write: %v", name, sh.name, err)
				continue
			}
			back, err := st.Read(context.Background(), id)
			if err != nil {
				t.Logf("%-9s %-45s read back: %v", name, sh.name, err)
				continue
			}
			built.report(r, shapeRun{name: sh.name, back: back, codecOnly: true})
			for _, c := range r.result.Checks {
				t.Logf("%-9s %-45s %s", name, c.ID, c.Key())
			}
		}
	}
}
