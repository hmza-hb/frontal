# ADR 0002: Local system of record

Date: 2026-11-22
Status: accepted

## Context

Conversations, memory, attachments, agent history, and settings are personal data. The product must work as a private record. Cloud backup is optional and must not be required to keep a thread.

## Decision

The device is the system of record.

- SQLite with SQLCipher for structured data.
- The app filesystem for attachments.
- A local vector index for semantic memory.
- Operating-system secure storage for the database key and credentials.

Uninstall removes the app sandbox. Operating-system backups may still contain it, and the product will say so.

The chat milestone starts with a SQLCipher spike. If the binding cannot ship through Expo on a target platform, a new ADR replaces this storage choice before feature code depends on it.

Sign-in attaches guest threads to an account on the device. It does not upload them.

## Alternatives

- Supabase Postgres as the primary store. Rejected because a chat would then depend on an account and a network.
- Unencrypted SQLite. Rejected because a lost or borrowed device would expose the archive in plaintext.
- Bundling the encryption key in the app. Rejected because anyone with the binary could open every database.

## Consequences

Sync, when it exists, copies ciphertext of a record that already lives on the device. Features cannot assume a server round trip. The SQLCipher spike can still force a replacement ADR, so M0 does not pretend the database exists.
