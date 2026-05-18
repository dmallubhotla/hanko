# Migrations — gitversion to hanko

Concrete before/after examples for the cicd shared workflows.
Each migration is paired with a "what got better and why" callout so we can pressure-test whether hanko actually earns its slot before pushing it into production.

## What we're optimising for

Self-hosted runners on this org are K8s pods: ephemeral, no shared state between jobs, vfs storage, no Marketplace actions.
That constraint changes what "good CI ergonomics" means.
Specifically:

1. **Make each job a self-contained unit.**
   A job that needs the version should compute it from its own checkout, not consume a string piped from an earlier job.
   This is the inverse of GitVersion's typical "compute once, thread through `needs.*.outputs`" pattern — see [D-010](../../docs/design-decisions.md#d-010).
2. **Collapse the version-derivation surface.**
   GitVersion-based pipelines accumulate satellite tools: shell glue that re-parses semver, composite actions that fan out image tags, sed scripts that edit Chart.yaml.
   Hanko absorbs all of these.
   Fewer places where a version can drift.
3. **Split jobs by permission boundary, not by intermediate value.**
   A workflow that builds an image then tags HEAD belongs in two jobs only because `contents: write` is needed for one and not the other.
   The two jobs both call `hanko` directly; they don't pass version strings between themselves.
4. **No 3rd-party actions.**
   First-party (`actions/checkout`) only.
   Hanko is on the runner via `actions-runner-image`; no Marketplace install steps.
5. **Reproducibility.**
   Commit date (not wall-clock) for OCI `created` labels and ldflags `date`.
   Same commit → byte-identical artifact.

## Why "re-derive instead of thread" works with hanko but not GitVersion

| Cost  | GitVersion | hanko |
|-------|------------|-------|
| Tool install on a fresh pod | 30–60s (.NET SDK + tool, via `gittools/actions/setup`) | 0s (already on runner image) |
| Single invocation | ~3–6s | ~10ms |
| Output marshalling | Multiple `outputs.*`, structured carefully | None — just rerun |

When invocation cost is sub-second, re-derivation is *cheaper* than the cost of carefully threading string outputs across job boundaries (which carries yaml-quoting hazards every time).
With GitVersion you save the install cost by computing once and reusing; with hanko you don't have an install cost to save.

## Migrations in this folder

| Workflow | File | What collapses |
|----------|------|----------------|
| `cicd/.github/workflows/build-package.yml` | [build-package.md](./build-package.md) | 3 composite actions (`resolve-version`, `compute-image-tags`, `podman-publish`) → ~5 shell steps in one job |
| `cicd/.github/workflows/autotag.yml` + `tag.yml` | [autotag.md](./autotag.md) | 2 reusable workflows + 20 lines of bash → 2 jobs, ~2 shell commands |

## Known limitations to surface in any migration PR

- **Default branch detection.**
  `hanko stamp docker tags` currently hardcodes `main`/`master` as mainline.
  Repos with `develop` or another default branch will not emit `:latest`.
  [D-009](../../docs/design-decisions.md#d-009) tracks the fix; until then, callers can `--extra latest` conditionally.
- **Detached HEAD on tag-push events.**
  `github.ref` is the tag, not the branch, so hanko falls back to a `"detached"` sentinel.
  Callers should pass `--source <branch>` when running on a tag event.
  [D-001](../../docs/design-decisions.md#d-001).
- **Shallow clones.**
  Hanko silently miscount-able on shallow today.
  [D-004](../../docs/design-decisions.md#d-004) — until resolved, every caller workflow must `fetch-depth: 0`.
- **Configurability.**
  `.hanko.yaml` is sketched but not implemented.
  Repos that diverge from the hardcoded branch policy (mainline / release/x.y / hotfix/* / feature) get the policy that's in `internal/version/version.go` and nothing else for now.
