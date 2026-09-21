# tools

Two scripts that turn probe output into the tables in the repo README. They
exist because both tables were hand-edited once, and both picked up mapping
bugs that way: `WebSearch` did not match `web_search` because the comparison
was case-sensitive, and codex's `spawn_agent` and `request_user_input` were
missing from the capability lists, so two agents were shown lacking
capabilities they have.

## capability-matrix.py

Turns `--probe tools` output into the capability matrix.

```bash
harness-test --harness all --mode headless --probe tools 2>&1 \
  | grep -o '\[tools\] [a-z]*/[a-z]* wanted "[^"]*", declared: .*' \
  | sed 's/\[tools\] \([a-z]*\)\/[a-z]*.*declared: /\1 /' > tools.txt
python3 tools/capability-matrix.py tools.txt
```

Each capability lists the tool names that provide it, most specific first, and
an agent with a shell but no dedicated tool for a file row is marked `↳shell`
rather than `—`: a bare dash meant both "has no tool for this" and "reaches it
another way", and codex read as having nothing when it does all file work
through `exec_command`.

## package-sweep.py

Reports tool-shaped strings in an agent's shipped package that no turn
declared. Run it in the container after a suite run has installed the agents:

```bash
docker run --rm --entrypoint bash \
  -v "$PWD:/work:ro" agents-installed:latest \
  -lc 'python3 /work/tools/package-sweep.py'
```

It resolves each agent's code from three shapes: an npm package under the
shared prefix, a binary the installer dropped in a bin directory, and a pip
package in site-packages. The curl and pip installers write into `$HOME`, and
the suite points `HOME` at a temp directory it deletes, so those agents have to
be installed against a fixed `HOME` before the sweep can see them — set
`SWEEP_HOME` if it is not `/home/testuser`.

The output is candidates, not measurements. It reads strings near other
strings, so parameter names and status values survive the filter, and a name
in a package may be dead code, an alias or a flagged feature. The wire is
still the only place an answer comes from.
