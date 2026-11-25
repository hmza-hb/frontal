# ADR 0008: Auth sessions and guest

Date: 2026-11-25
Status: accepted

## Context

M2 needs Google and email sign-in, a guest path, and session restore. Chats must stay on the device. Tests must not call a production Supabase project. Settings and chat are later milestones.

## Decision

- `AuthPort` is the only way screens start a session. Tests inject an in-memory adapter.
- A guest session is local. It never calls Supabase. It has no user id.
- Google and email use Supabase Auth when `EXPO_PUBLIC_SUPABASE_URL` and `EXPO_PUBLIC_SUPABASE_ANON_KEY` are set. Email uses password sign-in. Google uses the OAuth provider on the port.
- If those values are missing, guest still works. Cloud sign-in returns a configuration error.
- Restore reads the last session. A restored guest or signed-in session skips auth and continues to a settings destination. That destination has no toggles until M3.
- Signing in does not upload threads.

## Alternatives

- Magic-link email. Rejected for M2 because unit and Playwright tests cannot receive mail.
- Treating guest as a Supabase anonymous user. Rejected because guest must work offline and must not create a cloud identity.
- Implementing settings in this milestone. Rejected because M3 owns preferences.

## Consequences

Auth UI talks to a port, not to Supabase types. A later store can persist guest across process death. Native Google OAuth still needs provider keys in a configured project. The launch enum gains a settings screen and still has no router.
