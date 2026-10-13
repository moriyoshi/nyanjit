#!/bin/sh
# Create a standalone Go module for a reproducer that depends on NyanJIT via
# a replace directive, so nothing is written into the repository.
# Usage: new-poc-module.sh DIR [NYANJIT_TREE]
set -eu
dir=${1:?usage: new-poc-module.sh DIR [NYANJIT_TREE]}
tree=${2:-$(git -C "$(dirname "$0")" rev-parse --show-toplevel)}
tree=$(cd "$tree" && pwd)
case "$(cd "$(dirname "$dir")" 2>/dev/null && pwd || echo "$dir")/" in
"$tree"/*)
	case "$dir" in
	*/.agents-workspace/*) ;;
	*) echo "refusing to create a PoC module inside the repository: $dir" >&2; exit 1 ;;
	esac
	;;
esac
mkdir -p "$dir"
goversion=$(sed -n 's/^go //p' "$tree/go.mod")
cat > "$dir/go.mod" <<MOD
module poc

go $goversion

require (
	github.com/moriyoshi/nyanjit v0.0.0
	github.com/moriyoshi/nyanjit/backend v0.0.0
)

replace (
	github.com/moriyoshi/nyanjit => $tree
	github.com/moriyoshi/nyanjit/backend => $tree/backend
)
MOD
cp "$tree/go.sum" "$dir/go.sum"
echo "module created at $dir (nyanjit => $tree)"
echo "run with: GOWORK=off GOFLAGS=-mod=mod GOPROXY=off GOMAXPROCS=4 GOMEMLIMIT=4GiB timeout 300 sh -c 'ulimit -v 16000000; go run .'"
