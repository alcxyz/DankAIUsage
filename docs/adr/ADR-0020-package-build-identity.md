# ADR-0020: Stamp development identities into installed packages

**Status:** Accepted
**Date:** 2026-09-19
**Applies to:** manual packaging, Nix packaging, helper diagnostics

## Context

DMS reads its plugin version from `plugin.json`, while the helper receives a
separate compiled version. Installing a development source directory alongside
a helper reporting the manifest's release number makes QA builds look released.
Nix build sandboxes do not retain Git metadata.

## Decision

- Keep the tracked manifest version strictly `X.Y.Z`. The shared CI workflow
  continues to create immutable `vX.Y.Z` releases from `main`; no dev tags or
  workflow changes are needed.
- Default packages to `X.Y.Z-dev.<12-character-commit>`, adding `.dirty` for
  modified source. Stamp a copied manifest and the helper with exactly the same
  string. Never rewrite the source manifest as part of building.
- Package the helper under `bin/` and its matching plugin under
  `share/dms-plugins/DankAIUsage/`. DMS wiring must use the packaged directory.
- Offer a Python standard-library packaging command for manual installation.
  Manual `--release` requires a clean checkout at the manifest's exact release
  tag. Ordinary un-packaged `go build` uses Go's embedded VCS information to
  report `dev-<commit>[-dirty]`, falling back to `dev` without that information.
- Pass Git revision metadata explicitly to Nix builds. Default outputs are
  development builds; a deliberate `release` output/override uses the stable
  version and must be built from the published release source. Nix cannot infer
  release provenance from a source tree or commit hash alone. Reject dirty or
  unidentified revisions for that output.
- When no Git revision is available, Nix uses the existing public-source
  fingerprint (`X.Y.Z-dev.source.<fingerprint>`); manual source archives use
  `X.Y.Z-dev.unknown`. Never label unidentified development source as released.
- Keep diagnostic validation restricted to these public build formats. Read
  dirty metadata independently of its ordering in Go's build settings.

## Alternatives and consequences

Committing dev hashes into `plugin.json` would interfere with shared release
automation. Changing only the helper would leave DMS reporting a different
version. Per-commit Git tags add no information needed for build identification.

DMS's manifest schema accepts semver prereleases and its plugin browser displays
the string directly. This does not change registry update policy or automatically
switch a registry-managed installation to the development branch. Release builds
remain explicit rather than guessing a Git branch inside a sandbox.
