# CI playbook: a red nightly

The nightly fails on any change, so most red nights are drift: an agent
released something new. This is the path from a red run to a green one, in
the order to follow it. Every step works from the failed run's artifacts; no
agent is re-run until the last step.

## 1. Triage

```bash
tests/triage.sh                # latest completed Nightly
tests/triage.sh <run-id>       # a specific run
```

The script downloads the artifacts of each failed job and classes every
failure (`triage.md` for people, `triage.json` for automation). It exits 0
when every failure is `auto`, 1 when any needs review.

| class | means | action |
|---|---|---|
| `surface-added` (auto) | `--help` lists new commands or flags, none gone | accept (step 2) |
| `hidden-new` (auto) | a new `surface/hidden.<name>` that works (`pass:*`) | accept (step 2) |
| `already-accepted` (auto) | `tests/expected` already matches the run: fixed since | nothing |
| `surface-removed` | a command or flag left `--help` | check the registry and belt's hooks for it: `grep -rn -- '<flag>' ~/inference/go/agentprotocol`. Used → a break, fix the driver. Unused → accept by hand |
| `protocol-unused` | a hidden entry point answers ACP/JSON-RPC and the registry does not drive it | the finding that caught cursor's `acp`. Decide whether the registry should use it; record the decision in the README's hidden-command list, then accept |
| `check-new` | a new check id outside `surface/hidden.*` | expected only after a probe change here. Nightly on unchanged code → the agent took a new path; read the message in `probes.log` |
| `check-gone` | a check is no longer reported | the probe stopped reaching that point. Read `<run>.log` in the artifact; usually an earlier step failed |
| `outcome-changed` | a check answered differently | the case the gate exists for. `pass → fail/skip` is a regression in the agent or the runner: reproduce with `--agent-version` (old vs new), then fix the driver or record it as a finding. `fail/finding → pass` is an agent fix: accept and note it |
| `run-incomplete` | a probe run left no report | a timeout or crash. Rerun the job once (step 4); twice in a row is a hang to debug |
| `modes-fail` | a modes job failed | read the log. `VERSION —` / `installed but will not run` is a broken release (wait a day, it auto-updates). `hook did not fire` / `no requests` with a good version is usually an install flake: rerun once |
| `infra` | a job failed without the artifact its class needs | runner, network or build step. Rerun |

One review item on an agent holds back that agent's whole probe file:
`--update-expected` would accept the review item with the drift.

## 2. Accept drift

```bash
tests/triage.sh <run-id> --apply
```

writes the new surface snapshots and probe files for the auto classes, then
prints what it changed. Check it compares clean against the same artifacts:

```bash
go run . --harness <agent> --compare tests/expected/<agent>.json --reports $OUT/art/probes-<agent>
```

Scan the new commands before committing. A surface change is also news: a new
`mcp` subcommand, an ACP flag, a `--desktop` mode may be something belt
should use. Put anything notable in the commit body.

Update the README's compatibility matrix in the same commit when a version
left its row (`major.minor.x`; cursor is a build date). The versions are in
the artifacts:

```bash
for f in $OUT/art/surface-*/*.version; do echo "$(basename $f .version): $(cat $f)"; done
```

Commit message: `nightly <run-id>: <agent> <version> <what's new>, ...`.

## 3. Review items

Work each by its row above. When the fix is in the runner or agentprotocol,
regenerate that agent's probe file in Docker and compare clean on a second run
(README, CI → Expected outcomes). When the change is accepted as the agent's
new behaviour, `--apply` will not write it; regenerate the file the same way.

A check that differs between identical runs goes under `nondeterministic`
with the outcomes seen and the reason, and only after a runner fix was ruled
out.

## 4. Confirm

```bash
git push
gh workflow run nightly.yml -f agents=<agent>,<agent>
gh run watch -R belt-sh/harness-test $(gh run list -R belt-sh/harness-test -w Nightly -L 1 --json databaseId -q '.[0].databaseId')
```

Done when that run is green. The next scheduled run is the second confirmation.

## Automation

`.github/workflows/triage.yml` runs `tests/triage.sh <run> --apply` after
every failed Nightly (or `gh workflow run triage.yml -f run=<id>`):

- every failure auto: commits the new expected files to main as
  `nightly <run>: accept drift (<agents>)` and dispatches a nightly for those
  agents. The README matrix is left to a person; the versions are in the
  triage job's summary.
- any failure needs review: pushes the auto part to `triage/<run>`, writes the
  table to the job summary and fails, naming the review classes. Work them
  from this playbook, then merge or drop the branch.

A new failure mode gets a class here first, then a row in the table, then a
branch in the script.
