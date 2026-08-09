package revision

import (
	"net/url"
	"regexp"
	"strings"
)

// branchPattern accepts the conservative subset of refname syntax this suite
// generates and consumes. It is intentionally narrower than git's own rules:
// anything outside it is refused rather than escaped.
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

var objectIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ValidBranchName reports whether a branch name is safe to pass as argv and to
// embed in a ref path. `..` is refused separately because the character class
// allows a single dot.
func ValidBranchName(name string) bool {
	if !branchPattern.MatchString(name) {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	// A leading dash would be read as an option even in an explicit argv when a
	// caller forgets the `--` separator.
	if strings.HasPrefix(name, "-") {
		return false
	}
	if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, "/") {
		return false
	}
	return true
}

// ValidObjectID reports whether text is a full 40-character lowercase SHA-1
// object name. Abbreviated names are refused because they are ambiguous across
// repository growth.
func ValidObjectID(text string) bool { return objectIDPattern.MatchString(text) }

// allowedRemoteSchemes are the transports publication may push to. Everything
// else — most importantly ext:: and file:// — is refused outright, because
// git's ext transport executes a command named in the URL.
var allowedRemoteSchemes = map[string]bool{
	"https": true,
	"ssh":   true,
}

// ValidateRemoteURL refuses remotes that could execute code or reach outside
// the intended transports.
//
// scp-style `git@host:owner/repo` is accepted because it is the ordinary form
// git writes for an SSH remote, but only when the host and path are plain.
func ValidateRemoteURL(remote string) error {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return newError(CodeUnsafeRemote, "the repository has no push remote")
	}
	if strings.ContainsAny(trimmed, "\x00\n\r") {
		return newError(CodeUnsafeRemote, "the remote URL contains control characters")
	}
	if strings.HasPrefix(trimmed, "-") {
		return newError(CodeUnsafeRemote, "the remote URL may not begin with a dash")
	}
	// `ext::` and `fd::` are transports that run a command; refuse before any
	// generic parse can normalize them into something that looks ordinary.
	lowered := strings.ToLower(trimmed)
	for _, forbidden := range []string{"ext::", "fd::", "file://", "/./", "/../"} {
		if strings.Contains(lowered, forbidden) {
			return newError(CodeUnsafeRemote, "the remote URL uses an unsupported transport")
		}
	}

	if scpHost, _, ok := splitSCPStyle(trimmed); ok {
		if scpHost == "" {
			return newError(CodeUnsafeRemote, "the remote URL has no host")
		}
		return nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return newError(CodeUnsafeRemote, "the remote URL could not be parsed").
			withDetail(err.Error()).withCause(err)
	}
	if !allowedRemoteSchemes[strings.ToLower(parsed.Scheme)] {
		return newError(CodeUnsafeRemote, "the remote URL uses an unsupported transport")
	}
	if parsed.Host == "" {
		return newError(CodeUnsafeRemote, "the remote URL has no host")
	}
	return nil
}

// splitSCPStyle recognizes `user@host:path` without a scheme. It deliberately
// rejects anything containing a slash before the colon, which would be a local
// path rather than an SSH target.
func splitSCPStyle(remote string) (host, path string, ok bool) {
	if strings.Contains(remote, "://") {
		return "", "", false
	}
	colon := strings.Index(remote, ":")
	if colon <= 0 {
		return "", "", false
	}
	prefix := remote[:colon]
	if strings.ContainsAny(prefix, "/\\") {
		return "", "", false
	}
	if at := strings.LastIndex(prefix, "@"); at >= 0 {
		prefix = prefix[at+1:]
	}
	return prefix, remote[colon+1:], prefix != ""
}
