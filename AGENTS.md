# Documents for both humans and coding agents

* [README.md](./README.md) ... what NyanJIT is, the API and its contracts, supported targets and tiers, limits, and the Performance section with the comparison tables.
* [docs/](./docs/) ... consumer references, one subject each: [ir.md](./docs/ir.md) (the IR specification), [dual-tier-design.md](./docs/dual-tier-design.md) (tier selection and segment front ends), [sealed-programs.md](./docs/sealed-programs.md), [backend-parity.md](./docs/backend-parity.md) and the others.
* `backend/target/*/FORMS.md` ... the documented encoding forms and sizes per target; `TestDocumentedForms` assembles every row.
* [TODO.md](./TODO.md) ... the project's to-do list: audit findings, encoder findings and open follow-ups, in dated sections. Check and update it when picking up or finishing work.
* [e2e/README.md](./e2e/README.md) ... the cross-platform end-to-end matrix.

# Documents for coding agents

* [./.agents/docs/ARCHITECTURE.md](./.agents/docs/ARCHITECTURE.md) ... compiler implementation, subsystem invariants, historical measurements and archived target-validation/baseline records. Keep implementation plans and acceptance history here rather than in consumer references under `docs/`.
* [./.agents/docs/JOURNAL.md](./.agents/docs/JOURNAL.md) ... findings, decisions, benchmark results, and review history. Append-only.
* [./.agents/docs/LTM/INDEX.md](./.agents/docs/LTM/INDEX.md) ... long-term memory index for durable project knowledge under `./.agents/docs/LTM/`.
* `./.agents/skills/` ... project skills (also reachable as `.claude/skills`):
  * `good-sleep` distills JOURNAL entries into LTM topic documents, and moves their open items to `TODO.md`;
  * `deep-sleep` merges overlapping LTM documents into syntheses;
  * `reconcile-journal-ltm` audits that the journal's durable content is really in LTM, then removes consolidated entries;
  * `distill-memories` promotes durable LTM knowledge into `README.md`, `docs/`, `FORMS.md` and `.agents/docs/QUALITY_GATE.md`;
  * `nyanjit-jit-bench`, `nyanjit-review-rounds` and `nyanjit-security-review` are the benchmark, review and security procedures.

# Rules and protocols

## Journal, memory and to-dos

* Append to `./.agents/docs/JOURNAL.md` when work produces a design decision (with the alternatives rejected), a bug class and which test layer did or did not catch it, before/after benchmark results, or review findings. New entries go at the end, under `## YYYY-MM-DD — <short title>`. Do not edit or delete existing sections; only `reconcile-journal-ltm` removes entries, and only ones already consolidated into LTM.
* Open follow-ups go to `TODO.md`, not to the journal.
* LTM documents are edited and refined over time. Consult `./.agents/docs/LTM/INDEX.md` before re-investigating a subsystem.

## API

* No new exported identifier, and no changed exported signature, without the user's approval. Present the exact Go signature first. This applies to subagents too: they stop and report the signature instead of adding it.
* No `internal/` packages; `machine`, `regalloc` and the targets are public, stable API.
* The project is unpublished: no compatibility shims or deprecations. A replaced API is deleted outright.

## File management

* Write summary documents for your work under `./.agents/docs`, not under `/tmp`.
* Temporary files (scratch trees, profiles, benchmark binaries, logs) go under `./.agents-workspace/tmp`, not under `/tmp` and not in the version-controlled tree. `*.test` binaries and `instrument/jitbench/.build/` are ignored, but do not leave them in the tree either.
* Never delete user files without permission. Only files you created in this session under `./.agents-workspace/tmp/` are safe to delete. Assume all pre-existing files belong to the user.
* Never run `git stash` in the main tree, and commit only when the user asks.

## Shared host

* Other projects' Docker containers and tmux sessions run on this machine. Start containers with `--rm` and a `nyanjit-` name prefix, pin them with `--cpuset-cpus`, and never stop, kill, remove or prune containers or images you did not start.
* Pin heavy jobs with `taskset` and bound them with `timeout`. Benchmarks run on one quiet big core (core 5 on the Cortex-X925 host) with `GOMAXPROCS=1`, alternating the binaries being compared; see `nyanjit-jit-bench`.

## Style

* Repo-authored documentation uses half-width parentheses and colons, and no full-width punctuation.
* Match the surrounding code's style, comment density and naming; `gofmt` every Go file you change.
