#!/bin/sh
# Copy the NyanJIT working tree to DEST for review while another agent edits
# the real tree. Excludes git metadata, agent scratch and e2e build output.
# Usage: snapshot.sh DEST [REPO]
set -eu
dest=${1:?usage: snapshot.sh DEST [REPO]}
repo=${2:-$(git -C "$(dirname "$0")" rev-parse --show-toplevel)}
mkdir -p "$dest"
rsync -a --delete \
	--exclude .git --exclude .agents-workspace --exclude e2e/.build \
	--exclude '*.test' "$repo"/ "$dest"/
if (cd "$dest" && timeout 300 go build ./... >/dev/null 2>&1); then
	echo "snapshot at $dest builds"
else
	echo "snapshot at $dest does NOT build (the tree may be mid-edit; retry later)" >&2
	exit 1
fi
