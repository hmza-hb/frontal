# ADR 0007: Launch sequence without a router

Date: 2026-11-24
Status: accepted

## Context

M1 needs a launch screen that then hands off to auth. The client is still a single Expo root. Auth, settings, and chat do not exist yet. Adding a navigation library now would pull Expo Router or React Navigation into a milestone that must not implement account logic.

## Decision

Keep launch as an in-app screen enum in the Expo root.

- Native splash uses `expo-splash-screen` with a near-black background while JavaScript loads.
- After the native splash hides, the JS splash holds for a short, tested duration.
- The next screen is an auth destination with no Google, email, or guest actions.
- Expo Router and React Navigation wait until more than two product screens exist.

## Alternatives

- Expo Router in this milestone. Rejected because M1 has one transition and must not start the login milestone.
- A multi-second branded delay. Rejected because the interface should feel immediate.
- Jumping from native splash straight into login UI. Rejected because M1's requirement is an explicit launch screen and a handoff, not account flows.

## Consequences

M2 replaces the auth destination contents and does not have to unwind a router. A later ADR can introduce Expo Router when splash, auth, settings, and chat all exist. Tests control the hold with fake timers. Web proves the handoff in Playwright. Native splash appearance still needs a device build to match production.
