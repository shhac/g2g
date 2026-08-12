---
description: Prepare a guarded gt2gh release; publishing automation is not configured
argument-hint: <patch|minor|major>
---

# Release

`gt2gh` is not release-configured yet: this repository has no `origin` remote,
release workflow, formula, release tags, or documented artifact contract. Treat
this command as a preflight and planning workflow. Do not create a tag, push,
publish a GitHub release, or claim a version has shipped until those pieces
exist and the user explicitly approves the release.

## Steps

1. Require `$ARGUMENTS` to be `patch`, `minor`, or `major`; otherwise stop and
   ask for a valid release level.
2. Check the release state before deciding a version:
   ```bash
   git status --short
   git branch --show-current
   git remote -v
   git tag --list 'v*'
   ```
   Stop if the tree is not clean, the branch is not `main`, `origin` is absent,
   or the latest SemVer tag / first-release version has not been agreed.
3. Run the local release checks without leaving an artifact in the worktree:
   ```bash
   gofmt -d $(rg --files -g '*.go')
   go test ./...
   go vet ./...
   go build -trimpath -ldflags "-X main.version=v<version>" \
     -o "$(mktemp -d)/gt2gh" ./cmd/gt2gh
   ```
4. Report the blockers and proposed version. Before any tag is created, add
   and verify a release contract: remote repository, tag-triggered CI or a
   documented manual artifact process, supported build targets, and any
   distribution destination. Re-run the checks after that configuration lands.
5. Only after the user explicitly authorizes publication and the contract is
   verified, create and push the agreed `v<version>` tag, then verify the
   resulting CI run and release artifacts. Do not improvise a GitHub release or
   Homebrew update for this initial repository state.
