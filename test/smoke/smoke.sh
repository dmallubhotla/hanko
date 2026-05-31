#!/usr/bin/env bash
#
# End-to-end smoke tests for the hanko binary.
#
# Exercises the CLI as users will: build the binary, set up a throwaway git
# repo, run `hanko version` / `hanko tag` with various flags, assert the
# expected output and side effects.
#
# Unit tests (go test ./...) cover the library layer. This script covers
# command-line plumbing, exit codes, and idempotency — the layer above.
#
# Usage:
#   test/smoke/smoke.sh                       # builds via `go build`
#   test/smoke/smoke.sh /path/to/hanko        # uses an existing binary
#   HANKO=./result/bin/hanko test/smoke/smoke.sh
#
# Exits 0 if everything passes, non-zero otherwise.

set -euo pipefail

HANKO="${1:-${HANKO:-}}"
if [[ -z $HANKO ]]; then
  echo "building hanko via go build..."
  HANKO="$(mktemp -d)/hanko"
  go build -o "$HANKO" .
fi
[[ -x $HANKO ]] || {
  echo "hanko binary not found or not executable: $HANKO" >&2
  exit 2
}

# ── tiny assertion framework ───────────────────────────────────────────────

pass=0
fail=0
fail_names=()

ok() {
  printf "  ok   %s\n" "$1"
  pass=$((pass + 1))
}
fail() {
  printf "  FAIL %s\n        want: %q\n        got:  %q\n" "$1" "$2" "$3"
  fail=$((fail + 1))
  fail_names+=("$1")
}

assert_eq() {
  # assert_eq <name> <expected> <actual>
  if [[ $2 == "$3" ]]; then ok "$1"; else fail "$1" "$2" "$3"; fi
}

assert_contains() {
  # assert_contains <name> <needle> <haystack>
  if [[ $3 == *"$2"* ]]; then ok "$1"; else fail "$1" "*$2*" "$3"; fi
}

assert_exit() {
  # assert_exit <name> <expected_code> <actual_code>
  if [[ $2 -eq $3 ]]; then ok "$1"; else fail "$1" "exit $2" "exit $3"; fi
}

# ── fresh repo helper ──────────────────────────────────────────────────────

mkrepo() {
  local dir
  dir="$(mktemp -d)"
  git -C "$dir" init -q --initial-branch=main
  git -C "$dir" config user.email t@e.invalid
  git -C "$dir" config user.name t
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" config tag.gpgsign false
  printf "%s" "$dir"
}

commit() {
  echo "$2" >>"$1/f"
  git -C "$1" add f
  git -C "$1" commit -q -m "$2"
}

# ── tests ──────────────────────────────────────────────────────────────────

section() { printf "\n== %s ==\n" "$1"; }

section "hanko version"
repo=$(mkrepo)
commit "$repo" one
got=$("$HANKO" --repo "$repo" version)
assert_eq "no tag → 0.1.0-main.1" "0.1.0-main.1" "$got"

git -C "$repo" tag v1.2.3
got=$("$HANKO" --repo "$repo" version)
assert_eq "at tag → 1.2.3" "1.2.3" "$got"

commit "$repo" two
commit "$repo" three
got=$("$HANKO" --repo "$repo" version)
# D-013: mainline past a tag bumps patch by 1 — once — regardless of commit count.
assert_eq "2 commits past tag on main → 1.2.4" "1.2.4" "$got"

got=$("$HANKO" --repo "$repo" version --format gha | LC_ALL=C sort)
expected=$(printf "branch=main\nfull=1.2.4\nis-prerelease=false\nmajor-minor=1.2\nmajor=1\nminor=2\npatch=4\nshort-sha=%s" "$(git -C "$repo" rev-parse --short HEAD)")
assert_eq "--format gha shape" "$expected" "$got"

section "hanko version on a feature branch"
git -C "$repo" checkout -q -b feature/foo
commit "$repo" four
# Latest reachable tag from feature/foo is still v1.2.3 (4 commits ago counting
# the two on main + one on the branch + branch-tip = 3). git describe is
# reachable-only so the main-branch advance counts toward this branch too.
got=$("$HANKO" --repo "$repo" version)
assert_eq "feature branch is a pre-release on the tag base" "1.2.3-feature-foo.3" "$got"

# commit_yaml writes .hanko.yaml + commits it so the worktree stays clean.
commit_yaml() {
  local dir=$1
  local content=$2
  printf "%s" "$content" >"$dir/.hanko.yaml"
  git -C "$dir" add .hanko.yaml
  git -C "$dir" commit -q -m "add hanko config"
}

section "hanko version — works in linked git worktrees"
# `git worktree add` attaches a second working dir to the same .git. hanko
# should treat it like a normal repo — same tag history, branch state from
# *this* worktree, dirty detection scoped to it.
repoWT=$(mkrepo)
commit "$repoWT" one
git -C "$repoWT" tag v1.0.0
commit "$repoWT" two
git -C "$repoWT" branch feature/wt
wtDir=$(mktemp -d)/linked
git -C "$repoWT" worktree add -q "$wtDir" feature/wt
# Main worktree's view: on main, past v1.0.0.
got=$("$HANKO" --repo "$repoWT" version)
assert_eq "main worktree → 1.0.1" "1.0.1" "$got"
# Linked worktree's view: on feature/wt branch from the same starting commit.
# Feature branch → prerelease shape.
got=$("$HANKO" --repo "$wtDir" version)
assert_contains "linked worktree picks up its own branch" "feature-wt" "$got"
# In-progress detection works in linked worktree too — git keeps per-worktree
# markers in .git/worktrees/<name>/, which `rev-parse --git-dir` resolves to.
: >"$(git -C "$wtDir" rev-parse --git-dir)/MERGE_HEAD"
set +e
out=$("$HANKO" --repo "$wtDir" version 2>&1)
code=$?
set -e
assert_exit "refuses mid-operation in worktree" 1 "$code"
assert_contains "names the operation" "merge in progress" "$out"

section "hanko version — does not recurse into nested git repos"
# Submodule promise: --repo <parent> reads only the parent's git state, never
# the inner repo's tags / history. (We don't bother with `git submodule add`
# — a bare nested .git tree is enough to exercise the boundary.)
repoOuter=$(mkrepo)
commit "$repoOuter" outer-one
git -C "$repoOuter" tag v1.0.0
commit "$repoOuter" outer-two
sub="$repoOuter/sub"
git init -q "$sub"
git -C "$sub" config user.email t@e.invalid
git -C "$sub" config user.name t
git -C "$sub" config commit.gpgsign false
git -C "$sub" config tag.gpgsign false
echo a >"$sub/a"
git -C "$sub" add a
git -C "$sub" commit -q -m sub-one
git -C "$sub" tag v9.9.9
# Outer doesn't see inner's tags.
got=$("$HANKO" --repo "$repoOuter" version)
assert_eq "outer ignores inner tags" "1.0.1" "$got"
# Pointing --repo at the inner gets the inner's view, standalone.
got=$("$HANKO" --repo "$sub" version)
assert_eq "inner is its own repo" "9.9.9" "$got"

section "hanko version — refuses when a git operation is in progress"
# Detection is path-based (MERGE_HEAD, rebase-merge/, etc.) so we can simulate
# the state by touching the marker file without actually triggering a merge
# conflict. Mirrors the unit tests in internal/gitinfo/inprogress_test.go.
repoInProgress=$(mkrepo)
commit "$repoInProgress" one
git -C "$repoInProgress" tag v1.0.0
commit "$repoInProgress" two
: >"$repoInProgress/.git/MERGE_HEAD"
set +e
out=$("$HANKO" --repo "$repoInProgress" version 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error names the operation" "merge in progress" "$out"
assert_contains "error suggests abort" "--abort" "$out"
# After cleaning up, version computes normally.
rm "$repoInProgress/.git/MERGE_HEAD"
got=$("$HANKO" --repo "$repoInProgress" version)
assert_eq "version works after the operation completes" "1.0.1" "$got"

section "hanko stamp (no args) — declarative stamp-targets"
repoTgt=$(mkrepo)
# Build the files we'll stamp BEFORE the tag so the version-on-mainline math
# is straightforward (commits past v1.0.0 → 1.0.1).
mkdir -p "$repoTgt"
cat >"$repoTgt/pyproject.toml" <<'EOF'
[project]
name = "demo"
version = "0.0.1"
EOF
cat >"$repoTgt/package.json" <<'EOF'
{
  "name": "demo",
  "version": "0.0.1"
}
EOF
cat >"$repoTgt/Chart.yaml" <<'EOF'
apiVersion: v2
name: demo
version: 0.0.1
appVersion: "0.0.1"
EOF
cat >"$repoTgt/.hanko.yaml" <<'EOF'
seal:
  push-remote: ""
stamp-targets:
  - path: pyproject.toml
    format: toml
    key: project.version
  - path: package.json
    format: json
    key: version
  - path: Chart.yaml
    format: yaml
    keys: [version, appVersion]
EOF
git -C "$repoTgt" add .
git -C "$repoTgt" commit -q -m initial
git -C "$repoTgt" tag v1.0.0
commit "$repoTgt" two

# Dry-run prints per-target descriptions, doesn't write.
out=$("$HANKO" --repo "$repoTgt" stamp --dry-run)
assert_contains "dry-run lists pyproject" "pyproject.toml (toml)" "$out"
assert_contains "dry-run lists package.json" "package.json (json)" "$out"
assert_contains "dry-run lists Chart.yaml" "Chart.yaml (yaml)" "$out"
assert_contains "dry-run shows the new value in pyproject" "project.version: 0.0.1 → 1.0.1" "$out"
got_after_dryrun=$(grep '^version' "$repoTgt/pyproject.toml")
assert_eq "dry-run did not write pyproject" 'version = "0.0.1"' "$got_after_dryrun"

# Real run mutates all three files.
"$HANKO" --repo "$repoTgt" stamp >/dev/null
got_py=$(grep '^version' "$repoTgt/pyproject.toml")
assert_eq "pyproject bumped" 'version = "1.0.1"' "$got_py"
got_js=$(grep '"version"' "$repoTgt/package.json")
assert_contains "package.json bumped" '"version": "1.0.1"' "$got_js"
got_helm_v=$(grep '^version:' "$repoTgt/Chart.yaml")
assert_eq "Chart.yaml version bumped" "version: 1.0.1" "$got_helm_v"
got_helm_a=$(grep '^appVersion:' "$repoTgt/Chart.yaml")
assert_eq "Chart.yaml appVersion bumped (quoted preserved)" 'appVersion: "1.0.1"' "$got_helm_a"

section "hanko stamp (no args) — refuses when no targets declared"
repoNo=$(mkrepo)
commit "$repoNo" one
set +e
out=$("$HANKO" --repo "$repoNo" stamp 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions stamp-targets" "stamp-targets" "$out"

section "hanko seal — dry-run"
repoSeal=$(mkrepo)
commit "$repoSeal" one
commit_yaml "$repoSeal" 'seal:
  push-remote: ""
  pre-commit:
    - "echo hook saw {semver}"
'
git -C "$repoSeal" tag v0.1.0
commit "$repoSeal" two
out=$("$HANKO" --repo "$repoSeal" seal --dry-run)
assert_contains "dry-run shows version" "version:        0.1.1" "$out"
assert_contains "dry-run shows tag" "tag name:       v0.1.1" "$out"
assert_contains "dry-run shows default commit message with chore prefix" "chore: Release 0.1.1" "$out"
assert_contains "dry-run expands hook template" "echo hook saw 0.1.1" "$out"
# Confirm nothing was committed or tagged.
tags_after=$(git -C "$repoSeal" tag -l | tr '\n' ',')
assert_eq "dry-run created no tag" "v0.1.0," "$tags_after"

section "hanko seal — happy path (local, no push)"
out=$("$HANKO" --repo "$repoSeal" seal 2>&1)
assert_contains "prints created tag" "v0.1.1" "$out"
got_tag=$(git -C "$repoSeal" tag -l v0.1.1)
assert_eq "tag v0.1.1 was created" "v0.1.1" "$got_tag"

section "hanko seal — runs stamp-targets before pre-commit hooks"
repoSealTgt=$(mkrepo)
mkdir -p "$repoSealTgt"
cat >"$repoSealTgt/pyproject.toml" <<'EOF'
[project]
name = "demo"
version = "0.0.1"
EOF
cat >"$repoSealTgt/.hanko.yaml" <<'EOF'
seal:
  push-remote: ""
stamp-targets:
  - path: pyproject.toml
    format: toml
    key: project.version
EOF
git -C "$repoSealTgt" add .
git -C "$repoSealTgt" commit -q -m initial
git -C "$repoSealTgt" tag v1.0.0
commit "$repoSealTgt" two
"$HANKO" --repo "$repoSealTgt" seal >/dev/null
got=$(grep '^version' "$repoSealTgt/pyproject.toml")
assert_eq "seal bumped pyproject" 'version = "1.0.1"' "$got"
got_tag=$(git -C "$repoSealTgt" tag -l v1.0.1)
assert_eq "seal created tag" "v1.0.1" "$got_tag"
# Verify the release commit includes the pyproject bump (single commit).
got_files=$(git -C "$repoSealTgt" show --stat HEAD --pretty=format: | grep -v '^$' | head -3 | tr -d ' ')
assert_contains "release commit touched pyproject" "pyproject.toml" "$got_files"

section "hanko seal — end-to-end push to a bare-repo origin"
# Confirms the push path actually works: bare repo as origin, seal pushes the
# release commit + tag atomically, both arrive in the bare repo.
bareRepo=$(mktemp -d)/origin.git
git init -q --bare "$bareRepo"

repoPush=$(mkrepo)
git -C "$repoPush" remote add origin "$bareRepo"
mkdir -p "$repoPush"
cat >"$repoPush/flake.nix" <<'EOF'
{
  outputs = _: {
    packages.default = mkDerivation {
      version = "0.0.1";
    };
  };
}
EOF
commit_yaml "$repoPush" 'stamp-targets:
  - path: flake.nix
    format: nix
    key: version
seal:
  commit-message: "Release {semver}"
'
git -C "$repoPush" add flake.nix
git -C "$repoPush" commit -q -m "add flake"
git -C "$repoPush" tag v1.0.0
commit "$repoPush" two

# Run the seal — push to origin (the bare repo).
out=$("$HANKO" --repo "$repoPush" seal 2>&1)
assert_contains "seal output names the tag" "v1.0.1" "$out"

# Tag landed in the bare repo.
got=$(git -C "$bareRepo" tag -l v1.0.1)
assert_eq "tag pushed to bare origin" "v1.0.1" "$got"

# Tag points at the same commit as the working repo's HEAD.
expectedSha=$(git -C "$repoPush" rev-parse HEAD)
gotSha=$(git -C "$bareRepo" rev-list -n1 v1.0.1)
assert_eq "tag in bare repo points at the release commit" "$expectedSha" "$gotSha"

# Main branch advanced in the bare repo too (commit pushed alongside tag).
gotMain=$(git -C "$bareRepo" rev-parse main)
assert_eq "main updated in bare origin" "$expectedSha" "$gotMain"

# The release commit on origin contains the flake.nix bump.
gotFlake=$(git -C "$bareRepo" show v1.0.1:flake.nix | grep '^      version')
assert_contains "flake.nix in release commit was bumped" 'version = "1.0.1"' "$gotFlake"

section "hanko seal — exits 0 with 'no release needed' when bump direction is none"
# Strict-conventional setup: branch has `increment: none`, so any range
# without feat:/fix:/feat!: signals computes the same tag we already have.
repoNoRelease=$(mkrepo)
commit "$repoNoRelease" one
commit_yaml "$repoNoRelease" 'seal:
  push-remote: ""
branches:
  - name: mainline
    regex: ^(main|master)$
    is-mainline: true
    increment: none
    label: ""
  - name: feature
    regex: .*
    increment: none
    label: "{branch}"
'
git -C "$repoNoRelease" tag v1.0.0
commit "$repoNoRelease" "chore: tidy"
commit "$repoNoRelease" "docs: typo"
set +e
out=$("$HANKO" --repo "$repoNoRelease" seal 2>&1)
code=$?
set -e
assert_exit "exit 0 on no-release-needed" 0 "$code"
assert_contains "message names the no-release path" "no release needed" "$out"
assert_contains "message identifies the unchanged tag" "v1.0.0" "$out"
assert_contains "message hints at why (no signal)" "none with feat" "$out"
# Verify no new tag was created.
tags_after=$(git -C "$repoNoRelease" tag -l | tr '\n' ',')
assert_eq "no new tag was created" "v1.0.0," "$tags_after"

section "hanko version --verbose prints decision rationale to stderr"
repoVerbose=$(mkrepo)
commit "$repoVerbose" one
git -C "$repoVerbose" tag v1.0.0
commit "$repoVerbose" "feat: new shiny"
# stdout is the version; stderr is the decision rationale.
stdout=$("$HANKO" --repo "$repoVerbose" version --verbose 2>/dev/null)
stderr=$("$HANKO" --repo "$repoVerbose" version --verbose 2>&1 >/dev/null)
assert_eq "stdout is just the version" "1.1.0" "$stdout"
assert_contains "stderr names the strategy" "strategy:        conventional-commits" "$stderr"
assert_contains "stderr shows the commit subject" "feat: new shiny" "$stderr"
assert_contains "stderr marks the strongest signal" "← strongest" "$stderr"
assert_contains "stderr names the direction" "direction:       minor" "$stderr"

section "hanko seal — refuses dirty"
repoDirty=$(mkrepo)
commit "$repoDirty" one
commit_yaml "$repoDirty" 'seal:
  push-remote: ""
'
git -C "$repoDirty" tag v0.1.0
commit "$repoDirty" two
echo dirt >"$repoDirty/dirt"
set +e
out=$("$HANKO" --repo "$repoDirty" seal 2>&1)
code=$?
set -e
assert_exit "exit code on dirty" 1 "$code"
assert_contains "error mentions dirty" "dirty" "$out"

section "hanko seal — refuses prerelease by default"
repoPre=$(mkrepo)
commit "$repoPre" one
commit_yaml "$repoPre" 'seal:
  push-remote: ""
'
git -C "$repoPre" tag v0.1.0
git -C "$repoPre" checkout -q -b feature/x
commit "$repoPre" two
set +e
out=$("$HANKO" --repo "$repoPre" seal 2>&1)
code=$?
set -e
assert_exit "exit code on prerelease" 1 "$code"
assert_contains "error mentions pre-release" "pre-release" "$out"

section "hanko seal --initial — bootstraps first release with verbatim value"
repoSealInit=$(mkrepo)
commit "$repoSealInit" one
commit_yaml "$repoSealInit" 'seal:
  push-remote: ""
'
got=$("$HANKO" --repo "$repoSealInit" seal --initial=v0.1.0 2>&1)
assert_contains "seal output names the tag" "v0.1.0" "$got"
got_tag=$(git -C "$repoSealInit" tag -l v0.1.0)
assert_eq "tag v0.1.0 was created" "v0.1.0" "$got_tag"

section "hanko seal --initial — bare form uses initial-version from config"
repoSealInitCfg=$(mkrepo)
commit "$repoSealInitCfg" one
commit_yaml "$repoSealInitCfg" 'initial-version: "0.2.0"
seal:
  push-remote: ""
'
got=$("$HANKO" --repo "$repoSealInitCfg" seal --initial 2>&1)
assert_contains "seal output names the tag from config initial-version" "0.2.0" "$got"
got_tag=$(git -C "$repoSealInitCfg" tag -l 0.2.0)
assert_eq "bare tag 0.2.0 was created" "0.2.0" "$got_tag"

section "hanko seal --initial — stamps the initial value into stamp-targets"
repoSealInitStamp=$(mkrepo)
cat >"$repoSealInitStamp/pyproject.toml" <<'EOF'
[project]
name = "demo"
version = "0.0.0"
EOF
cat >"$repoSealInitStamp/.hanko.yaml" <<'EOF'
seal:
  push-remote: ""
stamp-targets:
  - path: pyproject.toml
    format: toml
    key: project.version
EOF
git -C "$repoSealInitStamp" add .
git -C "$repoSealInitStamp" commit -q -m initial
"$HANKO" --repo "$repoSealInitStamp" seal --initial=v0.1.0 >/dev/null
got=$(grep '^version' "$repoSealInitStamp/pyproject.toml")
assert_eq "stamp-target received the verbatim semver (no v)" 'version = "0.1.0"' "$got"

section "hanko seal --initial — refuses if any semver tag already exists"
repoSealInitTaken=$(mkrepo)
commit "$repoSealInitTaken" one
git -C "$repoSealInitTaken" tag v0.1.0
commit_yaml "$repoSealInitTaken" 'seal:
  push-remote: ""
'
commit "$repoSealInitTaken" two
set +e
out=$("$HANKO" --repo "$repoSealInitTaken" seal --initial=v1.0.0 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error explains why" "--initial only valid when no semver-shaped tag exists" "$out"

section "hanko seal --initial — refuses non-semver values"
repoSealInitBad=$(mkrepo)
commit "$repoSealInitBad" one
commit_yaml "$repoSealInitBad" 'seal:
  push-remote: ""
'
set +e
out=$("$HANKO" --repo "$repoSealInitBad" seal --initial=banana 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions shape" "not a semver-shaped tag" "$out"

section "hanko seal --initial — refuses combination with --bump"
repoSealInitBump=$(mkrepo)
commit "$repoSealInitBump" one
commit_yaml "$repoSealInitBump" 'seal:
  push-remote: ""
'
set +e
out=$("$HANKO" --repo "$repoSealInitBump" seal --initial=v0.1.0 --bump=minor 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions mutually exclusive" "mutually exclusive" "$out"

section "hanko seal --initial — prerelease-shaped initial still trips the blocker"
repoSealInitPre=$(mkrepo)
commit "$repoSealInitPre" one
commit_yaml "$repoSealInitPre" 'seal:
  push-remote: ""
'
set +e
out=$("$HANKO" --repo "$repoSealInitPre" seal --initial=v0.1.0-beta.1 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions pre-release" "pre-release" "$out"

section "hanko version --bump (manual override)"
repoBump=$(mkrepo)
commit "$repoBump" one
git -C "$repoBump" tag v1.0.0
commit "$repoBump" "chore: no signal here"
# Default fixed strategy + branch's `increment: patch` would yield 1.0.1.
assert_eq "no --bump → patch from branch policy" "1.0.1" "$("$HANKO" --repo "$repoBump" version)"
assert_eq "--bump minor → 1.1.0" "1.1.0" "$("$HANKO" --repo "$repoBump" version --bump minor)"
assert_eq "--bump major → 2.0.0" "2.0.0" "$("$HANKO" --repo "$repoBump" version --bump major)"
assert_eq "--bump none → 1.0.0" "1.0.0" "$("$HANKO" --repo "$repoBump" version --bump none)"
# Bad value:
set +e
out=$("$HANKO" --repo "$repoBump" version --bump bananas 2>&1)
code=$?
set -e
assert_exit "rejects bad --bump value" 1 "$code"
assert_contains "error names valid values" "patch, minor, major, none" "$out"

section "hanko version with bump-strategy: conventional-commits"
repoCC=$(mkrepo)
commit "$repoCC" one
git -C "$repoCC" tag v1.0.0
cat >"$repoCC/.hanko.yaml" <<'EOF'
bump-strategy: conventional-commits
EOF
# chore: alone → no signal, falls back to branch's increment (patch).
commit "$repoCC" "chore: tidy"
assert_eq "chore alone → 1.0.1 (fallback)" "1.0.1" "$("$HANKO" --repo "$repoCC" version)"
# feat: → minor bump.
commit "$repoCC" "feat: shiny new thing"
assert_eq "feat → 1.1.0 (minor)" "1.1.0" "$("$HANKO" --repo "$repoCC" version)"
# feat!: → major bump.
commit "$repoCC" "feat!: redo api"
assert_eq "feat! → 2.0.0 (major)" "2.0.0" "$("$HANKO" --repo "$repoCC" version)"

section "hanko tag — happy path"
repo=$(mkrepo)
commit "$repo" one
git -C "$repo" tag v1.0.0
commit "$repo" two
got=$("$HANKO" --repo "$repo" tag)
assert_eq "creates v1.0.1" "v1.0.1" "$got"
got=$(git -C "$repo" cat-file -t v1.0.1)
assert_eq "tag is annotated" "tag" "$got"

section "hanko tag — idempotent rerun"
got=$("$HANKO" --repo "$repo" tag)
assert_eq "second run echoes existing tag" "v1.0.1" "$got"

section "hanko tag — follows existing bare-tag convention (D-002)"
# Bootstrap with a bare tag, then a follow-up `hanko tag` should also be bare.
repoBareTag=$(mkrepo)
commit "$repoBareTag" one
git -C "$repoBareTag" tag 1.0.0
commit "$repoBareTag" two
got=$("$HANKO" --repo "$repoBareTag" tag)
assert_eq "creates bare 1.0.1 (no auto-v-prefix)" "1.0.1" "$got"

section "hanko tag — refuses dirty"
# Fresh repo: HEAD is past the latest tag, so the idempotency short-circuit
# does NOT fire; dirty check should reject.
repo2=$(mkrepo)
commit "$repo2" one
git -C "$repo2" tag v1.0.0
commit "$repo2" two
echo dirt >"$repo2/c"
set +e
out=$("$HANKO" --repo "$repo2" tag 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions dirty" "dirty" "$out"

section "hanko tag — refuses prerelease unconditionally (D-011)"
# D-011: `hanko tag` never creates pre-release tags; no escape-hatch flag.
git -C "$repo" checkout -q -b feature/bar
commit "$repo" three
set +e
out=$("$HANKO" --repo "$repo" tag 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions pre-release" "pre-release" "$out"
assert_contains "error suggests merging to main" "merge to main" "$out"

section "hanko tag --dry-run on a non-prerelease commit"
# Use a fresh repo to keep this self-contained.
repo3=$(mkrepo)
commit "$repo3" one
git -C "$repo3" tag v2.0.0
commit "$repo3" two
out=$("$HANKO" --repo "$repo3" tag --dry-run 2>&1)
assert_contains "announces would-create" "would create annotated tag" "$out"
# Verify dry-run really did nothing:
tags_after=$(git -C "$repo3" tag -l | sort | tr '\n' ',')
assert_eq "no new tag created by --dry-run" "v2.0.0," "$tags_after"

section "hanko tag --initial — bootstraps first release in a fresh repo"
# D-011 (revised): `--initial` is the only escape hatch from the pre-release
# refusal, and only when no semver-shaped tag exists yet.
repoInit=$(mkrepo)
commit "$repoInit" one
got=$("$HANKO" --repo "$repoInit" tag --initial v0.1.0)
assert_eq "creates v0.1.0 verbatim" "v0.1.0" "$got"
got=$(git -C "$repoInit" cat-file -t v0.1.0)
assert_eq "initial tag is annotated" "tag" "$got"

# Idempotent re-run: same --initial on a repo that already has the tag at HEAD.
got=$("$HANKO" --repo "$repoInit" tag --initial v0.1.0)
assert_eq "rerun echoes existing tag" "v0.1.0" "$got"

section "hanko tag --initial — verbatim value preserves bare-tag convention"
repoBare=$(mkrepo)
commit "$repoBare" one
got=$("$HANKO" --repo "$repoBare" tag --initial 0.1.0)
assert_eq "creates bare 0.1.0 (no auto-v-prefix)" "0.1.0" "$got"

section "hanko tag --initial — refuses if any semver tag already exists"
repoTagged=$(mkrepo)
commit "$repoTagged" one
git -C "$repoTagged" tag v0.1.0
commit "$repoTagged" two
set +e
out=$("$HANKO" --repo "$repoTagged" tag --initial v1.0.0 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error explains why" "--initial only valid when no semver-shaped tag exists" "$out"

section "hanko tag --initial — refuses non-semver values"
repoBad=$(mkrepo)
commit "$repoBad" one
set +e
out=$("$HANKO" --repo "$repoBad" tag --initial banana 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions shape" "not a semver-shaped tag" "$out"

section "hanko tag — refuses to overwrite a conflicting tag"
# Setup: on main, hanko will compute v1.0.1 (1 commit past v1.0.0). On an
# unreachable side branch, squat v1.0.1 at a different commit. git describe
# is reachable-only so hanko's *computation* is unaffected; show-ref is
# global, so the tag conflict is detected at create time.
repo=$(mkrepo)
commit "$repo" one
git -C "$repo" tag v1.0.0
git -C "$repo" checkout -q -b side
commit "$repo" side-one
git -C "$repo" tag v1.0.1
git -C "$repo" checkout -q main
commit "$repo" two
set +e
out=$("$HANKO" --repo "$repo" tag 2>&1)
code=$?
set -e
assert_exit "exit code" 1 "$code"
assert_contains "error mentions conflict" "does not point at HEAD" "$out"

section "hanko stamp go-ldflags"
repo=$(mkrepo)
commit "$repo" one
git -C "$repo" tag v1.0.0
commit "$repo" two
out=$("$HANKO" --repo "$repo" stamp go-ldflags)
assert_contains "has -X main.version" "-X main.version=1.0.1" "$out"
assert_contains "has -X main.commit" "-X main.commit=" "$out"
assert_contains "has -X main.date" "-X main.date=" "$out"

out=$("$HANKO" --repo "$repo" stamp go-ldflags --package example.com/foo)
assert_contains "honours --package" "-X example.com/foo.version=1.0.1" "$out"

section "hanko stamp nix"
nixrepo=$(mkrepo)
cat >"$nixrepo/flake.nix" <<'EOF'
{
  outputs = _: {
    packages.default = mkDerivation {
      pname = "demo";
      version = "0.0.1";
      src = ./.;
    };
  };
}
EOF
git -C "$nixrepo" add flake.nix
git -C "$nixrepo" commit -q -m one
git -C "$nixrepo" tag v1.2.3
commit "$nixrepo" two
"$HANKO" --repo "$nixrepo" stamp nix >/dev/null
got=$(grep 'version = ' "$nixrepo/flake.nix")
assert_contains "flake.nix version bumped" 'version = "1.2.4";' "$got"

# Dry-run leaves the file alone.
git -C "$nixrepo" checkout -q -- flake.nix
out=$("$HANKO" --repo "$nixrepo" stamp nix --dry-run)
assert_contains "dry-run announces change" "0.0.1 → 1.2.4" "$out"
got=$(grep 'version = ' "$nixrepo/flake.nix")
assert_contains "dry-run did not write" 'version = "0.0.1";' "$got"

section "hanko stamp docker tags — non-prerelease on main"
out=$("$HANKO" --repo "$repo" stamp docker tags ghcr.io/example/demo)
expected="ghcr.io/example/demo:1.0.1
ghcr.io/example/demo:1.0
ghcr.io/example/demo:1
ghcr.io/example/demo:latest
ghcr.io/example/demo:main-$(git -C "$repo" rev-parse --short HEAD)"
assert_eq "full fan-out on mainline" "$expected" "$out"

out=$("$HANKO" --repo "$repo" stamp docker tags ghcr.io/example/demo --latest-on-default-branch=false --branch-sha-tag=false)
expected="ghcr.io/example/demo:1.0.1
ghcr.io/example/demo:1.0
ghcr.io/example/demo:1"
assert_eq "knobs turn off latest and branch-sha" "$expected" "$out"

section "hanko stamp docker tags — pre-release"
# feature/bar branches off main HEAD (which already has 1 commit past v1.0.0)
# and adds one more, so git describe sees 2 commits since v1.0.0 — the
# pre-release counter reflects that, not "commits on this branch".
git -C "$repo" checkout -q -b feature/bar
commit "$repo" three
short=$(git -C "$repo" rev-parse --short HEAD)
out=$("$HANKO" --repo "$repo" stamp docker tags ghcr.io/example/demo)
expected="ghcr.io/example/demo:1.0.0-feature-bar.2
ghcr.io/example/demo:feature-bar-$short"
assert_eq "prerelease emits only full + branch-sha" "$expected" "$out"

section "hanko stamp docker labels — args mode"
git -C "$repo" checkout -q main
out=$("$HANKO" --repo "$repo" stamp docker labels --source https://example.com/foo --title demo)
assert_contains "version label" "--label org.opencontainers.image.version=1.0.1" "$out"
assert_contains "revision label" "--label org.opencontainers.image.revision=" "$out"
assert_contains "source label" "--label org.opencontainers.image.source=https://example.com/foo" "$out"
assert_contains "title label" "--label org.opencontainers.image.title=demo" "$out"

section "hanko stamp docker labels — file mode"
labelfile=$(mktemp)
"$HANKO" --repo "$repo" stamp docker labels --output file --file "$labelfile"
content=$(<"$labelfile")
assert_contains "file contains version line" "org.opencontainers.image.version=1.0.1" "$content"

section "hanko stamp helm"
chart=$(mktemp -d)
mkdir -p "$chart/templates"
cat >"$chart/Chart.yaml" <<'YAML'
apiVersion: v2
name: demo
version: 0.0.0
appVersion: "0.0.0"  # trailing comment
YAML
# Repo for version computation:
helmrepo=$(mkrepo)
commit "$helmrepo" one
git -C "$helmrepo" tag v2.0.0
commit "$helmrepo" two
out=$("$HANKO" --repo "$helmrepo" stamp helm "$chart" --dry-run)
assert_contains "dry-run mentions version" "version: 0.0.0 → 2.0.1" "$out"
# File should not have changed on dry-run:
assert_contains "dry-run does not write" "version: 0.0.0" "$(<"$chart/Chart.yaml")"
"$HANKO" --repo "$helmrepo" stamp helm "$chart" >/dev/null
after=$(<"$chart/Chart.yaml")
assert_contains "applies version" "version: 2.0.1" "$after"
assert_contains "applies appVersion (quoted)" 'appVersion: "2.0.1"' "$after"
assert_contains "preserves trailing comment" "# trailing comment" "$after"

# ── summary ────────────────────────────────────────────────────────────────

printf "\n%d passed, %d failed\n" "$pass" "$fail"
if ((fail > 0)); then
  printf "failed tests:\n"
  for n in "${fail_names[@]}"; do printf "  - %s\n" "$n"; done
  exit 1
fi
