# Handoff format

The implementer reads the whole file, so keep it tight. Use neutral compiler
vocabulary. Each item must be actionable without re-deriving the review.

```markdown
# Compiler follow-ups from review (round N)

nyanjit is a JIT compiler backend library for Go. These are follow-ups from
review of round N-1.

The review checked these items and found them clean:
- <each claim verified, with counts: "listings unchanged: 3.1M fresh
  comparisons, 0 diffs, 0 flips">
- <perf claims confirmed independently, with numbers>
- <QEMU: N configs, 0 failures>

Do NOT commit. Do not modify host system state.

scratchpad = <absolute path>
Reviewer files: `$scratchpad/revN/...` (repros, harness, logs).

## 1. Required: <one-line statement of the defect>
- **Where:** `file:line` and what the code does there.
- **Trigger:** concrete input or API sequence, and what goes wrong.
- **Repro:** path to a minimal test under the scratchpad.
- **Fix:** the direction; offer options when the design choice is theirs.
- **Also check:** where the same pattern may recur.
- **Test / acceptance:** what must hold, measurably.

## k. <non-required items, then "Small items" as one-line bullets>

## Gates
- Native: `go test ./...` and `go -C backend test ./...` (also with
  `GOWORK=off`), poisoned `./...`, `go test -race ./...`,
  `./scripts/vet-all.sh`, `go -C e2e test ./...`, `git diff --check`.
- Listings: frozen corpus (exact match), plus a fresh differential run.
- Mutation fuzz extended to the fields touched (0 panics, 0
  accepted-then-invalid).
- Scaling and retention gates (name any new case).
- Benchmarks before/after, pinned, alternating plus reversed.
- QEMU: package binaries and e2e for 386, amd64, riscv64, loong64 (both
  CPUs) and ppc64le.

Report what changed and the commands you ran.
```

Conventions:

- **Order:** put "Required" items first, ordered by severity:
  1. panics and miscompiles;
  2. validation/compiler disagreement;
  3. API contract;
  4. performance;
  5. retention.
- **Regressions:** state each one against a named baseline ("+3% vs round 7
  and +4.5% vs round 6, both orders").
- **Unconfirmed causes:** say "Likely cause … Not confirmed" rather than
  asserting.
- **Mistakes in the report:** when the implementer's report is wrong (e.g.
  "zero panics" when a panic remains), say so plainly in the item.
- **Addenda:** if verification runs are still going when you send, say an
  addendum may follow, then write `handoff-followupsN-addendum.md` only if
  they find something new.
- **API changes:** ask for one contract per API, deletion rather than
  deprecation, and no `internal/` packages, every time an API changes.
- **No new public API:** every handoff says "extend existing APIs; do not add
  new public methods or functions without asking". When the implementer
  reports a "user-approved" design, check the question it asked for a false
  either/or before accepting it. `Code.Call`/`CallPair` stay raw escape
  hatches.
