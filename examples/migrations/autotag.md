# Migration: `autotag.yml` + `tag.yml`

Replaces the existing two-workflow autotag pattern with one workflow, two jobs, no inter-job string threading.

## Before — current cicd shape

`autotag.yml` (orchestrator):

```yaml
name: AutoTag
on:
  push: { branches: [master] }
jobs:
  gitversion:
    uses: ./.github/workflows/gitversion.yml
  create-tag:
    uses: ./.github/workflows/tag.yml
    needs: [gitversion]
    permissions: { contents: write }
    with:
      name: ${{ needs.gitversion.outputs.fullSemVer }}
```

`gitversion.yml`:

```yaml
jobs:
  gitversion:
    runs-on: ci-gha-general-runners
    outputs:
      fullSemVer: ${{ steps.buildstring.outputs.fullSemVer }}
    steps:
      - uses: actions/checkout@v6
        with: { fetch-depth: 0 }
      - uses: gwre-hazardhub/cicd/.github/actions/resolve-version@v1
        id: version
      - id: buildstring
        run: |
          VERSION="v${{ steps.version.outputs.full }}"
          echo "fullSemVer=$VERSION" >> "$GITHUB_OUTPUT"
```

`tag.yml`:

```yaml
jobs:
  create-tag:
    runs-on: ci-gha-general-runners
    permissions: { contents: write }
    outputs:
      tag: ${{ steps.create-tag.outputs.tag-name }}
    steps:
      - uses: actions/checkout@v6
      - id: create-tag
        run: |
          NEW_TAG="${{ inputs.name }}"
          if git show-ref --tags "$NEW_TAG" --quiet; then
            if ! git tag --points-at HEAD | grep "$NEW_TAG"; then
              echo "Tag '$NEW_TAG' is already in use and does not point at HEAD! Aborting"
              exit 1
            fi
            echo "Tag '$NEW_TAG' already exists, perhaps from an earlier run; skipping."
          else
            git tag "$NEW_TAG"
            git push --tags
          fi
          echo "tag=$NEW_TAG" >> "$GITHUB_OUTPUT"
```

## After — hanko-based

```yaml
name: AutoTag
on:
  push: { branches: [master] }

jobs:
  tag:
    runs-on: ci-gha-general-runners
    permissions:
      contents: write       # the only reason this is its own job
    steps:
      - uses: actions/checkout@v6
        with: { fetch-depth: 0 }
      - run: hanko tag --push
```

That's the whole thing.
One job, two steps.

## What changed in shape

- **No `gitversion.yml` job.**
  The version is computed inline by `hanko tag` from the job's own checkout.
  There's no `outputs.fullSemVer` to thread because nothing downstream consumes it as a string.
- **No `tag.yml` reusable workflow.**
  The idempotency dance that the bash script did (`show-ref` + `points-at HEAD`) is built into `hanko tag`, with the same semantics: tag already at HEAD → exit 0 silently; tag exists elsewhere → fail loudly.
- **Lightweight → annotated tags.**
  `hanko tag` creates annotated tags with a default `Release v<semver>` message.
  One-time observable difference: `git for-each-ref --format='%(objecttype)' refs/tags/...` flips from `commit` (lightweight) to `tag` (annotated).
  Strictly more metadata; nothing breaks.
- **Scoped push.**
  `hanko tag --push` pushes only the new tag.
  `tag.yml` does `git push --tags` which pushes every tag in the repo.
  In practice they're equivalent on a single-tag-per-commit workflow, but scoped is safer when the local repo has stray tags (e.g. from a previous WIP run).

## Why the version job goes away

The original split into `gitversion` + `create-tag` jobs was driven by two forces:

1. **Permission scoping** — `create-tag` needs `contents: write`, `gitversion` doesn't.
   The principle of least privilege says split.
2. **Cost of GitVersion** — running it twice (once to compute, once to tag) was expensive enough that you'd build a passing-string mechanism.

With hanko, only force (1) survives.
Computing the version twice costs nothing, so there's no reason to thread `fullSemVer` across a job boundary.
**One job at the privileged tier is the right shape**, not two jobs at different tiers connected by a string output.

This is the general pattern from [D-010](../../docs/design-decisions.md#d-010): split by permission, not by value.

## What's the same (or slightly worse)

- **Still needs `fetch-depth: 0`.**
  Tag computation needs full history; shallow clones miscount commits-since-tag.
  Same as today.
- **`workflow_call`/`workflow_dispatch` entry points lost.**
  The original `tag.yml` could be `uses:`'d from other workflows or run manually with an explicit tag name.
  If callers depend on that, the migration needs to either reintroduce a reusable wrapper or document `hanko tag --message <m>` as the direct equivalent.
  Trivial; just not done in this sketch.

## Edges worth checking before rollout

- **Permission propagation through composite actions.**
  This migration works because `hanko tag --push` uses `GITHUB_TOKEN` from the job's env by default (git's standard auth path).
  Confirm that `GITHUB_TOKEN` is exposed to step env on the self-hosted runners — should be, but the default GHA model relies on it.
- **Concurrent autotag runs.**
  Two pushes to master in quick succession could both trigger autotag; the second one would compute the same version as the first (if no new commits) or a different version (if there are new commits).
  `hanko tag` is idempotent on the no-new-commits case; on the new-commits case both runs would succeed with different tags, which is correct.
  Confirm the workflow's concurrency settings (or lack thereof) match expectations.
