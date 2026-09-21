#!/usr/bin/env python3
"""Report tool-shaped strings in an agent's package that no turn declared.

Takes the wire-measured tool names an agent declared, finds the file in its
package where they cluster, and lists the other tool-shaped strings around
them. Output is candidates to go and measure, not measurements: it reads
strings near strings, so parameter names and status values survive, and a name
in a package may be dead code, an alias or a feature behind a flag.

Run inside the container after a suite run has installed the agents.
TOOLS_TXT points at "<agent> <tool> <tool> ..." lines from --probe tools.
"""
import os,re
declared={}
for line in open(os.environ.get("TOOLS_TXT", "/work/tools.txt")):
    p=line.split()
    if p: declared[p[0]]=set(p[1:])
# Where each agent's code lives. npm packages install under a shared prefix
# that survives a run; the curl and pip installers write into $HOME, which the
# suite points at a temp directory it deletes — so those five were missing from
# this sweep entirely until they were installed against a fixed HOME. The split
# was never a property of the agents, only of where this script looked.
NM = '/home/testuser/.npm-global/lib/node_modules'
HOME = os.environ.get('SWEEP_HOME', '/home/testuser')

PKG = {'claude': '@anthropic-ai', 'pi': '@earendil-works', 'copilot': '@github',
       'gemini': '@google', 'kilo': '@kilocode', 'kimi': '@moonshot-ai',
       'omp': '@oh-my-pi', 'codex': '@openai', 'qwen': '@qwen-code',
       'droid': 'droid', 'opencode': 'opencode-ai'}

# Agents whose installer drops a binary rather than a package tree. The value
# is the binary name; the sweep reads the file the symlink resolves to, and
# its directory when that holds a bundle.
BIN = {'cursor': 'cursor-agent', 'goose': 'goose', 'grok': 'grok',
       'hermes': 'hermes', 'kiro': 'kiro-cli'}

BIN_DIRS = ['.local/bin', '.grok/bin', '.npm-global/bin']


def roots_for(agent):
    """Every directory or file that may hold this agent's code."""
    pkg = PKG.get(agent)
    if pkg:
        d = os.path.join(NM, pkg)
        return [d] if os.path.isdir(d) else []
    name = BIN.get(agent)
    if not name:
        return []
    out = []
    for d in BIN_DIRS:
        p = os.path.join(HOME, d, name)
        if not os.path.exists(p):
            continue
        real = os.path.realpath(p)
        out.append(real)
        # A launcher usually sits beside the bundle it runs — but only when
        # that directory belongs to this agent. ~/.local/bin holds every
        # agent's shim, and adding it swept goose's binary for hermes and for
        # kiro, reporting goose's strings under their names. Same mistake the
        # detector made taking ".config" as opencode's config directory: a
        # parent shared by many programs identifies none of them.
        parent = os.path.dirname(real)
        shared = any(os.path.realpath(os.path.join(HOME, d)) == parent for d in BIN_DIRS)
        if not shared and parent not in out and os.path.isdir(parent):
            out.append(parent)
    # A pip-installed agent's code is in site-packages, not behind the shim.
    # The first version of this walked one level under ~/.local/lib and looked
    # for a directory starting with the agent's name, which only ever saw
    # "python3.11" — hermes reported no cluster while its source sat two
    # levels further down.
    import glob
    for pat in (f'{HOME}/.local/lib/python*/site-packages/{agent}*',
                f'/usr/lib/python3/dist-packages/{agent}*',
                f'/usr/local/lib/python*/dist-packages/{agent}*'):
        out.extend(d for d in glob.glob(pat) if os.path.isdir(d) and not d.endswith('.dist-info'))
    return out


def files(root):
    if os.path.isfile(root): return [root]
    out=[]
    for dp,_,fs in os.walk(root):
        for f in fs:
            p=os.path.join(dp,f)
            try:
                if os.path.getsize(p)>300*1024*1024: continue
            except OSError: continue
            out.append(p)
    return out
for agent in list(PKG) + list(BIN):
    want = declared.get(agent, set())
    roots = roots_for(agent)
    if not roots:
        print(f"{agent}\tNOT-FOUND"); continue
    if not want:
        print(f"{agent}\tNO-BASELINE"); continue
    best = (None, 0)
    for root in roots:
        for p in files(root):
            try: data = open(p, 'rb').read()
            except Exception: continue
            hits = sum(1 for w in want if (b'"' + w.encode() + b'"') in data)
            if hits > best[1]: best = (p, hits)
    path, hits = best
    if not path or hits < 3:
        print(f"{agent}\tNO-CLUSTER hits={hits}/{len(want)}"); continue
    data = open(path, 'rb').read()
    idxs = []
    for w in want:
        t = b'"' + w.encode() + b'"'; i = data.find(t)
        while i != -1: idxs.append(i); i = data.find(t, i + 1)
    idxs.sort()
    cand = {}
    for i in idxs:
        seg = data[max(0, i - 1200):i + 1200]
        for m in re.finditer(rb'"([A-Za-z][A-Za-z0-9_]{2,40})"', seg):
            n = m.group(1).decode()
            if n not in want: cand[n] = cand.get(n, 0) + 1
    top = [f"{n}({c})" for n, c in sorted(cand.items(), key=lambda kv: -kv[1])[:30]]
    print(f"{agent}\tOK {hits}/{len(want)} in {os.path.basename(path)}\n   {' '.join(top)}")
