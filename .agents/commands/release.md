---
description: Release via tag push — CI builds, publishes, and bumps the Homebrew formula
argument-hint: <patch|minor|major>
---

# Release

Releasing `g2g` is automated. Pushing a `v*` tag triggers
`.github/workflows/release.yml`, whose `verify` job runs `gofmt`, `go vet`, and
`go test` and gates the release job. That job calls the shared `go-release`
workflow in `shhac/homebrew-tap` to cross-build every platform, publish the
GitHub Release, and regenerate + push `Formula/g2g.rb` to the tap — the
shared workflow builds and publishes only, so `verify` is the sole automated
check that the tagged tree is sound. The tag also triggers
`.github/workflows/publish-skill.yml`, which publishes `skills/g2g` to
`shhac/agent-skills`. **No manual build, and no manual formula bump.**
The formula installs the executable as `g2g`, including bash, zsh, and fish
completions generated from that installed executable.

## Steps

1. `$ARGUMENTS` must be `patch`, `minor`, or `major` — else stop and ask.
2. Pre-flight (the tag's `verify` job gates the release, but check locally
   first so a bad tag is never pushed):
   - Clean tree (`git status --short`), on `main`, up to date with `origin/main`.
   - Format, tests, and vet pass: `gofmt -d $(rg --files -g '*.go')`,
     `go test ./...`, and `go vet ./...`. The version is injected from the tag
     (`-ldflags -X main.version=…`) — there is no version file to edit
     (`cmd/g2g/main.go::version` stays `"dev"`).
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
   gh run watch --repo shhac/g2g
   gh release view "v${new_version}" --repo shhac/g2g
   ```
   Install / upgrade: `brew install shhac/tap/g2g` · `brew upgrade shhac/tap/g2g`
   (then use `g2g`, not the formula name, to run the installed command).

## Manual fallback (only if the workflow itself is broken)

Re-run a failed release with `gh run rerun <id> --repo shhac/g2g`. To bypass
the workflow entirely, build the `GOOS/GOARCH` binaries with
`-ldflags "-s -w -X main.version=<v>"`, use `gh release create` for the
tarballs, and edit `Formula/g2g.rb` by hand.
