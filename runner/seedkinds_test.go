package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inference-sh/agentprotocol/transcript"
)

func TestSeedKindsPlantsEachFactOnce(t *testing.T) {
	r := &TestRunner{}
	facts := newSeedFacts()
	entries := r.seedKindsEntries(facts, time.Now())
	all, _ := json.Marshal(entries)
	for _, f := range facts {
		if n := strings.Count(string(all), f.fact); n != 1 {
			t.Errorf("%s: fact appears %d times", f.kind, n)
		}
	}
	for _, f := range facts {
		if !contextHolds(entries, f.fact) {
			t.Errorf("%s: contextHolds cannot see it", f.kind)
		}
	}
	var use transcript.Block
	for _, b := range entries[1].Content {
		if b.Kind == transcript.BlockToolUse {
			use = b
		}
	}
	if use.Name == "" || !strings.Contains(string(use.Input), factFor(facts, kindToolArgs)) {
		t.Errorf("tool call = %+v", use)
	}
}

func TestPlantInStringsReachesNestedArguments(t *testing.T) {
	cases := map[string]string{
		"kiro":   `{"operations":[{"mode":"Line","path":"README.md"}]}`,
		"flat":   `{"file_path":"/repo/a.txt"}`,
		"empty":  `{}`,
		"number": `{"n":1}`,
	}
	for name, args := range cases {
		r := &TestRunner{}
		r.harness.ToolCallName, r.harness.ToolCallArgs = "t", args
		_, out := r.seedToolCall("SEED-X")
		if !strings.Contains(string(out), "SEED-X") {
			t.Errorf("%s: fact not planted: %s", name, out)
		}
	}
}
