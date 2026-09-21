# gemini-cli: ACP `session/load` fails with "Invalid session identifier" for sessions the same process just created

**Version:** gemini-cli 0.60.0 (also seen on 0.57–0.59)
**Mode:** ACP (`gemini --experimental-acp`), Linux, Docker
**Impact:** an ACP client cannot reliably reattach to a session. `session/load` is advertised at `initialize` and then rejects ids the agent itself issued.

## What happens

1. Client calls `session/new`, gets a session id, runs one prompt turn to completion.
2. Client closes the session cleanly and ends the process.
3. A second process calls `session/load` with that id.
4. The agent answers:

```
Internal error: {"details":"Invalid session identifier \"81300e32-618c-4717-9018-66d2bf7c6f14\".
  Searched for sessions in <home>/.gemini/tmp/<project>/chats.
  Use --list-sessions to see available sessions, then use --resume {number}, --resume {uuid}, or --resume latest."}
```

Retried at 0s, 2s, 5s, 10s and 20s. All five return the same error, so this is
not the session file being written late.

## It is intermittent, and that is the worst part

The same probe, same image, same agent version, run twice on one day:

| run | clean close | after a kill mid-turn | after `/compress` |
|-----|-------------|-----------------------|-------------------|
| morning | resumed, 2 updates replayed, 58ms | failed | failed |
| afternoon | failed, all 5 retries | failed | failed |

So a clean close sometimes works and sometimes does not. The kill and
compaction paths have never succeeded in any run.

Eleven other ACP agents were measured on the identical probe and all eleven
resume on every path, so this is not the client.

## The compaction case needs no crash

`--probe compact` fills a session, runs `/compress`, closes the first process
cleanly, and loads from a second. The compaction itself succeeds — gemini
reports compressing 12 messages — and the load then fails with the same
"Invalid session identifier" naming the same `chats` directory. So `/compress`
appears to replace the session rather than rewrite it, and the id the client
still holds stops resolving.

## A session file on disk is not evidence it is loadable

After a failed load there is often a `.jsonl` in that `chats` directory whose
first line carries the very id the load asked for, and `--list-sessions` does
not offer it, while it does list a session the same run closed cleanly. Two
files can carry the same session id. We checked for the file first and it
pointed the wrong way.

## Why it matters

`initialize` advertises `loadSession: true` on every path, including the ones
that then fail, so a client cannot tell in advance whether reattaching will
work. Any ACP client that resumes after a disconnect — a closed laptop, a
crashed editor, a restarted daemon — silently loses the conversation, and a
pending tool approval parked at the time of the disconnect is lost with it.

## Reproduction

```
git clone https://github.com/belt-sh/harness-test && cd harness-test/tests
docker compose run --build test --harness gemini --mode acp \
  --probe resume,inflight,compact
```

Probe source: `runner/testrunner.go` (`probeSessionLoad`, `probeToolCallInFlight`,
`probeCompactedResume`). The client is `github.com/inference-sh/agentprotocol`.
