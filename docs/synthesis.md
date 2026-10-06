# Synthesis backends

AI synthesis is off until you enable it and save **Settings**. The [README](../README.md#optional-ai-synthesis) describes what synthesis does, and [Data and privacy](data-and-privacy.md) describes what it sends. This page lists the model behavior of each backend.

The Settings dialog offers a short model list per backend. You can set any model that the selected CLI can reach in `settings.json`, including a model behind an API proxy such as `ANTHROPIC_BASE_URL` or a third-party provider.

## OpenCode

The model list includes *OpenCode default for a new run* and, when the installed CLI can list them, free OpenCode Zen models. The default option passes no model. OpenCode v2 selects its current catalog default in a fresh, isolated process. OpenCode v1 can instead use a model from your configuration. Either model can differ from the model of an existing OpenCode session. If the resolved model is paid, each debrief bills your account.

## Pi

Pi synthesis is verified with Pi 0.99.2 on macOS and Pi 1.0.0 with an offline provider on Windows. The default option uses the provider and model that Pi has configured for a new run, with your existing authentication and provider environment.

To pin a model, set `synthesis.backend` to `pi-cli` and `synthesis.model` to a provider-qualified ID in `settings.json`, for example `amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0`.

Provider access errors and expired credentials show as synthesis failures in the inspector. Refresh the credentials (for example, `aws sso login --profile <profile>`), then retry synthesis. The resolved model can use paid account usage.

## Grok Build

Grok synthesis is available on macOS and Windows. It uses the installed `grok` CLI and your existing Grok login. The default model is `grok-4.7` with high effort. If you choose `default`, the CLI selects the model. The resolved model can use paid Grok usage.

coSlash stores the token totals and cost when Grok reports them. If the Grok result omits spend, coverage stays unknown.
