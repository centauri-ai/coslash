# Security Policy

## Reporting a vulnerability

Report vulnerabilities with [GitHub Private Vulnerability Reporting](https://github.com/centauri-ai/coslash/security/advisories/new).
Do not disclose vulnerabilities, proofs of concept, transcripts, prompts,
credentials, or other private data in public issues or discussions.

## Threat model

coSlash binds to loopback only and holds no credentials of its own. It reads
agent transcripts from your home directory, can open a terminal running your
agent CLI, and sends transcript excerpts to Anthropic through the `claude` CLI
under your own CLI credentials. Anyone with an account on the machine can
reach the API. It is not designed to be exposed to a network or run on a
shared host.
