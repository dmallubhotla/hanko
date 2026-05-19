# hanko

> 判子 — the stamp you press onto a finished thing.

`hanko` is a small Go CLI that computes a version from your git history and stamps it onto build artifacts: container images, helm charts, Go binaries, OS packages, archives.
It is intended as a more specific, single-static-binary replacement for [GitVersion](https://github.com/GitTools/GitVersion).

## Philosophy

Hanko has three main commands:
- `hanko version`: return a descriptor of current repository state. read-only and idempotent.
- `hanko stamp …`: apply the current repository state to an artifact
- `hanko tag`: creates a git tag with the current version

A useful litmus test: if running `hanko version` could change behavior elsewhere, the design is wrong.
It's a label-reader, not a state-machine step.

## Status

M0–M3 shipped: real version computation, idempotent tagging, and stamping for `go-ldflags` / `docker tags` / `docker labels` / `helm` / `nix` work end-to-end against unit, smoke, and flow tests.
See [ROADMAP.md](./ROADMAP.md) for what's left before v1, and [docs/design-decisions.md](./docs/design-decisions.md) for open design questions.

## Quick start

```sh
nix build
./result/bin/hanko version             # → e.g. 1.2.3 or 1.2.3-feature-foo.4
./result/bin/hanko version --format full
./result/bin/hanko version --format json
./result/bin/hanko version --format env
./result/bin/hanko version --format gha  # key=value lines for $GITHUB_OUTPUT
```

For more, see [examples/local-usage.md](./examples/local-usage.md) and the migration sketches in [examples/migrations/](./examples/migrations/).

## Commands

| Command                              | Purpose                                                                |
| ------------------------------------ | ---------------------------------------------------------------------- |
| `hanko version`                      | Compute the current version. Formats: `semver` / `full` / `json` / `env` / `gha` |
| `hanko tag [--push]`                 | Create (and optionally push) an annotated git tag for that version     |
| `hanko stamp go-ldflags`             | Emit `-X main.version=… -X main.commit=… -X main.date=…` for `go build` |
| `hanko stamp docker tags <image>`    | Fan version out into `<image>:<full>`, `:<major.minor>`, `:<major>`, `:latest`, `:<branch>-<sha>` |
| `hanko stamp docker labels`          | Emit `org.opencontainers.image.*` labels as `--label` args or a label file |
| `hanko stamp helm <chart-dir>`       | Set `version` and `appVersion` in `Chart.yaml` in place                |
| `hanko stamp nix [flake-file]`       | Set the first `version = "..."` attr in `flake.nix` (release-time bump)|
| `hanko config show`                  | Print the resolved (merged-with-defaults) `.hanko.yaml`                |

## Defaults

Hanko is opinionated: no `.hanko.yaml` means **production-quality defaults**, not "no behaviour."
A repo with no config file is treated as if it had this:

```yaml
# Equivalent to running with no .hanko.yaml — print live via `hanko config show`.

tag-prefix: "^v?(.+)$"           # regex applied to existing tags to extract a semver; write-side follows the repo's existing tag shape
mode: continuous-delivery        # commits on mainline advance patch; non-mainline gets pre-release labels
dirty-suffix: true               # dirty worktree appends `.dirty` to build metadata
initial-version: "0.1.0"         # base used when no semver tag is reachable
on-shallow: refuse               # exit non-zero on shallow clones — see D-004

tag-match:                       # globs that decide which tags are eligible for discovery (`git describe --match`)
  - "v[0-9]*.[0-9]*.[0-9]*"
  - "[0-9]*.[0-9]*.[0-9]*"

branches:                        # evaluated in order, first regex match wins
  - name: mainline
    regex: ^(main|master)$
    is-mainline: true
    increment: patch             # patch += commits-since-tag
    label: ""                    # no pre-release suffix → release-shaped
  - name: release
    regex: ^release/(\d+)\.(\d+)$
    is-mainline: true
    increment: patch
    label: ""
    major-from: 1                # major/minor bind to capture groups
    minor-from: 2
  - name: hotfix
    regex: ^hotfix/.*$
    increment: patch             # one-time +1, not progressive
    label: hotfix                # → 1.2.4-hotfix.N
  - name: feature
    regex: .*
    increment: none
    label: "{branch}"            # → 1.2.3-feature-foo.N
```

To override any of these, create `.hanko.yaml` at the repo root with just the keys you want to change — the rest fall back to defaults.
See [`docs/hanko-yaml.md`](./docs/hanko-yaml.md) for the full schema reference.

## Build & develop

This repo uses Nix + `gomod2nix`.
Common tasks live in the `justfile`:

- `just build` — build the binary via `nix build`
- `just test` — run Go unit tests
- `just smoke` — CLI smoke tests on minimal repos (verifies command shape, flag handling, exit codes)
- `just flows` — CLI flow tests on mock repos with realistic tag histories (verifies outcomes on hotfix / release-branch / multi-tag / push-to-remote scenarios)
- `just check-cli` — both `smoke` and `flows`
- `just check` — `nix flake check`
- `just fmt` — format files via treefmt
- `just fixtures` — (re)build dev fixtures under `./fixtures/` (gitignored)
- `just chores` — `go mod tidy` + regenerate `gomod2nix.toml`

