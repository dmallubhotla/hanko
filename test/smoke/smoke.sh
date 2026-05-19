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
assert_eq "2 commits past tag on main → 1.2.5" "1.2.5" "$got"

got=$("$HANKO" --repo "$repo" version --format gha | LC_ALL=C sort)
expected=$(printf "branch=main\nfull=1.2.5\nis-prerelease=false\nmajor-minor=1.2\nmajor=1\nminor=2\npatch=5\nshort-sha=%s" "$(git -C "$repo" rev-parse --short HEAD)")
assert_eq "--format gha shape" "$expected" "$got"

section "hanko version on a feature branch"
git -C "$repo" checkout -q -b feature/foo
commit "$repo" four
# Latest reachable tag from feature/foo is still v1.2.3 (4 commits ago counting
# the two on main + one on the branch + branch-tip = 3). git describe is
# reachable-only so the main-branch advance counts toward this branch too.
got=$("$HANKO" --repo "$repo" version)
assert_eq "feature branch is a pre-release on the tag base" "1.2.3-feature-foo.3" "$got"

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
