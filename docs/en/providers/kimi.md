# Kimi (Moonshot) Setup

## Get API Key

Choose the key source for your product:

- Pay-as-you-go API: create a key in [Kimi Open Platform](https://platform.kimi.com/).
- Kimi Code: create a key in the [Kimi Code console](https://www.kimi.com/code/console). Up to five keys are supported and the full value is shown only once.

## Official Endpoints

Kimi supports both OpenAI and Anthropic protocols. Kimi Code uses the same key for both endpoints, while the model list is filtered by the key's membership entitlement:

| Protocol | Base URL | Common endpoint |
|---------|----------|-----------------|
| OpenAI (pay-as-you-go) | `https://api.moonshot.cn/v1` | `https://api.moonshot.cn/v1/chat/completions` |
| Anthropic (Kimi Code) | `https://api.kimi.com/coding/` | `https://api.kimi.com/coding/v1/messages` |
| OpenAI (Kimi Code) | `https://api.kimi.com/coding/v1` | `https://api.kimi.com/coding/v1/chat/completions` |

When Kimi is added from CCX's automatic provider flow (also available as `Kimi Code`), CCX validates and binds an endpoint per key. Kimi Code models are discovered with `GET /coding/v1/models`.

## Kimi Code model entitlements

| Membership | Models | Context window |
|------------|--------|----------------|
| Andante | `kimi-for-coding` | 1M |
| Moderato | `k3`, `kimi-for-coding` | K3 256K; `kimi-for-coding` 1M |
| Allegretto and above | `k3`, `kimi-for-coding`, `kimi-for-coding-highspeed` | Up to 1M for K3; `kimi-for-coding` 1M; highspeed 256K |

Both `k3` and `kimi-for-coding` support `low`, `high`, and `max` reasoning effort (default `high` for `k3`, default `max` for `kimi-for-coding`). Disabling thinking routes K3-series and `kimi-for-coding` requests to K2.8 Preview (non-thinking); thinking stays enabled server-side for `kimi-for-coding-highspeed`.

The `k3[1m]` form is only a Claude Code environment-variable hint. API requests and CCX model fields use `k3`.

## Add Channel in CCX

### Automatic Kimi Code setup (recommended)

Select **Kimi** / **Kimi Code** and enter one or more Kimi Code API keys. CCX probes `https://api.kimi.com/coding/v1/models` per key instead of sending a fake inference request, so models outside the membership tier are not listed.

### Option 2: Chat Endpoint (OpenAI Compatible)

| Field | Value |
|-------|-------|
| Name | `Kimi` |
| Service Type | `openai` |
| Base URL | `https://api.moonshot.cn/v1` |
| API Keys | Your Moonshot API Key |

### Option 3: Messages Endpoint (Anthropic Compatible)

For Claude Code CLI and other tools using Claude Messages protocol.

| Field | Value |
|-------|-------|
| Name | `Kimi Code` |
| Service Type | `claude` |
| Base URL | `https://api.kimi.com/coding/` |
| API Keys | Your Kimi Code API Key |

Kimi Code does not require mapping `opus`, `sonnet`, or `haiku` to a Claude model name. Prefer the model IDs returned by `/coding/v1/models`; configure an explicit CCX mapping when a client sends a generic alias.

## Available Models

| Model | Description |
|-------|-------------|
| `k3` | Kimi K3 flagship coding model; 256K on Moderato and up to 1M on Allegretto+; supports `low` / `high` / `max` |
| `kimi-for-coding` | Kimi K2.8 Preview (model ID unchanged since 2026-09, underlying model upgraded); overall performance close to K3, 1M context, supports `low` / `high` / `max` (default `max`), available to all memberships |
| `kimi-for-coding-highspeed` | Kimi K2.7 Code HighSpeed, about 5-6x faster output with higher usage; Allegretto+ |
| `kimi-k2.7` | Latest pay-as-you-go model, native multimodal Agentic model |
| `kimi-k2.6` | Multimodal Agentic model, 1T total / 32B active |
| `kimi-k2.5` | Multimodal Agentic model |
| `moonshot-v1-auto` | Auto-selects context length (legacy) |
| `moonshot-v1-128k` | 128K context (legacy) |

::: warning Deprecation Notice
`kimi-k2` will be retired on **2026/05/25**. Please migrate to `kimi-k2.6`.
:::

## Notes

- The China pay-as-you-go OpenAI-compatible Base URL is `https://api.moonshot.cn/v1`; CCX also retains compatibility with the global `https://api.moonshot.ai/v1` endpoint
- Kimi Code Anthropic Base URL is `https://api.kimi.com/coding/`; its OpenAI Base URL is `https://api.kimi.com/coding/v1`
- Kimi Code `/coding/v1/models` returns the model range for the current key. A 401/403 from that endpoint usually means the key is invalid or unusable; an inference request can also return 401 when its model or context entitlement is unavailable
- `kimi-k2.7` is the latest pay-as-you-go model with long-context coding support
- `kimi-for-coding` and `kimi-for-coding-highspeed` should only be used with the Kimi Code endpoint
