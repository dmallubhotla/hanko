#!/usr/bin/env bash
#
# Full-flow tests.
# Each scenario builds a mock repo with realistic tag history and asserts what hanko does at multiple points in that history.
#
# Distinct from `test/smoke/smoke.sh`, which verifies command shape on minimal repos.
# Flows verify *outcomes* on real-looking inputs: pre-existing tags, multiple branches, release-branch maintenance, detached HEADs at tag pushes, push-to-remote, end-to-end stamping pipelines.
#
# Usage:
#   test/flows/flows.sh                     # builds hanko via `go build`
#   test/flows/flows.sh /path/to/hanko      # uses an existing binary
#   HANKO=./result/bin/hanko test/flows/flows.sh
#
# Exits 0 if everything passes, non-zero otherwise.

set -euo pipefail

HANKO="${1:-${HANKO:-}}"
if [[ -z "$HANKO" ]]; then
  echo "building hanko via go build..."
  HANKO="$(mktemp -d)/hanko"
  go build -o "$HANKO" .
fi
[[ -x "$HANKO" ]] || { echo "hanko binary not found or not executable: $HANKO" >&2; exit 2; }

# ── tiny assertion framework ───────────────────────────────────────────────
# Same shape as smoke.sh — kept duplicated rather than extracted because the
# two scripts have different lifetimes and shouldn't be coupled.

pass=0
fail=0
fail_names=()

ok()   { printf "  ok   %s\n" "$1"; pass=$((pass+1)); }
fail() { printf "  FAIL %s\n        want: %q\n        got:  %q\n" "$1" "$2" "$3"; fail=$((fail+1)); fail_names+=("$1"); }

assert_eq() {
  if [[ "$2" == "$3" ]]; then ok "$1"; else fail "$1" "$2" "$3"; fi
}
assert_contains() {
  if [[ "$3" == *"$2"* ]]; then ok "$1"; else fail "$1" "*$2*" "$3"; fi
}
assert_exit() {
  if [[ "$2" -eq "$3" ]]; then ok "$1"; else fail "$1" "exit $2" "exit $3"; fi
}

# ── repo helpers ───────────────────────────────────────────────────────────
# Deterministic env so SHAs and dates reproduce across runs.

export GIT_AUTHOR_NAME="flow"
export GIT_AUTHOR_EMAIL="flow@example.invalid"
export GIT_COMMITTER_NAME="flow"
export GIT_COMMITTER_EMAIL="flow@example.invalid"

mkrepo() {
  local dir
  dir="$(mktemp -d)"
  git -C "$dir" init -q --initial-branch=main
  git -C "$dir" config user.email flow@example.invalid
  git -C "$dir" config user.name  flow
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" config tag.gpgsign    false
  printf "%s" "$dir"
}

# commit <repo> <msg> [date]
# Bumps a numbered counter file so each commit's content is unique even with the same message.
commit() {
  local repo="$1" msg="$2" date="${3:-2026-01-01T00:00:00Z}"
  local n
  n=$(( $(cat "$repo/.n" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$repo/.n"
  echo "$msg" >> "$repo/f"
  git -C "$repo" add f .n
  GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" \
    git -C "$repo" commit -q -m "$msg"
}

# annotated_tag <repo> <name>
annotated_tag() {
  GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z" \
    git -C "$1" tag -a "$2" -m "$2"
}

# lightweight_tag <repo> <name>
lightweight_tag() {
  git -C "$1" tag "$2"
}

section() { printf "\n== %s ==\n" "$1"; }

# ── S1 — Mainline progression with prior tag history ──────────────────────
# Repo with three tagged releases on main, then 3 more commits past the latest tag.
# Confirms hanko picks the most recent reachable tag and bumps patch by commit count.

section "S1 — mainline progression with pre-existing tag history"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v0.1.0
commit "$repo" c2
annotated_tag "$repo" v0.2.0
commit "$repo" c3
commit "$repo" c4
annotated_tag "$repo" v1.0.0
commit "$repo" c5
commit "$repo" c6
commit "$repo" c7

# At HEAD (3 commits past v1.0.0):
assert_eq "main HEAD past v1.0.0 by 3 → 1.0.3" "1.0.3" "$("$HANKO" --repo "$repo" version)"

# Walk back through history and verify each tagged-commit gives the clean version:
sha_v1=$(git -C "$repo" rev-parse v1.0.0)
sha_v02=$(git -C "$repo" rev-parse v0.2.0)
sha_v01=$(git -C "$repo" rev-parse v0.1.0)

git -C "$repo" checkout -q "$sha_v1"
# D-001: detached + tag-at-HEAD emits the tag verbatim.
assert_eq "at v1.0.0 (detached) → 1.0.0 verbatim (D-001)" "1.0.0" "$("$HANKO" --repo "$repo" version)"
git -C "$repo" checkout -q main

# One commit past v1.0.0 (c5).
# Detached + feature-rule applies (no patch bump; base 1.0.0 unchanged), so → 1.0.0-detached.1.
# Not 1.0.1-detached.1 — that would require a "mainline" rule which hanko doesn't apply to detached HEADs.
git -C "$repo" checkout -q main
git -C "$repo" checkout -q HEAD~2  # back to c5, detached
assert_eq "one commit past v1.0.0 (detached) → 1.0.0-detached.1" "1.0.0-detached.1" "$("$HANKO" --repo "$repo" version)"
git -C "$repo" checkout -q main

# ── S2 — Hotfix branch off the latest release tag ──────────────────────────
# Production line: v1.1.0 is shipped, main has moved on, a P1 bug needs a fix.
# Branch hotfix/p1 from v1.1.0, commit two fixes.
# Hanko computes a hotfix pre-release; per D-011, `hanko tag` refuses unconditionally — no escape hatch.

section "S2 — hotfix branch off a release tag"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v1.0.0
commit "$repo" c2
annotated_tag "$repo" v1.1.0
commit "$repo" c3  # main moves on past the hotfix base

# Branch hotfix from v1.1.0:
git -C "$repo" checkout -q -b hotfix/p1 v1.1.0
commit "$repo" fix-1
commit "$repo" fix-2

got=$("$HANKO" --repo "$repo" version)
assert_eq "hotfix HEAD → 1.1.1-hotfix.2" "1.1.1-hotfix.2" "$got"

# D-011: tagging refuses prereleases unconditionally; no `--allow-prerelease-tag` flag.
set +e
out=$("$HANKO" --repo "$repo" tag 2>&1); code=$?
set -e
assert_exit "tag refuses prerelease unconditionally" 1 "$code"
assert_contains "error mentions prerelease" "pre-release" "$out"
assert_contains "error suggests merging to main" "merge to main" "$out"

# Simulate the merge-back: merge hotfix into main with --no-ff.
# The shared counter file `.n` would otherwise conflict; resolve in favour of the hotfix side.
git -C "$repo" checkout -q main
GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z" \
  git -C "$repo" merge --no-ff -X theirs -q -m "Merge hotfix/p1" hotfix/p1
# After merge: main is 3 commits past v1.1.0 (c3 + fix-1 + fix-2 + merge commit, with the prior counter at v1.1.0 = 0).
# Latest reachable semver tag is v1.1.0 (no prerelease tags were created because D-011 prevented them).
got=$("$HANKO" --repo "$repo" version)
assert_eq "main after merge-back computes from v1.1.0 → 1.1.4" "1.1.4" "$got"
# Tag the merge commit normally:
got=$("$HANKO" --repo "$repo" tag 2>&1)
assert_eq "release tag after merge-back" "v1.1.4" "$got"

# ── S3 — Release branch maintenance ────────────────────────────────────────
# Repo with v1.0.0 on main, then main moves to v2.0.0.
# release/1.0 branch from v1.0.0 takes its own bug-fix commits.
# Hanko should pick the latest *reachable* tag for the release branch (v1.0.0), not v2.0.0.

section "S3 — release/x.y maintenance branch"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v1.0.0
commit "$repo" c2
annotated_tag "$repo" v2.0.0
commit "$repo" c3-main

git -C "$repo" checkout -q -b release/1.0 v1.0.0
commit "$repo" fix-a
commit "$repo" fix-b

assert_eq "release/1.0 HEAD → 1.0.2" "1.0.2" "$("$HANKO" --repo "$repo" version)"

# release/x.y is non-prerelease, so plain `hanko tag` works:
got=$("$HANKO" --repo "$repo" tag 2>&1)
assert_eq "tag on release branch → v1.0.2" "v1.0.2" "$got"

# Main remains independent:
git -C "$repo" checkout -q main
assert_eq "main is still 2.0.1" "2.0.1" "$("$HANKO" --repo "$repo" version)"

# ── S4 — Mixed tag formats ─────────────────────────────────────────────────
# Real repos accumulate tags with inconsistent prefixes over years.
# Confirm hanko parses `v1.0.0`, bare `1.0.1`, and silently ignores non-semver tags.

section "S4 — mixed tag formats"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v1.0.0
commit "$repo" c2
annotated_tag "$repo" "1.0.1"            # bare semver
commit "$repo" c3
annotated_tag "$repo" "release-frozen"   # non-semver, should be ignored by version parser
commit "$repo" c4

# D-012: hanko passes --match patterns to `git describe`, so non-semver tags like release-frozen are skipped at the source.
# Describe walks back to the most recent semver-shaped tag (bare `1.0.1` here, 2 commits past HEAD).
got=$("$HANKO" --repo "$repo" version)
assert_eq "non-semver tag is skipped; walks back to 1.0.1 → 1.0.3" "1.0.3" "$got"

# Same result even after we delete the rogue tag, by construction:
git -C "$repo" tag -d release-frozen
got=$("$HANKO" --repo "$repo" version)
assert_eq "deleting the rogue tag doesn't change the answer" "1.0.3" "$got"

# ── S5 — Detached HEAD on a tag (CI tag-push event) ────────────────────────
# D-001: hanko emits the tag's version verbatim when detached at a tagged commit.
# Covers the canonical GHA `on: push: tags:` flow.

section "S5 — detached HEAD on a tag (D-001)"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v1.2.3
git -C "$repo" checkout -q v1.2.3

got=$("$HANKO" --repo "$repo" version)
assert_eq "tag-push event → 1.2.3 verbatim" "1.2.3" "$got"

# Even with a prerelease tag, hanko emits it exactly:
commit "$repo" c2
annotated_tag "$repo" v1.2.4-rc.1
git -C "$repo" checkout -q v1.2.4-rc.1
got=$("$HANKO" --repo "$repo" version)
assert_eq "prerelease tag-push event → 1.2.4-rc.1 verbatim" "1.2.4-rc.1" "$got"

# ── S6 — Tag conflict on an unreachable side branch ────────────────────────
# Common in busy repos: feature branches that get tagged but never merged.
# Hanko should detect the conflict at create-time even though `git describe` doesn't see the rogue tag.

section "S6 — conflicting tag on unreachable side branch"
repo=$(mkrepo)
commit "$repo" c1
annotated_tag "$repo" v1.0.0
git -C "$repo" checkout -q -b dead-end v1.0.0
commit "$repo" abandoned
annotated_tag "$repo" v1.0.1
git -C "$repo" checkout -q main
commit "$repo" c2

# hanko on main computes v1.0.1, but the tag exists pointing at dead-end:
set +e
out=$("$HANKO" --repo "$repo" tag 2>&1); code=$?
set -e
assert_exit "tag refuses conflict" 1 "$code"
assert_contains "error mentions conflict" "does not point at HEAD" "$out"

# ── S7 — Long-running release with newer mainline tags ─────────────────────
# Same as S3 but with extended history: years pass, mainline reaches v3.x, an old release/1.x still takes patches.

section "S7 — release/1.0 maintained while mainline reaches v3.x"
repo=$(mkrepo)
commit "$repo" c1; annotated_tag "$repo" v1.0.0
commit "$repo" c2; annotated_tag "$repo" v1.1.0
commit "$repo" c3; annotated_tag "$repo" v2.0.0
commit "$repo" c4; annotated_tag "$repo" v3.0.0
commit "$repo" c5  # main HEAD is 1 commit past v3.0.0

git -C "$repo" checkout -q -b release/1.0 v1.0.0
commit "$repo" oldfix-1
commit "$repo" oldfix-2

assert_eq "release/1.0 sees v1.0.0 as latest reachable → 1.0.2" "1.0.2" "$("$HANKO" --repo "$repo" version)"
git -C "$repo" checkout -q main
assert_eq "main sees v3.0.0 → 3.0.1" "3.0.1" "$("$HANKO" --repo "$repo" version)"

# ── S8 — End-to-end build pipeline ─────────────────────────────────────────
# A repo with two prior tags, three commits past the latest, contains both a Helm chart and a Go program.
# Run hanko's three stamping commands; build the Go binary and verify it embeds the right values; verify Chart.yaml edit.

section "S8 — end-to-end stamping pipeline against multi-tag history"
repo=$(mkrepo)

# Set up the Go program:
cat > "$repo/go.mod" <<'EOF'
module example.invalid/demo
go 1.24
EOF
cat > "$repo/main.go" <<'EOF'
package main
import "fmt"
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)
func main() {
	fmt.Printf("version=%s\ncommit=%s\ndate=%s\n", version, commit, date)
}
EOF

# Set up the Helm chart:
mkdir -p "$repo/charts/demo"
cat > "$repo/charts/demo/Chart.yaml" <<'EOF'
apiVersion: v2
name: demo
version: 0.0.0
appVersion: "0.0.0"  # bumped by hanko in CI
EOF

git -C "$repo" add -A
GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z" git -C "$repo" commit -q -m initial
annotated_tag "$repo" v1.0.0
commit "$repo" "more"
annotated_tag "$repo" v1.1.0
commit "$repo" "post-tag-1"
commit "$repo" "post-tag-2"
commit "$repo" "post-tag-3"

# Expected version: 1.1.3
expected_semver="1.1.3"
assert_eq "version on this multi-tag repo" "$expected_semver" "$("$HANKO" --repo "$repo" version)"

# 1. Go ldflags — build the binary and check it embeds the right strings.
ldflags=$("$HANKO" --repo "$repo" stamp go-ldflags)
( cd "$repo" && go build -ldflags "$ldflags" -o "$repo/demo" . )
embedded=$("$repo/demo")
assert_contains "embedded version=$expected_semver" "version=$expected_semver" "$embedded"
assert_contains "embedded commit=full sha" "commit=$(git -C "$repo" rev-parse HEAD)" "$embedded"
assert_contains "embedded date=commit date" "date=2026-01-01T00:00:00" "$embedded"

# 2. Docker tags — full fan-out on mainline.
out=$("$HANKO" --repo "$repo" stamp docker tags ghcr.io/example/demo)
short=$(git -C "$repo" rev-parse --short HEAD)
expected_tags="ghcr.io/example/demo:$expected_semver
ghcr.io/example/demo:1.1
ghcr.io/example/demo:1
ghcr.io/example/demo:latest
ghcr.io/example/demo:main-$short"
assert_eq "docker tag fan-out on mainline" "$expected_tags" "$out"

# 3. Docker labels — args mode.
out=$("$HANKO" --repo "$repo" stamp docker labels --source https://example.com/demo --title demo)
assert_contains "version label" "--label org.opencontainers.image.version=$expected_semver" "$out"
assert_contains "created label is commit date" "--label org.opencontainers.image.created=2026-01-01T00:00:00" "$out"

# 4. Helm — edit the chart in place.
"$HANKO" --repo "$repo" stamp helm "$repo/charts/demo" > /dev/null
chart_after=$(<"$repo/charts/demo/Chart.yaml")
assert_contains "Chart.yaml has new version" "version: $expected_semver" "$chart_after"
assert_contains "Chart.yaml has quoted appVersion" "appVersion: \"$expected_semver\"" "$chart_after"
assert_contains "Chart.yaml preserves trailing comment" "# bumped by hanko in CI" "$chart_after"

# ── S10 — Refuse on shallow clones ─────────────────────────────────────────
# D-004: a shallow clone is silently miscount-able under git rev-list, so hanko refuses outright.
# Set up a bare remote with two commits and a tag, then clone shallowly.

section "S10 — refuse on shallow clone (D-004)"
src=$(mkrepo)
commit "$src" c1; annotated_tag "$src" v1.0.0
commit "$src" c2
bare=$(mktemp -d)/shallow-src.git
git init -q --bare --initial-branch=main "$bare"
git -C "$src" remote add origin "$bare"
git -C "$src" push -q origin main
git -C "$src" push -q origin --tags

# `git clone --depth` ignores --depth for local-path sources unless we use a file:// URL.
shallow=$(mktemp -d)/shallow-clone
git clone -q --depth 1 --branch main "file://$bare" "$shallow"

set +e
out=$("$HANKO" --repo "$shallow" version 2>&1); code=$?
set -e
assert_exit "version refuses on shallow" 1 "$code"
assert_contains "error names the problem" "shallow" "$out"
assert_contains "error hints at the fix" "fetch-depth" "$out"

# Deepen the clone; hanko should now compute fine.
git -C "$shallow" fetch -q --unshallow
got=$("$HANKO" --repo "$shallow" version)
assert_eq "after --unshallow, hanko computes" "1.0.1" "$got"

# ── S9 — Push to a bare remote ─────────────────────────────────────────────
# Verifies the actual --push path, which smoke tests skip.
# Bare remote in a tmp dir; push the new tag; reclone to confirm.

section "S9 — hanko tag --push against a bare remote"
repo=$(mkrepo)
bare=$(mktemp -d)/origin.git
git init -q --bare "$bare"
git -C "$repo" remote add origin "$bare"

commit "$repo" c1
annotated_tag "$repo" v1.0.0
commit "$repo" c2
git -C "$repo" push -q origin main

got=$("$HANKO" --repo "$repo" tag --push)
assert_eq "hanko tag --push creates v1.0.1" "v1.0.1" "$got"

# Reclone and verify the tag landed:
reclone=$(mktemp -d)/reclone
git clone -q "$bare" "$reclone"
got=$(git -C "$reclone" tag -l)
assert_contains "bare remote has v1.0.1" "v1.0.1" "$got"

# Idempotent: rerun --push on the same HEAD should no-op (already at tag).
got=$("$HANKO" --repo "$repo" tag --push)
assert_eq "rerun --push is idempotent" "v1.0.1" "$got"

# ── summary ────────────────────────────────────────────────────────────────

printf "\n%d passed, %d failed\n" "$pass" "$fail"
if (( fail > 0 )); then
  printf "failed tests:\n"
  for n in "${fail_names[@]}"; do printf "  - %s\n" "$n"; done
  exit 1
fi
