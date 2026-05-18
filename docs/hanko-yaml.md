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

# Versioning mode.
# Two values:
#   continuous-delivery — pre-release labels on non-mainline branches (current M1 behaviour)
#   mainline            — mirrors GitVersion's `mode: mainline`: every commit on a mainline
#                         branch bumps patch; off-mainline branches still get pre-release labels.
mode: continuous-delivery

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

## Worked examples

- Repo has a `.hanko.yaml` that drops `release/x.y` mapping → the catch-all `feature` rule handles all non-main branches uniformly.
  Useful for trunk-based teams.
- Repo with `tag-prefix: '^release-(.+)$'` consumes existing `release-1.2.3` tags without renaming them.
- Repo with `dirty-suffix: false` and `on-shallow: refuse` is the "strict CI" preset.
  Likely the default in our `actions-runner-image`.

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

## Cross-references

- Resolves `docs/design-decisions.md`: **D-002** (tag prefix), partially **D-003** (sanitisation lives in code, but per-branch label templates extend it), **D-004** (shallow-clone behaviour becomes config-driven).
- Keeps **D-001** out: the `--source` flag for detached HEAD stays a CLI flag, not config — it's a per-invocation concern, not a repo-level policy.
