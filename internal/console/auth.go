package console

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "edge_session"
	sessionLifetime   = 7 * 24 * time.Hour
	maxSessionBody    = 4 << 10
)

type sessionAuthenticator struct {
	mu          sync.RWMutex
	enabled     bool
	tokenDigest [sha256.Size]byte
	signingKey  [sha256.Size]byte
	now         func() time.Time
	random      io.Reader
}

type sessionState struct {
	Authenticated   bool `json:"authenticated"`
	PairingRequired bool `json:"pairing_required"`
}

func newSessionAuthenticator(token string) *sessionAuthenticator {
	trimmed := strings.TrimSpace(token)
	authenticator := &sessionAuthenticator{
		enabled: trimmed != "",
		now:     time.Now,
		random:  rand.Reader,
	}
	if trimmed == "" {
		return authenticator
	}
	authenticator.tokenDigest = sha256.Sum256([]byte(trimmed))
	authenticator.signingKey = sha256.Sum256([]byte("everyapi-edge-console-session\x00" + trimmed))
	return authenticator
}

func (a *sessionAuthenticator) session(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		authenticated := a.authenticated(r)
		a.mu.RLock()
		pairingRequired := a.enabled
		a.mu.RUnlock()
		writeJSON(w, http.StatusOK, sessionState{Authenticated: authenticated, PairingRequired: pairingRequired})
	case http.MethodPost:
		a.login(w, r)
	case http.MethodDelete:
		http.SetCookie(w, a.expiredCookie(r.TLS != nil))
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, errors.New("API route not found"))
	}
}

func (a *sessionAuthenticator) rotate(token string) error {
	trimmed := strings.TrimSpace(token)
	if len(trimmed) < 32 {
		return errors.New("pairing token must be at least 32 characters")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = true
	a.tokenDigest = sha256.Sum256([]byte(trimmed))
	a.signingKey = sha256.Sum256([]byte("everyapi-edge-console-session\x00" + trimmed))
	return nil
}

func (a *sessionAuthenticator) login(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.enabled {
		writeJSON(w, http.StatusOK, sessionState{Authenticated: true, PairingRequired: false})
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSessionBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid pairing request"))
		return
	}
	provided := sha256.Sum256([]byte(strings.TrimSpace(input.Token)))
	if subtle.ConstantTimeCompare(provided[:], a.tokenDigest[:]) != 1 {
		writeError(w, http.StatusUnauthorized, errors.New("pairing token is invalid"))
		return
	}
	cookie, err := a.newSessionCookie(r.TLS != nil)
	if err != nil {
		writePrivateError(w, http.StatusInternalServerError, "The control room could not create a session.", err)
		return
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, sessionState{Authenticated: true, PairingRequired: true})
}

func (a *sessionAuthenticator) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authenticated(r) {
			writeError(w, http.StatusUnauthorized, errors.New("pairing required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *sessionAuthenticator) authenticated(r *http.Request) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authenticatedLocked(r)
}

func (a *sessionAuthenticator) authenticatedLocked(r *http.Request) bool {
	if !a.enabled {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	now := a.now().UTC()
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(sessionLifetime)) {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected := a.signature(parts[0] + "." + parts[1])
	return hmac.Equal(signature, expected)
}

func (a *sessionAuthenticator) newSessionCookie(secure bool) (*http.Cookie, error) {
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(a.random, nonce); err != nil {
		return nil, err
	}
	expiresAt := a.now().UTC().Add(sessionLifetime)
	payload := strconv.FormatInt(expiresAt.Unix(), 10) + "." + base64.RawURLEncoding.EncodeToString(nonce)
	value := payload + "." + base64.RawURLEncoding.EncodeToString(a.signature(payload))
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

func (a *sessionAuthenticator) expiredCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (a *sessionAuthenticator) signature(payload string) []byte {
	mac := hmac.New(sha256.New, a.signingKey[:])
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
