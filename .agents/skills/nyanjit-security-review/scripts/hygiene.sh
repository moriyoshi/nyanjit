#!/bin/sh
# Pre-publication repository checks for NyanJIT. Read-only; prints findings.
# Usage: hygiene.sh [REPO]
set -u
repo=${1:-$(git -C "$(dirname "$0")" rev-parse --show-toplevel)}
cd "$repo" || exit 1

echo "== .gitignore"
if [ -f .gitignore ]; then
	for p in .agents-workspace/ e2e/.build/ '*.test'; do
		grep -qxF "$p" .gitignore || echo "missing pattern: $p"
	done
else
	echo "no .gitignore"
fi

echo "== untracked files that git add -A would publish (largest first)"
git ls-files --others --exclude-standard -z | xargs -0 -r du -sk 2>/dev/null |
	sort -rn | head -15

echo "== tracked or untracked binaries / large files (>1 MiB)"
git ls-files --cached --others --exclude-standard -z |
	xargs -0 -r -I{} sh -c '[ -f "{}" ] && [ $(wc -c < "{}") -gt 1048576 ] && echo "{}"' 2>/dev/null

echo "== possible secrets or local paths"
git ls-files --cached --others --exclude-standard -z |
	xargs -0 -r grep -nIE \
	'(/home/[a-z][a-z0-9_-]*/|-home-[a-z][a-z0-9_]*-|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|BEGIN [A-Z ]*PRIVATE KEY|xox[baprs]-[A-Za-z0-9-]{10,})' \
	2>/dev/null | grep -v '^\.agents/skills/' | head -20

echo "== GitHub workflows"
for f in .github/workflows/*.yml .github/workflows/*.yaml; do
	[ -f "$f" ] || continue
	grep -q 'pull_request_target' "$f" && echo "$f: uses pull_request_target"
	grep -q '^permissions:' "$f" || echo "$f: no top-level permissions block"
	grep -nE 'uses: [^@]+@[^ ]+' "$f" | grep -vE '@[0-9a-f]{40}( |$)' | sed "s|^|$f: unpinned action: |"
	grep -nE 'run:.*\$\{\{ *github\.event\.' "$f" | sed "s|^|$f: event data interpolated into run: |"
done

echo "== go.mod pre-release / pseudo-version requirements"
grep -nE 'v[0-9]+\.[0-9]+\.[0-9]+-(alpha|beta|rc|0\.[0-9]{14}|[0-9]+\.[0-9]{14})' go.mod

echo "== done"
