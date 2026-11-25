# Changelog

## Unreleased

M2 login and guest. No product release.

- Splash restores a session, then Google, email, or guest. Both paths continue to a settings destination with no toggles.
- Tradeoff: without Supabase env, cloud sign-in errors and guest still works, so CI never calls a production project.

## 0.0.0

Foundation only. No product release.

- Cursor rules, living docs, and ADRs describe a local system of record and an Auto intelligence default.
- The Expo app renders a placeholder. The Go API answers `GET /v1/health`.
- Tradeoff: the client and the API are scaffolded before any chat exists, so the gates are real and the product is not.
