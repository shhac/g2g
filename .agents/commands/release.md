---
description: Release via tag push — CI builds, publishes, and bumps the Homebrew formula
argument-hint: <patch|minor|major>
---

# Release

Releasing `gt2gh` is automated. Pushing a `v*` tag triggers
`.github/workflows/release.yml`, which calls the shared `go-release` workflow in
`shhac/homebrew-tap` to cross-build every platform, publish the GitHub Release,
and regenerate + push `Formula/gt2gh.rb` to the tap. The tag also triggers
`.github/workflows/publish-skill.yml`, which publishes `skills/gt2gh` to
`shhac/agent-skills`. **No manual build, and no manual formula bump.**

## Steps

1. `$ARGUMENTS` must be `patch`, `minor`, or `major` — else stop and ask.
2. Pre-flight (CI re-runs tests on the tag, but check locally first):
   - Clean tree (`git status --short`), on `main`, up to date with `origin/main`.
   - Format, tests, and vet pass: `gofmt -d $(rg --files -g '*.go')`,
     `go test ./...`, and `go vet ./...`. The version is injected from the tag
     (`-ldflags -X main.version=…`) — there is no version file to edit
     (`cmd/gt2gh/main.go::version` stays `"dev"`).
3. Compute the new version by bumping the latest tag
   (`git describe --tags --abbrev=0`): patch → x.y.(z+1), minor → x.(y+1).0,
   major → (x+1).0.0.
4. Tag and push — this is the whole release:
   ```bash
   git tag "v${new_version}"
   git push origin "v${new_version}"
   ```
5. Verify CI and the outputs:
   ```bash
   gh run watch --repo shhac/gt2gh
   gh release view "v${new_version}" --repo shhac/gt2gh
   ```
   Install / upgrade: `brew install shhac/tap/gt2gh` · `brew upgrade shhac/tap/gt2gh`

## Manual fallback (only if the workflow itself is broken)

Re-run a failed release with `gh run rerun <id> --repo shhac/gt2gh`. To bypass
the workflow entirely, build the `GOOS/GOARCH` binaries with
`-ldflags "-s -w -X main.version=<v>"`, use `gh release create` for the
tarballs, and edit `Formula/gt2gh.rb` by hand.
