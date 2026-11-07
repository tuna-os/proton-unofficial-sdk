# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) (0.x: the API may still change).

## [0.1.2] - 2026-11-07

### Added

- `search`: server-side search over message metadata — keyword, subject,
  sender, recipient, a date range, unread and starred — in a folder or
  label (All Mail by default), paged by cursor. The criteria go to
  Proton's own search parameters and every hit is re-checked on its
  metadata. Bodies and attachments are end-to-end encrypted and cannot be
  searched; the README says what can.
- Kotlin: `ProtonAccount.search` with `ProtonSearchQuery` and
  `ProtonSearchResult`.

## [0.1.1] - 2026-11-07

### Added

- `delete`: permanent delete of up to 50 ids, with per-id outcomes.
- `empty_folder`: empties Trash or Spam a page at a time, answering
  `more` when a large folder needs another run.
- `label`: adds or removes a user label on up to 50 ids.
- `save_draft`: writes an encrypted draft to Drafts, with attachments and
  threading, and deletes the draft it `replaces`.
- `create_folder`, `rename_folder`, `delete_folder` for user folders and
  labels, and the `folder_readonly` code for system folders.
- Kotlin: `ProtonAccount.delete`, `emptyFolder`, `label`, `saveDraft`,
  `createFolder`, `renameFolder`, `deleteFolder`, with `ProtonDraft`,
  `ProtonDraftResult`, `ProtonEmptyResult` and
  `ProtonErrorCode.FOLDER_READONLY`.
- Tests that the README's error-code table covers every code the Go
  library produces and matches the Kotlin `ProtonErrorCode` exactly.

### Changed

- `send` and `save_draft` share one draft-writing path; `send`'s
  behaviour and codes are unchanged.

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
