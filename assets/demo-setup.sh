#!/bin/bash
# Setup script for the g2g demo recording.
#
# Everything happens inside /tmp/g2g-demo, rebuilt on every run: a bare
# repository stands in for the remote, and a second clone for a colleague.
# No real repository, remote, or global git config is touched, and nothing
# leaves the machine. g2g is built from this checkout, so the recording shows
# the code beside it.
set -e

repo=$(cd "$(dirname "$0")/.." && pwd)
demo=/tmp/g2g-demo
rm -rf "$demo"
mkdir -p "$demo/bin"
(cd "$repo" && go build -o "$demo/bin/g2g" ./cmd/g2g)
cp "$repo/assets/demo-trunk-moves.sh" "$demo/trunk-moves.sh"

export GIT_AUTHOR_NAME=Demo GIT_AUTHOR_EMAIL=demo@example.test
export GIT_COMMITTER_NAME=Demo GIT_COMMITTER_EMAIL=demo@example.test
export GIT_CONFIG_GLOBAL=/dev/null

git init -q --bare -b main "$demo/origin.git"
git clone -q "$demo/origin.git" "$demo/seed" 2>/dev/null
cd "$demo/seed"
printf 'export const port = 8080\n' > server.js
git add server.js
git commit -qm "Start the API server"
git push -q origin main

git clone -q "$demo/origin.git" "$demo/acme-api"
git clone -q "$demo/origin.git" "$demo/colleague"
