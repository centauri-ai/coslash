package hubclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	FailureOK             = "ok"
	FailureDNS            = "dns"
	FailureConnect        = "connect"
	FailureTLS            = "tls"
	FailureProxy          = "proxy"
	FailureHubUnavailable = "hub_unavailable"
	FailureRejected       = "rejected"
)

type HubFailure struct {
	Cause    string
	RawError string
	Message  string
}

// ClassifyHubContact includes successful contact as the "ok" cause and
// delegates failures to ClassifyHubFailure.
func ClassifyHubContact(err error) (HubFailure, bool) {
	if err == nil {
		return HubFailure{Cause: FailureOK, Message: "Hub contact succeeded."}, true
	}
	return ClassifyHubFailure(err)
}

var (
	hubURLPattern           = regexp.MustCompile(`(?i)https?://[^\s]+`)
	hubAuthorizationPattern = regexp.MustCompile(`(?i)\bauthorization\b\s*[:=]\s*(?:(?:bearer|device)\s+)?[^\s,;]+`)
	hubSecretPattern        = regexp.MustCompile(`(?i)\b(access[_-]?token|token|password|secret|api[_-]?key)\b\s*[:= ]\s*([^\s,;]+)`)
	hubCredentialPattern    = regexp.MustCompile(`(?i)\b(Bearer|Device)\s+[A-Za-z0-9._~+/-]{8,}`)
)

// ClassifyHubFailure maps a Hub request error to a content-free user-facing
// cause. It deliberately ignores cancellation and errors unrelated to contact.
func ClassifyHubFailure(err error) (HubFailure, bool) {
	if err == nil || errors.Is(err, context.Canceled) {
		return HubFailure{}, false
	}
	cause := hubFailureCause(err)
	if cause == "" {
		return HubFailure{}, false
	}
	raw := safeHubError(err)
	return HubFailure{Cause: cause, RawError: raw, Message: HubFailureMessage(cause, raw)}, true
}

func hubFailureCause(err error) string {
	var problem V4Problem
	if errors.As(err, &problem) {
		switch {
		case problem.HTTPStatus == 407:
			return FailureProxy
		case problem.HTTPStatus == 429, problem.HTTPStatus >= 500, problem.Code == "maintenance":
			return FailureHubUnavailable
		case problem.HTTPStatus >= 400 && problem.HTTPStatus < 500 && problem.Code != "" && !strings.HasPrefix(problem.Code, "http_"):
			return FailureRejected
		}
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "proxyconnect") || strings.Contains(lower, "proxy connect") || strings.Contains(lower, "proxy authentication") {
		return FailureProxy
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return FailureDNS
	}
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var hostnameError x509.HostnameError
	var verificationError *tls.CertificateVerificationError
	var recordHeaderError *tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &unknownAuthority) || errors.As(err, &invalidCertificate) || errors.As(err, &hostnameError) ||
		errors.As(err, &verificationError) || errors.As(err, &recordHeaderError) || errors.As(err, &alert) ||
		strings.Contains(lower, "tls:") || strings.Contains(lower, "certificate") || strings.Contains(lower, "x509:") {
		return FailureTLS
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureConnect
	}
	var operationError *net.OpError
	var networkError net.Error
	if errors.As(err, &operationError) || errors.As(err, &networkError) {
		return FailureConnect
	}
	return ""
}

// HubFailureMessage is the single plain-language sentence used by the CLI and
// Local UI for a classified contact failure.
func HubFailureMessage(cause, raw string) string {
	switch cause {
	case FailureOK:
		return "Hub contact succeeded."
	case FailureDNS:
		return "Hub name could not be found. Check the Hub address or your DNS."
	case FailureConnect:
		lower := strings.ToLower(raw)
		switch {
		case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"), strings.Contains(lower, "deadline exceeded"):
			return "Connection timed out. Check your network, VPN or proxy."
		case strings.Contains(lower, "connection refused"):
			return "Connection refused. Check your network, VPN or proxy."
		case strings.Contains(lower, "no route"), strings.Contains(lower, "network is unreachable"):
			return "No route to Hub. Check your network, VPN or proxy."
		default:
			return "Could not connect. Check your network, VPN or proxy."
		}
	case FailureTLS:
		return "The TLS certificate or handshake failed. Check the time and any HTTPS proxy."
	case FailureProxy:
		return "The proxy could not reach Hub. Check your proxy settings and authentication."
	case FailureHubUnavailable:
		return "Hub is temporarily unavailable. Try again shortly."
	case FailureRejected:
		lower := strings.ToLower(raw)
		switch {
		case strings.Contains(lower, "client_update_required"), strings.Contains(lower, "version_too_old"):
			return "This Local version is too old. Update coSlash Local before syncing."
		case strings.Contains(lower, "device_revoked"), strings.Contains(lower, "device_removed"), strings.Contains(lower, "not_paired"):
			return "This device is no longer paired with Hub. Connect it again to resume sync."
		}
		return "Hub rejected this device request. Check the Hub problem code and device pairing."
	default:
		return "Hub contact failed. Check your network and Hub settings."
	}
}

func safeHubError(err error) string {
	var problem V4Problem
	if errors.As(err, &problem) && problem.HTTPStatus > 0 {
		code := problem.Code
		if len(code) > 64 || code == "" || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			return fmt.Sprintf("HTTP %d", problem.HTTPStatus)
		}
		return fmt.Sprintf("HTTP %d: %s", problem.HTTPStatus, code)
	}
	var requestError *url.Error
	if errors.As(err, &requestError) && requestError.Err != nil {
		err = requestError.Err
	}
	value := strings.ToValidUTF8(err.Error(), "�")
	value = hubURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "<url>"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	})
	value = hubAuthorizationPattern.ReplaceAllString(value, "Authorization=[redacted]")
	value = hubSecretPattern.ReplaceAllString(value, "$1=[redacted]")
	value = hubCredentialPattern.ReplaceAllString(value, "$1 [redacted]")
	value = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 240 {
		value = value[:240]
	}
	if value == "" {
		return "request failed"
	}
	return value
}
