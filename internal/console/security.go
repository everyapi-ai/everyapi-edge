package console

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var errCrossSiteMutation = errors.New("cross-site mutation denied")

var errUnexpectedHost = errors.New("unexpected Host header")

// guardHostHeader rejects requests that did not address the console by a name it answers to.
//
// This is the defense against DNS rebinding, and nothing else in the stack provides it. The console binds to
// 127.0.0.1:8421 by default, which is not a security boundary a browser respects: an attacker publishes
// evil.example with a short TTL, has the victim load a page from it, then re-answers the name with 127.0.0.1.
// The victim's browser now issues requests to http://evil.example:8421 that land on this server — and as far as
// the browser is concerned they are *same-origin*, because the origin is evil.example either way. So
// Sec-Fetch-Site says "same-origin", the Origin header matches r.Host exactly, and sameOriginMutations waves the
// request through. GET and HEAD skip that guard entirely, so reads were never checked at all.
//
// With no pairing token configured, sessionAuthenticator.authenticated returns true for every request, which
// makes the origin check the *only* gate in front of the whole management API — model pulls, storage paths,
// resource policy, the playground.
//
// The Host header is what rebinding cannot forge: the browser sends the name the page was served from, and the
// attacker's name is never one of ours. Applied to every method, ahead of authentication.
func guardHostHeader(consoleAddr string, extraHosts []string, next http.Handler) http.Handler {
	allowed := allowedHostSet(consoleAddr, extraHosts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostIsOurs(r.Host, allowed) {
			writeError(w, http.StatusForbidden, errUnexpectedHost)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHostSet collects the names this console answers to: the hostname an operator bound it to, so a
// deployment that listens on a real name (edge.internal:8421) keeps working, plus whatever the operator listed in
// EVERYAPI_CONSOLE_ALLOWED_HOSTS. A wildcard bind contributes no name — "0.0.0.0" says "every interface", not
// "answer to any name" — which is exactly the deployment the allowlist exists for.
func allowedHostSet(consoleAddr string, extraHosts []string) map[string]bool {
	allowed := make(map[string]bool, len(extraHosts)+1)
	for _, host := range append([]string{consoleAddr}, extraHosts...) {
		if name := normalizeHost(host); name != "" && name != "0.0.0.0" && name != "::" && name != "*" {
			// IP literals are accepted unconditionally in hostIsOurs; no need to carry them here.
			if net.ParseIP(name) == nil {
				allowed[name] = true
			}
		}
	}
	return allowed
}

// hostIsOurs reports whether the Host header names this console.
func hostIsOurs(rawHost string, allowed map[string]bool) bool {
	host := normalizeHost(rawHost)
	if host == "" {
		return false
	}
	// An IP literal cannot be rebound: rebinding works by re-answering a *name*, and the browser reports the
	// literal it was given. Whatever address the console is reachable at, addressing it directly is legitimate.
	if net.ParseIP(host) != nil {
		return true
	}
	// RFC 6761 reserves localhost (and its subdomains) for the loopback interface; resolvers must not hand it to
	// an attacker.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	return allowed[host]
}

// normalizeHost reduces a "host", "host:port", or "[::1]:port" form to a bare lowercase hostname.
func normalizeHost(raw string) string {
	host := strings.TrimSpace(raw)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	// A trailing dot is a fully qualified name for the same host, and browsers do send it.
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

// sameOriginMutations protects the browser-facing management API from being driven by an unrelated website. Metadata-free internal callers use the separate Control handler, which deliberately does not install this guard.
func sameOriginMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		switch r.Header.Get("Sec-Fetch-Site") {
		case "same-origin":
			next.ServeHTTP(w, r)
			return
		case "cross-site", "same-site":
			writeError(w, http.StatusForbidden, errCrossSiteMutation)
			return
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			writeError(w, http.StatusForbidden, errCrossSiteMutation)
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || parsed.Host != r.Host {
			writeError(w, http.StatusForbidden, errCrossSiteMutation)
			return
		}
		next.ServeHTTP(w, r)
	})
}
