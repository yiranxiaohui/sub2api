# Release-only GitHub Actions design

## Goal

Only release operations should run in GitHub Actions. Ordinary pushes and pull
requests must not start CI, security scanning, packaging, or CLA workflows.

## Change

Delete the three non-release workflow files:

- `.github/workflows/backend-ci.yml`
- `.github/workflows/security-scan.yml`
- `.github/workflows/cla.yml`

Keep `.github/workflows/release.yml` unchanged. It remains triggerable by:

- tags matching `v*`; and
- `workflow_dispatch` with an explicit release tag.

## Resulting behavior

Commits and pull requests do not trigger GitHub Actions. Release tags and
manual release dispatches continue to build the frontend, run GoReleaser,
publish container/release artifacts, synchronize the version file, and send
configured notifications.

## Verification

- Confirm `.github/workflows/` contains only `release.yml`.
- Parse the remaining workflow as YAML.
- Confirm its trigger configuration still contains only the existing `v*` tag
  push and manual dispatch paths.
- Confirm no unrelated files are modified.
