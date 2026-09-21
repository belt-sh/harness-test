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
NM='/home/testuser/.npm-global/lib/node_modules'
PKG={'claude':'@anthropic-ai','pi':'@earendil-works','copilot':'@github','gemini':'@google',
     'kilo':'@kilocode','kimi':'@moonshot-ai','omp':'@oh-my-pi','codex':'@openai',
     'qwen':'@qwen-code','droid':'droid','opencode':'opencode-ai'}
def files(root):
    out=[]
    for dp,_,fs in os.walk(root):
        for f in fs:
            p=os.path.join(dp,f)
            try:
                if os.path.getsize(p)>300*1024*1024: continue
            except OSError: continue
            out.append(p)
    return out
for agent,pkg in PKG.items():
    want=declared.get(agent,set())
    root=os.path.join(NM,pkg)
    if not os.path.isdir(root): print(f"{agent}\tMISSING\t{root}"); continue
    if not want: print(f"{agent}\tNO-BASELINE"); continue
    best=(None,0)
    for p in files(root):
        try: data=open(p,'rb').read()
        except Exception: continue
        hits=sum(1 for w in want if (b'"'+w.encode()+b'"') in data)
        if hits>best[1]: best=(p,hits)
    path,hits=best
    if not path or hits<3:
        print(f"{agent}\tNO-CLUSTER hits={hits}/{len(want)}"); continue
    data=open(path,'rb').read()
    idxs=[]
    for w in want:
        t=b'"'+w.encode()+b'"'; i=data.find(t)
        while i!=-1: idxs.append(i); i=data.find(t,i+1)
    idxs.sort()
    cand={}
    for i in idxs:
        seg=data[max(0,i-1200):i+1200]
        for m in re.finditer(rb'"([A-Za-z][A-Za-z0-9_]{2,40})"',seg):
            n=m.group(1).decode()
            if n not in want: cand[n]=cand.get(n,0)+1
    top=[f"{n}({c})" for n,c in sorted(cand.items(),key=lambda kv:-kv[1])[:30]]
    print(f"{agent}\tOK {hits}/{len(want)} in {os.path.relpath(path,root)}\n   {' '.join(top)}")
