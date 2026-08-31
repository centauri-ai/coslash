// Package httpsec guards the loopback server against browser-borne attacks.
// A page in the user's browser can reach 127.0.0.1 even though a remote host
// cannot, so loopback binding alone is not an authorization boundary.
package httpsec

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/observe"
)

const documentPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'"

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
			observe.Event("issue.httpsec.reject",
				"reason", "host",
				"status", http.StatusForbidden,
				"detail", "rejected unexpected host",
			)
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			navigation := strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") &&
				strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document")
			if !navigation && !strings.EqualFold(site, "same-origin") && !strings.EqualFold(site, "none") {
				log.Printf("http security: rejected cross-site request")
				observe.Event("issue.httpsec.reject",
					"reason", "cross_site",
					"status", http.StatusForbidden,
					"detail", "rejected cross-site request",
				)
				http.Error(w, "request rejected", http.StatusForbidden)
				return
			}
		} else if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+g.Addr {
			log.Printf("http security: rejected unexpected origin %q", origin)
			observe.Event("issue.httpsec.reject",
				"reason", "origin",
				"status", http.StatusForbidden,
				"detail", "rejected unexpected origin",
			)
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") && !g.allowedToken(r) {
			log.Printf("http security: rejected unauthenticated API request")
			observe.Event("issue.httpsec.reject",
				"reason", "unauthenticated",
				"status", http.StatusUnauthorized,
				"detail", "rejected unauthenticated API request",
			)
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
	return subtle.ConstantTimeCompare([]byte(provided), []byte(g.Token)) == 1
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
