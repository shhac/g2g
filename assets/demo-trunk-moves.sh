#!/bin/bash
# A colleague lands a change on main while the demo's stack is in progress.
set -e
export GIT_AUTHOR_NAME=Colleague GIT_AUTHOR_EMAIL=colleague@example.test
export GIT_COMMITTER_NAME=Colleague GIT_COMMITTER_EMAIL=colleague@example.test
export GIT_CONFIG_GLOBAL=/dev/null
cd /tmp/g2g-demo/colleague
printf 'export function health() {\n  return { status: "ok" }\n}\n' > health.js
git add health.js
git commit -qm "Add a health check"
git push -q origin main
