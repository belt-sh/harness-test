package runner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/inference-sh/agentprotocol/harness"
	"github.com/inference-sh/agentprotocol/transcript/all"
)

// HARNESS_SEEDKINDS_CODECS=1 go test ./runner -run SeedKindsCodecs -v
//
// The codec half of the seed kinds probe for every agent, with no agent:
// write the session into an empty home with the agent's codec, read it back
// and print what the checks would say. It separates a codec's answer from
// an agent's in a second. Off by default: it reports codec behaviour, and
// a codec change is not a failure of this suite.
func TestSeedKindsCodecs(t *testing.T) {
	if os.Getenv("HARNESS_SEEDKINDS_CODECS") == "" {
		t.Skip("set HARNESS_SEEDKINDS_CODECS=1")
	}
	var names []string
	for n := range harness.All {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		home := t.TempDir()
		cwd := filepath.Join(home, "test-repo")
		os.MkdirAll(cwd, 0o755)
		st, ok, err := all.Open(name, home)
		if !ok || err != nil {
			continue
		}
		r := &TestRunner{harness: harness.All[name]}
		facts, foreign := newSeedFacts(), newForeignSeed()
		now := time.Now().UTC()
		s := sessionFor(r, facts, foreign, cwd, now)
		id, err := st.Write(context.Background(), s)
		if err != nil {
			t.Logf("%s: write: %v", name, err)
			continue
		}
		back, err := st.Read(context.Background(), id)
		if err != nil {
			t.Logf("%s: read back: %v", name, err)
			continue
		}
		r.reportForeignCodec(foreign, back)
		ctx := back.Context()
		for _, f := range facts {
			if !contextHolds(ctx, f.fact) {
				r.finding("seedkinds."+slug(f.kind)+":not-kept", f.kind)
			}
		}
		for _, c := range foreign.tools {
			if !contextHolds(ctx, c.fact) {
				r.finding("seedkinds.foreign.call:not-kept", c.kind)
			}
		}
		for _, c := range r.result.Checks {
			t.Logf("%-9s %-45s %s", name, c.ID, c.Key())
		}
	}
}
