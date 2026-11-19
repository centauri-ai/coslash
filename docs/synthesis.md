# Synthesis backends

coSlash derives goals, outcomes, timelines, and artifacts from transcripts without a model. Optional AI synthesis adds model-generated detail to these debriefs.

Synthesis is off until you enable it and save **Settings**. It uses a local Claude Code, Codex, OpenCode, Cursor, Pi, or Grok Build CLI with that CLI's existing account. Paid models bill that account. Only substantial sessions qualify, and coSlash caches results under `~/.coslash`. See [Data and privacy](data-and-privacy.md#outbound-data) for the input limits and what synthesis sends.

The Settings dialog offers a short model list per backend. You can set any model that the selected CLI can reach in `settings.json`, including a model behind an API proxy such as `ANTHROPIC_BASE_URL` or a third-party provider.

## OpenCode

Settings prefers *OpenCode default for a new run*, while preserving existing explicit model selections. The list also includes free OpenCode Zen models when the installed CLI can list them. For the default option, OpenCode v2 resolves your configured default through a separate probe with external plugins and project configuration disabled. Synthesis receives only the selected model and its provider configuration, in a fresh, isolated process. OpenCode v1 resolves its default through its existing configuration. The default for new work can differ from the model of an existing session. If the resolved model is paid, each debrief bills your account.

OpenCode v2 synthesis uses a hard-deny execution policy. The model can see tool definitions, but every tool execution is blocked, including reads and writes. This preserves the no-tools boundary without the free-provider rejection that permission-based tool removal can trigger. The CLI still writes its temporary runtime data to isolated storage.

## Pi

Pi synthesis is verified with Pi 0.99.2 on macOS and Pi 1.0.0 with an offline provider on Windows. The default option uses the provider and model that Pi has configured for a new run, with your existing authentication and provider environment.

To pin a model, set `synthesis.backend` to `pi-cli` and `synthesis.model` to a provider-qualified ID in `settings.json`, for example `amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0`.

Provider access errors and expired credentials show as synthesis failures in the inspector. Refresh the credentials (for example, `aws sso login --profile <profile>`), then retry synthesis. The resolved model can use paid account usage.

## Grok Build

Grok synthesis is available on macOS and Windows. It uses the installed `grok` CLI and your existing Grok login. The default model is `grok-4.7` with high effort. If you choose `default`, the CLI selects the model. The resolved model can use paid Grok usage.

coSlash stores the token totals and cost when Grok reports them. If the Grok result omits spend, coverage stays unknown.
