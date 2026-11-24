# Security model

Status: target controls. Version 0.0.0 stores no personal data and exposes only a public health check.

## Local record

The planned database is SQLite encrypted with SQLCipher. The key lives in operating-system secure storage, not in the database file and not in the client bundle. Attachments live in the app sandbox. Provider credentials live in secure storage.

Uninstall removes the app sandbox. The operating system may still restore a device backup that includes that sandbox. Frontal cannot promise that an OS backup disappears with the app. The settings and security copy will say so when those screens exist.

## Sessions

Google and email sign-in use Supabase Auth from the login milestone onward. A guest session is local. It has no server identity and cannot call a frontier provider, enable sync, or enable Histeeria.

Signing in does not upload threads. It attaches the local guest threads to the account.

## Inference

Cloud-inference consent is separate from sync and defaults off.

A consented frontier call contains the context package only. The full history, the memory database, and files that are not part of the package are not uploaded. The call goes from the device to the provider. The Go API is not a relay for that call.

Private / Offline does not make that call. Auto does not upgrade a private or unanswered offline turn into a cloud call.

A Frontal-brokered provider key is not part of the current design. Adding one requires an ADR, because Frontal would then see the context package.

## Sync

Optional sync is end-to-end encrypted. The client encrypts before upload. Postgres stores ciphertext. Keys stay with the user. The server is not given a path to plaintext personal data.

Row level security still applies to ciphertext and account rows so one account cannot read another account's blobs. The policy is written into this document when the first table lands.

## Histeeria and organizations

Histeeria receives personal data only after its own authorization. Organization workspaces are a later trust domain. An organization policy may require cloud. That policy does not change the personal device record or the personal mode switches.

## Version 0.0.0

`GET /v1/health` returns a fixed status and accepts no body. It has no authentication because it reveals no tenant data. The launch sequence has no network call and no local database. The auth destination does not collect credentials.
