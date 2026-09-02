package console

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

var testConsoleToken = strings.Repeat("a1", 32)

func consoleRequest(method, target, body string) *http.Request {
	request := consoleHTTPRequest(method, target, strings.NewReader(body))
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	return request
}

func TestSessionReportsPairingRequirementWithoutExposingToken(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken}, NewStore(1)).Browser
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, consoleRequest(http.MethodGet, "/api/session", ""))

	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Authenticated   bool `json:"authenticated"`
		PairingRequired bool `json:"pairing_required"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Authenticated || !payload.PairingRequired {
		t.Fatalf("session payload = %#v", payload)
	}
	if strings.Contains(response.Body.String(), testConsoleToken) {
		t.Fatal("session response exposed the pairing token")
	}
}

func TestSessionAllowsPasswordlessLoopbackMode(t *testing.T) {
	handler := NewHandlers(Config{}, NewStore(1)).Browser
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, consoleRequest(http.MethodGet, "/api/session", ""))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) || !strings.Contains(response.Body.String(), `"pairing_required":false`) {
		t.Fatalf("passwordless session = %d %s", response.Code, response.Body.String())
	}
}

func TestBrowserAPIRequiresPairedSession(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken}, NewStore(1)).Browser
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, consoleRequest(http.MethodGet, "/api/node", ""))

	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("protected API = %d %s", response.Code, response.Body.String())
	}
}

func TestSessionRejectsInvalidPairingToken(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken}, NewStore(1)).Browser
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, consoleRequest(http.MethodPost, "/api/session", `{"token":"wrong-token"}`))

	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatalf("invalid pairing = %d cookies=%v body=%s", response.Code, response.Result().Cookies(), response.Body.String())
	}
}

func TestPairingTokenRotationInvalidatesExistingSessionAndOldToken(t *testing.T) {
	newToken := strings.Repeat("b2", 32)
	handlers := NewHandlers(Config{
		ConsoleToken: testConsoleToken,
		RotateConsoleToken: func() (string, error) {
			return newToken, nil
		},
	}, NewStore(1))
	login := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(login, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	cookie := login.Result().Cookies()[0]

	rotate := httptest.NewRecorder()
	request := consoleRequest(http.MethodPost, "/api/session/rotate", "{}")
	request.AddCookie(cookie)
	handlers.Browser.ServeHTTP(rotate, request)
	if rotate.Code != http.StatusOK || !strings.Contains(rotate.Body.String(), newToken) {
		t.Fatalf("rotate = %d %s", rotate.Code, rotate.Body.String())
	}

	oldSession := httptest.NewRecorder()
	request = consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(cookie)
	handlers.Browser.ServeHTTP(oldSession, request)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained valid: %d %s", oldSession.Code, oldSession.Body.String())
	}

	oldLogin := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(oldLogin, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old token remained valid: %d %s", oldLogin.Code, oldLogin.Body.String())
	}

	newLogin := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(newLogin, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+newToken+`"}`))
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new token rejected: %d %s", newLogin.Code, newLogin.Body.String())
	}
}

func TestPairingTokenRotationKeepsTheRotatingOperatorSignedIn(t *testing.T) {
	newToken := strings.Repeat("c3", 32)
	handlers := NewHandlers(Config{
		ConsoleToken: testConsoleToken,
		NodeName:     "studio-gpu",
		RotateConsoleToken: func() (string, error) {
			return newToken, nil
		},
	}, NewStore(1))
	operator := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(operator, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	operatorCookie := operator.Result().Cookies()[0]
	bystander := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(bystander, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	bystanderCookie := bystander.Result().Cookies()[0]

	rotate := httptest.NewRecorder()
	request := consoleRequest(http.MethodPost, "/api/session/rotate", "{}")
	request.AddCookie(operatorCookie)
	handlers.Browser.ServeHTTP(rotate, request)
	if rotate.Code != http.StatusOK || !strings.Contains(rotate.Body.String(), newToken) {
		t.Fatalf("rotate = %d %s", rotate.Code, rotate.Body.String())
	}
	rotated := rotate.Result().Cookies()
	if len(rotated) != 1 || rotated[0].Name != sessionCookieName || rotated[0].MaxAge <= 0 {
		t.Fatalf("rotation cookie = %#v", rotated)
	}

	profile := httptest.NewRecorder()
	request = consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(rotated[0])
	handlers.Browser.ServeHTTP(profile, request)
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), "studio-gpu") {
		t.Fatalf("rotating operator lost the session that carries the new token: %d %s", profile.Code, profile.Body.String())
	}

	poll := httptest.NewRecorder()
	request = consoleRequest(http.MethodGet, "/api/session", "")
	request.AddCookie(rotated[0])
	handlers.Browser.ServeHTTP(poll, request)
	if poll.Code != http.StatusOK || !strings.Contains(poll.Body.String(), `"authenticated":true`) {
		t.Fatalf("session poll after rotation = %d %s", poll.Code, poll.Body.String())
	}

	other := httptest.NewRecorder()
	request = consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(bystanderCookie)
	handlers.Browser.ServeHTTP(other, request)
	if other.Code != http.StatusUnauthorized {
		t.Fatalf("a session opened before rotation survived it: %d %s", other.Code, other.Body.String())
	}
}

func TestPairingTokenRotationIsNotAvailableThroughRemoteControl(t *testing.T) {
	handlers := NewHandlers(Config{
		ConsoleToken: testConsoleToken,
		RotateConsoleToken: func() (string, error) {
			return strings.Repeat("b2", 32), nil
		},
	}, NewStore(1))
	response := httptest.NewRecorder()
	handlers.Control.ServeHTTP(response, consoleRequest(http.MethodPost, "/api/session/rotate", "{}"))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "b2b2") {
		t.Fatalf("remote control exposed token rotation: %d %s", response.Code, response.Body.String())
	}
}

func TestSessionLoginIssuesHardenedCookieAndUnlocksBrowserAPI(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken, NodeName: "studio-gpu"}, NewStore(1)).Browser
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, consoleRequest(http.MethodPost, "https://edge.local/api/session", `{"token":"`+testConsoleToken+`"}`))

	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %v", cookies)
	}
	cookie := cookies[0]
	if cookie.Value == "" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge <= 0 || cookie.Domain != "" {
		t.Fatalf("session cookie = %#v", cookie)
	}

	profile := httptest.NewRecorder()
	request := consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(cookie)
	handler.ServeHTTP(profile, request)
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), "studio-gpu") {
		t.Fatalf("authenticated API = %d %s", profile.Code, profile.Body.String())
	}
}

func TestSessionRejectsTamperedCookie(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken}, NewStore(1)).Browser
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	cookie := login.Result().Cookies()[0]
	cookie.Value += "tampered"

	response := httptest.NewRecorder()
	request := consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(cookie)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered session status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestSessionRejectsExpiredCookie(t *testing.T) {
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	authenticator := newSessionAuthenticator(testConsoleToken)
	authenticator.now = func() time.Time { return now }
	cookie, err := authenticator.newSessionCookie(false)
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now.Add(sessionLifetime + time.Second) }
	request := consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(cookie)
	if authenticator.authenticated(request) {
		t.Fatal("expired session cookie remained authenticated")
	}
}

func TestSessionLogoutExpiresCookie(t *testing.T) {
	handler := NewHandlers(Config{ConsoleToken: testConsoleToken}, NewStore(1)).Browser
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	cookie := login.Result().Cookies()[0]

	logout := httptest.NewRecorder()
	request := consoleRequest(http.MethodDelete, "/api/session", "")
	request.AddCookie(cookie)
	handler.ServeHTTP(logout, request)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body=%s", logout.Code, logout.Body.String())
	}
	expired := logout.Result().Cookies()
	if len(expired) != 1 || expired[0].MaxAge >= 0 || !expired[0].Expires.Before(time.Now()) {
		t.Fatalf("logout cookie = %#v", expired)
	}
}

func TestControlAPIBypassesBrowserSession(t *testing.T) {
	handlers := NewHandlers(Config{ConsoleToken: testConsoleToken, NodeName: "studio-gpu"}, NewStore(1))
	response := httptest.NewRecorder()
	handlers.Control.ServeHTTP(response, consoleRequest(http.MethodGet, "/api/node", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "studio-gpu") {
		t.Fatalf("control API = %d %s", response.Code, response.Body.String())
	}
}

func TestRotateLeavesCredentialsIntactWhenEntropyFails(t *testing.T) {
	// A rotation that swapped the key and then failed would leave a node whose stored token nobody holds and whose every session is already void, with no way back in.
	authenticator := newSessionAuthenticator(testConsoleToken)
	before := authenticator.signingKey
	authenticator.random = iotest.ErrReader(errors.New("entropy source failed"))

	nonce, err := authenticator.sessionNonce()
	if err == nil {
		t.Fatal("rotation must fail when it cannot draw a nonce for the replacement cookie")
	}
	if nonce != nil {
		t.Fatalf("failed nonce draw returned bytes: %v", nonce)
	}
	if authenticator.signingKey != before {
		t.Fatal("failed rotation replaced the signing key anyway")
	}
	if authenticator.tokenDigest != sha256.Sum256([]byte(testConsoleToken)) {
		t.Fatal("failed rotation invalidated the original pairing token")
	}
}

func TestFailedPairingRotationLeavesTheOperatorSignedInWithTheOldToken(t *testing.T) {
	// Rotation persists the new token and then installs it. If the write fails, nothing may have moved: the operator keeps the session they still hold and the token they still know.
	handlers := NewHandlers(Config{
		ConsoleToken: testConsoleToken,
		RotateConsoleToken: func() (string, error) {
			return "", errors.New("identity file is read-only")
		},
	}, NewStore(1))
	login := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(login, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	cookie := login.Result().Cookies()[0]

	rotate := httptest.NewRecorder()
	request := consoleRequest(http.MethodPost, "/api/session/rotate", "{}")
	request.AddCookie(cookie)
	handlers.Browser.ServeHTTP(rotate, request)
	if rotate.Code != http.StatusInternalServerError {
		t.Fatalf("failed rotation status = %d, body=%s", rotate.Code, rotate.Body.String())
	}
	if strings.Contains(rotate.Body.String(), "read-only") {
		t.Fatalf("the failure leaked the agent's own message: %s", rotate.Body.String())
	}

	stillOn := httptest.NewRecorder()
	request = consoleRequest(http.MethodGet, "/api/node", "")
	request.AddCookie(cookie)
	handlers.Browser.ServeHTTP(stillOn, request)
	if stillOn.Code == http.StatusUnauthorized {
		t.Fatal("a failed rotation signed the operator out")
	}

	stillPairs := httptest.NewRecorder()
	handlers.Browser.ServeHTTP(stillPairs, consoleRequest(http.MethodPost, "/api/session", `{"token":"`+testConsoleToken+`"}`))
	if stillPairs.Code != http.StatusOK {
		t.Fatalf("a failed rotation invalidated the token the operator still holds: %d", stillPairs.Code)
	}
}
