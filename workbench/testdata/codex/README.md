# Codex rendering fixtures

These resources are a snapshot of the Codex extension used by Pencode's renderer
regression tests. They belong to the host tests and do not require a checked-out
extension or follow changes in the extension repository automatically.

Source: https://github.com/pencode-labs/codex
License: MIT (see LICENSE).

Tests that build and run the actual extension are in
`codex_integration_test.go`, behind the `codex_integration` build tag.
Initialize `extensions/codex` before running them.
