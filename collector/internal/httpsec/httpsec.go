// Package httpsec guards the loopback server against browser-borne attacks.
// A page in the user's browser can reach 127.0.0.1 even though a remote host
// cannot, so loopback binding alone is not an authorization boundary.
package httpsec

import (
	"crypto/subtle"
	"iter"
	"log"
	"net"
	"net/http"
	"strings"
)

const documentPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'"

// A browser cannot set request headers on a WebSocket handshake, so the API
// token rides in Sec-WebSocket-Protocol instead. The client offers the static
// name plus one token-carrying entry; the server echoes only the static name,
// keeping the token out of the response and out of proxy logs.
const (
	// TerminalSubprotocol is the only subprotocol the server ever echoes.
	TerminalSubprotocol = "coslash.terminal.v1"
	// tokenSubprotocolPrefix marks the entry carrying the current API token.
	tokenSubprotocolPrefix = "coslash.token."
)

// Guard restricts requests to the loopback listener and its browser origin.
type Guard struct {
	Addr  string
	Token string
}

// Wrap applies loopback request validation and security response headers.
func (g Guard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header(), r.URL.Path)

		if !g.allowedHost(r.Host) {
			log.Printf("http security: rejected unexpected host %q", r.Host)
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			navigation := strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") &&
				strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document")
			if !navigation && !strings.EqualFold(site, "same-origin") && !strings.EqualFold(site, "none") {
				log.Printf("http security: rejected cross-site request")
				http.Error(w, "request rejected", http.StatusForbidden)
				return
			}
		} else if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+g.Addr {
			log.Printf("http security: rejected unexpected origin %q", origin)
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") && !g.allowedToken(r) {
			log.Printf("http security: rejected unauthenticated API request")
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (g Guard) allowedHost(requestHost string) bool {
	if requestHost == g.Addr {
		return true
	}
	_, listenerPort, err := net.SplitHostPort(g.Addr)
	if err != nil {
		return false
	}
	host, port, err := net.SplitHostPort(requestHost)
	if err != nil || port != listenerPort {
		return false
	}
	return strings.EqualFold(host, "localhost") || host == "::1"
}

func (g Guard) allowedToken(r *http.Request) bool {
	if g.Token == "" {
		return false
	}
	provided := r.Header.Get("X-Coslash-Token")
	if provided == "" {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			provided = bearer
		}
	}
	// Only a real handshake may fall back to the subprotocol, so an ordinary
	// request still needs one of the header forms above.
	if provided == "" && IsWebSocketUpgrade(r) {
		provided = subprotocolToken(r)
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(g.Token)) == 1
}

// IsWebSocketUpgrade reports whether r is a WebSocket handshake.
func IsWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
				return true
			}
		}
	}
	return false
}

// NegotiateSubprotocol returns the subprotocol a terminal handshake may echo,
// or "" when the client did not offer the static name. The token-carrying entry
// is deliberately never returned.
func NegotiateSubprotocol(r *http.Request) string {
	for entry := range subprotocols(r) {
		if entry == TerminalSubprotocol {
			return TerminalSubprotocol
		}
	}
	return ""
}

func subprotocolToken(r *http.Request) string {
	for entry := range subprotocols(r) {
		if token, ok := strings.CutPrefix(entry, tokenSubprotocolPrefix); ok {
			return token
		}
	}
	return ""
}

// subprotocols yields each offered subprotocol; the header may repeat or use one
// comma-separated value.
func subprotocols(r *http.Request) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, value := range r.Header.Values("Sec-WebSocket-Protocol") {
			for _, entry := range strings.Split(value, ",") {
				if !yield(strings.TrimSpace(entry)) {
					return
				}
			}
		}
	}
}

func setSecurityHeaders(header http.Header, requestPath string) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	if strings.HasPrefix(requestPath, "/api/") {
		header.Set("Cache-Control", "no-store")
		return
	}
	header.Set("Content-Security-Policy", documentPolicy)
}
