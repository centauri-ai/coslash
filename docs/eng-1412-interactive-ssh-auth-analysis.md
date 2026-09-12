# ENG-1412: Interactive SSH authentication analysis

Issue: [ENG-1412 — Support interactive SSH authentication during remote host setup](https://linear.app/centauri-ai/issue/ENG-1412/support-interactive-ssh-authentication-during-remote-host-setup)

Status of this document: approved initial implementation direction. The first
release covers aliases and bounded `user@host` destinations, macOS terminal
authentication, five-minute attempts, and authentication as an action/reason
that preserves the existing freshness state.

## Summary

ENG-1412 combines three related but independently useful changes:

1. Accept a documented SSH config alias or a bounded `user@host` destination and report validation failures clearly.
2. Let a user satisfy password, key-passphrase, host-key, or MFA prompts in their system terminal while coSlash continues to use non-interactive SSH internally.
3. Represent expired authentication as an actionable condition and reconnect without removing the machine.

The codebase already has most of the transport foundation. coSlash uses the system OpenSSH client, creates a private control-socket directory, starts a `%C`-keyed control master, reuses it for SFTP and helper operations, and can open Apple Terminal or iTerm2. The missing pieces are a shared destination model, an explicit interactive control-master bootstrap, a structured API/state contract, and coordination between authentication attempts and background refreshes.

The recommended direction is to preserve the current non-interactive probe, offer a deliberate **Authenticate in Terminal** action only for actionable authentication conditions, and run a fixed coSlash-owned terminal command identified by an opaque attempt ID. OpenSSH should remain the only process that reads or renders credentials and MFA responses.

## Current behavior

### `user@host` fails before SSH starts

The current destination is named `sshAlias` throughout settings, APIs, and the UI. Validation permits only `A-Z`, `a-z`, digits, `.`, `_`, and `-`. Consequently, `janedoe@devvm1872.cln0` is rejected before OpenSSH is invoked.

The failure path is:

```text
user enters janedoe@devvm1872.cln0
        |
        v
frontend sends { sshAlias: "janedoe@devvm1872.cln0" }
        |
        v
backend alias validator rejects "@"
        |
        v
backend returns a plain-text HTTP 400
        |
        v
frontend cannot decode a structured API error
        |
        v
Remote test failed (400)
```

Relevant implementation:

- [`ValidSSHAlias`](../collector/internal/settings/settings.go) applies the alias-only regex.
- [`handleRemoteTest`](../collector/cmd/coslash/api_remote.go) returns plain-text `400` responses for malformed or rejected input.
- [`testRemoteAlias`](../frontend/src/pages/coslash/lib/remote-api.ts) falls back to `Remote test failed (<status>)` when no structured error is available.

### Valid aliases cannot prompt interactively

For a valid alias, remote setup opens SFTP through system OpenSSH. Before opening SFTP, coSlash checks for or starts its own control master under `~/.coslash/ssh`.

Both the SFTP command and control-master startup force `BatchMode=yes`. This is correct for background work, but OpenSSH then disables password, passphrase, host-key confirmation, and keyboard-interactive prompts. The UI currently tells the user to run `ssh <alias>` manually and retry the flow.

Relevant implementation:

- [`SSHArgs`, `controlMasterStartArgs`, and `ensureControlMaster`](../collector/internal/remote/sftp.go) implement the existing non-interactive control-master path.
- [`MachinesSettingsSection`](../frontend/src/pages/coslash/components/MachinesSettingsSection.tsx) displays the manual Terminal-and-retry guidance.
- [`classifyError`](../collector/internal/remote/manager.go) maps SSH stderr to a small set of machine reasons using substring matching.

### Existing capabilities we should reuse

- System OpenSSH remains the SSH implementation.
- `~/.coslash/ssh` is created with mode `0700` and rejected if it is a symlink.
- `ControlPath=~/.coslash/ssh/cm-%C` separates sockets by effective host, port, and user.
- `ControlPersist=10m` keeps an authenticated connection available for later SFTP/helper operations.
- Apple Terminal and iTerm2 launch adapters already exist in [`internal/launch`](../collector/internal/launch/launch.go).
- Machine health already distinguishes current, stale, and unavailable data, and preserves the last successful snapshot after connection failures.

## Problem decomposition

| Problem | User impact | Root cause | Desired result |
| --- | --- | --- | --- |
| Destination mismatch | Natural `user@host` input produces an opaque 400 | An alias-only regex is used as the destination model | Validate aliases and a deliberately bounded `user@host` form before SSH |
| Unstructured errors | Users cannot tell invalid input, DNS failure, timeout, auth failure, and SFTP failure apart | Some API failures are plain text and SSH outcomes are broadly classified | Stable error/reason codes with specific, safe UI copy |
| No interactive bootstrap | Password, passphrase, host-key, and MFA users cannot finish setup | All coSlash-owned SSH starts use `BatchMode=yes` | A deliberate terminal flow starts the shared authenticated master |
| Setup restart | The user must leave coSlash, run SSH manually, then repeat setup | coSlash does not observe completion of the manual SSH step | Poll master readiness and continue the existing setup automatically |
| Reauthentication looks offline | Expired credentials are indistinguishable from network failures | Authentication is represented only as a generic failed refresh | Show an Authentication required action while preserving stale data |
| Retry races | Background collection may compete with terminal authentication | There is no authentication-attempt coordinator | Suppress or serialize automatic master creation during an interactive attempt |
| Diagnostic leakage | SSH stderr can contain usernames, hosts, paths, banners, and prompt text | Classification and diagnostic persistence share the same raw input | Classify stderr in bounded memory, persist only safe enums and measurements |

## Solution options

### Option A: Improve validation and keep authentication manual

Implement destination parsing, structured error responses, and better UI instructions, but continue asking the user to run SSH in Terminal themselves.

Advantages:

- Smallest and lowest-risk change.
- Fixes the reported `user@host`/HTTP 400 failure immediately.
- Does not introduce authentication-attempt lifecycle or terminal coordination.

Limitations:

- Users must still switch contexts and manually repeat setup.
- coSlash cannot distinguish an abandoned prompt from a completed authentication.
- Reauthentication remains awkward after a control master expires.

Best use: an independently shippable first slice, not the complete ENG-1412 solution.

### Option B: Launch a generated `ssh` command directly in Terminal

After a non-interactive authentication failure, open the selected terminal with an `ssh -f -N` command configured to create the coSlash control master. Poll `ssh -O check` from the main process.

Advantages:

- Uses the existing terminal adapters and control-socket implementation.
- Relatively little new backend machinery.
- Credentials and prompts remain in OpenSSH and the terminal.

Limitations:

- The terminal adapters accept a command string, so destination data must be mechanically shell-quoted even if it was parsed safely.
- Cancellation and terminal exit status are difficult to observe through the existing `osascript` launch boundary.
- It is harder to associate a terminal window with a particular setup attempt or prevent stale attempts from completing a newer flow.

Best use: a pragmatic implementation if the product accepts weaker attempt tracking and carefully tested shell quoting.

### Option C: Launch a fixed coSlash authentication command

Create an authentication attempt in the running collector, identified by a random opaque ID. Open Terminal with a fixed command such as:

```text
coslash ssh-auth <opaque-attempt-id>
```

The subcommand resolves server-owned, already-validated attempt data and invokes system OpenSSH with structured argv. The UI polls an authentication-status API, which checks the matching socket with `ssh -O check` and reports terminal, timeout, cancellation, or readiness states.

Advantages:

- User-provided destination text is not interpolated into a terminal shell command.
- Authentication attempts have explicit identity, expiry, and cancellation semantics.
- Terminal output can show a small coSlash-owned success/failure message without returning prompt text to the browser.
- The approach extends naturally to reauthentication.

Limitations:

- Requires an attempt registry or tightly permissioned temporary attempt record.
- The new subprocess must locate and safely communicate with the running collector or read a short-lived local record.
- More lifecycle and integration-test work than Option B.

Best use: the recommended complete solution because it creates the cleanest security and lifecycle boundary.

### Option D: Render SSH prompts inside the web UI

Run SSH behind a PTY and relay prompts and responses through the local API and browser.

Advantages:

- Keeps the user in one window.

Limitations:

- coSlash would receive credential and MFA responses.
- Prompt detection is brittle across OpenSSH versions, locales, PAM configurations, and enterprise providers.
- It materially expands secret handling, logging, memory, API, and browser security boundaries.
- It contradicts ENG-1412's core privacy constraint.

Best use: reject this option.

## Recommended design

### 1. Centralize destination parsing

Replace scattered alias validation with a single immutable destination value. It should distinguish:

- An SSH config alias, passed as the destination argument.
- A supported `user@host` form, emitted as structured arguments such as `-l user host`.

All SFTP, control-master, control-check, control-exit, helper, platform-probe, and remote-launch commands must use this parser and argument builder. Do not independently expand regexes in each caller.

For backward compatibility, the persisted `sshAlias` field can initially retain its name and store the normalized display form. A later schema migration may rename it to `sshDestination`; renaming is not necessary to deliver the behavior.

Recommended initial boundary: support existing aliases and `user@host`; require SSH config for ports, ProxyJump, identity selection, and other advanced configuration.

### 2. Keep background SSH non-interactive

Continue using `BatchMode=yes` for tests, collection, helper operations, and recovery attempts. A background refresh must never unexpectedly open a password or MFA prompt.

The test should return a typed outcome rather than treating every operational SSH failure as an HTTP exception. Suggested reasons include:

- `invalid_destination`
- `dns_failed`
- `connection_timeout`
- `connection_failed`
- `host_key_confirmation_required`
- `host_key_changed`
- `authentication_required`
- `authentication_failed`
- `sftp_unavailable`
- `terminal_unavailable`
- `authentication_cancelled`
- `authentication_timeout`

Malformed JSON and invalid destinations should use structured 4xx API errors. A well-formed connection test may return HTTP 200 with a typed unsuccessful test result, matching the current `MachineFact` pattern.

### 3. Start an interactive master only after explicit user action

When the batch probe returns an actionable authentication reason, present **Authenticate in Terminal**. The action should:

1. Create a short-lived authentication attempt bound to the exact normalized destination.
2. Suspend new automatic master-start attempts for that destination.
3. Open the configured and available terminal with the fixed coSlash authentication command.
4. Have that command invoke system OpenSSH with `ControlMaster=yes`, the coSlash `ControlPath`, the selected `ControlPersist`, and interactive prompting enabled.
5. Never return stdin, prompt text, or raw stderr through the HTTP API.

An unknown host key may use this flow because OpenSSH can display and record the user's confirmation. A changed host key should be a distinct security condition with explicit remediation; it should not be presented as an ordinary authentication prompt.

### 4. Observe readiness and continue setup

The main process should poll the expected master using `ssh -O check`. Once healthy:

1. Mark the authentication attempt ready.
2. Rerun the ordinary non-interactive SFTP test through the shared master.
3. Save the host using the existing settings flow.
4. Continue to connector consent/setup without requiring the user to restart.

“Return to coSlash automatically” should mean that the existing web flow advances automatically. Automatically activating an arbitrary browser window should be treated as separate scope.

### 5. Model authentication separately from data freshness

The current top-level machine state describes data availability: `ok`, `limited`, `stale`, `error`, and so on. Authentication is an actionable connection condition, not a complete replacement for freshness. A machine can simultaneously have stale cached data and require authentication.

Recommended representation:

```json
{
  "state": "stale",
  "reason": "authentication_required",
  "actionRequired": "authenticate",
  "authState": "required"
}
```

Possible `authState` values are `not_required`, `required`, `launching`, `waiting`, and `ready`. This preserves stale/error semantics while allowing Settings and the machine indicator to show **Authentication required · Reconnect**.

Alternative: add `needs_auth` as a top-level machine state. This is simpler to display but conflates connection remediation with whether a usable cached snapshot exists and requires updates to every machine-state consumer.

The first release uses the orthogonal reason/action approach rather than a
new top-level `needs_auth` state.

### 6. Coordinate refresh, cancellation, and cleanup

Only one authentication attempt should be active for a destination. While it is active:

- Do not let periodic refreshes start a competing batch-mode control master.
- Allow repeated status checks without extending the attempt forever.
- Let the user cancel waiting.
- Treat closing the terminal before readiness as cancelled or failed once observable.
- Expire short-lived attempt metadata conservatively while retaining terminal
  outcomes long enough to distinguish a ready master from a cancellation.
- Close a healthy coSlash control master when a host is removed.
- Clean up stale socket files conservatively; never delete an unverified arbitrary path.

The first release uses a five-minute attempt timeout. Cancellation records a
safe cancelled outcome and closes any coSlash control master, but does not
terminate the user-owned terminal SSH process. Terminal outcomes remain until
expiry cleanup so a status poll and the terminal process cannot mistake a
ready master for a cancelled attempt.

### 7. Minimize diagnostics at the source

Raw SSH stderr should exist only in a bounded in-memory buffer long enough to classify the failure. Persist and expose only:

- Stable reason code.
- Attempt state.
- Bounded timing information.
- Exit category, if available.
- Whether the control socket became healthy.

Do not persist prompt text, usernames, hosts, paths, banners, or complete SSH stderr. Redaction should be defense in depth rather than the main privacy mechanism.

## Intended user experience

For the reported user, the completed recommended design changes the setup flow
from an opaque validation failure into a guided, resumable SSH setup:

1. The user enters a supported `user@host` destination, such as
   `janedoe@devvm1872.cln0`. coSlash accepts it rather than rejecting `@`
   before SSH begins.
2. coSlash runs its ordinary non-interactive SSH/SFTP probe. A successful
   probe proceeds directly to saving the host and the existing connector
   install-or-skip choice.
3. If the probe determines that a password, private-key passphrase,
   keyboard-interactive/MFA response, or unknown-host-key confirmation is
   required, coSlash shows a specific result and an **Authenticate in
   Terminal** action. It does not show an opaque HTTP 400 or ask the user to
   run a separate SSH command and restart setup.
4. A Terminal or iTerm2 window opens only after the user explicitly chooses
   that action. The user completes the native OpenSSH prompt in that terminal;
   credentials, MFA responses, and prompt text never enter the browser or
   coSlash diagnostics.
5. coSlash observes the authenticated control master, reruns the normal
   non-interactive SFTP test through it, saves the host, and advances the
   existing setup UI to connector consent/setup automatically. The user does
   not need to re-enter the destination or click **Add remote host** again.
6. If authentication later expires, existing cached sessions remain visible
   as stale. The machine presents **Authentication required · Reconnect**,
   rather than appearing simply offline or requiring the user to remove and
   add the host again.

A terminal must never open from a background refresh. A changed host key is a
separate security condition with explicit remediation, not an ordinary
authentication prompt.

## Reproducing the current failure and validating the replacement

### Current failure

The current `user@host` failure is deterministic and does not require a
Linux machine, ARM hardware, DNS, or a reachable SSH service. In the current
Settings → Machines flow, enter a value containing `@`, for example
`testuser@agent-box-arm`, and choose **Add remote host**. The backend rejects
the destination before invoking OpenSSH with a plain-text HTTP 400; the
frontend then displays `Remote test failed (400)`.

This should become a regression test at both API and UI levels. It directly
exercises the current alias-only validation and generic client error fallback.

### End-to-end environment

`agent-box-arm` is suitable for validating an SSH-config alias, interactive
control-master reuse, SFTP, and the automatic continuation of setup. It is
not by itself a faithful raw-`user@host` test environment when its connection
depends on SSH-config-only behavior such as `ProxyCommand`, `ProxyJump`,
non-default ports, or identity selection. The initial destination boundary
requires those advanced configurations to continue using an SSH alias.

Use two isolated test paths:

1. **Alias path:** configure a dedicated `coslash-e2e` SSH alias for
   `agent-box-arm` and a disposable test account. Exercise the terminal
   authentication flow, control-master readiness, SFTP reuse, and automatic
   progression to connector consent without manual SSH or a retry.
2. **Raw destination path:** provide a dedicated ARM Linux test account at a
   directly reachable hostname for `user@host`. Do not depend on an SSH
   config alias or an IAP/proxy tunnel. This is the environment that proves
   parsing and structured argv construction for the newly supported syntax.

Run the app with an isolated `COSLASH_HOME` so test settings and control
sockets do not affect a developer's normal coSlash configuration. Configure
the disposable account or test host to cover password authentication,
encrypted-key passphrases, keyboard-interactive MFA, unknown host-key
confirmation, changed host-key warnings, and SFTP-unavailable behavior. The
key manual acceptance assertion is that a successful terminal authentication
advances setup automatically, with no manual `ssh` command and no second
**Add remote host** action.

## Recommended delivery slices

### Slice 1: Destination and error contract

- Add the shared destination parser and argv builder.
- Accept a bounded `user@host` form.
- Keep existing aliases working without migration.
- Return structured validation and connection-test outcomes.
- Replace `Remote test failed (400)` with specific inline guidance.
- Add unit tests for every accepted and rejected destination form and all command builders.

This slice fixes the immediate bug and can ship independently.

### Slice 2: Terminal-assisted initial setup

- Add authentication-attempt creation and status APIs.
- Add the fixed `coslash ssh-auth <attempt-id>` terminal command.
- Add **Authenticate in Terminal** and authentication-progress UI.
- Poll the control socket and continue SFTP test, save, and connector setup automatically.
- Cover success, unavailable terminal, timeout, cancellation, failed auth, and SFTP-after-auth failure.

### Slice 3: Reauthentication lifecycle

- Expose authentication-required as an explicit action on a configured machine.
- Preserve stale history while waiting for authentication.
- Suppress futile background retries while user action is required.
- Add Reconnect without remove/re-add.
- Cover master loss, expiry, application restart, host removal, and cleanup.

### Slice 4: Guidance and hardening

- Document ssh-agent, macOS Keychain, SSH certificates, and supported enterprise/SSO agents.
- Explain the limitations of servers that demand fresh MFA for every connection.
- Add privacy regression tests proving that prompt and stderr content do not enter API responses, diagnostics, or durable state.
- Add integration fixtures for keyboard-interactive auth and control-master reuse where practical.

## Testing strategy

### Unit tests

- Destination parsing, normalization, maximum lengths, and rejected option-like inputs.
- Argument construction for SFTP, master start/check/exit, helper operations, and remote launch.
- Structured API errors for malformed JSON and invalid destinations.
- Classification of DNS, timeout, unknown host key, changed host key, authentication, and SFTP failures.
- State transitions and retry suppression during an active authentication attempt.
- Bounded diagnostics and absence of raw SSH text in serialized state.

### Integration tests

- Existing non-interactive aliases remain unchanged.
- `user@host` reaches OpenSSH as structured argv.
- A keyboard-interactive fixture reaches terminal-assisted authentication.
- A healthy master is detected and reused by SFTP and helper operations.
- Setup resumes without a second Add action.
- Master loss produces the authentication-required action when appropriate.
- Cancellation, timeout, stale attempts, and concurrent refresh attempts are handled deterministically.
- Host removal closes or releases coSlash-owned master state safely.

### Manual acceptance

- Password authentication.
- Encrypted private-key passphrase.
- Unknown host-key confirmation.
- Changed host-key warning.
- Keyboard-interactive MFA/passcode.
- ssh-agent or Keychain success without terminal assistance.
- ProxyJump or enterprise agent configured through an SSH alias.
- Apple Terminal and iTerm2 availability and automation-permission failures.

## Decisions needed before implementation

1. Exact supported destination grammar, especially ports and IPv6.
2. Whether terminal-assisted auth is explicitly macOS-only for this release.
3. Whether authentication is a new machine state or an orthogonal reason/action substate.
4. Interactive attempt timeout and cancellation semantics.
5. Whether cancellation must terminate an in-progress terminal SSH process.
6. Whether `ControlPersist=10m` remains the intended lifetime.
7. Whether browser/window refocusing is out of scope.

## Issue assessment

ENG-1412 scores **4.2/5** and is ready for product and technical breakdown, but it is broader than a single bounded implementation task.

| Element | Status | Score | Assessment |
| --- | --- | ---: | --- |
| Purpose | Present | 5 | The affected users, failure mode, and privacy motivation are explicit |
| Required outcome | Unclear | 3 | Strong direction, but destination grammar, state semantics, and attempt lifecycle remain undecided |
| Acceptance | Present | 4 | Broad and observable, though cancellation and interactive-failure behavior need exact definitions |
| Constraints | Present | 5 | Credential ownership, argv safety, diagnostics, and non-interactive defaults are strong |
| Context | Present | 4 | The reproduction and proposed architecture are useful; platform and current-system details should be made explicit |

The most important issue refinements are:

1. **Split** delivery into destination/errors, initial interactive setup, and reauthentication lifecycle.
2. **Add** explicit decisions for destination grammar, state representation, timeout, and cancellation.
3. **Replace** the general statement about returning to coSlash with the observable requirement that the existing web flow advances after the control socket becomes healthy.
