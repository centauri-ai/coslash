package hardening

import (
	"net/http"
	"strings"
	"testing"
)

// The threat model this file encodes: a page in the user's browser can reach
// 127.0.0.1 even though a remote host cannot, so binding to loopback is not an
// authorization boundary. Everything below is a request such a page could
// actually make.

func TestEveryCanvasAPIRouteRefusesAnUnauthenticatedRequest(t *testing.T) {
	suite := newSuite(t)
	for _, route := range suite.apiRoutes() {
		route.noToken = true
		response, body := suite.do(t, route)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s answered %d without a token, want 401 (body %q)",
				route.method, route.path, response.StatusCode, body)
		}
	}
	if suite.dagamaReached.Load() {
		t.Fatal("an unauthenticated request reached the DaGama route group")
	}
}

func TestEveryCanvasAPIRouteRefusesAWrongToken(t *testing.T) {
	suite := newSuite(t)
	for _, route := range suite.apiRoutes() {
		route.token = wrongToken
		response, body := suite.do(t, route)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s answered %d with a wrong token, want 401 (body %q)",
				route.method, route.path, response.StatusCode, body)
		}
	}
	if suite.dagamaReached.Load() {
		t.Fatal("a wrongly authenticated request reached the DaGama route group")
	}
}

func TestATokenPrefixIsNotAToken(t *testing.T) {
	suite := newSuite(t)
	// A prefix comparison would accept these; a constant-time full comparison
	// does not. Both directions are checked because a naive HasPrefix can be
	// wrong either way round.
	// The empty credential is covered separately; the harness treats an empty
	// token field as "send the valid one". Surrounding whitespace is not
	// listed: HTTP strips optional whitespace around a field value, so " tok"
	// never reaches the guard as anything but "tok".
	for _, token := range []string{
		validToken[:len(validToken)-1],
		validToken + "x",
		strings.ToUpper(validToken),
		strings.Repeat("a", len(validToken)),
	} {
		response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1", token: token})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q was accepted with %d", token, response.StatusCode)
		}
	}
}

func TestBearerAuthorizationIsAcceptedAndNothingElseIs(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		path:    "/api/canvas/workspaces/claude/session-1",
		noToken: true,
		headers: map[string]string{"Authorization": "Bearer " + validToken},
	})
	if response.StatusCode == http.StatusUnauthorized {
		t.Fatal("a valid bearer token was refused")
	}
	for _, header := range []string{"Basic " + validToken, validToken, "bearer " + validToken} {
		response, _ := suite.do(t, call{
			path:    "/api/canvas/workspaces/claude/session-1",
			noToken: true,
			headers: map[string]string{"Authorization": header},
		})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q was accepted with %d", header, response.StatusCode)
		}
	}
}

func TestAnUnexpectedHostIsRefusedBeforeAuthentication(t *testing.T) {
	suite := newSuite(t)
	// DNS rebinding: the attacker controls the name, the browser resolves it to
	// 127.0.0.1, and the request arrives with the attacker's Host.
	for _, host := range []string{"evil.example.com", "evil.example.com:80", "127.0.0.1:1", "attacker"} {
		response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1", host: host})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("host %q answered %d, want 403", host, response.StatusCode)
		}
	}
}

func TestLoopbackAliasesOnTheListenerPortAreAccepted(t *testing.T) {
	suite := newSuite(t)
	port := suite.address[strings.LastIndex(suite.address, ":"):]
	for _, host := range []string{"localhost" + port, "LOCALHOST" + port, "[::1]" + port} {
		response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1", host: host})
		if response.StatusCode == http.StatusForbidden {
			t.Fatalf("loopback alias %q was refused", host)
		}
	}
}

func TestACrossSiteRequestIsRefusedEvenWithAValidToken(t *testing.T) {
	suite := newSuite(t)
	// The token lives in the page, so a stolen-token cross-site request is the
	// case that matters: Sec-Fetch-Site is the header a browser sets and a
	// hostile page cannot forge.
	for _, site := range []string{"cross-site", "same-site"} {
		response, _ := suite.do(t, call{
			path: "/api/canvas/workspaces/claude/session-1",
			site: site,
		})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("Sec-Fetch-Site %q answered %d, want 403", site, response.StatusCode)
		}
	}
	if suite.dagamaReached.Load() {
		t.Fatal("a cross-site request reached a route group")
	}
}

func TestSameOriginAndDirectNavigationAreAccepted(t *testing.T) {
	suite := newSuite(t)
	for _, site := range []string{"same-origin", "none"} {
		response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1", site: site})
		if response.StatusCode == http.StatusForbidden {
			t.Fatalf("Sec-Fetch-Site %q was refused", site)
		}
	}
	// A top-level navigation is allowed through even cross-site, because that is
	// the user clicking the URL printed in their terminal.
	response, _ := suite.do(t, call{
		path: "/api/canvas/workspaces/claude/session-1",
		site: "cross-site", mode: "navigate", dest: "document",
	})
	if response.StatusCode == http.StatusForbidden {
		t.Fatal("a top-level navigation was refused")
	}
}

func TestAnUnexpectedOriginIsRefusedWhenTheBrowserSendsNoFetchMetadata(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		path:   "/api/canvas/workspaces/claude/session-1",
		origin: "http://evil.example.com",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin answered %d, want 403", response.StatusCode)
	}
	same, _ := suite.do(t, call{
		path:   "/api/canvas/workspaces/claude/session-1",
		origin: "http://" + suite.address,
	})
	if same.StatusCode == http.StatusForbidden {
		t.Fatal("the server's own origin was refused")
	}
}

func TestAPIResponsesCarryTheHardeningHeaders(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
	for header, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
		// An API answer that a browser cached would survive a token rotation.
		"Cache-Control": "no-store",
	} {
		if got := response.Header.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestRefusalsCarryTheHardeningHeadersToo(t *testing.T) {
	suite := newSuite(t)
	// A 401 is still a response a browser stores and renders, so the headers
	// have to be set before the decision, not after it.
	response, _ := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1", noToken: true})
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("a refusal was served without nosniff")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("a refusal was served cacheable")
	}
}

func TestAGuardWithNoTokenAuthenticatesNothing(t *testing.T) {
	// A collector that failed to load its token must be closed, not open. The
	// empty-token case is the one where a "provided == configured" comparison
	// silently authenticates every request.
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		path:    "/api/canvas/workspaces/claude/session-1",
		noToken: true,
		headers: map[string]string{"X-Coslash-Token": ""},
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an empty credential answered %d, want 401", response.StatusCode)
	}
}

func TestTheTerminalWebSocketRefusesAHandshakeWithoutTheTokenSubprotocol(t *testing.T) {
	suite := newSuite(t)
	// A browser cannot set headers on a WebSocket handshake, so the token rides
	// in Sec-WebSocket-Protocol. A handshake offering only the static name is
	// therefore unauthenticated and must be refused.
	response, _ := suite.do(t, call{
		path:    "/api/terminals/terminal-1/ws",
		noToken: true,
		headers: map[string]string{
			"Upgrade":                "websocket",
			"Connection":             "Upgrade",
			"Sec-WebSocket-Version":  "13",
			"Sec-WebSocket-Key":      "dGhlIHNhbXBsZSBub25jZQ==",
			"Sec-WebSocket-Protocol": "coslash.terminal.v1",
		},
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated handshake answered %d, want 401", response.StatusCode)
	}
}

func TestAWrongTokenSubprotocolIsRefused(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		path:    "/api/terminals/terminal-1/ws",
		noToken: true,
		headers: map[string]string{
			"Upgrade":                "websocket",
			"Connection":             "Upgrade",
			"Sec-WebSocket-Version":  "13",
			"Sec-WebSocket-Key":      "dGhlIHNhbXBsZSBub25jZQ==",
			"Sec-WebSocket-Protocol": "coslash.terminal.v1, coslash.token." + wrongToken,
		},
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong subprotocol token answered %d, want 401", response.StatusCode)
	}
}

func TestTheSubprotocolTokenIsNotAcceptedOnAnOrdinaryRequest(t *testing.T) {
	suite := newSuite(t)
	// Without this, a hostile page could authenticate a plain fetch by setting
	// a subprotocol header, which a browser will happily let it do.
	response, _ := suite.do(t, call{
		path:    "/api/canvas/workspaces/claude/session-1",
		noToken: true,
		headers: map[string]string{"Sec-WebSocket-Protocol": "coslash.token." + validToken},
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a subprotocol token authenticated a plain request with %d", response.StatusCode)
	}
}

func TestTheTerminalHandshakeNeverEchoesTheToken(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		path:    "/api/terminals/terminal-1/ws",
		noToken: true,
		headers: map[string]string{
			"Upgrade":                "websocket",
			"Connection":             "Upgrade",
			"Sec-WebSocket-Version":  "13",
			"Sec-WebSocket-Key":      "dGhlIHNhbXBsZSBub25jZQ==",
			"Sec-WebSocket-Protocol": "coslash.terminal.v1, coslash.token." + validToken,
		},
	})
	// The terminal does not exist, so this cannot complete — what matters is
	// that no response header ever repeats the credential back, where a proxy
	// log or a devtools panel would keep it.
	for name, values := range response.Header {
		for _, value := range values {
			if strings.Contains(value, validToken) {
				t.Fatalf("response header %s echoed the token: %q", name, value)
			}
		}
	}
}
