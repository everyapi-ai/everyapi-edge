package console

import (
	"errors"
	"net/http"
	"net/url"
)

var errCrossSiteMutation = errors.New("cross-site mutation denied")

// sameOriginMutations protects the browser-facing management API from being
// driven by an unrelated website. Metadata-free internal callers use the
// separate Control handler, which deliberately does not install this guard.
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
