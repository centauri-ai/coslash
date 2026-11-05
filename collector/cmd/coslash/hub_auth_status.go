package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const (
	hubAuthUsage         = "usage: coslash hub auth status [--json]"
	hubAuthStatusTimeout = 90 * time.Second
	hubAuthFieldLimit    = 256
	hubAuthRerunCommand  = "coslash hub auth status"
)

const (
	hubAuthExitAuthenticated   = 0
	hubAuthExitError           = 1
	hubAuthExitUsage           = 2
	hubAuthExitLoginRequired   = 3
	hubAuthExitUnreachable     = 4
	hubAuthExitCredentialError = 5
	hubAuthExitBusy            = 6
)

type hubAuthAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// hubAuthStatusResult is the documented `coslash hub auth status` result.
// Every field is always present; nil pointers encode as JSON null. Scopes is
// nil when the grant is unknown and an empty slice for an empty grant.
type hubAuthStatusResult struct {
	Status          string          `json:"status"`
	HubURL          *string         `json:"hub_url"`
	Scopes          *[]string       `json:"scopes"`
	ExpiresAt       *string         `json:"expires_at"`
	Account         *hubAuthAccount `json:"account"`
	IdentityStatus  string          `json:"identity_status"`
	ErrorCode       *string         `json:"error_code"`
	RecoveryCommand *string         `json:"recovery_command"`
}

func runHubAuthCLI(stdout, stderr io.Writer, args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") ||
		len(args) == 2 && args[0] == "status" && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(stdout, hubAuthUsage)
		return 0
	}
	if len(args) == 0 || args[0] != "status" || len(args) > 2 || len(args) == 2 && args[1] != "--json" {
		fmt.Fprintln(stderr, hubAuthUsage)
		return hubAuthExitUsage
	}
	asJSON := len(args) == 2
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, hubAuthStatusTimeout)
	defer cancel()
	result, code := newHubAgentCLI(io.Discard).authStatus(ctx)
	if err := writeHubAuthStatus(stdout, result, asJSON); err != nil {
		fmt.Fprintln(stderr, "Error: could not write the Hub authentication status")
		return hubAuthExitError
	}
	return code
}

// authStatus verifies one saved connection with an MCP ping and, when the
// grant allows, reads the account it belongs to. It never starts a login and
// never deletes credentials.
func (c *hubAgentCLI) authStatus(ctx context.Context) (hubAuthStatusResult, int) {
	var revision uint64
	var snapshot hubAgentCredential
	err := c.withCredentialLock(ctx, func(ctx context.Context, lock *os.File) error {
		var err error
		if revision, err = readHubAgentRevision(lock); err != nil {
			return &hubCredentialStoreError{err: err}
		}
		snapshot, err = c.loadCredentialInTransaction(ctx)
		return err
	})
	if err != nil {
		return hubAuthFailure(err, nil)
	}
	_, credential, err := c.mcpRequestWith(ctx, snapshot, "ping", map[string]any{})
	if err != nil {
		return hubAuthFailure(err, &snapshot)
	}
	scopes := hubGrantedScopes(credential.Scope)
	expires := credential.ExpiresAt.UTC().Format(time.RFC3339)
	result := hubAuthStatusResult{Status: "authenticated", HubURL: &credential.HubURL, Scopes: scopes, ExpiresAt: &expires, IdentityStatus: "insufficient_scope"}
	if scopes == nil || slices.Contains(*scopes, "hub:read") {
		var identityErr error
		result.Account, result.IdentityStatus, identityErr = c.hubAccount(ctx, credential)
		if identityErr != nil {
			return hubAuthFailure(identityErr, &snapshot)
		}
		if result.IdentityStatus == "unavailable" {
			code := "identity_unavailable"
			result.ErrorCode = &code
		}
	}
	if err := c.confirmHubConnection(ctx, revision, snapshot); err != nil {
		return hubAuthFailure(err, &snapshot)
	}
	return result, hubAuthExitAuthenticated
}

// hubAccount reads the account for credential once. An error is returned only
// when the read shows that the login itself is no longer valid or changed.
func (c *hubAgentCLI) hubAccount(ctx context.Context, credential hubAgentCredential) (*hubAuthAccount, string, error) {
	response, _, err := c.mcpRequestWith(ctx, credential, "tools/call", map[string]any{"name": "coslash_get_account", "arguments": map[string]any{}})
	if err != nil {
		if _, _, exit := classifyHubAuthError(err); exit == hubAuthExitLoginRequired || exit == hubAuthExitBusy || exit == hubAuthExitCredentialError {
			return nil, "unavailable", err
		}
		return nil, "unavailable", nil
	}
	var result hubMCPToolResult
	if json.Unmarshal(response, &result) != nil {
		return nil, "unavailable", nil
	}
	payload, err := hubToolPayload(result)
	if err != nil {
		return nil, "unavailable", nil
	}
	if result.IsError {
		if code, _ := hubToolError(payload); code == "SCOPE_MISSING" {
			return nil, "insufficient_scope", nil
		}
		return nil, "unavailable", nil
	}
	root, _ := payload.(map[string]any)
	profile, _ := root["profile"].(map[string]any)
	id, _ := profile["id"].(string)
	name, _ := profile["name"].(string)
	email, _ := profile["email"].(string)
	account := &hubAuthAccount{ID: safeHubAuthText(id), Name: safeHubAuthText(name), Email: safeHubAuthText(email)}
	if account.ID == "" {
		return nil, "unavailable", nil
	}
	return account, "available", nil
}

// confirmHubConnection checks that the login revision and connection are the
// ones status started with, so a result never mixes two logins.
func (c *hubAgentCLI) confirmHubConnection(ctx context.Context, revision uint64, snapshot hubAgentCredential) error {
	return c.withCredentialLock(ctx, func(ctx context.Context, lock *os.File) error {
		current, err := readHubAgentRevision(lock)
		if err != nil {
			return &hubCredentialStoreError{err: err}
		}
		if current != revision {
			return errHubConnectionChanged
		}
		credential, err := c.loadCredentialInTransaction(ctx)
		if errors.Is(err, hubclient.ErrNotPaired) || err == nil && !sameHubConnection(credential, snapshot) {
			return errHubConnectionChanged
		}
		return err
	})
}

func hubAuthFailure(err error, snapshot *hubAgentCredential) (hubAuthStatusResult, int) {
	status, code, exit := classifyHubAuthError(err)
	result := hubAuthStatusResult{Status: status, IdentityStatus: "not_checked", ErrorCode: &code}
	if snapshot != nil && exit != hubAuthExitBusy {
		hubURL := snapshot.HubURL
		result.HubURL = &hubURL
	}
	var recovery string
	switch exit {
	case hubAuthExitLoginRequired:
		recovery = hubLoginRecovery(result.HubURL)
	case hubAuthExitCredentialError:
		switch code {
		case "store_unavailable":
			recovery = hubAuthRerunCommand
		case "save_failed", "invalid_credentials":
			recovery = hubLoginRecovery(result.HubURL)
		}
	case hubAuthExitUnreachable, hubAuthExitBusy:
		recovery = hubAuthRerunCommand
	}
	if recovery != "" {
		result.RecoveryCommand = &recovery
	}
	return result, exit
}

// classifyHubAuthError maps a typed failure to its status, error code and exit.
func classifyHubAuthError(err error) (string, string, int) {
	var lockErr *hubCredentialLockError
	var storeErr *hubCredentialStoreError
	var oauthErr *hubOAuthError
	var statusErr *hubHTTPStatusError
	var urlErr *url.Error
	var netErr net.Error
	switch {
	case errors.Is(err, errHubConnectionChanged):
		return "busy", "connection_changed", hubAuthExitBusy
	case errors.As(err, &lockErr):
		if errors.Is(err, context.Canceled) {
			return "busy", "cancelled", hubAuthExitBusy
		}
		if errors.Is(err, errHubCredentialsBusy) || errors.Is(err, context.DeadlineExceeded) {
			return "busy", "lock_timeout", hubAuthExitBusy
		}
		return "credential_error", "store_unavailable", hubAuthExitCredentialError
	case errors.Is(err, context.Canceled):
		return "busy", "cancelled", hubAuthExitBusy
	case errors.Is(err, hubclient.ErrNotPaired):
		return "login_required", "not_logged_in", hubAuthExitLoginRequired
	case errors.Is(err, errHubLockMalformed):
		return "credential_error", "invalid_lock_file", hubAuthExitCredentialError
	case errors.Is(err, errHubStoredCredentialInvalid):
		return "credential_error", "invalid_credentials", hubAuthExitCredentialError
	case errors.As(err, &storeErr):
		if storeErr.save {
			return "credential_error", "save_failed", hubAuthExitCredentialError
		}
		if errors.Is(err, hubclient.ErrCredentialCorrupt) {
			return "credential_error", "invalid_credentials", hubAuthExitCredentialError
		}
		// Includes an OS keychain prompt left unanswered until the deadline.
		return "credential_error", "store_unavailable", hubAuthExitCredentialError
	case errors.Is(err, context.DeadlineExceeded):
		return "unreachable", "network_error", hubAuthExitUnreachable
	case errors.Is(err, errHubRefreshUnavailable):
		return "login_required", "token_expired", hubAuthExitLoginRequired
	case errors.As(err, &oauthErr):
		switch oauthErr.Code {
		case "invalid_grant":
			return "login_required", "invalid_grant", hubAuthExitLoginRequired
		case "invalid_client", "unauthorized_client":
			return "login_required", "unauthorized", hubAuthExitLoginRequired
		case "access_denied":
			return "error", "access_denied", hubAuthExitError
		case "temporarily_unavailable":
			return "error", "server_error", hubAuthExitError
		}
		return "error", "protocol_error", hubAuthExitError
	case errors.As(err, &statusErr):
		switch {
		case statusErr.Status == 401:
			return "login_required", "unauthorized", hubAuthExitLoginRequired
		case statusErr.Status == 403:
			return "error", "access_denied", hubAuthExitError
		case statusErr.Status == 429:
			return "error", "rate_limited", hubAuthExitError
		case statusErr.Status >= 500:
			return "error", "server_error", hubAuthExitError
		}
		return "error", "protocol_error", hubAuthExitError
	case errors.As(err, &urlErr), errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded):
		return "unreachable", "network_error", hubAuthExitUnreachable
	}
	return "error", "protocol_error", hubAuthExitError
}

func hubLoginRecovery(hubURL *string) string {
	if hubURL == nil {
		return "coslash hub login"
	}
	if _, err := validateHubURL(*hubURL); err != nil {
		return "coslash hub login"
	}
	return "coslash hub login --hub " + shellQuoteHubArgument(*hubURL)
}

// shellQuoteHubArgument leaves ordinary URLs unquoted so the command works
// in POSIX shells and PowerShell alike, and single-quotes anything else.
func shellQuoteHubArgument(value string) string {
	plain := value != ""
	for _, character := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:/@%+=,", character) {
			plain = false
			break
		}
	}
	if plain {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// hubGrantedScopes returns the sorted, de-duplicated grant, or nil when the
// grant is unknown.
func hubGrantedScopes(scope *string) *[]string {
	if scope == nil {
		return nil
	}
	scopes := []string{}
	for _, value := range strings.Fields(*scope) {
		if value = safeHubAuthText(value); value != "" {
			scopes = append(scopes, value)
		}
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	return &scopes
}

// safeHubAuthText removes terminal control and invisible formatting
// characters from untrusted Hub text and bounds its length.
func safeHubAuthText(value string) string {
	var builder strings.Builder
	count := 0
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) || character == unicode.ReplacementChar {
			continue
		}
		if count == hubAuthFieldLimit {
			break
		}
		builder.WriteRune(character)
		count++
	}
	return strings.TrimSpace(builder.String())
}

func writeHubAuthStatus(output io.Writer, result hubAuthStatusResult, asJSON bool) error {
	if asJSON {
		return writeHubJSON(output, result)
	}
	var text strings.Builder
	line := func(label, value string) { fmt.Fprintf(&text, "%-10s %s\n", label+":", value) }
	line("Status", strings.ReplaceAll(result.Status, "_", " "))
	if result.HubURL != nil {
		line("Hub", *result.HubURL)
	}
	if result.Status == "authenticated" {
		switch {
		case result.Scopes == nil:
			line("Scopes", "unknown (saved by an older coslash; refreshed on the next token renewal)")
		case len(*result.Scopes) == 0:
			line("Scopes", "none granted")
		default:
			line("Scopes", strings.Join(*result.Scopes, " "))
		}
		line("Expires", *result.ExpiresAt)
		switch result.IdentityStatus {
		case "available":
			account := result.Account.Name
			if result.Account.Email != "" {
				account = strings.TrimSpace(account + " <" + result.Account.Email + ">")
			}
			line("Account", strings.TrimSpace(account+" ("+result.Account.ID+")"))
		case "insufficient_scope":
			line("Account", "unknown (this login was not granted hub:read)")
		default:
			line("Account", "unknown (the Hub account could not be read)")
		}
	}
	if result.ErrorCode != nil {
		line("Error", *result.ErrorCode)
	}
	if result.RecoveryCommand != nil {
		line("Next", *result.RecoveryCommand)
	}
	_, err := io.WriteString(output, text.String())
	return err
}
