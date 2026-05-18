# Examples

Sketches of how hanko is expected to be consumed downstream.
Some are runnable today, some still wait on features described in ROADMAP.md.
Update these as the binary acquires features — when hanko reaches v1, this folder should match what people actually copy-paste into their repos.

## Inventory

- [`migrations/`](./migrations/) — concrete before/after for cicd's shared workflows (`build-package.yml`, `autotag.yml` + `tag.yml`).
  Start here if you want to understand what hanko *buys* in production CI.
- [`cicd-composite-action/`](./cicd-composite-action/) — drop-in replacement shape for `cicd/.github/actions/resolve-version`.
  Useful when a caller wants the structured-fields contract (separate `full`, `major`, `minor`, …) rather than calling hanko directly.
  Many migrations in `migrations/` skip it in favour of inline `hanko` calls — see [D-010](../docs/design-decisions.md#d-010).
- [`cicd-reusable-workflow/`](./cicd-reusable-workflow/) — thin reusable-workflow wrapper that replaces `cicd/.github/workflows/gitversion.yml`.
  Same caveat: many migrations don't need this layer.
- [`local-usage.md`](./local-usage.md) — how a developer would invoke hanko on their workstation.
  Read-only by default, no surprises.
