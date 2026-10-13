#!/usr/bin/env python3
"""Convert Go benchmark output to reproducible per-instruction JSON medians."""
import json
import os
from pathlib import Path
import platform
import re
import statistics
import subprocess
import sys

benchmark = sys.argv[3] if len(sys.argv) > 3 else "BenchmarkPrepare"
samples = {}
for line in Path(sys.argv[1]).read_text().splitlines():
    parts = line.split()
    if not parts or not parts[0].startswith(benchmark + '/'):
        continue
    name = re.sub(r'-\d+$', '', parts[0].split('/', 1)[1])
    row = {parts[i + 1]: float(parts[i]) for i in range(2, len(parts) - 1, 2)}
    samples.setdefault(name, []).append(row)
result = {
    'schema': 1,
    'go': subprocess.check_output(['go', 'version'], text=True).strip(),
    'host': platform.machine(),
    'gomaxprocs': 1,
    'affinity_cpu': int(os.environ['NYANJIT_BENCH_CPU']) if 'NYANJIT_BENCH_CPU' in os.environ else None,
    'command': (('taskset -c ' + os.environ['NYANJIT_BENCH_CPU'] + ' env ') if 'NYANJIT_BENCH_CPU' in os.environ else '') + "GOMAXPROCS=1 go test ./compile -run '^$' -bench '^" + benchmark + "$' -benchmem -benchtime=300ms -count=5 -cpu=1",
    'denominator': ('one complete construction/Finish' if benchmark == 'BenchmarkBuild' else 'source IR instructions, before vector legalization'),
    'workloads': {
        name: {'median': {metric: statistics.median(row[metric] for row in rows) for metric in rows[0]}, 'samples': rows}
        for name, rows in samples.items()
    },
}
Path(sys.argv[2]).write_text(json.dumps(result, indent=2) + '\n')
for name, data in result['workloads'].items():
    row = data['median']
    if 'ns/insn' in row:
        print(f"{name:16s} {row['ns/op']/1e6:9.3f} ms  {row['ns/insn']:9.1f} ns/insn  {row['B/insn']:9.1f} B/insn  {row['allocs/insn']:8.3f} allocs/insn")
    else:
        print(f"{name:16s} {row['ns/op']:12.0f} ns/op  {row['B/op']:12.0f} B/op  {row['allocs/op']:9.0f} allocs/op")
