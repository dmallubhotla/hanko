# hanko

> 判子 — the stamp you press onto a finished thing.

`hanko` is a small Go CLI that computes a version from your git history and stamps it onto build artifacts: container images, helm charts, Go binaries, OS packages, archives.
It is intended as a more specific, single-static-binary replacement for [GitVersion](https://github.com/GitTools/GitVersion).

## Philosophy

Hanko has **three primitives** and **one ritual** that composes them:

- **`hanko version`** — *identity query*.
  Read-only, idempotent.
  Same `(commit, branch, dirty, tag-history)` → same answer.
  Re-run it freely; every CI job that needs a label computes from its own checkout.
- **`hanko stamp …`** — *apply identity to an artifact*.
  Two output shapes (see below).
  Set version in an app, or apply version to a docker image
- **`hanko tag`** — *promote identity to a permanent git ref*.
  The only command that creates a release artifact in git; everything else is preparation.

These are wired together into:
- **`hanko seal`** — *the release rite*.
  Sugar over the primitives: stamp the declared targets, run `pre-commit:` hooks, single commit, tag, push.
  One invocation, one release commit, atomic push.

## Install

Prebuilt binaries for `linux-amd64` and `linux-arm64` are attached to every tagged release.
The install script fetches the binary for your platform, verifies it against the published `checksums.txt`, and drops it into `$HOME/.local/bin` (override with `-d` or `HANKO_INSTALL_DIR`):

```sh
curl -fsSL https://github.com/dmallubhotla/hanko/releases/latest/download/install.sh | bash
```

Pin a specific release with `-V`:

```sh
curl -fsSL https://github.com/dmallubhotla/hanko/releases/latest/download/install.sh | bash -s -- -V v0.2.4
```

Or skip the script and grab `hanko-<target>` + `checksums.txt` from the [releases page](https://github.com/dmallubhotla/hanko/releases) directly.
Nix users can `nix run github:dmallubhotla/hanko -- version` or add this flake as an input.

### Stamp shapes

Stamp commands come in two flavors, sorted by what they touch:

- **Build-time stamps (stdout / labels).**
  `stamp go-ldflags` prints `-X main.version=…` for splicing into `go build`.
  `stamp docker tags` prints the image-ref fan-out.
  `stamp docker labels` emits `--label org.opencontainers.image.*` args.
  These run on every build, don't touch the repo, and the dirty worktree they produce is intentional and discarded with the build directory.
- **Release-time stamps (in-place file mutation).**
  `stamp helm <chart-dir>` sets `version` / `appVersion` in `Chart.yaml`.
  `stamp nix [flake-file]` sets the `version = "..."` attr in `flake.nix`.
  `stamp` (no args, config-driven) reads `stamp-targets:` from `.hanko.yaml` and applies all declared targets in one pass — works across `pyproject.toml`, `package.json`, `Cargo.toml`, `Chart.yaml`, `flake.nix`, plain `VERSION` files.
  These mutate source files that record the project's version; they're release-time operations, not build-time.

### Release rite (`hanko seal`)

`hanko seal` is the bundled release flow.
It effectively does `hanko stamp -> git commit -> hanko tag --push`.

1. **Pre-flight.** Refuse a dirty worktree (so the release commit only contains what seal produced) or a detached HEAD. Refuse pre-release versions unless `seal.refuse-prerelease: false`.
2. **Stamp targets.** Apply every `stamp-targets:` entry to the computed version. Failures abort the seal.
3. **Pre-commit hooks.** Run `seal.pre-commit:` commands in order — changelog generation, lockfile regeneration, anything that produces files that should be in the release commit. Templated args (`{semver}`, `{full}`, `{major}`, etc.) expand to fields of the computed version.
4. **Commit.** A single release commit with everything the previous steps produced, using `seal.commit-message:` (default `Release {semver}`).
5. **Tag.** Annotated tag matching `hanko tag` semantics.
6. **Push.** `git push --atomic` for commit + tag, to `seal.push-remote:` (default `origin`).

`hanko seal --dry-run` walks the same pipeline without mutating, printing each step.

Anyone who prefers a hand-rolled recipe can still call the primitives directly — seal exists for ergonomics, not as a gatekeeper.

## Commands

| Command                              | Purpose                                                                |
| ------------------------------------ | ---------------------------------------------------------------------- |
| `hanko version`                      | Compute the current version. `--format <semver\|full\|json\|env\|gha>`. `--bump <patch\|minor\|major\|none>` short-circuits the bump strategy for one invocation. |
| `hanko tag [--push]`                 | Create (and optionally push) an annotated git tag for that version     |
| `hanko seal [--dry-run]`             | Run the release rite: stamp targets → run hooks → commit → tag → push  |
| `hanko stamp` (no args)              | Apply every declared `stamp-targets:` to the computed version          |
| `hanko stamp go-ldflags`             | Emit `-X main.version=… -X main.commit=… -X main.date=…` for `go build` |
| `hanko stamp docker tags <image>`    | Fan version out into `<image>:<full>`, `:<major.minor>`, `:<major>`, `:latest`, `:<branch>-<sha>` |
| `hanko stamp docker labels`          | Emit `org.opencontainers.image.*` labels as `--label` args or a label file |
| `hanko stamp helm <chart-dir>`       | Set `version` and `appVersion` in `Chart.yaml` in place                |
| `hanko stamp nix [flake-file]`       | Set the `version = "..."` attr in `flake.nix` (release-time bump)      |
| `hanko config show`                  | Print the resolved (merged-with-defaults) `.hanko.yaml`                |

## Config

Hanko is opinionated: no `.hanko.yaml` means **production-quality defaults**, not "no behaviour."
A repo with no config file is treated as if it had this:

```yaml
# Equivalent to running with no .hanko.yaml — print live via `hanko config show`.

tag-prefix: "^v?(.+)$"           # regex applied to existing tags to extract a semver; write-side follows the repo's existing tag shape
dirty-suffix: true               # dirty worktree appends `.dirty` to build metadata
initial-version: "0.1.0"         # base used when no semver tag is reachable
on-shallow: refuse               # refuse | warn | ignore — see D-004
bump-strategy: conventional-commits  # conventional-commits | fixed. conventional-commits parses commit subjects (`feat:` / `fix:` / `feat!:`) for bump direction and falls back to the branch's `increment` when no commit contributes a signal; fixed skips the parser entirely.

tag-match:                       # globs that decide which tags are eligible for discovery (`git describe --match`)
  - "v[0-9]*.[0-9]*.[0-9]*"
  - "[0-9]*.[0-9]*.[0-9]*"

branches:                        # evaluated in order, first regex match wins
  # `increment` (patch | minor | major | none) names the direction of a one-time
  # bump past the latest tag (D-013). `label` controls the pre-release suffix:
  # empty = release-shaped, non-empty = pre-release. Both fields accept
  # `{branch}` and `{N}` (Nth regex capture group) template variables.
  # Each branch may also set `bump-strategy: fixed | conventional-commits` to
  # override the top-level strategy (e.g. "mainline reads commits, hotfix always
  # bumps patch").
  - name: mainline
    regex: ^(main|master)$
    is-mainline: true
    increment: patch
    label: ""
  - name: release
    regex: ^release/(\d+)\.(\d+)$
    is-mainline: true
    increment: patch
    label: ""
    major-from: 1                # major/minor bind to capture groups
    minor-from: 2
  - name: hotfix
    regex: ^hotfix/.*$
    increment: patch
    label: hotfix                # → 1.2.4-hotfix.N
  - name: feature
    regex: .*
    increment: none
    label: "{branch}"            # → 1.2.3-feature-foo.N

seal:                            # hanko seal — the release rite
  commit-message: "chore: Release {semver}"  # `chore:` keeps the release commit classified as no-bump under conventional-commits
  push-remote: origin            # `git push --atomic <remote> <branch> <tag>`; set to "" to disable push
  refuse-prerelease: true        # mirror D-011; set to false to seal pre-release versions
```

The commit count past the latest tag lives **only in build metadata** (`+N.short-sha`), not in the SemVer core.
A repo where v1.2.3 is the latest tag computes:

| State                        | SemVer      | FullSemVer                  |
| ---------------------------- | ----------- | --------------------------- |
| at v1.2.3                    | `1.2.3`     | `1.2.3+0.abc1234`           |
| 1 commit past on main        | `1.2.4`     | `1.2.4+1.abc1234`           |
| 47 commits past on main      | `1.2.4`     | `1.2.4+47.abc1234`          |
| on a hotfix branch (n=2)     | `1.2.4-hotfix.2`     | `1.2.4-hotfix.2+2.abc1234`     |
| on `feature/foo` (n=3)       | `1.2.3-feature-foo.3` | `1.2.3-feature-foo.3+3.abc1234`|

The "next" patch is decided when `hanko tag` runs, not by how many commits have accumulated.
That keeps the SemVer slot honest — it names a release, doesn't count commits.

To override any of these, create `.hanko.yaml` at the repo root with just the keys you want to change — the rest fall back to defaults.
See [`docs/hanko-yaml.md`](./docs/hanko-yaml.md) for the full schema reference.

### `--repo` scope

`--repo <path>` operates **only on the named repo**:

- **Linked git worktrees** (`git worktree add …`) are supported: hanko reads the worktree's own branch / dirty / in-progress state.
  The shared `.git` dir is resolved via `git rev-parse --git-dir` so per-worktree markers (MERGE_HEAD, rebase-merge/, etc.) work correctly.
- **Nested git repos / submodules are not traversed.**
  Tags, commits, and dirty state from inside a submodule are not visible to `hanko --repo <parent>`.
  Point `--repo` at the submodule directly to compute *its* version standalone.

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

