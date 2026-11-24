# System design

Status: target architecture. Version 0.0.0 shows a launch screen, then an auth destination, and a health check. It does not store chats or call a model.

## Purpose

Frontal is a cross-platform conversational and agentic application. A person can open it and talk to a high-quality model without managing weights. Their conversations, memory, attachments, and agent history stay on the device unless they opt into a cloud capability.

Android, iOS, and web ship from one Expo app. Desktop is a later decision.

## Principles

- The device is the system of record.
- The default intelligence experience is excellent. Local inference is the offline and private option.
- Privacy by default. Personal data leaves the device only after an explicit consent or an explicit provider choice.
- Cloud inference and cloud synchronization are separate consents.
- Model independence. The next turn can use a different runtime without moving the thread.
- Guest mode is the full local app. An account unlocks cloud sync, Histeeria, and frontier models.

## Requirements

| ID | Requirement |
| --- | --- |
| FR-01 | Google and email accounts, plus a guest session that does not require registration. |
| FR-02 | Create, manage, search, and delete conversations on the device. |
| FR-03 | Stream model output into the open thread. |
| FR-04 | Auto, Best quality, and Private / Offline, with frontier and custom providers behind Best quality and the cloud half of Auto. |
| FR-05 | Local memory by default. Histeeria is an optional cloud memory provider. |
| FR-06 | A user can configure a custom memory provider. |
| FR-07 | Custom model endpoints, and a managed on-device runtime for offline use. |
| FR-08 | File and image attachments stored on the device. |
| FR-09 | Organizations can create workspaces and manage members. |
| FR-10 | Organization storage is a separate trust domain and may require cloud by policy. |
| FR-11 | User settings and, later, organization settings. |
| FR-12 | Optional end-to-end encrypted sync across a user's devices. |

| ID | Requirement |
| --- | --- |
| NFR-01 | The interface and a prepared model response feel immediate. |
| NFR-02 | Cloud services scale only for the accounts that opt in. |
| NFR-03 | Encrypt data in transit. Encrypt the local database and synced ciphertext at rest. |
| NFR-04 | Tenant isolation for organization data. Personal plaintext stays on the device. |
| NFR-05 | A local thread survives an app restart. Sync must not be required to keep it. |
| NFR-06 | Model, memory, and storage providers are replaceable ports. |
| NFR-07 | Android, iOS, and web from the Expo app. |
| NFR-08 | A new model or memory provider does not reshape the client. |
| NFR-09 | Private / Offline runs without sending the prompt off the device, on hardware that can run a local model. |
| NFR-10 | One visual language. The chat reference is a near-black thread, gray bubbles, a pill composer, and a circular send control. |

## Intelligence modes

Auto is the default.

- Online, signed in, and cloud-inference consent on: a frontier model answers.
- Offline, and a local model is already prepared: that model answers.
- Offline, and no local model is prepared: the app explains the limit and can offer to prepare offline AI. It does not hold the prompt for a later silent cloud send.
- Best quality uses frontier models and requires sign-in plus cloud-inference consent.
- Private / Offline uses an on-device model even when the network is up. It never promotes a turn to the cloud.

A signed-in person who accepts cloud inference can chat immediately. They are not asked to choose or download a model. Sync stays off until they enable it.

Guests do not get frontier inference. Auto and Best quality keep the prompt on the device and show Login. Private / Offline is available to a guest once a local runtime exists.

## Accounts and guest mode

First launch shows a splash, then Google or email sign-in, with a guest entry. The next screen is a small settings screen, then chat. Later launches go from the splash to chat. Settings stay reachable from chat.

Signing in attaches existing guest threads to the account. It does not upload them.

## Not decided yet

- Which frontier provider ships in the first chat milestone.
- Whether a Frontal-held provider key is ever offered. The default path is device to provider.
- The Histeeria contract.
- The SQLCipher binding that passes the Expo spike.
- Which on-device runtime wins the offline spike: Android AICore, Gemma 3n, Apple Foundation Models, LiteRT-LM, or a hidden llama.cpp fallback.
- Production hosting and data residency for ciphertext and organization data.
- Desktop packaging.
