# ADR 0005: Provider ports

Date: 2026-11-22
Status: accepted

## Context

Models, memory systems, and storage will change. Histeeria, custom endpoints, organization databases, and on-device runtimes need to arrive without a rewrite of the chat screen.

## Decision

Define three ports:

- Model: given a context package and a mode, stream a reply and a runtime id.
- Memory: store and retrieve snippets on device, with an optional cloud adapter.
- Storage: read and write attachment bytes, local first.

Local memory is the default memory adapter. Histeeria is a later adapter and waits on its real contract. No Histeeria protocol is invented here. Custom model endpoints use the model port with a caller-supplied base URL and credential, behind the same sign-in gate as other frontier routes.

Organization storage, when it exists, is another storage adapter under organization policy. It does not replace the personal local record.

## Alternatives

- Call each provider SDK from the chat component. Rejected because the screen would change for every vendor.
- One cloud memory service as the only memory implementation. Rejected because offline memory is a product requirement.

## Consequences

New providers are adapters plus contract tests. The chat milestone can ship against a fake model port. Histeeria work cannot start on a guessed API.
