#!/bin/bash
# Setup script for the g2g demo recordings.
#
#   bash assets/demo-setup.sh            an empty project on main
#   bash assets/demo-setup.sh published  plus a three-branch stack, pushed
#
# Everything happens inside /tmp/g2g-demo, rebuilt on every run: a bare
# repository stands in for the remote, and a second clone for a colleague.
# No real repository, remote, or global git config is touched, and nothing
# leaves the machine. g2g is built from this checkout, so a recording shows
# the code beside it.
set -e

repo=$(cd "$(dirname "$0")/.." && pwd)
demo=/tmp/g2g-demo
rm -rf "$demo"
mkdir -p "$demo/bin"
(cd "$repo" && go build -o "$demo/bin/g2g" ./cmd/g2g)
cp "$repo/assets/demo-trunk-moves.sh" "$demo/trunk-moves.sh"

export PATH="$demo/bin:$PATH"
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

[ "$1" = published ] || exit 0

cd "$demo/acme-api"
work() {
	printf 'export function %s() {}\n' "$2" > "$2.js"
	git add "$2.js"
	g2g create "$1" -m "$3" --apply >/dev/null
}
work feature/auth auth "Add the auth middleware"
work feature/login login "Add the login form"
work feature/logout logout "Add logout"
g2g push --apply >/dev/null
