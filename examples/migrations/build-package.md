# Migration: `build-package.yml`

Replaces three composite actions (`resolve-version`, `compute-image-tags`, `podman-publish`) with hanko + direct podman invocation.
Net: one job, ~5 shell steps, no composite actions consumed.

## Before — current cicd shape

```yaml
# Caller workflow in some repo:
jobs:
  build:
    uses: gwre-hazardhub/cicd/.github/workflows/build-package.yml@v1
    with:
      image-name: example/demo
      context: .
      dockerfile: Dockerfile
      build-args: |
        VERSION=foo
        BUILD_NUMBER=42
```

The reusable workflow itself runs `actions/checkout`, then sequentially:

1. `gwre-hazardhub/cicd/.github/actions/resolve-version@v1`
   wraps `gittools/actions/setup@v1.1.0` (~30–60s on a cold runner) + `gittools/actions/execute@v1.1.0` (~3s) + shell glue that maps gitversion's CamelCase outputs onto our `full / major / minor / ...` schema.
2. `gwre-hazardhub/cicd/.github/actions/compute-image-tags@v1`
   is a shell script that consumes 11 inputs and decides which tags to fan out to (`<full>`, `<major>.<minor>`, `<major>`, `:latest`, `<branch>-<sha>`, plus `extra-tags`).
3. `gwre-hazardhub/cicd/.github/actions/podman-publish@v1`
   does `podman login` + `podman build` with `--label` + `--build-arg` assembly from newline-separated inputs + `podman push` per tag.

## After — hanko-based

```yaml
name: Build and push image
on: [push]

jobs:
  build:
    runs-on: ci-gha-general-runners
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v6
        with: { fetch-depth: 0 }

      - name: Compute tags and labels
        env:
          IMAGE: ghcr.io/${{ github.repository }}
          DEFAULT_BRANCH: ${{ github.event.repository.default_branch }}
        run: |
          set -euo pipefail
          # Tag fan-out — main/master is hardcoded for now (D-009); for repos with another default branch, append :latest manually.
          hanko stamp docker tags "$IMAGE" > tags.txt

          # OCI labels.
          # --source is the repo URL; --title is human-friendly.
          hanko stamp docker labels \
            --output file --file labels.txt \
            --source "${{ github.server_url }}/${{ github.repository }}" \
            --title "${{ github.event.repository.name }}"

          # Any caller-supplied extra labels go in this file too:
          # printf '%s\n' "extra.key=value" >> labels.txt

      - name: Login to ghcr.io
        run: echo "${{ secrets.GITHUB_TOKEN }}" | podman login ghcr.io -u "${{ github.actor }}" --password-stdin

      - name: Build
        env:
          BUILD_ARGS: |
            VERSION=foo
            BUILD_NUMBER=42
        run: |
          set -euo pipefail
          tflags=$(awk '{printf " -t %s", $0}' tags.txt)
          aflags=$(while IFS= read -r kv; do
            [[ -n "$kv" ]] && printf -- "--build-arg %q " "$kv"
          done <<< "$BUILD_ARGS")

          podman build \
            --platform linux/amd64 \
            --label-file labels.txt \
            $aflags \
            $tflags \
            -f Dockerfile \
            .

      - name: Push every tag
        run: xargs -n1 podman push < tags.txt
```

## What's better

- **One job, one checkout.**
  No reusable workflow → no second runner pod → no second cold start.
- **No gittools/actions.**
  Saves the 30–60s setup-and-execute step per build.
- **No composite-action layer.**
  `compute-image-tags`' shell math is now one `hanko stamp docker tags` call; `podman-publish`'s wrapper logic is visible in the workflow instead of hidden behind an action boundary.
- **Tags file is grep-able.**
  Debugging "why did we push `:1` to prod?" is `cat tags.txt`, not "step into the composite, read compute.sh."
- **Deterministic `created` label.**
  Comes from git commit date; same commit always produces the same `org.opencontainers.image.created`.
  This also makes registry-backed layer caching actually work — cache key stability across runs of the same commit.

## What's the same (or slightly worse)

- **Caller still does `podman login` themselves.**
  The old composite did this internally.
  Hanko is not in the registry-auth business, and we're not building a podman-wrapper subcommand — it would re-introduce the "tool tries to do too many things" problem.
- **Build-arg threading still needs a shell loop.**
  Hanko emits tags and labels; the caller still composes `--build-arg`/`-t` flags.
  This is fine — those values aren't version-derived.
- **Caller workflows lose the `version-override` input.**
  With hanko, override is `hanko version --override <semver>` — but [D-002 / D-003 in design-decisions](../../docs/design-decisions.md) haven't decided the exact flag shape.
  For migration, treat the override path as a v1.1 follow-up rather than a v1.0 blocker.

## Edges this migration glosses over

- **Tag events** (push to `refs/tags/v1.2.3`).
  Hanko sees a detached HEAD and falls back to `"detached"` for the branch — wrong for tag fan-out on `:latest`.
  Caller should add `--source ${{ github.event.repository.default_branch }}` to the hanko calls when `github.ref_type == 'tag'`.
  Worth checking whether the original `gittools/actions/execute` step handled this; it did, implicitly, via the tag-pointing-at-a-branch lookup.
- **Multi-platform builds** (`linux/amd64,linux/arm64`).
  The example hardcodes `linux/amd64`.
  The old action surfaced this as an input.
  Trivial to thread through; just not done in the sketch.
- **`push: false`** for PR-validation builds.
  The example always pushes.
  Conditional `if: github.event_name != 'pull_request'` on the push step preserves the old behavior.
- **Bare branch tags.**
  Some repos pushed `:<branch>` (a moving pointer) in addition to `:<branch>-<sha>`.
  Achievable via `hanko stamp docker tags ... --extra "${{ github.ref_type == 'branch' && github.ref_name || '' }}"`.

## Suggested next step

Run this against one real repo (`casualty-ms` or similar) on a side branch.
Compare the pushed tag list before vs after.
Surface anything that breaks.
