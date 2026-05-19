# `.hanko.yaml` — sketch

**Status:** design proposal, not implemented.
Lives here so we can pressure-test it against real repo needs before writing the loader.
When implemented, this file becomes the schema reference.

## Where it lives

A single optional file at the repo root: `.hanko.yaml`.
Absent → hanko uses the M1 hard-coded defaults (see `internal/version/version.go`).
Present → parsed once at startup, overrides defaults field-by-field.

Search order: `--repo` path → walk up to git root → look for `.hanko.yaml`.
Don't traverse beyond the repo.
Don't read `$HOME` configs — hanko is a project-scoped tool.

## Design principles

1. **Every key has a hard-coded default.**
   Empty file is valid; whole sections may be omitted.
2. **GitVersion field names where they fit.**
   Easier porting; the ROADMAP.md compatibility promise.
3. **No turing-completeness.**
   No scripting, no `!include`, no env-var interpolation.
   If the value depends on the environment, it's a CLI flag, not a config key.
4. **Branch policies match in declaration order**, first match wins.
   Lets repos shadow defaults without re-declaring every branch type.

## Schema sketch

```yaml
# .hanko.yaml — all keys optional, shown with their hard-coded defaults.

# Regex applied when parsing existing tags into a semver base.
# Tag prefix stripping happens via this regex's first capture group.
# If no capture group, the whole match is the version.
# (Resolves design-decisions.md D-002.)
tag-prefix: "^v?(.+)$"

# How the bump direction is chosen on a tagged repo. (D-013: magnitude is
# always +1; only the direction is configurable.)
#   conventional-commits — parse `<latest-tag>..HEAD` subjects; pick the
#                          strongest signal (feat!:/BREAKING CHANGE → major,
#                          feat: → minor, fix: → patch). If no commit
#                          contributes a signal, fall back to the branch's
#                          `increment`. Default (D-016) — a repo that
#                          doesn't follow the convention gets unchanged
#                          behaviour via the fall-back.
#   fixed                — skip the parser entirely; use each branch's
#                          declared `increment`. Set this if commit messages
#                          shouldn't influence the bump.
# A `hanko version --bump <direction>` flag short-circuits the strategy for
# one invocation — useful for "I broke the API but my commits don't say so".
# Branches can override: `branches[].bump-strategy: fixed` keeps a hotfix
# rule patch-only even when mainline reads conventional commits.
bump-strategy: conventional-commits

# Whether a dirty worktree appends `.dirty` to build metadata.
# Off → dirty is silently ignored in version output (still surfaced as a warning on stderr).
# Useful for ephemeral CI where the user knows their workspace is dirty but doesn't want it polluting tags.
dirty-suffix: true

# Tag-on-no-tag fallback.
# When the repo has no reachable tag, hanko uses this as the base version.
# Always emits a pre-release suffix.
initial-version: "0.1.0"

# Behaviour on shallow clone.
# One of:
#   refuse — exit non-zero (recommended; see D-004)
#   warn   — print warning to stderr, continue
#   ignore — silent
# When shipping, we'll lean toward `refuse` to keep faith with the "honest about what it sees" principle.
on-shallow: warn

# Branch policy.
# Evaluated in order; first match wins.
# `regex` is required.
# Other keys override the matched branch's behaviour.
# `increment` may be: patch, minor, major, none.
# `label` is the pre-release label template. `{branch}` expands to the sanitised branch name.
branches:
  - name: mainline
    regex: '^(main|master)$'
    is-mainline: true
    increment: patch
    label: ""              # empty → no pre-release suffix

  - name: release
    regex: '^release/(\d+)\.(\d+)$'
    is-mainline: true
    increment: patch
    label: ""
    # Capture groups from `regex` set major / minor:
    major-from: 1
    minor-from: 2

  - name: hotfix
    regex: '^hotfix/.*$'
    increment: patch       # bumps patch by one BEFORE applying label/n
    label: "hotfix"

  - name: pull-request
    regex: '^(refs/)?pull/(\d+)/'
    increment: none
    label: "pr-{2}"        # `{2}` references the second capture group

  - name: feature
    regex: '.*'            # catch-all
    increment: none
    label: "{branch}"
```

## Stamp targets (release-time source mutation)

Hanko-managed apps usually have one or more files that record the current
version inside the repo: `pyproject.toml`, `Cargo.toml`, `package.json`,
`Chart.yaml`, `flake.nix`, plain `VERSION` files.
At release time, those files get bumped in lock-step with the new git tag.

The `stamp-targets:` section declares which files to mutate.
Each target is `(path, format, key)`.
A single `hanko stamp` invocation walks the list and applies all of them.

```yaml
stamp-targets:
  - path: pyproject.toml
    format: toml
    key: project.version

  - path: Chart.yaml
    format: yaml
    keys: [version, appVersion]   # multiple keys → use `keys:` (list form)

  - path: package.json
    format: json
    key: version

  - path: flake.nix
    format: nix
    key: version

  - path: VERSION
    format: plain    # whole file is the value; no key needed
```

`key:` is the singular form (one key) and `keys:` is the list form (when several attrs in the same file get the same value, like Chart.yaml's `version` + `appVersion`).
A target must set exactly one of them (except `format: plain`, which ignores both).

### Engine choice — line-based first

Hanko stamps line-based, not via AST round-trip.
This is the same strategy used today for `Chart.yaml` and (just landed)
`flake.nix`: a regex finds the `<key> = "<val>"` line, the value is
substituted, comments and ordering survive untouched.
The cost is strictness — files must place the targeted key on its own line
in a canonical shape.
That's nearly universal in formatter-output configs (`pyproject.toml` from
poetry/hatch, `Cargo.toml` from cargo, `package.json` from npm), so the loss
is small and the implementation stays trivial.

AST-based engines per format are a follow-up if a real file shape forces it.
Concretely, each format gets one line-pattern + one comment-handling rule;
that's it.

### Field paths

`key` (or `keys`) is a dotted path into the file's structure.
For top-level scalars (`version` in `package.json`) the path is a single
segment.
For nested keys (`project.version` in `pyproject.toml`) the path walks the
section headers — currently only TOML's bracketed sections are honoured;
YAML/JSON nesting is on the deferred list.
A `keys:` list means "stamp all of these to the same new value" — useful
for Chart.yaml where both `version` and `appVersion` conventionally hold
the same semver.

**Coincidental-match assumption** (D-015): when several `version = "X"`
lines in the same file share the same value, the engine assumes they all
refer to the same release. False positives (e.g. a vendored override
pinned at the same string) get rewritten too. Refuse-on-divergence catches
the obvious bad shape; document this when adopting.

### What gets stamped

The new value is `Version.SemVer` (e.g. `1.2.3`, or `1.2.3-feature-foo.4` on
a feature branch).
**Pre-release versions stamp identically to release versions** — `stamp` is
an identity-application step, not a release decision; the release decision
is the orchestrating `seal` command (next section), which can refuse
pre-releases by default.

## Sealing — orchestrated releases

`hanko seal` is the higher-level rite: stamp targets, run user-declared
hooks (changelog generation, lockfile regen, anything that produces files
to fold into the release commit), commit, tag, push.
Atomic on success; the worktree is clean again at the end (or stayed clean,
if there was nothing to do).

```yaml
seal:
  # commands run after stamping, before the release commit. CWD is repo root.
  # Each command runs in sequence; non-zero exit aborts the seal.
  pre-commit:
    - git-cliff --tag v{semver} --output CHANGELOG.md
    - poetry lock --no-update

  # commit-message template. `{...}` interpolates fields from the computed Version.
  # Default: "chore: Release {semver}". The `chore:` prefix keeps the
  # release commit classified as no-bump under the conventional-commits
  # strategy — defensive, even though the release commit is behind the tag
  # and the next `<tag>..HEAD` range won't see it.
  commit-message: "chore: Release {semver}"

  # remote to push commit + tag to. Same as `hanko tag --remote`.
  push-remote: origin

  # whether `seal` will refuse to operate on a pre-release version.
  # Mirrors D-011 (`hanko tag` refuses prereleases unconditionally);
  # set to `false` if a repo really wants to seal hotfix prereleases.
  refuse-prerelease: true
```

### Template variables

`{...}` placeholders expand from the computed `Version` struct, mirroring
the field set already exposed by `hanko version --format env`:

| Placeholder       | Maps to                |
| ----------------- | ---------------------- |
| `{semver}`        | `Version.SemVer`       |
| `{full}`          | `Version.FullSemVer`   |
| `{major}`         | `Version.Major`        |
| `{minor}`         | `Version.Minor`        |
| `{patch}`         | `Version.Patch`        |
| `{major-minor}`   | `"{major}.{minor}"`    |
| `{branch}`        | `Version.BranchName`   |
| `{short-sha}`     | `Version.ShortSha`     |
| `{is-prerelease}` | `Version.IsPreRelease` |

Available anywhere strings appear in `seal:` — commit-message,
pre-commit command args.

### What seal does, in order

1. **Pre-flight**.
   Refuse if the worktree is dirty (the seal is meant to be atomic; pre-existing dirt would get folded into the release commit).
   Refuse if `refuse-prerelease: true` and the computed version is a pre-release.
2. **Stamp** all declared targets.
3. **Run hooks** from `pre-commit:` in declared order, in the repo root.
   Each command sees the post-stamp worktree.
   Any stdout is forwarded.
4. **Commit** everything in the working tree (the union of stamp mutations + hook outputs).
   Single commit, message from `commit-message:`.
5. **Tag** via the same logic as `hanko tag` (annotated tag, prefix follows D-002).
6. **Push** the commit + tag to `push-remote:`.

Failures at any step leave the worktree as-is so the user can inspect.
No partial commit, no partial push.

### Why not bake `git-cliff` (or similar) in?

Hanko owns version computation and stamping.
Changelog generation is a different tool with its own opinions
(conventional-commits parsing, templating, grouping).
Folding it in dilutes scope and forces every hanko user to live with one
project's choice of changelog style.
The hook mechanism lets users compose whichever tool they want without
hanko taking sides.

## Worked examples

- Repo has a `.hanko.yaml` that drops `release/x.y` mapping → the catch-all `feature` rule handles all non-main branches uniformly.
  Useful for trunk-based teams.
- Repo with `tag-prefix: '^release-(.+)$'` consumes existing `release-1.2.3` tags without renaming them.
- Repo with `dirty-suffix: false` and `on-shallow: refuse` is the "strict CI" preset.
  Likely the default in our `actions-runner-image`.

### Sealing examples

- **Python service.**
  `stamp-targets:` lists `pyproject.toml` (toml, `project.version`) and `Chart.yaml` (yaml, `[version, appVersion]`).
  `seal.pre-commit` runs `poetry lock --no-update` and `git-cliff --tag v{semver} --output CHANGELOG.md`.
  One `hanko seal` invocation produces a single release commit + tag with the bumped manifests, regenerated lockfile, and a fresh changelog entry.
- **Helm-only chart repo.**
  `stamp-targets:` lists just `Chart.yaml`.
  No `seal.pre-commit` hooks.
  `hanko seal` reduces to "bump Chart.yaml, commit, tag, push" — same as today's manual flow.
- **CI-driven release.**
  A `workflow_dispatch` GH Actions job runs `hanko seal`.
  Hanko handles the full sequence; the workflow's only job is "have credentials and call hanko."
  Pre-existing dirt aborts the workflow before any mutation.

## Open questions before implementation

- **Should we ship presets** (`preset: strict-ci`, `preset: gitversion-compat`) that expand into a full config?
  Reduces boilerplate, costs explainability.
- **JSON Schema** for editor completion.
  Probably yes; generate from a single Go struct so the loader and schema can't drift.
- **Validation timing.**
  Lint at startup vs lazy-validate on first branch-match?
  Startup wins for fail-fast; pay the parse cost regardless.
- **Per-branch tag prefix?**
  Some teams use `release-x.y.z` for pre-releases and `v.x.y.z` for releases.
  Probably YAGNI for v1.
- **Stamp-target dry-run UX.**
  Should `hanko stamp --dry-run` (no args, reads config) emit a per-target diff like the existing per-format `--dry-run`?
  Almost certainly yes; the noise is fine because the dry-run is opt-in.
- **What does `hanko stamp <name>` do?**
  Looks up a single declared target by `path` or a future `name:` field?
  Useful for "rebuild just Chart.yaml" flows; needs naming policy.
- **Where do hook stdouts go?**
  Forward verbatim, capture to log, or quiet-by-default with `--verbose`?
  Leaning forward verbatim since seal is already an explicit user act.
- **Hook failure recovery.**
  If a hook fails partway through, the worktree is left mid-mutation.
  `hanko seal --abort` to revert? Or trust the user with `git checkout -- .`?
  Leaning trust-the-user; an `--abort` is documentation more than logic.
- **`stamp-targets` discovery for repos without `.hanko.yaml`.**
  Today's `stamp helm <chart-dir>` and `stamp nix <file>` accept positional paths.
  Probably keep them as shorthand even after config-driven `stamp` lands; the positional form is "I know what I want, just do it."

## Cross-references

- Resolves `docs/design-decisions.md`: partially **D-003** (sanitisation lives in code, but per-branch label templates extend it), **D-004** (shallow-clone behaviour becomes config-driven).
  D-002 (tag prefix) was resolved without `.hanko.yaml` — follow-existing-repo-shape covers the common case; per-branch tag prefixes remain an open question in this file.
- Extends **D-011** semantics (`hanko tag` refuses computed prereleases): `hanko seal` defaults to refusing prereleases too, with `seal.refuse-prerelease: false` as the escape hatch (same shape as `--initial` is for `hanko tag`).
- Keeps **D-001** out: the `--source` flag for detached HEAD stays a CLI flag, not config — it's a per-invocation concern, not a repo-level policy.
