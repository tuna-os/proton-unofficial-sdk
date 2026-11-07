# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) (0.x: the API may still change).

## [0.1.0] - 2026-11-07

The first release, extracted with its history from the `hylki-proton`
helper of the Hylki mail client (`native/proton` in tuna-os/hylki-fork).

### Added

- `protonsdk`, the Go package holding every operation: SRP login with
  TOTP, mailbox passwords and human verification; folder, label and header
  sync with keyset paging, an id snapshot and event-log changes; decrypted
  RFC 5322 messages; mark read, star and move with per-id outcomes;
  end-to-end encrypted sending with threading and uncertain-submit probing;
  server-side logout. Exported as `Request`, `RequestBytes` and `Serve`.
- The `events` operation, the poll for watching an account: Proton has no
  push channel for third-party clients.
- `cmd/proton-helper`, the stdin/stdout front end, identical when installed
  as `hylki-proton`.
- `org.tunaos.proton:proton-sdk-android`: the package bound by gomobile for
  arm64-v8a and x86_64 (minSdk 24) with a Kotlin wrapper — typed results,
  a sealed `ProtonException`, a polling `watch` flow, and the
  session-rotation rules enforced by `ProtonAccount`.
- CI: gofmt, vet and tests on x86_64 and aarch64, a helper smoke test, the
  gomobile build, the wrapper's JVM tests, and releases with the AAR and a
  Maven repository zip.
