import sys, re, statistics as st
# per-process aggregation (min and median of count=N lines); pairs process i of base with process i of new
def parse(paths):
    d = {}
    for path in paths.split(','):
        cur = None
        for line in open(path):
            if line.startswith('== proc'):
                cur = {}
                for k in list(d): pass
                procs = d.setdefault('__procs__', []); procs.append(cur); continue
            if not line.startswith('Benchmark') or cur is None: continue
            f = line.split(); name = re.sub(r'-\d+$', '', f[0])
            vals = {}
            for i in range(2, len(f)-1):
                if f[i+1] in ('ns/op','B/op','allocs/op'): vals[f[i+1]] = float(f[i])
            if 'ns/op' in vals: cur.setdefault(name, []).append(vals)
    return d.get('__procs__', [])
def q(v, p):
    v = sorted(v); k = (len(v)-1)*p; i = int(k); return v[i] if i+1 >= len(v) else v[i]+(v[i+1]-v[i])*(k-i)
A, B = parse(sys.argv[1]), parse(sys.argv[2])
n = min(len(A), len(B))
names = list(dict.fromkeys(k for p in A for k in p))
print(f"{'benchmark':40} {'n':>3} {'min base':>11} {'min new':>11} {'ratio':>6} {'pair-med':>8} {'pair-IQR':>13} {'pair min..max':>14} {'B base':>11} {'B new':>11} {'al base':>7} {'al new':>7}")
for k in names:
    pa = [min(x['ns/op'] for x in A[i][k]) for i in range(n) if k in A[i] and k in B[i]]
    pb = [min(x['ns/op'] for x in B[i][k]) for i in range(n) if k in A[i] and k in B[i]]
    pr = [b/a for a, b in zip(pa, pb)]
    ba = st.median(x['B/op'] for i in range(n) for x in A[i].get(k, []))
    bb = st.median(x['B/op'] for i in range(n) for x in B[i].get(k, []))
    aa = st.median(x['allocs/op'] for i in range(n) for x in A[i].get(k, []))
    ab = st.median(x['allocs/op'] for i in range(n) for x in B[i].get(k, []))
    print(f"{k:40} {len(pr):>3} {st.median(pa):11,.0f} {st.median(pb):11,.0f} {st.median(pb)/st.median(pa):6.3f} {st.median(pr):8.3f} {q(pr,.25):6.3f}-{q(pr,.75):6.3f} {min(pr):6.3f}..{max(pr):6.3f} {ba:11,.0f} {bb:11,.0f} {aa:7,.0f} {ab:7,.0f}")
