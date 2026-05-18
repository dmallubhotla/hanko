# Local usage

Read-only by default.
Nothing here writes to the repo or the network.

## Print the current version

```sh
hanko version
# → 1.2.3-feature-foo.4
```

## Get the full SemVer with build metadata

```sh
hanko version --format full
# → 1.2.3-feature-foo.4+4.a1b2c3d
```

## Dump the whole thing as JSON

```sh
hanko version --format json
# → { "major": 1, "minor": 2, ... }
```

## Source environment variables into a shell

```sh
eval "$(hanko version --format env)"
echo "$HANKO_SEMVER"
```

## Operate on a different repo

```sh
hanko --repo /path/to/other/repo version
```

## Tagging

```sh
# Compute the version and create v<semver> as an annotated tag on HEAD.
hanko tag

# Same, plus push to origin (use --remote to push elsewhere).
hanko tag --push

# Show what would happen without doing it.
hanko tag --dry-run

# Tag a pre-release version (refused by default to keep mainline tags clean).
hanko tag --allow-prerelease-tag
```

## Stamping

```sh
# Emit ldflags ready to splice into `go build`.
go build -ldflags "$(hanko stamp go-ldflags)" ./...

# Fan-out container tags for an image.
hanko stamp docker tags ghcr.io/example/demo

# OCI labels as `--label key=value` args (default) or as a label file.
hanko stamp docker labels --source https://github.com/example/demo --title demo
hanko stamp docker labels --output file --file labels.txt

# Edit a chart's Chart.yaml in place.
hanko stamp helm ./charts/foo
hanko stamp helm ./charts/foo --dry-run
```
