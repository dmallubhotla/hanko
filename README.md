# hanko

> 判子 — the stamp you press onto a finished thing.

`hanko` is a small Go CLI that computes a version from your git history and stamps it onto build artifacts: container images, helm charts, Go binaries, OS packages, archives.
It is intended as a more specific, single-static-binary replacement for [GitVersion](https://github.com/GitTools/GitVersion).

## Status

M0–M3 shipped: real version computation, idempotent tagging, and stamping for `go-ldflags` / `docker tags` / `docker labels` / `helm` work end-to-end against unit, smoke, and flow tests.
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

## Layout

```
.
├── main.go
├── cmd/                # cobra commands (version, tag, stamp ...)
├── internal/
│   ├── gitinfo/        # extract relevant git state (read-only)
│   ├── gittag/         # tag creation + push (write-side)
│   ├── version/        # version-calculation engine
│   ├── logging/        # slog file logger
│   └── testrepo/       # shared test helper (temp git repos)
├── docs/
│   ├── design-decisions.md   # running log of open / decided design questions
│   └── hanko-yaml.md         # sketched `.hanko.yaml` config (not implemented)
├── examples/
│   ├── local-usage.md
│   ├── migrations/     # before / after for cicd's shared workflows
│   ├── cicd-composite-action/    # drop-in replacement for resolve-version
│   └── cicd-reusable-workflow/   # drop-in for gitversion.yml
├── test/
│   ├── smoke/          # CLI shape tests
│   ├── flows/          # mock-repo scenario tests
│   └── fixtures/       # script that builds /fixtures (gitignored)
├── flake.nix
├── justfile
└── ROADMAP.md
```
