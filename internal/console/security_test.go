package console

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// consoleHTTPRequest builds a request addressed the way a browser on the local console addresses it.
//
// httptest.NewRequest defaults Host to "example.com", which is precisely the header a DNS-rebinding attacker
// sends. The console now rejects it, so tests that exercise the browser surface must name the console.
func consoleHTTPRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "127.0.0.1:8421"
	return request
}

// TestBrowserSurfaceRejectsForeignHostHeaders pins the DNS-rebinding defense.
//
// The console binds to 127.0.0.1:8421, which is not a boundary a browser enforces. An attacker publishes
// evil.example with a one-second TTL, gets the victim to load a page from it, then re-answers the name with
// 127.0.0.1. Every subsequent fetch to http://evil.example:8421 lands on this server, and the browser considers
// it *same-origin* — the origin is evil.example on both ends. So Sec-Fetch-Site reads "same-origin", Origin
// equals Host, and sameOriginMutations lets the request through; GET and HEAD were never checked by it at all.
// With no pairing token configured the session authenticator also returns true for everything, which left the
// defeated origin check as the only gate in front of model pulls, storage paths, resource policy and the
// playground.
//
// The Host header is the one thing rebinding cannot forge, because the browser reports the name the page came
// from. Every case below carries the exact headers the attack produces.
func TestBrowserSurfaceRejectsForeignHostHeaders(t *testing.T) {
	handler := NewHandlers(Config{ConsoleAddr: "127.0.0.1:8421"}, NewStore(1)).Browser

	for _, test := range []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "read the node inventory", method: http.MethodGet, target: "/api/node"},
		{name: "read the session state", method: http.MethodGet, target: "/api/session"},
		{name: "load the console page", method: http.MethodGet, target: "/"},
		{name: "drain the node", method: http.MethodPost, target: "/api/drain", body: `{"drain":true}`},
		{name: "pull a model", method: http.MethodPost, target: "/api/models/pull", body: `{"name":"qwen3:8b"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			// Exactly what a rebound browser sends: it believes it is talking to its own origin.
			request.Host = "evil.example:8421"
			request.Header.Set("Origin", "http://evil.example:8421")
			request.Header.Set("Sec-Fetch-Site", "same-origin")

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("%s %s from a rebound origin = %d, want 403; body=%s",
					test.method, test.target, response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "unexpected Host header") {
				t.Fatalf("rejection body = %s", response.Body.String())
			}
		})
	}
}

// TestBrowserSurfaceAcceptsTheNamesTheConsoleAnswersTo keeps the guard from breaking real deployments: loopback
// binaries, containers published on a LAN address, and operators who reach a wildcard bind through a name they
// listed themselves.
func TestBrowserSurfaceAcceptsTheNamesTheConsoleAnswersTo(t *testing.T) {
	for _, test := range []struct {
		name         string
		consoleAddr  string
		allowedHosts []string
		host         string
	}{
		{name: "loopback literal", consoleAddr: "127.0.0.1:8421", host: "127.0.0.1:8421"},
		{name: "localhost", consoleAddr: "127.0.0.1:8421", host: "localhost:8421"},
		{name: "fully qualified localhost", consoleAddr: "127.0.0.1:8421", host: "localhost.:8421"},
		{name: "ipv6 loopback", consoleAddr: "[::1]:8421", host: "[::1]:8421"},
		{name: "lan address behind a wildcard bind", consoleAddr: "0.0.0.0:8421", host: "192.168.1.20:8421"},
		{name: "bound hostname", consoleAddr: "edge.internal:8421", host: "edge.internal:8421"},
		{name: "operator allowlisted name", consoleAddr: "0.0.0.0:8421", allowedHosts: []string{"studio.local"}, host: "studio.local:8421"},
		{name: "no port", consoleAddr: "127.0.0.1:8421", host: "127.0.0.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandlers(Config{ConsoleAddr: test.consoleAddr, AllowedHosts: test.allowedHosts}, NewStore(1)).Browser
			request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
			request.Host = test.host

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("GET /api/session as %q = %d, want 200; body=%s", test.host, response.Code, response.Body.String())
			}
		})
	}
}

// TestControlSurfaceIsNotHostGuarded documents the deliberate asymmetry: Control is the in-process gateway
// surface, reached over a pipe rather than a browser, and it carries no browser metadata at all — the same reason
// sameOriginMutations is absent there.
func TestControlSurfaceIsNotHostGuarded(t *testing.T) {
	handlers := NewHandlers(Config{ConsoleAddr: "127.0.0.1:8421"}, NewStore(1))
	request := httptest.NewRequest(http.MethodGet, "/api/node", nil)
	request.Host = "anything.invalid"

	response := httptest.NewRecorder()
	handlers.Control.ServeHTTP(response, request)

	if response.Code == http.StatusForbidden {
		t.Fatalf("control surface rejected an internal caller: %d %s", response.Code, response.Body.String())
	}
}

func TestHostIsOurs(t *testing.T) {
	allowed := allowedHostSet("0.0.0.0:8421", []string{"Studio.Local:8421", " edge.internal ", ""})

	for host, want := range map[string]bool{
		"127.0.0.1:8421":     true,
		"127.0.0.1":          true,
		"192.168.1.20:8421":  true,
		"[::1]:8421":         true,
		"localhost:8421":     true,
		"LOCALHOST":          true,
		"console.localhost":  true,
		"studio.local:8421":  true,
		"edge.internal:8421": true,
		"evil.example:8421":  false,
		"evil.example":       false,
		"":                   false,
		"0.0.0.0.evil.com":   false,
		"notlocalhost":       false,
	} {
		if got := hostIsOurs(host, allowed); got != want {
			t.Errorf("hostIsOurs(%q) = %t, want %t", host, got, want)
		}
	}
}

// A wildcard bind must not be readable as "answer to any name": "0.0.0.0" is an interface selector.
func TestAllowedHostSetIgnoresWildcardBinds(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8421", "[::]:8421", ":8421", ""} {
		if allowed := allowedHostSet(addr, nil); len(allowed) != 0 {
			t.Errorf("allowedHostSet(%q) = %v, want no names", addr, allowed)
		}
	}
	if allowed := allowedHostSet("edge.internal:8421", nil); !allowed["edge.internal"] {
		t.Errorf("allowedHostSet dropped the bound hostname: %v", allowed)
	}
}
