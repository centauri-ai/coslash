# Security Policy

## Reporting a vulnerability

Report vulnerabilities with [GitHub Private Vulnerability Reporting](https://github.com/centauri-ai/coslash/security/advisories/new).
Do not disclose vulnerabilities, proofs of concept, transcripts, prompts,
credentials, or other private data in public issues or discussions.

## Threat model

coSlash binds to loopback and protects API requests with a per-start token. It
reads agent transcripts from your home directory and can open a terminal running
your agent CLI. Optional synthesis sends bounded session facts through your
selected Claude Code or Codex CLI using that tool's existing authentication.

Processes running as your macOS user can read coSlash data and its access token.
Do not proxy or forward the server port, or run coSlash under a shared user
account.
