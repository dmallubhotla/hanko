# executes default, set to listing commands
default:
    just --list

# build the hanko binary via nix
build:
    nix build

# run go tests via nix develop
test:
    nix flake check
    # go test ./...

# end-to-end CLI smoke tests (verifies command shape on minimal repos)
smoke:
    nix develop --command bash test/smoke/smoke.sh

# end-to-end flow tests (mocks realistic tag histories, multi-branch scenarios, push-to-remote, end-to-end stamping)
flows:
    nix develop --command bash test/flows/flows.sh

# run smoke + flows
check-cli: smoke flows

# (re)build dev fixtures under ./fixtures (gitignored)
fixtures:
    bash test/fixtures/init.sh

# run nix flake check
check:
    nix flake check

# format files
fmt:
    nix fmt

# update flake inputs
update:
    nix flake update

# regenerate gomod2nix.toml and tidy go modules
chores:
    nix develop --command go mod tidy
    nix develop --command gomod2nix

# release: bump flake.nix to the hanko-computed semver, commit, tag, push.
# Uses hanko from the devshell (self-dogfood). If hanko's source is broken,
# fall back: `nix develop --command go run . stamp nix` etc.
release:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -n "$(git status --porcelain)" ]; then
        echo "worktree dirty; commit or stash before releasing" >&2
        exit 1
    fi
    nix develop --command hanko stamp nix
    ver=$(nix develop --command hanko version)
    git add flake.nix
    git commit -m "Release ${ver}"
    nix develop --command hanko tag --push
