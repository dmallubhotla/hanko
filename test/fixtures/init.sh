#!/usr/bin/env bash
#
# Materialise dev fixtures under fixtures/ in the repo root. Idempotent:
# nukes and rebuilds. Output is gitignored.
#
# Used for hand-running hanko against realistic-shaped inputs (helm chart,
# go program, container build context). Distinct from test/smoke/smoke.sh
# which builds throwaway tmp repos for hermetic assertions.
#
# Usage:
#   test/fixtures/init.sh             # rebuild all fixtures
#   test/fixtures/init.sh helm        # rebuild a single fixture
#
# Each fixture is a self-contained git repo so `hanko version`/`hanko stamp`
# work as they would in production.

set -euo pipefail

ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
FIX="$ROOT/fixtures"

# Deterministic git env across all fixtures so tags + shas are reproducible.
export GIT_AUTHOR_DATE="2026-01-01T00:00:00Z"
export GIT_COMMITTER_DATE="2026-01-01T00:00:00Z"
export GIT_AUTHOR_NAME="fixture"
export GIT_AUTHOR_EMAIL="fixture@example.invalid"
export GIT_COMMITTER_NAME="fixture"
export GIT_COMMITTER_EMAIL="fixture@example.invalid"

mkrepo() {
  local dir="$1"
  mkdir -p "$dir"
  git -C "$dir" init -q --initial-branch=main
  git -C "$dir" config user.email fixture@example.invalid
  git -C "$dir" config user.name  fixture
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" config tag.gpgsign    false
}

# ── go-app ────────────────────────────────────────────────────────────────
# A tiny program with var version / commit / date so we can demo
# `hanko stamp go-ldflags` end-to-end:
#     go build -ldflags "$(hanko stamp go-ldflags --repo fixtures/go-app)" -o /tmp/demo ./fixtures/go-app
init_go_app() {
  local dir="$FIX/go-app"
  rm -rf "$dir"
  mkrepo "$dir"

  cat > "$dir/go.mod" <<'EOF'
module example.invalid/demo

go 1.24
EOF

  cat > "$dir/main.go" <<'EOF'
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

  git -C "$dir" add -A
  git -C "$dir" commit -q -m "initial commit"
  git -C "$dir" tag v0.1.0
  echo "// placeholder" >> "$dir/main.go"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m "another change"
}

# ── helm-chart ────────────────────────────────────────────────────────────
# Minimal valid Helm chart for `hanko stamp helm fixtures/helm-chart`.
init_helm_chart() {
  local dir="$FIX/helm-chart"
  rm -rf "$dir"
  mkrepo "$dir"

  cat > "$dir/Chart.yaml" <<'EOF'
apiVersion: v2
name: demo
description: A tiny demo chart for hanko fixtures
type: application
version: 0.0.0
appVersion: "0.0.0"
EOF

  mkdir -p "$dir/templates"
  cat > "$dir/templates/configmap.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-demo
data:
  app-version: "{{ .Chart.AppVersion }}"
EOF

  git -C "$dir" add -A
  git -C "$dir" commit -q -m "initial commit"
  git -C "$dir" tag v1.4.0
  echo "# trailing comment" >> "$dir/Chart.yaml"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m "trivial edit past tag"
  git -C "$dir" commit --allow-empty -q -m "second commit past tag"
}

# ── docker-image ──────────────────────────────────────────────────────────
# Containerfile context for `hanko stamp docker labels` and tag fan-out.
init_docker_image() {
  local dir="$FIX/docker-image"
  rm -rf "$dir"
  mkrepo "$dir"

  cat > "$dir/Containerfile" <<'EOF'
FROM docker.io/library/alpine:3.20
CMD ["echo", "hello from hanko fixture"]
EOF

  cat > "$dir/README.md" <<'EOF'
# docker-image fixture

Tag fan-out demo:

    hanko --repo fixtures/docker-image stamp docker tags ghcr.io/example/demo

Label emission:

    hanko --repo fixtures/docker-image stamp docker labels
EOF

  git -C "$dir" add -A
  git -C "$dir" commit -q -m "initial"
  git -C "$dir" tag v2.1.0
  git -C "$dir" commit --allow-empty -q -m "one past tag"
  git -C "$dir" commit --allow-empty -q -m "two past tag"
}

# ── dispatch ──────────────────────────────────────────────────────────────
declare -a WANT=("${@:-go-app helm-chart docker-image}")
WANT=(${WANT[@]})

mkdir -p "$FIX"
for f in "${WANT[@]}"; do
  case "$f" in
    go-app)       init_go_app       ; echo "built $FIX/go-app" ;;
    helm-chart)   init_helm_chart   ; echo "built $FIX/helm-chart" ;;
    docker-image) init_docker_image ; echo "built $FIX/docker-image" ;;
    *) echo "unknown fixture: $f" >&2; exit 2 ;;
  esac
done
