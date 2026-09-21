#!/usr/bin/env python3
"""Turn --probe tools output into the capability matrix in the README.

Input lines are "<agent> <tool> <tool> ...". Each capability lists the names
that provide it, most specific first, and the first match wins.

An agent with a shell but no dedicated tool for a file row is marked reaching
it through the shell rather than with a bare dash: the dash meant both "has no
tool for this" and "reaches it another way", and codex read as having nothing
when it does all file work through exec_command.

Name matching is case-insensitive. It was not, and claude's WebSearch did not
match web_search, so an agent was shown lacking a capability it has.
"""
import sys

CAPS = [
    ("read a file",     ["read", "read_file", "view"]),
    ("write a file",    ["write", "write_file", "create"]),
    ("edit in place",   ["edit", "replace", "search_replace", "apply_patch", "patch", "code"]),
    ("run a shell cmd", ["bash", "shell", "execute", "exec_command", "run_shell_command",
                         "run_terminal_command", "terminal", "execute_code"]),
    ("find files",      ["glob", "list_dir", "list_directory", "ls", "tree", "search_files"]),
    ("search contents", ["grep", "grep_search", "rg"]),
    ("todo list",       ["todowrite", "todo_write", "todo__todo_write", "todolist", "todo_list", "todo"]),
    ("subagent",        ["task", "delegate_task", "delegate", "spawn_subagent", "spawn_agent",
                         "subagent", "invoke_agent", "agentswarm", "agent"]),
    ("skills",          ["load_skill", "activate_skill", "skill_manage", "skills_list", "skill_view", "skill"]),
    ("fetch a url",     ["web_fetch", "webfetch", "fetchurl"]),
    ("web search",      ["google_web_search", "web_search", "websearch"]),
    ("plan mode",       ["enter_plan_mode", "enterplanmode", "exitspecmode"]),
    ("ask the user",    ["ask_user_question", "askuserquestion", "request_user_input", "question"]),
    ("deferred tools",  ["toolsearch", "tool_search", "search_tool", "use_tool", "tool_call"]),
]

# Rows an agent with a shell reaches by running a command.
SHELL_REACHABLE = {"read a file", "write a file", "edit in place", "find files", "search contents"}
SHELL = dict(CAPS)["run a shell cmd"]


def main(path):
    rows = {}
    for line in open(path):
        parts = line.split()
        if parts:
            rows[parts[0]] = {t.lower(): t for t in parts[1:]}
    order = sorted(rows)
    has_shell = {a: any(n in rows[a] for n in SHELL) for a in order}

    print("| capability | " + " | ".join(order) + " |")
    print("|---|" + "---|" * len(order))
    for cap, names in CAPS:
        cells = []
        for a in order:
            hit = next((rows[a][n] for n in names if n in rows[a]), None)
            if hit is None and cap in SHELL_REACHABLE and has_shell[a]:
                hit = "↳shell"
            cells.append(hit or "—")
        print(f"| {cap} | " + " | ".join(cells) + " |")

    print("\n-- unique to one agent --")
    known = {n for _, ns in CAPS for n in ns}
    for a in order:
        extra = sorted(v for k, v in rows[a].items() if k not in known)
        if extra:
            print(f"{a}: {' '.join(extra)}")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "tools.txt")
