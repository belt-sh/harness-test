package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inference-sh/agentprotocol/transcript"
	"github.com/inference-sh/agentprotocol/transcript/all"
	_ "github.com/inference-sh/agentprotocol/transcript/sqlite/registrar"
)

// The two probes here check the transcript codecs against the agents that
// write the formats, which is the only check that notices when a vendor
// changes a private, unversioned format under them.
//
// Round-trip: after a turn, decode the session the agent wrote and look for
// the prompt the runner sent and the answer the mock gave. Seed: write a
// session the agent never had, with a fact planted in it, load it, and see
// whether the fact reaches the model. Seed is the proof that "bring your own
// history" works; round-trip is the proof that reading does.

// probeTranscriptRoundTrip decodes the newest session the agent stored for
// the working directory and checks it holds this turn.
func (r *TestRunner) probeTranscriptRoundTrip(phase string) {
	fmt.Printf("[probe] transcript round-trip (%s)\n", phase)
	st, ok, err := all.Open(r.harness.Name, r.agentHome())
	if !ok {
		r.skip(fmt.Sprintf("transcript: no codec for %s", r.harness.Name))
		return
	}
	if err != nil {
		r.fail(fmt.Sprintf("transcript: open %s store: %v", r.harness.Name, err))
		return
	}
	ctx := context.Background()
	infos, err := st.List(ctx, r.workDir())
	if err != nil {
		r.fail(fmt.Sprintf("transcript: list %s sessions: %v", r.harness.Name, err))
		return
	}
	if len(infos) == 0 {
		// The agent ran a turn in this directory and the codec finds nothing:
		// either the agent does not persist in this mode or the codec looks in
		// the wrong place. Both are worth knowing, neither is untested.
		r.finding(fmt.Sprintf("transcript: %s stored no session the codec can find for %s", r.harness.Name, r.workDir()))
		return
	}
	s, err := st.Read(ctx, infos[0].ID)
	if err != nil {
		r.fail(fmt.Sprintf("transcript: read %s session %s: %v", r.harness.Name, infos[0].ID, err))
		return
	}

	var user, answer bool
	for _, e := range s.Messages() {
		switch e.Role {
		case transcript.RoleUser:
			user = user || strings.Contains(e.Text(), promptText)
		case transcript.RoleAssistant:
			answer = answer || strings.Contains(e.Text(), r.server.Response())
		}
	}
	switch {
	case user && answer:
		r.pass(fmt.Sprintf("transcript: %s session %s decodes to this turn — the prompt and the answer, %d message(s)",
			r.harness.Name, s.ID, len(s.Messages())))
	case !user && !answer:
		r.fail(fmt.Sprintf("transcript: %s session %s decodes to %d message(s) and neither is this turn",
			r.harness.Name, s.ID, len(s.Messages())))
	case !user:
		r.fail(fmt.Sprintf("transcript: %s session %s has the answer but not the prompt that was sent", r.harness.Name, s.ID))
	default:
		r.fail(fmt.Sprintf("transcript: %s session %s has the prompt but not the answer the model gave", r.harness.Name, s.ID))
	}
}

// probeTranscriptSeed writes a session the agent never had, loads it over
// ACP, and checks whether a fact planted in it reaches the model.
//
// A file on disk is not a loadable session, and a loadable session is not one
// whose history reaches the model — gemini has shown both separately. So each
// attempt answers three questions in order and stops at the first "no": did
// the store accept the write, did the agent accept the load, did the fact
// arrive.
//
// It seeds twice. Hand-built is a session made from nothing, the case "bring
// your own history" actually needs. Appended reads the session the ACP phase
// just left and adds the planted turn to it, so it carries whatever vendor
// state a real session has. Hand-built failing where appended works names
// state the agent requires and the codec's writer does not produce.
func (r *TestRunner) probeTranscriptSeed() {
	if len(r.harness.ACPCmd) == 0 {
		return
	}
	fmt.Println("[probe] transcript seed (a session the agent never had)")
	st, ok, err := all.Open(r.harness.Name, r.agentHome())
	if !ok {
		r.skip(fmt.Sprintf("seed: no codec for %s", r.harness.Name))
		return
	}
	if err != nil {
		r.fail(fmt.Sprintf("seed: open %s store: %v", r.harness.Name, err))
		return
	}
	ctx := context.Background()

	fact := "SEED-" + randomHex(4)
	now := time.Now().UTC()
	built := &transcript.Session{
		Agent: r.harness.Name, CWD: r.workDir(), Created: now, Updated: now,
		Entries: seedTurn(fact, now, "seed-1", "seed-2"),
	}
	seeded := r.seedAndLoad(st, "hand-built", built, fact)

	infos, err := st.List(ctx, r.workDir())
	if err != nil || len(infos) == 0 {
		r.skip(fmt.Sprintf("seed (appended): %s has no earlier session to append to", r.harness.Name))
		return
	}
	// The newest session may be the hand-built one just written; append to a
	// session the agent wrote itself. Skip it by the id Write returned as well
	// as by content, since a codec may decode the seed's text into blocks the
	// content check does not see.
	var base *transcript.Session
	for _, in := range infos {
		if in.ID == seeded {
			continue
		}
		if s, err := st.Read(ctx, in.ID); err == nil && !holdsSeed(s) {
			base = s
			break
		}
	}
	if base == nil {
		r.skip(fmt.Sprintf("seed (appended): %s has no session of its own to append to", r.harness.Name))
		return
	}
	fact = "SEED-" + randomHex(4)
	// Empty ids and nil Raw: the writer links appended entries to the
	// session's active leaf.
	base.Entries = append(base.Entries, seedTurn(fact, time.Now().UTC(), "", "")...)
	r.seedAndLoad(st, "appended", base, fact)
}

func seedTurn(fact string, at time.Time, userID, answerID string) []transcript.Entry {
	parent := ""
	if answerID != "" {
		parent = userID
	}
	return []transcript.Entry{
		{ID: userID, Role: transcript.RoleUser, Time: at,
			Content: []transcript.Block{{Kind: transcript.BlockText, Text: "Remember this for later: the project codename is " + fact + "."}}},
		{ID: answerID, ParentID: parent, Role: transcript.RoleAssistant, Time: at.Add(time.Second),
			Content: []transcript.Block{{Kind: transcript.BlockText, Text: "Noted. The project codename is " + fact + "."}}},
	}
}

func holdsSeed(s *transcript.Session) bool {
	for _, e := range s.Messages() {
		if strings.Contains(e.Text(), "SEED-") {
			return true
		}
	}
	return false
}

// seedAndLoad writes s, loads it, and reports whether the fact arrived. It
// returns the id Write gave the session, "" when nothing was written.
func (r *TestRunner) seedAndLoad(st transcript.Store, variant string, s *transcript.Session, fact string) string {
	name := r.harness.Name
	before := sessionIDs(st, r.workDir())
	id, err := st.Write(context.Background(), s)
	if errors.Is(err, transcript.ErrReadOnly) {
		r.skip(fmt.Sprintf("seed (%s): %s's store is read-only, so nothing can be seeded", variant, name))
		return ""
	}
	if err != nil {
		r.fail(fmt.Sprintf("seed (%s): write %s session: %v", variant, name, err))
		return ""
	}
	// A hand-built session is new, so its id must not be one the store
	// already held — goose's writer reused the agent's own session id and
	// overwrote it. An appended session keeps its id by design.
	if s.ID == "" && before[id] {
		r.fail(fmt.Sprintf("seed (%s): %s's writer gave the new session the id %s, which already belonged to a session in the store — the write replaced it", variant, name, id))
	}

	bin, args, dir := r.acpInvocation()
	d := NewACPDriver(bin, args, dir, r.envIn(dir))
	d.ResumeSessionID = id
	if err := d.Start(); err != nil {
		d.Close()
		r.finding(fmt.Sprintf("seed (%s): %s rejected session/load of %s: %v", variant, name, id, err))
		return id
	}
	defer d.Close()

	logFrom := r.server.LogCount()
	answered := r.server.AnswersServed()
	if err := d.SendPrompt(promptText); err != nil {
		r.fail(fmt.Sprintf("seed (%s): prompt after load: %v", variant, err))
		return id
	}
	d.WaitAnswered(r.server.AnswersServed, answered, 60*time.Second)

	// The mock answers with canned text, so asking the model for the fact
	// proves nothing. What the agent sent the model is the answer.
	for _, e := range r.server.Log()[logFrom:] {
		if strings.Contains(string(e.Body), fact) {
			r.pass(fmt.Sprintf("seed (%s): %s loaded %s and the planted fact reached the model", variant, name, id))
			return id
		}
	}
	r.finding(fmt.Sprintf("seed (%s): %s loaded %s, and the planted fact never reached the model", variant, name, id))
	return id
}

func sessionIDs(st transcript.Store, cwd string) map[string]bool {
	out := map[string]bool{}
	infos, _ := st.List(context.Background(), cwd)
	for _, in := range infos {
		out[in.ID] = true
	}
	return out
}

// agentHome is the HOME the agent actually runs with, which is where its
// store is. It is not always r.home: agents with ACPNeedsTempHome run over ACP
// under a fresh temp home, and the probe reading the runner's home found no
// opencode or kilo session and wrote seeded sessions where neither looks.
func (r *TestRunner) agentHome() string {
	for _, kv := range r.agentEnv() {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			return v
		}
	}
	return r.home
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}
