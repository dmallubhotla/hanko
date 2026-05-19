# Design decisions log

Running list of design questions surfaced during implementation.
Each entry is either **decided** (with the chosen path and rationale) or **open** (waiting on a critical-mass review).
Add to this as you go; revisit collectively rather than litigating each one in isolation.

## Decided

- **D-001 — Detached HEAD at a tag emits the tag verbatim. No `--source` flag.**
  Canonical case: GHA `on: push: tags:` workflow checks out the tag ref, leaving HEAD detached.
  `version.Compute` now special-cases `Detached && LatestTag != "" && CommitsSinceTag == 0` and emits the tag's version exactly (preserving prerelease and build metadata; appending `.dirty` if dirty).
  Other detached states (random SHA, not at a tag) still get the honest `<base>-detached.<n>` fallback.
  **Why no flag.** Every other GHA trigger has a real branch; the tag-push case has a tag at HEAD; nothing else needed the override.

- **D-003 — Branch sanitisation rule.**
  Lowercase, replace runs of non-alphanumerics with a single `-`, trim leading / trailing `-`, empty fallback `"branch"`.
  Implemented in `version.sanitizeBranch`.
  Mirrors GitVersion well enough for tag-portable pre-release labels.

- **D-004 — Refuse on shallow clones. No opt-out flag.**
  `git rev-list --count` is wrong on shallow, so any computed version would be wrong; "warn but continue" was the worst of both options.
  `cmd/resolve.go` guards every command path with `ErrShallow` if `info.Shallow` is true.
  Error message points to `fetch-depth: 0` (CI) or `git fetch --unshallow` (local).

- **D-005 — Empty repo / no commits.**
  `gitinfo.Read` returns `ErrNoCommits` when a git repo exists but has no commits.
  Caller surfaces this via cobra error path.

- **D-006 — `--format gha` field names match cicd contract.**
  `full`, `major`, `minor`, `patch`, `major-minor`, `short-sha`, `is-prerelease`, `branch`.
  Lowercase-dashed for GHA; internal `Version` keeps `CamelCase`.
  **Frozen** — changing these breaks the cicd swap.

- **D-007 — No env auto-detection.**
  `--format gha` is the only path that emits GHA-shaped output.
  CLI behavior stays independent of environment.

- **D-008 — Tag fan-out lives in hanko.**
  `stamp docker tags` owns the `<full> / <major>.<minor> / <major> / :latest` policy.
  Resolved in M3; cicd's `compute-image-tags` composite becomes deletable.

- **D-009 — Hardcode main/master as the only mainline branches. No `--default-branch` flag.**
  Org policy: `main` is the canonical name; `master` is kept for legacy.
  Anything else (`develop`, `trunk`, …) is outside hanko's convention and gets feature-branch treatment.
  Repos that disagree should rename, or wait for `.hanko.yaml` to grow a `mainline-branches` key.

- **D-011 — `hanko tag` refuses computed pre-release versions. One narrow escape hatch: `--initial <version>` for the first release.**
  Pre-release versions live on feature / hotfix branches; the canonical release tag happens after merge to mainline.
  Refusing computed pre-releases eliminates the non-idempotent re-tag case (the second run would compute a different tag based on the first run's tag).
  **The exception**: a fresh repo with no tag has nowhere to bootstrap from — every commit computes a pre-release and `hanko tag` would loop on its own rule.
  `--initial <version>` resolves this: it takes the value **verbatim** (caller picks `v0.1.0` vs `0.1.0`, sidestepping D-002 for the bootstrap case), refuses if any semver-shaped tag already exists, and otherwise creates the tag and optionally pushes.
  After the bootstrap tag exists, normal computation takes over — `--initial` is unusable a second time.
  Anyone with a hard need to mark a hotfix iteration still uses `git tag` by hand.

- **D-002 — Tag prefix policy: follow the existing repo's convention.**
  Read side accepts both `v1.2.3` and `1.2.3` (via `v?` in the tag regex).
  Write side (`hanko tag`) detects the prefix from `info.LatestTag` and mirrors it: latest tag starts with `v` → new tag uses `v`; bare → bare.
  Bootstrap is handled by `--initial <version>` taking the value verbatim (see D-011).
  Mixed-shape repos get latest-wins, which is "weird but predictable" — mixed-shape repos should pick one shape.
  More exotic prefixes (`release-1.2.3`, `<pkg>-v1.2.3`) are out of scope until `.hanko.yaml` lands.

- **D-014 — Conventional Commits parsing lives inside hanko.**
  Hanko owns the bump *direction*, not just the magnitude (D-013).
  The natural input to that decision is the commit subjects between the latest tag and HEAD, which hanko already has access to via its `gitinfo` package.
  Delegating to `git-cliff` / `release-please` / `semantic-release` was considered and rejected: those tools generate changelogs and ship PRs (different jobs), each opinionated about its own templating, and forcing hanko users to run two tools to answer one identity query ("what does this commit call itself") is poor ergonomics.
  The parser is small (~50 LOC, one regex), well-defined by the Conventional Commits spec, and inlines into the same `Compute` call path that handles the rest of the version computation.
  Out of scope for v1: changelog generation, commit-message linting, scope-based bump overrides — all defer to specialised tools downstream of hanko.
  Manual override: `hanko version --bump <direction>` short-circuits the strategy for the case "I broke the API but my commits don't say so."

- **D-013 — Mainline bumps patch by 1, not by commit count.**
  GitVersion's `mode: ContinuousDeployment` heritage was `patch = base.patch + commits-since-tag`.
  Killed for several reasons:
  (1) the number is content-free — `1.2.3 → 1.2.8` doesn't mean "5 fixes shipped," it means "5 commits happened" (some likely refactors, docs, or features that should have been minor);
  (2) it contradicts SemVer's PATCH-is-for-bug-fixes contract;
  (3) the commit count already lives in build metadata (`+5.abc1234`), so `patch += n` is a redundant restatement;
  (4) it makes `hanko version` a state-machine step (version drifts forward as commits land) rather than the identity query we've designed for elsewhere;
  (5) forgetting to tag balloons the patch number to absurd values.
  Replaced with a one-time bump per `increment` rule (`patch` → +1), gated on `n > 0` for mainline so commits at the tag emit the tag verbatim.
  Pre-release branches were already bumping +1 — no change there.
  Counter remains visible in `FullSemVer`'s build metadata.
  Side effect: `mode: continuous-delivery` config key is removed; it had no effect, since the +n behaviour it named is gone.
  Future bump strategies (conventional commits, manual override) will choose the *direction* of the +1 bump, not its magnitude.

- **D-017 — Refuse on long-running git operations in progress.**
  `merge`, `rebase`, `cherry-pick`, `revert`, `bisect` mid-flight all leave HEAD in a transient state that doesn't represent a real release candidate.
  Continuing through any of them produces a confusing version — same bug class D-004 prevents for shallow clones.
  Detection is path-based: `MERGE_HEAD` / `rebase-merge/` / `rebase-apply/` / `CHERRY_PICK_HEAD` / `REVERT_HEAD` / `BISECT_LOG` inside the `.git` dir (worktree-aware via `git rev-parse --git-dir`).
  No config knob — refusing is the only honest answer.
  If a user genuinely needs to compute a version mid-operation, the right answer is `git <op> --abort` (or `--continue`), not a `--force` flag.

- **D-015 — `stamp nix` rewrites every `version = "X";` line sharing the current value.**
  Surfaced by kestrel (first real consumer): its `flake.nix` exposes both `kest` and `kestci` derivations, each a `buildGoApplication` with its own `version = "..."`.
  First-match-wins silently left the second derivation on the previous release's version.
  **Rule chosen**:
  - Find every `version = "X";` line in the file.
  - If all share the same value → rewrite all of them.
  - If any diverge → refuse, with an error pointing at the shared-`let` pattern.
  No nix AST parsing — the rule is purely line-based, matched on the current value.
  This handles both shapes naturally: hoisted-via-`let` (one match, the binding; the `inherit version` references need no rewriting) and per-derivation-inline (N matches with one value).
  **Known false-positive surface**: a genuinely unrelated `version = "X";` line (e.g., a vendored dependency override that happens to be pinned at the same version) gets rewritten too.
  Documented as an assumption in `docs/hanko-yaml.md`; the divergence-refusal protects against the obvious bad shape.
  Independent-versions case (one flake, distinct products) deferred — reopen when a second consumer needs it; config-driven `nix.version-attrs` is the obvious shape if/when that demand surfaces.

- **D-012 — `git describe` filters to semver-shaped tags via `--match` patterns.**
  Two patterns passed: `v[0-9]*.[0-9]*.[0-9]*` and `[0-9]*.[0-9]*.[0-9]*`.
  Non-semver marker tags like `release-frozen` are skipped at the source.
  If no semver-shaped tag is reachable, describe returns empty and hanko's existing "no-tag fallback" fires correctly.

## M3 implicit decisions worth flagging

- **Helm Chart.yaml edit is line-based, not yaml-roundtripped.**
  Mirror comments, key order, and incidental whitespace verbatim.
  Trade-off: `version` and `appVersion` must be top-level scalars on their own lines (Helm's canonical shape).
  Anything more elaborate gets a clear refusal rather than a guess.
  yaml.v3 was added to go.mod but the editor doesn't use it; we kept the dep because the helm subcommand may grow more checks.
  _Reconsider whether to drop yaml.v3 if it stays unused through M4._
- **`stamp docker labels --source` and `--title` are caller-supplied**, not auto-derived.
  We don't know the project's source URL or human-friendly title from git alone, and guessing produces garbage (`origin/.../...` vs `github.com/...` etc.).
  Callers either pass them or accept their absence in the labels.
- **`stamp go-ldflags` stamps three fixed vars** (`version`, `commit`, `date`) on a configurable package.
  Repeatable `--var name=value` is a candidate for later but YAGNI for now — three named fields cover the ~100% case.
- **Recurring test trap (worth its own callout):** `git describe` and `git rev-list --count <tag>..HEAD` are *reachable*-only.
  A feature branch sees its parent's commits in the count.
  Don't write test expectations that imagine branch-local commit counts; verify with `git rev-list --count` against the same setup.

## M2 implicit decisions worth flagging

- **Idempotency wins over dirty-refusal.**
  If the requested tag is already on HEAD, `hanko tag` exits 0 even with a dirty worktree.
  Rationale: the desired side effect already exists; refusing serves no one.
  Surfaced by smoke test `test/smoke/smoke.sh` early — left in deliberately.
- **`--push` pushes only the computed tag**, not all tags.
  cicd's `tag.yml` uses `git push --tags` (push everything) for simplicity.
  Hanko's scoped push is strictly safer; flag this as a behaviour diff when migrating that workflow.
- **Annotated tags, not lightweight.**
  cicd's `tag.yml` uses `git tag $NEW` (lightweight) for historical reasons.
  Annotated tags carry author / date / message — strictly better for release artifacts.
  Migration users will see a one-time difference if they query tag metadata.

## M1 implicit decisions worth flagging

- **No-tag base is always a pre-release**, even on mainline.
  Rationale: surface "you forgot to tag" loudly.
  Output: `0.1.0-main.<n>+...`.
  Open to reversing if the noise is unwanted.
- **Hotfix policy bumps patch by 1 unconditionally**, ignoring commit count.
  ROADMAP wording was `<major>.<minor>.<patch+1>-hotfix.<n>` so `<n>` is the pre-release counter, not the patch bump.
  Confirm matches intent.
- **Dirty appends to build-metadata** (`+0.abc1234.dirty`) rather than pre-release.
  Build metadata doesn't affect semver ordering — feels right.

## Open

### Workflow shape on ephemeral self-hosted runners

- **D-010 — Re-derive in each job, don't thread outputs across jobs.**
  GitVersion's typical pattern is "compute once, pipe `outputs.semver` through `needs.*` to every downstream job", because invocation is expensive (.NET startup + tool install per job).
  Hanko inverts this: invocation is ~10ms, so every job that needs the version just runs `hanko version` again from its own checkout.
  Benefits:
  (a) no string-quoting hazards across job boundaries,
  (b) each job is independently reproducible from git state,
  (c) the runner can be wiped between jobs and nothing is lost.
  Where job-splitting is still appropriate: **by permission boundary**, not by intermediate value.
  Document in `examples/migrations/`.
  _Not strictly a "decision" — more an articulated principle that future workflow design should respect._

### Future / parking lot

- **Stamp-without-pre-existing-tag — revisit.**
  User flagged discomfort with `stamp helm` happily writing a prerelease version (e.g. `1.0.0-feature-foo.3`) to Chart.yaml when no tag has been created for that version.
  Today this is intentional: the CI flow is stamp-first then tag-last, so stamp can't require a pre-existing tag.
  But there may be a future "release pipeline" mode where stamp commands opt into requiring HEAD to be at a release tag.
  Park here; revisit if a concrete use case shows up.

### Edge cases worth covering in flows / smoke

- Branch name with slashes, uppercase, underscores. *(covered: S4 sanitisation cases)*
- Tag with extra suffix (`v1.2.3-rc.1`). *(covered: S5 prerelease tag-push)*
- Tag with build metadata (`v1.2.3+something`). *(parser handles it; no flow case yet)*
- Multiple tags on same commit. *(no case yet)*
- Tag with no `v` prefix (`1.2.3`). *(covered: S4 mixed formats)*
