# ADR 0004: Optional end-to-end encrypted sync

Date: 2026-11-22
Status: accepted

## Context

People will want a second device. A server-side copy of plaintext chats would make the cloud the system of record and would make Frontal able to read the archive. Inference and backup are different decisions: a person may send one turn to a frontier model and still refuse a backup.

## Decision

Sync is opt-in and defaults off. It is independent of intelligence mode, cloud-inference consent, and Histeeria.

The client encrypts conversations, attachments, memory, and settings before upload. Postgres stores ciphertext. Keys remain in the user's control. The server has no plaintext path. Another signed-in device downloads ciphertext and decrypts it.

Account rows and ciphertext blobs still use row level security so accounts cannot read each other.

## Alternatives

- Plaintext rows in Postgres with row level security. Rejected because a database operator could read the archive.
- Treating a frontier request as permission to sync the thread. Rejected because the consents mean different things.
- Making sync mandatory for sign-in. Rejected because an account is identity, not a backup.

## Consequences

The sync milestone has to build client-side key management, not only upload. Lost keys mean the ciphertext cannot be restored by Frontal. Until that milestone, the settings toggle may persist the preference and must not pretend a backup is running.
