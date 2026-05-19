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
- [~] Bump-direction hints from commit-message convention (Conventional Commits parser).
  Moved to its own milestone, **M5e — Bump strategies**, now that `.hanko.yaml` is wired in M5a.
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

## M5 — Sealing: declarative stamps + release orchestration

**Goal:** turn release-cutting into one hanko invocation.
A repo declares its source-side stamp targets and release hooks in
`.hanko.yaml`; `hanko seal` runs the whole rite (stamp → hooks → commit →
tag → push) atomically.
See `docs/hanko-yaml.md` for the schema sketch.

This is the milestone that turns hanko from "version+stamper primitives"
into "release tool" without taking on changelog generation or other
adjacent scope.

### M5a — `.hanko.yaml` loader

- [ ] Parse the version-computation sections (existing sketch: `tag-prefix`, `mode`, `dirty-suffix`, `initial-version`, `on-shallow`, `branches`).
- [ ] Wire those into the existing `version.Compute` path; today's hard-coded defaults stay as the no-config fallback.
- [ ] Schema validation at startup; clear error pointing at the offending key.
- [ ] JSON Schema (generated from the Go struct) for editor completion.

### M5b — `stamp-targets:` + generic stamp engine

- [x] Per-format line-based stampers behind a unified engine (`internal/stamper`): `toml`, `yaml`, `json`, `nix`, `plain`.
  Reuses the existing helm/nix line-pattern approach; canonical "key on its own line, scalar value" is the supported shape.
- [x] Nested keys via dotted path (`project.version` for `pyproject.toml`'s `[project]` section, etc.) — TOML engine is section-aware.
- [x] List form for multi-key targets — schema uses a separate `keys:` field (singular `key:` is shorthand for the one-key case).
- [x] `hanko stamp` (no args) reads `stamp-targets:` and applies all targets.
  `stamp helm` / `stamp nix` remain as positional-arg shorthand for one-off use.
- [x] `hanko stamp --dry-run` reads the config and emits per-target before/after diffs.
- [x] `hanko seal` auto-runs the declarative stamp pass before its `pre-commit:` hooks.

### M5c — `hanko seal`

- [x] Implements the pipeline: pre-flight refusal (dirty / prerelease), run `pre-commit:` hooks, single commit, tag, push.
  (Declarative stamping defers to M5b; users invoke per-format stampers as `pre-commit:` shell hooks for now.)
- [x] Template variables (`{semver}`, `{full}`, `{major}`, `{minor}`, `{patch}`, `{major-minor}`, `{short-sha}`, `{is-prerelease}`, `{branch}`) expanded in `commit-message:` and hook command strings.
- [x] Pre-release refusal mirrors D-011; opt out via `seal.refuse-prerelease: false`.
- [x] `hanko seal --dry-run` walks the pipeline without mutating: shows the version, branch, target tag, commit message, and the template-expanded hook commands.
- [x] Smoke coverage: dry-run, happy path (local, no push), refuses dirty, refuses pre-release by default.
- [ ] Once M5b lands, seal will run the declarative `hanko stamp` pass before the `pre-commit:` hooks — no schema change, just one extra step in the pipeline.

### M5e — Bump strategies

Today every bump is "+1 in the direction the branch's `increment` field names" (D-013).
That's the *fixed* strategy.
Other strategies decide the *direction* of the bump from commit content rather than from branch config alone.

- [x] Schema: top-level `bump-strategy:` with values `fixed` (default) and `conventional-commits` (parse commit messages between latest tag and HEAD).
- [x] **Conventional Commits parser** (`internal/bump`).
  Implements the Conventional Commits spec:
  - `feat!:` / `<type>(scope)!:` / `BREAKING CHANGE:` in body → `major`
  - `feat:` → `minor`
  - `fix:` → `patch`
  - other (`chore:`, `docs:`, `refactor:`, `test:`, `style:`, `perf:`, etc.) → contributes nothing on its own
  - no matching commits → fall back to the branch's declared `increment`
  Hanko owns this; the parser is ~50 LOC + one regex. See D-014.
- [x] **`hanko version` consumes the strategy.**
- [x] **`hanko tag` respects it** (it calls `version.Compute` internally).
- [x] **Manual override flag.** `hanko version --bump {patch,minor,major,none}` short-circuits the strategy for one invocation.
- [ ] **`hanko seal` will respect it** — wired automatically when seal lands in M5c.
- [ ] **Per-branch override** (deferred — `branches[].bump-strategy: fixed` to let a hotfix branch ignore commit-message hints).
  Cheap to add later when there's demand.
- [x] Tests: parser edge cases (scoped types, footer `BREAKING CHANGE`, uppercase, empty), strategy precedence, manual override, smoke coverage on a real repo.

Why hanko owns this rather than relegating to `git-cliff` or `release-please`:
those tools generate changelogs and ship PRs, which is a different job.
Hanko's job is "tell me what this commit calls itself" — the bump direction is part of that identity.
Asking the user to run two tools to answer one question is poor ergonomics.

**Exit criteria:** a repo with `bump-strategy: conventional-commits` and a mix of `feat:` and `fix:` commits computes the right next version without any human-supplied hint.

### M5d — Out of scope for v1 (capture, defer)

- AST-based round-trip mutation per format (would replace the line-based engine if a real file shape forces it).
- Hook stdout capture/log forwarding policy beyond "forward verbatim."
- A `--target NAME` selector for stamping one declared target.
  Needs a naming convention; revisit when there's demand.
- Presets (`preset: gitversion-compat`, etc.).

**Exit criteria:** at least one downstream service repo defines `.hanko.yaml`, runs `hanko seal` from a `workflow_dispatch` GH Actions job, and the resulting commit + tag + pushed-artifact set is what a human would have produced manually.

---

## M6 — Hardening

**Goal:** stop hand-waving the edge cases.

- [x] Shallow-clone detection (D-004). `on-shallow: refuse|warn|ignore` config knob lives in `.hanko.yaml`.
- [x] Submodule behaviour documented: `--repo` operates only on the named repo, no recursion. Smoke-tested with a nested git repo whose tags must not leak to the parent.
- [x] Worktree behaviour: linked worktrees (`git worktree add`) work transparently because `git rev-parse --git-dir` returns the per-worktree git dir. Smoke-tested including mid-operation refusal in a linked worktree.
- [x] Long-tail git states: rebase / bisect / merge / cherry-pick / revert in progress all refused with a clear error pointing at `git <op> --abort` (D-017).
- Cross-platform CI: matrix of `linux/x86_64`, `linux/arm64`, `darwin/arm64`, `windows/x86_64` for at least the smoke tests.
- [x] Golangci-lint clean (0 issues across `./...`); wired as `checks.golangci-lint` so it runs under `nix flake check`.
- [x] Coverage > 70% on `internal/version` (currently 82.4%); every `internal/*` package above 70% except `internal/logging` (small, 0%).
- **Smoke-test reorg.**
  `test/smoke/smoke.sh` is one ~700-line shell file with `section "…"` headers.
  Approaching the point where it's painful to navigate.
  Migrate to `go test ./test/smoke/...` — each section becomes a `Test*` function, fixtures shared via `t.Run` subtests, `internal/testrepo` already has the git scaffolding.
  Wins: parallel runs, structured output, single test idiom (no more "go test for units, bash for smoke"), faster (no per-section binary build).
  Cost: ~200 LOC of `exec.Cmd` harness + mechanical migration of existing assertions.
  Defer until smoke crosses ~150 assertions or starts flaking; current count is 80.

---

## M7 — v1.0.0

**Definition of done for v1:**

- All M0–M6 items shipped.
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
