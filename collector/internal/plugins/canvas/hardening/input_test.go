package hardening

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Malicious input, sent by a client that has already authenticated.
//
// Authentication is not the whole boundary: the token lives in the page, so any
// script the page loads can spend it. What stops a spent token from reading
// /etc/passwd or writing outside the workspace root is the scoping each handler
// does on the values it is given.

func TestSessionFilePreviewRefusesEscapingTheWorkspace(t *testing.T) {
	suite := newSuite(t)
	secret := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"../outside.md",
		"../../etc/passwd",
		"subdir/../../outside.md",
		"..%2Foutside.md",
		"/etc/passwd",
		secret,
		"./../../outside.md",
	} {
		response, body := suite.do(t, call{
			path: "/api/canvas/sessions/claude/session-1/files?path=" + escape(path),
		})
		if response.StatusCode == http.StatusOK {
			t.Fatalf("file preview served %q with 200: %s", path, body)
		}
		if strings.Contains(string(body), "private") {
			t.Fatalf("file preview leaked contents from outside the workspace for %q", path)
		}
	}
}

func TestSessionFilePreviewRefusesASymlinkOutOfTheWorkspace(t *testing.T) {
	suite := newSuite(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(suite.workingDirectory, "escape.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The path is inside the workspace and resolves outside it. Refusing needs
	// the check to be on the resolved target, not the given name.
	response, body := suite.do(t, call{
		path: "/api/canvas/sessions/claude/session-1/files?path=escape.md",
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("file preview followed a symlink out of the workspace: %s", body)
	}
	if strings.Contains(string(body), "private") {
		t.Fatal("file preview leaked the symlink target's contents")
	}
}

func TestRenderedFilePreviewIsInert(t *testing.T) {
	suite := newSuite(t)
	page := filepath.Join(suite.workingDirectory, "notes.md")
	// An agent's working directory can contain anything, including markup an
	// attacker put there. Markdown is deliberately rendered rather than served
	// as plain text, so what has to hold is that the rendering cannot execute:
	// the source is escaped, and the response sandboxes itself out of the
	// coSlash origin, where the API token lives.
	payload := "# notes\n<script>alert(1)</script>\n<img src=x onerror=alert(2)>\n"
	if err := os.WriteFile(page, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	response, body := suite.do(t, call{
		path: "/api/canvas/sessions/claude/session-1/files?path=notes.md",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("in-workspace file answered %d: %s", response.StatusCode, body)
	}

	// What makes the payload inert is that its angle brackets are escaped, so
	// no tag is ever opened. The attribute text survives as literal characters
	// inside a <pre>, which is the correct outcome, not a leak.
	rendered := string(body)
	for _, tag := range []string{"<script", "<img"} {
		if strings.Contains(rendered, tag) {
			t.Fatalf("workspace markup opened a real %q tag in the preview: %s", tag, rendered)
		}
	}
	for _, escaped := range []string{"&lt;script&gt;", "&lt;img src=x onerror=alert(2)&gt;"} {
		if !strings.Contains(rendered, escaped) {
			t.Fatalf("the preview did not escape %q: %s", escaped, rendered)
		}
	}

	policy := response.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "sandbox"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("a rendered preview was served without %q: %q", directive, policy)
		}
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("a rendered preview was served sniffable")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("a rendered preview was served cacheable")
	}
}

func TestAnUnsupportedFileTypeIsRefusedRatherThanGuessed(t *testing.T) {
	suite := newSuite(t)
	// The allowlist is what keeps an executable, an archive, or a credential
	// file out of a preview that a browser would then try to interpret.
	for name, contents := range map[string]string{
		"secrets.env": "TOKEN=abc",
		"payload.exe": "MZ",
		"archive.zip": "PK",
	} {
		if err := os.WriteFile(filepath.Join(suite.workingDirectory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		response, body := suite.do(t, call{
			path: "/api/canvas/sessions/claude/session-1/files?path=" + name,
		})
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("%s answered %d, want 415: %s", name, response.StatusCode, body)
		}
		if strings.Contains(string(body), contents) {
			t.Fatalf("%s leaked its contents in the refusal", name)
		}
	}
}

func TestWorkspaceIdentityNeverBecomesAFilesystemPath(t *testing.T) {
	suite := newSuite(t)
	// The store deliberately does not validate identities into path-safety; it
	// digests them, so no identity is ever a path component. That is the
	// stronger design, and this is the property it has to keep: a traversal in
	// either half must land inside the canvas root like any other identity.
	before := treeOf(t, suite.canvasHome)
	for _, identity := range [][2]string{
		{"claude", "..%2F..%2Fetc%2Fpasswd"},
		{"..%2F..%2Fclaude", "session-1"},
		{"claude", "%2Fabsolute%2Fpath"},
		{"claude", "....%2F%2F....%2F%2Fescape"},
	} {
		response, body := suite.do(t, call{
			method: http.MethodPut,
			path:   fmt.Sprintf("/api/canvas/workspaces/%s/%s", identity[0], identity[1]),
			body:   `{"schemaVersion":1,"expectedRevision":0,"state":{"note":"x"}}`,
		})
		if response.StatusCode >= 500 {
			t.Fatalf("identity %v answered %d: %s", identity, response.StatusCode, body)
		}
	}

	// Nothing may have appeared outside the canvas root, and every new file
	// must be a digest-named document.
	for _, path := range added(before, treeOf(t, suite.canvasHome)) {
		name := filepath.Base(path)
		if strings.Contains(path, "..") {
			t.Fatalf("a traversal identity produced the path %q", path)
		}
		if !strings.HasSuffix(name, ".json") {
			t.Fatalf("an identity produced a non-document file %q", path)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(suite.canvasHome), "etc")); err == nil {
		t.Fatal("a traversal identity created a directory beside the canvas root")
	}
}

func TestWorkspacesWithColliderIdentitiesStaySeparate(t *testing.T) {
	suite := newSuite(t)
	// Digesting agent+id must not let two different identities meet. The pair
	// below is the classic concatenation collision: "ab"+"c" versus "a"+"bc".
	write := func(agent, id, note string) {
		response, body := suite.do(t, call{
			method: http.MethodPut,
			path:   fmt.Sprintf("/api/canvas/workspaces/%s/%s", agent, id),
			body:   fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":0,"state":{"note":%q}}`, note),
		})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("write %s/%s answered %d: %s", agent, id, response.StatusCode, body)
		}
	}
	write("ab", "c", "first")
	write("a", "bc", "second")

	_, body := suite.do(t, call{path: "/api/canvas/workspaces/ab/c"})
	if !strings.Contains(string(body), "first") {
		t.Fatalf("identity ab/c read back the wrong document: %s", body)
	}
	_, body = suite.do(t, call{path: "/api/canvas/workspaces/a/bc"})
	if !strings.Contains(string(body), "second") {
		t.Fatalf("identity a/bc read back the wrong document: %s", body)
	}
}

func TestWorkspaceRefusesAnUnstorableIdentity(t *testing.T) {
	suite := newSuite(t)
	// Control characters and invalid UTF-8 cannot survive a JSON round trip
	// unambiguously, so they are refused at the boundary rather than stored.
	for _, path := range []string{
		"/api/canvas/workspaces/claude/%00null",
		"/api/canvas/workspaces/claude/%07bell",
		"/api/canvas/workspaces/claude/",
		"/api/canvas/workspaces//session-1",
	} {
		response, body := suite.do(t, call{path: path})
		if response.StatusCode == http.StatusOK {
			t.Fatalf("%s answered 200: %s", path, body)
		}
	}
}

func TestWorkspaceWritesAreRevisionCheckedNotLastWriteWins(t *testing.T) {
	suite := newSuite(t)
	if response := suite.writeWorkspace(t, "claude", "session-1", 0); response.StatusCode != http.StatusOK {
		t.Fatalf("first write answered %d", response.StatusCode)
	}
	// A second tab still holding revision 0 must not silently overwrite.
	response := suite.writeWorkspace(t, "claude", "session-1", 0)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("a stale write answered %d, want 409", response.StatusCode)
	}
}

func TestWorkspaceRefusesAnOversizedBody(t *testing.T) {
	suite := newSuite(t)
	// Unbounded state is a memory-exhaustion vector on a long-lived process.
	huge := strings.Repeat("a", 2<<20)
	response, _ := suite.do(t, call{
		method: http.MethodPut,
		path:   "/api/canvas/workspaces/claude/session-1",
		body:   fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":0,"state":{"note":%q}}`, huge),
	})
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized workspace answered %d, want 413", response.StatusCode)
	}
}

func TestWorkspaceRefusesAWrongMethodAndAWrongContentType(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{method: http.MethodDelete, path: "/api/canvas/workspaces/claude/session-1"})
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE answered %d, want 405", response.StatusCode)
	}
	if allow := response.Header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
		t.Fatalf("Allow header was %q", allow)
	}
	// A form content type is what a cross-origin form post would carry.
	form, _ := suite.do(t, call{
		method:  http.MethodPut,
		path:    "/api/canvas/workspaces/claude/session-1",
		body:    `{"schemaVersion":1,"expectedRevision":0,"state":{}}`,
		headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	})
	if form.StatusCode < 400 {
		t.Fatalf("a form-encoded write answered %d", form.StatusCode)
	}
}

func TestTerminalIdentityRefusesTraversalAndUnknownTerminals(t *testing.T) {
	suite := newSuite(t)
	for _, id := range []string{"..%2F..%2Fetc", "%2Fetc%2Fpasswd", "a b", strings.Repeat("x", 300)} {
		response, body := suite.do(t, call{path: "/api/terminals/" + id})
		if response.StatusCode == http.StatusOK {
			t.Fatalf("terminal id %q answered 200: %s", id, body)
		}
	}
}

func TestTerminalInputRefusesAnOversizedPayload(t *testing.T) {
	suite := newSuite(t)
	// Terminal input reaches a PTY. An unbounded write is both a memory and a
	// downstream-injection concern.
	response, _ := suite.do(t, call{
		method: http.MethodPost,
		path:   "/api/terminals/terminal-1/input",
		body:   fmt.Sprintf(`{"type":"input","data":%q}`, strings.Repeat("x", 1<<20)),
	})
	if response.StatusCode < 400 {
		t.Fatalf("an oversized terminal write answered %d", response.StatusCode)
	}
}

func TestSessionRenameRefusesAnUnknownSessionBeforeTouchingTheVendorStore(t *testing.T) {
	suite := newSuite(t)
	response, _ := suite.do(t, call{
		method: http.MethodPut,
		path:   "/api/canvas/sessions/claude/unknown-session/name",
		body:   `{"name":"renamed"}`,
	})
	if response.StatusCode < 400 {
		t.Fatalf("renaming an unknown session answered %d", response.StatusCode)
	}
	if suite.renamer.calls.Load() != 0 {
		t.Fatal("an unknown session reached the vendor metadata store")
	}
}

func TestErrorBodiesNeverCarryPrivatePathsOrCredentials(t *testing.T) {
	suite := newSuite(t)
	// Every refusal a hostile client can provoke, checked for the two things a
	// message must never contain: where the user's files are, and the token.
	for _, request := range []call{
		{path: "/api/canvas/sessions/claude/unknown/"},
		{path: "/api/canvas/sessions/claude/session-1/files?path=../../etc/passwd"},
		{path: "/api/canvas/workspaces/claude/nope"},
		{path: "/api/terminals/missing-terminal"},
		{method: http.MethodPut, path: "/api/canvas/workspaces/claude/session-1", body: `not json`},
	} {
		_, body := suite.do(t, request)
		text := string(body)
		for _, forbidden := range []string{suite.workingDirectory, validToken, "goroutine ", "/Users/", "panic:"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s leaked %q: %s", request.path, forbidden, text)
			}
		}
	}
}

func TestUnknownCanvasRoutesDoNotFallThroughToAnotherHandler(t *testing.T) {
	suite := newSuite(t)
	// A prefix-mounted group that answers everything under it would turn a
	// typo into an unintended surface.
	for _, path := range []string{
		"/api/canvas/",
		"/api/canvas/sessions",
		"/api/canvas/workspaces",
		"/api/terminals",
	} {
		response, _ := suite.do(t, call{path: path})
		if response.StatusCode == http.StatusOK {
			t.Fatalf("%s answered 200", path)
		}
	}
}

func escape(value string) string {
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "#", "%23", "&", "%26", "+", "%2B")
	return replacer.Replace(value)
}

// treeOf lists every regular file beneath root, so a test can assert that a
// hostile identity added nothing outside it.
func treeOf(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			found[path] = true
		}
		return nil
	})
	return found
}

func added(before, after map[string]bool) []string {
	var paths []string
	for path := range after {
		if !before[path] {
			paths = append(paths, path)
		}
	}
	return paths
}
