# Local Windows Grok support plan

The existing Grok collector, parser, session detail, UI, and synthesis paths are shared with macOS. A Windows Grok 1.0.46 store was checked: sessions live under `~/.grok/sessions/<URL-encoded-cwd>/<session-id>/`, use `chat_format_version: 1`, and have the same summary, update, usage, signal, and active-process files used by the collector. The installed CLI can be under `~/.grok/bin` even when `grok` is not on `PATH`.

This stack covers local Windows only. Remote collection, Linux, handoff, review, legacy transcripts, and portable full-session exports remain outside it.

| Step | Change | Proof | Status |
| --- | --- | --- | --- |
| 1 | Resolve the local Grok executable from `PATH` or its installed `bin` directory, and use that result for launch and diagnostics. | Windows tests cover new and resumed commands with a CLI outside `PATH`, including a custom `GROK_HOME`. | Complete |
| 2 | Check the shared collector against sanitized Windows-shaped session fixtures. | A finished record and an open turn retain fields, usage, and process status; the macOS fixtures continue to pass. | In progress |
| 3 | Cover Windows source health and parent/child behavior with temporary stores. | Found, empty, and unreadable roots are distinct; one child appears once beneath its parent. | Pending |
| 4 | Run end-to-end regression on Windows after the stack lands. | A fresh app shows sessions and details, launches and resumes Grok, runs eligible synthesis, and preserves other local vendors. Record observed results below. | Pending |

## End-to-end observations

Pending implementation and validation. Record the tested commit, Grok version, Windows version, scenarios, and any limits without including private paths, prompts, session IDs, or tokens.
