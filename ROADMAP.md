# Hanko Roadmap — Skeleton → v1.0.0

This is the path from "compiles and prints 0.0.0" to "drop-in replacement for GitVersion in our CI pipelines."
Milestones are ordered by dependency; each should be a small enough scope to land in a single PR.

## Guiding principles

1. **Read-only by default.**
   Computing a version should never mutate the repo or the network.
   `hanko tag` and `hanko stamp` are the only commands that write anything; everything else is `version`-style pure reporting.
2. **One static binary.**
   No runtime config files for the common case.
   CI should be able to `curl | tar | run` it.
   Project-specific behaviour comes from `.hanko.yaml` next to the repo, not a global config.
3. **Deterministic.**
   Same git state → same output, on every OS and every CI runner.
   No timestamps in `SemVer`, no machine-dependent fields.
4. **Honest about what it sees.**
   A dirty worktree, a missing tag, a detached HEAD — surface them clearly, don't paper over them.
5. **Compatible-ish with GitVersion.**
   Field names and JSON shape should be close enough that an existing `${{ steps.gitversion.outputs.SemVer }}` reference can be ported by find-and-replace.

---

## M0 — Skeleton

**Goal:** the binary builds, the command tree exists, and `hanko version` runs end-to-end with a placeholder result.

- [x] `main.go` + cobra root command
- [x] `version`, `tag`, `stamp {docker,helm,go-ldflags}` command stubs
- [x] `internal/gitinfo` shells out to `git` for branch / sha / tag / dirty
- [x] `internal/version` returns `0.0.0` with the right struct shape
- [x] `internal/logging` writes JSON slog to `$XDG_CACHE_HOME/hanko/logs/`
- [x] Nix flake + `gomod2nix` + `justfile` + `treefmt.nix`
- [x] `--format semver|full|json|env` output switch

**Exit criteria:** `just build && ./result/bin/hanko version --format json` prints a populated JSON document including the current branch and short sha.

---

## M1 — Real version computation

**Goal:** stop returning `0.0.0`.
Compute a meaningful SemVer from tags + commit count + branch.

### Tasks

- [x] Parse the latest reachable tag as semver (`v1.2.3` → `{1,2,3}`).
- [x] Count commits since that tag (`git rev-list --count <tag>..HEAD`).
- [x] Apply branch-name policy:
  - `main` / `master` → `<major>.<minor>.<patch+commits>`
  - `release/x.y`     → `<x>.<y>.<patch+commits>`
  - `hotfix/*`        → `<major>.<minor>.<patch+1>-hotfix.<n>`
  - everything else   → `<base>-<sanitized-branch>.<n>` (pre-release)
- [x] Append build metadata: `+<commits>.<short-sha>` for `FullSemVer`.
- [ ] Bump-direction hints from commit-message convention (Conventional Commits parser: `feat!:`/`feat:`/`fix:` → major / minor / patch).
  Behind a config flag, off by default in M1.
  **Deferred — revisit with `.hanko.yaml`.**
- [x] Handle edge cases:
  - [x] No tags in repo → `0.1.0-<branch>.<n>` (always pre-release; see `docs/design-decisions.md`)
  - [~] Detached HEAD → falls back to `"detached"` sentinel; `--source` flag still TBD (D-001)
  - [x] Dirty worktree → appends `.dirty` to build metadata
- [x] Unit tests with table-driven fixtures (small temp repos via `git init`).
- [x] **Pulled forward from M4:** `--format gha` emits the cicd resolve-version contract shape directly to `$GITHUB_OUTPUT`.

### Decisions to make in M1

- **go-git or shell out?**
  Skeleton shells out for portability.
  Reassess once we have ~10 git calls per invocation.
  Likely keep shelling out for v1 and cache results in-process.
- **Config file format.**
  `.hanko.yaml` at repo root, optional, with branch policy overrides.
  Keep field names borrowed from GitVersion (`mode`, `branches.*.tag`, `branches.*.increment`).
  See `docs/hanko-yaml.md` for the sketch.

**Exit criteria:** on a real project with tags, `hanko version` produces a SemVer that matches what we'd hand-pick.

---

## M2 — `hanko tag`

**Goal:** turn the computed version into a real annotated git tag.

- [x] `hanko tag` creates `v<SemVer>` as an annotated tag on `HEAD`.
- [x] Refuses to tag if:
  - [x] worktree is dirty (override with `--force`)
  - [x] the computed version already has a tag at this commit (idempotent: print the existing tag and exit 0)
  - [x] we're on a non-mainline branch and `--allow-prerelease-tag` was not given
- [x] `--push` pushes to `origin` (or `--remote <name>`).
- [x] `--dry-run` prints what would be tagged.
- [x] `--message` and `--sign` for annotated tag content.
- [x] CLI smoke tests in `test/smoke/smoke.sh` (run via `just smoke`).

**Exit criteria:** `hanko tag --push` on `main` produces the same tag a human would have created by hand.
_Met locally; need a real CI run to confirm._

---

## M3 — Stamping artifacts

**Goal:** the "what's it for" of the project.
Take the computed version and apply it to common artifacts.

### M3a — Go ldflags

```sh
go build -ldflags "$(hanko stamp go-ldflags --package main)" ./...
```

- [x] Emits `-X main.version=<SemVer> -X main.commit=<sha> -X main.date=<commit-date>`.
- [x] `--package` configurable.
- [ ] Repeatable `--var name=value` for arbitrary extra stamps.
  _Deferred — three fixed vars cover the common case._

### M3b — Docker / OCI labels & tags

Two subcommands, deliberately split so each composes independently:

- [x] `hanko stamp docker tags <image>` — emits the full image-ref fan-out (one per line).
  Non-prerelease on mainline: `<full>`, `<major>.<minor>`, `<major>`, `:latest`.
  Pre-release: only `<full>`.
  Knobs: `--latest-on-default-branch=false`, `--branch-sha-tag=false`, repeatable `--extra <tag>`.
  Replaces cicd's `compute-image-tags` composite.
- [x] `hanko stamp docker labels` — emits `org.opencontainers.image.*` labels.
  Always sets `version`, `revision`, `created`; `--source` and `--title` are caller-supplied.
  Output modes: `--output args` (default, splicable into `docker build`) and `--output file --file PATH`.
- [ ] Image-mutation mode (call `docker buildx imagetools` to attach labels to an already-built image).
  Deferred — `--label`/`--label-file` covers the build-time case which is the primary one.

### M3c — Helm

```sh
hanko stamp helm ./charts/foo
```

- [x] Edits `Chart.yaml` in place, setting `version` and `appVersion`.
- [x] Preserves comments and key order (line-based edit, not yaml round-trip).
- [x] `--dry-run` prints the changes that would be made without writing.

### M3d — Plain-file substitution (stretch)

```sh
hanko stamp file VERSION
hanko stamp file --template version.txt.tmpl --out version.txt
```

A small `text/template` substitution mode for projects with bespoke needs.
**Deferred** — revisit once a real demand surfaces.

**Exit criteria:** at least one downstream repo (kestrel? crime-ms?) uses hanko in CI for binary stamping and image labels.
_Local: all three M3a–c subcommands work end-to-end against fixtures, smoke tests, and flow tests; need a real CI rollout._

---

## M4 — CI integration ergonomics

**Goal:** `hanko` should be as nice to call from GitHub Actions as `gittools/actions/gitversion/execute` is.

- [x] `hanko version --format gha` writes `full=...` etc. to `$GITHUB_OUTPUT`.
  (Pulled forward to M1.)
- [ ] `hanko version --format dotenv` writes a `.env` file suitable for `--env-file` mounts.
- [x] Concrete migration sketches in `examples/migrations/` for the cicd workflows hanko replaces.
- [ ] Install path on self-hosted runners.
  Decision: bake into `actions-runner-image`; no per-job download.
  Implementation deferred (assumes hanko binary exists on PATH).
- [ ] Document the migration path from GitVersion in `docs/migrating.md`: field-by-field mapping, behavioural differences, gotchas around `mode: ContinuousDelivery`.

**Exit criteria:** one PR in another repo replacing a GitVersion step with hanko, with no consumer-visible diff in version strings.

---

## M5 — Hardening

**Goal:** stop hand-waving the edge cases.

- Shallow-clone detection.
  If `git rev-parse --is-shallow-repository` is true, warn loudly and refuse to compute (configurable).
  GitVersion's silent miscount on shallow clones is the bug we most want to avoid.
- Submodule behaviour: `hanko --repo path` always operates on the named repo, never traverses submodules.
  Document.
- Worktree behaviour: support `git worktree`-style auxiliary worktrees.
- Long-tail git states: rebase in progress, bisect in progress, merge in progress — surface clearly rather than producing a confusing version.
- Cross-platform CI: matrix of `linux/x86_64`, `linux/arm64`, `darwin/arm64`, `windows/x86_64` for at least the smoke tests.
- Golangci-lint clean.
  Coverage > 70% on `internal/version`.

---

## M6 — v1.0.0

**Definition of done for v1:**

- All M0–M5 items shipped.
- Used in production CI by at least 3 internal repos.
- Output stability promise: SemVer fields and JSON shape are frozen.
  New fields are additive; renames / removals require a v2.
- `hanko version` cold-start latency < 100ms on a 10k-commit repo (M1 should already be close; M5 measures and protects).
- README has a 30-second quickstart and a side-by-side comparison with GitVersion.
- Tagged `v1.0.0`.
  Released as a static binary on GitHub Releases plus a Nix package in this flake.

---

## Out of scope (for v1)

These are tempting but should wait:

- **GUI / TUI.**
  This is a CI tool.
  No bubbletea unless a clear user need appears.
- **Built-in changelog generation.**
  That's a different tool — `git-cliff`, `release-please`, etc. — and overlapping with them would dilute scope.
- **Calendar versioning, monorepo-aware versioning, multi-package versioning.**
  Real demand; large design space.
  Probably v2.
- **Daemon mode / language servers.**
  No.

---

## Open questions (capture as we go)

- Do we want a JSON Schema for `.hanko.yaml`?
  Probably yes by M3.
- Should `hanko stamp docker` build the image, or only label an existing one?
  Leaning: only label, build is someone else's job.
- Behaviour on the very first commit (no parent, no tags) — return `0.1.0-rc.1`?
  Pick once we have a test for it in M1.
