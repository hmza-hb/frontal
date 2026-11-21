# ADR 0003: Model management and intelligence modes

Date: 2026-11-22
Status: accepted

## Context

A local-first archive is not a useful product if the first answer is a weak model the person had to download and configure. Frontal has to compete on answer quality and still offer offline and private inference without a silent privacy break.

## Decision

The default mode is Auto. Settings also offer Best quality and Private / Offline.

- Auto, online, signed in, cloud-inference consent on: a frontier model.
- Auto, offline, local model already prepared: that local model.
- Auto, offline, nothing prepared: explain the limit and offer offline setup. Do not queue the prompt for a later cloud send.
- Best quality: frontier models, sign-in and cloud-inference consent required.
- Private / Offline: on-device only, including while the network is up.

Private mode never promotes a turn to the cloud. A turn already answered on device is not resent. Auto may use a frontier model on a later turn when the device is online and consent is already on. The transcript records the runtime.

A Model Management System detects capability, discovers runtimes, selects one, downloads and updates a curated model only after the user opts into offline AI, and watches performance. Users do not see GGUF files, sizes, quantization, or GPU settings. No model ships inside the installer. If the device cannot run a local model, the product says so.

Cloud-inference consent is separate from sync. A consented call sends the context package from the device to the provider. Guests do not get frontier inference.

The offline milestone spikes Android AICore and Gemma 3n, Apple Foundation Models, LiteRT-LM, and a hidden llama.cpp fallback. Those names are candidates, not a locked runtime.

## Alternatives

- Local inference as the default, with a model download during onboarding. Rejected because ordinary setup would be GGUF choice, multi-gigabyte downloads, and hardware troubleshooting.
- Silent fallback from Private mode to a frontier model when the local model fails. Rejected because it breaks the privacy promise.
- Bundling a tiny model in the installer. Rejected for the first release because it inflates install size before the offline path is proven.
- Letting the user pick quantization. Rejected because model management is the product's job.

## Consequences

The first useful chat milestone is a frontier provider, not an on-device runtime. Offline quality depends on whatever the spike can run, and some devices will have no offline mode. The context package stays the only payload that can leave the device for inference.
