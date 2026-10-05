# Local Windows Grok support plan

The existing Grok collector, parser, session detail, UI, and synthesis paths are shared with macOS. A Windows Grok 1.0.46 store was checked: sessions live under `~/.grok/sessions/<URL-encoded-cwd>/<session-id>/`, use `chat_format_version: 1`, and have the same summary, update, usage, signal, and active-process files used by the collector. The installed CLI can be under `~/.grok/bin` even when `grok` is not on `PATH`.

This stack covers local Windows only. Remote collection, Linux, handoff, review, legacy transcripts, and portable full-session exports remain outside it.

| Step | Change | Proof | Status |
| --- | --- | --- | --- |
| 1 | Resolve the local Grok executable from `PATH` or its installed `bin` directory, and use that result for launch and diagnostics. | Windows tests cover new and resumed commands with a CLI outside `PATH`, including a custom `GROK_HOME`. | Complete |
| 2 | Check the shared collector against sanitized Windows-shaped session fixtures. | A finished record and an open turn retain fields, usage, and process status; the macOS fixtures continue to pass. | Complete |
| 3 | Cover Windows source health and parent/child behavior with temporary stores. | Found, empty, and unreadable roots are distinct; one child appears once beneath its parent. | Complete |
| 4 | Run end-to-end regression on Windows after the stack lands. | A fresh app shows sessions and details, launches and resumes Grok, runs eligible synthesis, and preserves other local vendors. Record observed results below. | Partial; see below |

## End-to-end observations

Tested at `5558afaa` on Windows build 26100/amd64 with Grok 1.0.46. A fresh locally built app found two Grok sessions in the dashboard, showed a finished session's inspector fields and deterministic debrief, and reported the Grok session store and installed CLI in diagnostics. Other local agent sessions remained visible. The exact-detail API and Grok synthesis eligibility tests passed.

The Grok, collector, launch, session, and diagnostics Go packages passed, as did Go vet, the frontend build, tests, lint, and format check. The complete Go suite still has failures outside this stack in settings, Pi, and synthesis tests on this machine.

Live Start fresh, Resume, and AI synthesis remain unverified. A second standalone app build for an isolated launch fixture was quarantined by Windows security, so the interactive smoke test could not continue. The command construction and CLI discovery paths are covered by Windows tests; finish this manual check on a machine that can run the built app without changing security settings. Run AI synthesis only with a disposable transcript and a configured test runner.
