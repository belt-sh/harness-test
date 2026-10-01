#!/bin/sh
# Sort a failed nightly into what can be accepted as drift and what needs a
# person, from the run's own artifacts: nothing is re-run.
#
#   tests/triage.sh [run-id] [--apply]
#
# run-id defaults to the latest completed Nightly. --apply writes the
# expected files for every auto class into tests/expected/; review classes are
# never applied. Writes $OUT/triage.md and $OUT/triage.json (default: a temp
# dir, printed at the end). Exit 0 when every failure is auto, 1 when any
# needs review, 2 on a usage or download error.
#
# Classes (docs/ci-playbook.md says what to do with each):
#   auto    surface-added        --help lists new commands or flags, none gone
#   auto    hidden-new           a new surface/hidden.<name> that works (pass:*)
#   auto    already-accepted     tests/expected already matches the run (fixed since)
#   review  surface-removed      a command or flag left --help
#   review  protocol-unused      a hidden entry point answers a protocol the registry does not drive
#   review  check-new            a new check id outside surface/hidden.*
#   review  check-gone           a check no longer reported
#   review  outcome-changed      a check answered differently
#   review  run-incomplete       a probe run left no report (timeout, crash)
#   review  modes-fail           a modes job failed (flake, broken install, real break)
#   review  infra                a job failed without the artifact its class needs
set -eu
cd "$(dirname "$0")/.."

repo=${REPO:-belt-sh/harness-test}
run=""; apply=""
for a in "$@"; do
  case "$a" in
  --apply) apply=1 ;;
  -*) echo "unknown flag: $a" >&2; exit 2 ;;
  *) run=$a ;;
  esac
done
[ -n "$run" ] || run=$(gh run list -R "$repo" -w Nightly -s completed -L 1 --json databaseId -q '.[0].databaseId')
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT/art"

gh run view "$run" -R "$repo" --json jobs,headSha,createdAt,conclusion > "$OUT/run.json"
failed=$(jq -r '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | "\(.name)\t\(.databaseId)"' "$OUT/run.json")
: > "$OUT/items.jsonl"

# item <class> <agent> <job> <detail>: one line of the triage.
item() {
  jq -nc --arg class "$1" --arg agent "$2" --arg job "$3" --arg detail "$4" \
    '{class: $class, agent: $agent, job: $job, detail: $detail,
      auto: ($class | IN("surface-added", "hidden-new", "already-accepted"))}' >> "$OUT/items.jsonl"
}

fetch() { # fetch <artifact>: 0 when downloaded
  [ -d "$OUT/art/$1" ] || gh run download "$run" -R "$repo" -n "$1" -D "$OUT/art/$1" </dev/null >/dev/null 2>&1
}

printf '%s\n' "$failed" | while IFS="$(printf '\t')" read -r name id; do
  [ -n "$name" ] || continue
  kind=${name%% (*}; agent=$(echo "$name" | sed -n 's/.*(\(.*\))/\1/p')
  case "$kind" in
  surface)
    if ! fetch "surface-$agent" || [ ! -s "$OUT/art/surface-$agent/$agent.surface.txt" ]; then
      item infra "$agent" "$name" "no surface artifact; job $id"; continue
    fi
    d=$OUT/art/surface-$agent
    added=$(diff "tests/expected/$agent.surface.txt" "$d/$agent.surface.txt" | sed -n 's/^> //p' | tr '\n' ' ')
    removed=$(diff "tests/expected/$agent.surface.txt" "$d/$agent.surface.txt" | sed -n 's/^< //p' | tr '\n' ' ')
    ver=$(cat "$d/$agent.version" 2>/dev/null || echo "?")
    [ -z "$removed" ] || item surface-removed "$agent" "$name" "$ver: gone: $removed"
    if [ -n "$added" ]; then
      item surface-added "$agent" "$name" "$ver: new: $added"
      [ -z "$apply" ] || [ -n "$removed" ] || cp "$d/$agent.surface.txt" "tests/expected/$agent.surface.txt"
    fi
    [ -n "$added$removed" ] || item already-accepted "$agent" "$name" "$ver: tests/expected matches this run's surface"
    ;;
  probes)
    if ! fetch "probes-$agent"; then
      item infra "$agent" "$name" "no probes artifact; job $id"; continue
    fi
    new=$OUT/$agent.expected.json
    if ! go run . --harness "$agent" --compare "tests/expected/$agent.json" --reports "$OUT/art/probes-$agent" \
        --update-expected "$new" </dev/null > "$OUT/$agent.compare.txt" 2>&1 || [ ! -s "$new" ]; then
      item run-incomplete "$agent" "$name" "$(grep -m1 -E 'did not finish|no report' "$OUT/$agent.compare.txt" || tail -1 "$OUT/$agent.compare.txt")"
      continue
    fi
    # Every check id whose outcome differs, per run, from the old file to the new.
    jq -r --slurpfile new "$new" '
      [.runs[] | {run: .name, checks: (.checks // {})}] as $old
      | [$new[0].runs[] | {run: .name, checks: (.checks // {})}] as $cur
      | $cur[] as $c | ($old[] | select(.run == $c.run) | .checks) as $o
      | (($o + $c.checks) | keys[]) as $id
      | select($o[$id] != $c.checks[$id])
      | [$c.run, $id, ($o[$id] // ""), ($c.checks[$id] // "")] | join("|")' "tests/expected/$agent.json" > "$OUT/$agent.changes.tsv"
    review=0
    while IFS="|" read -r r cid want got; do
      if [ -z "$want" ]; then
        case "$cid:$got" in
        surface/hidden.*.registry:finding:*) item protocol-unused "$agent" "$name" "$r: $cid = $got"; review=1 ;;
        surface/hidden.*:pass:*) item hidden-new "$agent" "$name" "$r: $cid = $got" ;;
        *) item check-new "$agent" "$name" "$r: $cid = $got"; review=1 ;;
        esac
      elif [ -z "$got" ]; then
        item check-gone "$agent" "$name" "$r: $cid was $want"; review=1
      else
        item outcome-changed "$agent" "$name" "$r: $cid $want -> $got"; review=1
      fi
    done < "$OUT/$agent.changes.tsv"
    [ -s "$OUT/$agent.changes.tsv" ] || item already-accepted "$agent" "$name" "tests/expected matches this run's reports"
    # One review item holds the whole file back: the new expected file would
    # accept it along with the drift.
    [ -z "$apply" ] || [ "$review" = 1 ] || [ ! -s "$OUT/$agent.changes.tsv" ] || cp "$new" "tests/expected/$agent.json"
    ;;
  modes)
    item modes-fail "$agent" "$name" "job $id; logs: gh api repos/$repo/actions/jobs/$id/logs" ;;
  *)
    item infra "$agent" "$name" "job $id" ;;
  esac
done

jq -s --argjson run "$run" --slurpfile meta "$OUT/run.json" \
  '{run: $run, sha: $meta[0].headSha, created: $meta[0].createdAt, items: .}' "$OUT/items.jsonl" > "$OUT/triage.json"
{
  echo "# Nightly $run triage"
  echo
  echo "Run: https://github.com/$repo/actions/runs/$run ($(jq -r .created "$OUT/triage.json"))"
  echo
  echo "| | class | agent | detail |"
  echo "|---|---|---|---|"
  jq -r '.items | sort_by(.auto, .class, .agent)[] | "| \(if .auto then "auto" else "**review**" end) | \(.class) | \(.agent) | \(.detail | gsub("\\|"; "\\\\|")) |"' "$OUT/triage.json"
  if ls "$OUT"/art/surface-*/*.version >/dev/null 2>&1; then
    echo
    echo "Versions of the failed surface jobs (README matrix):"
    for f in "$OUT"/art/surface-*/*.version; do echo "- $(basename "$f" .version): $(cat "$f")"; done
  fi
} > "$OUT/triage.md"
cat "$OUT/triage.md"
echo
[ -z "$apply" ] || { echo "applied:"; git status --short tests/expected; }
echo "triage in $OUT"
[ "$(jq '[.items[] | select(.auto | not)] | length' "$OUT/triage.json")" = 0 ]
