# gemini-cli 0.61: ACP `session/load` destroys a session created in the same UTC minute

Internal record. Not filed upstream.

**Version:** gemini-cli 0.61.0, ACP (`gemini --acp`), Linux, Docker.
**Effect:** loading a session over ACP in the UTC minute it was created replaces
its conversation, and the load fails with "Invalid session identifier". Loaded
any later, the same session resumes on every path, after a clean close and after
a kill.

## Mechanism

From the 0.61.0 bundle (`gemini-*.js` `loadSession`, `ChatRecordingService`):

1. `loadSession` calls `initializeSessionConfig(sessionId, ...)` before
   `SessionSelector.resolveSession`.
2. That config starts a fresh recording for the same id, at
   `chats/session-<new Date() to the minute>-<id8>.jsonl`.
3. `appendRecord` appends to that path: a new header and a `$set.messages`
   snapshot holding only gemini's context message.
4. When the session was created in the same minute, that path is the session's
   own file. Replaying it, `$set.messages` replaces the conversation, so
   `hasResumableContent` is false and `getAllSessionFiles` drops the file.
5. `findSession` finds no session with the id and throws
   `Invalid session identifier "<uuid>". Searched for sessions in <home>/.gemini/tmp/<project>/chats.`

The failed load has already rewritten the file, so a retry later in the same
process or a new one fails too. When the minute differs, gemini writes a second
file for the same id, the original is untouched, and the load succeeds.
`--list-sessions` dedups the two by `lastUpdated`.

## Measured

2026-09-24, harness-test with agentprotocol v0.9.3, three runs of each:

| first process | load within 20s | first load after 65s (`--probe resumeafter=65s`) |
|---------------|-----------------|---------------------------------------------------|
| killed mid-session | 0 of 3 | 3 of 3 |
| closed cleanly | 1 of 3 | 3 of 3 |

Every delayed load replayed the session and carried the earlier turn to the
model. A deterministic check on a single hand-written session file: named for the
current minute, the load failed 3 of 3; named for an earlier minute, it passed
3 of 3 and replayed both messages.

## What this document used to say, and why it was wrong

Earlier versions (gemini 0.57–0.60) reported that gemini "cannot reliably
reattach", that the kill path failed seven times in eight, and that `/compress`
replaced the session. The probes resumed within seconds, so they always loaded
inside the creation minute:

- the kill and close results were this bug, decided by where the minute boundary fell;
- over ACP gemini does not run `/compress` as a command at all: it goes to the
  model as a prompt, so nothing was compacted, and the failed load afterwards was
  again this bug;
- the "session file on disk that `--list-sessions` does not offer" was the
  session's own file after the load had rewritten it.

Also found in the same investigation, and ours: agentprotocol's gemini writer
once wrote an empty `projectHash`, which gemini treats as a legacy record and
deletes at startup. Fixed in agentprotocol v0.6.4. The writer now also names a
session it creates in the current minute for the minute before (v0.7.2), so a
seeded session does not hit the bug above.

## For a client

- Do not `session/load` a gemini session in the UTC minute it was created.
- Do not retry a failed gemini load: the failure has already rewritten the file.
- `initialize` declares `loadSession: true` either way; it says nothing about this.

Reproduce:

```
cd harness-test/tests
docker compose run --build test --harness gemini --mode acp --probe resume=kill
docker compose run --build test --harness gemini --mode acp --probe resume=kill,resumeafter=65s
```
