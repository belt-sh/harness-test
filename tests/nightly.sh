#!/bin/sh
# The nightly measurements for one agent, run the way CI runs them.
#
#   tests/nightly.sh probes  <agent> [--update]   session phase + probes vs tests/expected/<agent>.json
#   tests/nightly.sh surface <agent> [--update]   --help commands/flags vs tests/expected/<agent>.surface.txt
#
# --update rewrites the committed file from this run instead of comparing.
# Every run gets a fresh container. The image is $IMAGE (default
# harness-test, built from tests/Dockerfile); package downloads are cached in
# the same named volumes docker compose uses. Reports and logs go to $OUT
# (default: a temp dir, printed at the end).
set -eu
cd "$(dirname "$0")/.."

what=${1:?usage: tests/nightly.sh probes|surface <agent> [--update]}
agent=${2:?usage: tests/nightly.sh probes|surface <agent> [--update]}
update=${3:-}
IMAGE=${IMAGE:-harness-test}
RUN_TIMEOUT=${RUN_TIMEOUT:-900}
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
# The container runs as testuser, not as whoever owns the checkout.
chmod 777 "$OUT"

vols="-v tests_bun-cache:/home/testuser/.cache/bun -v tests_npm-cache:/home/testuser/.cache/npm"
vols="$vols -v tests_uv-cache:/home/testuser/.cache/uv -v tests_pip-cache:/home/testuser/.cache/pip"
expected=$PWD/tests/expected

# kiro has no BYOK endpoint: it only works through the MITM intercept, which
# rewrites /etc/hosts and installs a CA, so it runs as root.
user=""; extra=""
if docker run --rm "$IMAGE" --list --json | jq -e --arg a "$agent" '.[] | select(.name == $a) | .needs_intercept == true' >/dev/null; then
  user="--user root"; extra="--intercept"
fi

# run <name> <timeout> <harness-test args...>: one fresh container, killed at
# the timeout. A run that is killed leaves no report, which the comparison
# reports as a run that did not finish.
run() {
  name=$1; limit=$2; shift 2
  cname="ht-$agent-$name-$$"
  echo "=== $agent/$name: $* ==="
  # shellcheck disable=SC2086
  timeout --foreground "$limit" docker run --rm --init --name "$cname" $user $vols -v "$OUT:/out" "$IMAGE" "$@" $extra \
    > "$OUT/$name.log" 2>&1 || status=$?
  if [ "${status:-0}" = 124 ]; then
    docker rm -f "$cname" >/dev/null 2>&1 || true
    echo "  ✗ $agent/$name did not finish within ${limit}s (log: $OUT/$name.log)"
  fi
  status=0
  grep -E '^\s+[✓✗○●]|^=== ' "$OUT/$name.log" || true
}

case "$what" in
probes)
  docker run --rm -v "$expected:/expected:ro" "$IMAGE" --harness "$agent" --runs "/expected/$agent.json" > "$OUT/runs.txt"
  while read -r name mode probes; do
    run "$name" "$RUN_TIMEOUT" --harness "$agent" --mode "$mode" --probe "$probes" --report "/out/$name.json" < /dev/null
  done < "$OUT/runs.txt"
  if [ "$update" = --update ]; then
    docker run --rm -v "$expected:/expected:ro" -v "$OUT:/out" "$IMAGE" --harness "$agent" \
      --compare "/expected/$agent.json" --reports /out --update-expected "/out/$agent.expected.json" || true
    cp "$OUT/$agent.expected.json" "tests/expected/$agent.json"
    echo "updated tests/expected/$agent.json (reports in $OUT)"
  else
    docker run --rm -v "$expected:/expected:ro" -v "$OUT:/out" "$IMAGE" --harness "$agent" \
      --compare "/expected/$agent.json" --reports /out
    echo "reports in $OUT"
  fi
  ;;
surface)
  run surface 300 --harness "$agent" --surface /out
  [ -s "$OUT/$agent.surface.txt" ] || { echo "no surface written for $agent (log: $OUT/surface.log)"; cat "$OUT/surface.log"; exit 1; }
  echo "version: $(cat "$OUT/$agent.version")"
  if [ "$update" = --update ]; then
    cp "$OUT/$agent.surface.txt" "tests/expected/$agent.surface.txt"
    echo "updated tests/expected/$agent.surface.txt"
  elif [ ! -f "tests/expected/$agent.surface.txt" ]; then
    echo "no snapshot at tests/expected/$agent.surface.txt; run: tests/nightly.sh surface $agent --update"
    exit 1
  elif ! diff -u "tests/expected/$agent.surface.txt" "$OUT/$agent.surface.txt" > "$OUT/surface.diff"; then
    echo "$agent's command surface changed (+ is new in $(cat "$OUT/$agent.version")):"
    cat "$OUT/surface.diff"
    exit 1
  else
    echo "surface unchanged"
  fi
  ;;
*)
  echo "unknown: $what (want probes or surface)" >&2; exit 2 ;;
esac
