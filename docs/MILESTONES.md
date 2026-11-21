# Milestones

Each milestone is one issue, one branch, tests, and a pull request. A later milestone does not start inside an earlier one. M1 starts after M0 is merged.

## M0 — Foundation

Engineering discipline, living docs, ADRs, CI, a Go health check, and a web smoke test. No product screens. This is the current milestone.

## M1 — Splash

Launch screen, then hand off to auth. No account logic.

## M2 — Login and guest

Google and email through Supabase Auth, plus a guest entry. Session restore. Both paths continue toward settings. The guest session is local only.

## M3 — Settings

A small screen for guest and signed-in users. Persist Auto, Best quality, or Private / Offline, local memory, permission defaults, cloud-inference consent, and sync. A guest sees the cloud controls locked. Defaults are Auto, inference consent off, and sync off. Saving a local toggle makes no network call.

## M4 — Local record and chat

SQLCipher spike, the OS keystore, conversations, and messages. Chat UI for guest and signed-in users, using the dark thread, gray bubbles, pill composer, and circular send control. The guest nav keeps Login. Messages survive a restart. No model call yet.

## M5 — Frontier chat

Signed-in Auto and Best quality call one managed frontier provider with the context package and stream tokens into the transcript. Cloud-inference consent is required. Sync stays unused. A guest send in Auto does not leave the device. The assistant message records the runtime. No model download and no model picker.

## M6 — Offline intelligence

Model Management System: device capability, an OS runtime where one exists, and a managed background download only after the user opts into offline AI. Private mode and offline Auto use that runtime. A prepared model answers with the network disabled. An unprepared device explains the limit. Private mode does not call the frontier provider when the network is up.

Spike candidates, not a locked choice: Android AICore and Gemma 3n, Apple Foundation Models, LiteRT-LM, and llama.cpp as a hidden fallback. No bundled model.

## M7 — Local memory

Memory port, an on-device index, and retrieval into the context package. Available offline.

## M8 — Agent runtime

Local plan and tool loop, task history, and permission checks on the same transcript as chat. A frontier model can plan when cloud-inference consent is on. Private mode stays on the device.

## M9 — Attachments

Images and files on the local filesystem. A context package includes a file only when that message or an approved tool needs it.

## M10 — More providers

Anthropic, Google, and custom base URLs under the same consent and context-package rules. Switching the next turn does not move history. A guest is sent to Login.

## M11 — Optional end-to-end encrypted sync

Wire the settings toggle. Client-side keys, ciphertext in Postgres, and decrypt on a second device. The toggle is independent of intelligence mode and defaults off.

## M12 — Histeeria

Optional cloud memory and evals behind the memory port. Authorization is separate from sync and from cloud inference. Signed-in only. This milestone waits on the real Histeeria contract.

## M13 — Organizations

Workspaces, members, and an organization policy that can require cloud. Personal devices keep local records and the same mode switches.

## M14 — Desktop and web local inference

Separate ADRs. An on-device runtime is not promised on web or desktop until a spike says it works.
