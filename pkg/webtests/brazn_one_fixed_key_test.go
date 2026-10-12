// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-present Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package webtests

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1768: ONE's ONE Tasks key is issued once, at sign-in, and a renewal never
// replaces it. A browser sign-in's key still changes at each renewal, with
// BRA-1670's grace.
//
// Mutation claim: the OAuth refresh grant at brazn/main 75691196d puts a new key
// in its answer at every renewal. Restoring that turns every case in the first
// two tests red at the first renewal (the answer carries refresh_token). Keeping
// the field out of the answer while still replacing the session's key turns
// TestBraznOneKeyIsIssuedOnceAtSignIn red on the stored key. Making the browser
// renewal keep its key turns TestBraznBrowserRenewalStillChangesTheKey red.

// fixedKeyClient is an OAuth client as it signs in and renews.
type fixedKeyClient struct {
	name        string
	clientID    string
	redirectURI string
	tokenPath   string
	encoding    string // "form" or "json"
}

// ONE, as every version of face/src-tauri/src/cloud/oauth.rs sends it: a form
// post to /api/v1/oauth/token, client one-windows, redirect one://oauth/callback.
var fixedKeyONE = fixedKeyClient{
	name:        "ONE",
	clientID:    "one-windows",
	redirectURI: "one://oauth/callback",
	tokenPath:   "/api/v1/oauth/token",
	encoding:    "form",
}

func fixedKeyTokenCall(e *echo.Echo, c fixedKeyClient, params map[string]string) *httptest.ResponseRecorder {
	var body []byte
	contentType := "application/json"
	if c.encoding == "form" {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}
		body = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else {
		body, _ = json.Marshal(params) //nolint:errchkjson
	}
	req := httptest.NewRequest(http.MethodPost, c.tokenPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// fixedKeyAnswer decodes a token answer field by field, so a test can tell a
// field that is left out from one that is present.
func fixedKeyAnswer(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &fields), rec.Body.String())
	return fields
}

func fixedKeyString(t *testing.T, fields map[string]json.RawMessage, name string) string {
	t.Helper()
	raw, ok := fields[name]
	require.True(t, ok, "the answer must carry %s", name)
	var value string
	require.NoError(t, json.Unmarshal(raw, &value))
	return value
}

// fixedKeySignIn runs the client's sign-in (authorize, then the
// authorization-code exchange) for user 1 and returns the key it was issued and
// the session the access token names.
func fixedKeySignIn(t *testing.T, e *echo.Echo, c fixedKeyClient) (key, sid string) {
	t.Helper()

	verifier := "bra-1768-one-fixed-key-verifier-" + c.clientID
	h := sha256.Sum256([]byte(verifier))
	jwt, err := auth.NewUserJWTAuthtoken(&testuser1, "bra-1768-authorizing-session")
	require.NoError(t, err)
	authorize, _ := json.Marshal(map[string]string{ //nolint:errchkjson
		"response_type":         "code",
		"client_id":             c.clientID,
		"redirect_uri":          c.redirectURI,
		"code_challenge":        base64.RawURLEncoding.EncodeToString(h[:]),
		"code_challenge_method": "S256",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/authorize", bytes.NewReader(authorize))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var code struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &code))
	require.NotEmpty(t, code.Code)

	fields := fixedKeyAnswer(t, fixedKeyTokenCall(e, c, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code.Code,
		"client_id":     c.clientID,
		"redirect_uri":  c.redirectURI,
		"code_verifier": verifier,
	}))
	key = fixedKeyString(t, fields, "refresh_token")
	require.NotEmpty(t, key, "sign-in is where the key is issued")
	return key, fixedKeySessionOf(t, fixedKeyString(t, fields, "access_token"))
}

// fixedKeyRenew renews with the key the client holds and requires the answer
// to carry an access token and no new key. It returns the access token.
func fixedKeyRenew(t *testing.T, e *echo.Echo, c fixedKeyClient, key string) string {
	t.Helper()

	fields := fixedKeyAnswer(t, fixedKeyTokenCall(e, c, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": key,
		"client_id":     c.clientID,
	}))
	_, carriesKey := fields["refresh_token"]
	assert.False(t, carriesKey, "a renewal answer must leave refresh_token out, got %s", fields["refresh_token"])
	access := fixedKeyString(t, fields, "access_token")
	require.NotEmpty(t, access)
	return access
}

// fixedKeySessionOf reads the `sid` claim out of an access token.
func fixedKeySessionOf(t *testing.T, accessToken string) string {
	t.Helper()

	parts := strings.Split(accessToken, ".")
	require.Len(t, parts, 3, "access token must be a JWT")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims struct {
		SID string `json:"sid"`
	}
	require.NoError(t, json.Unmarshal(payload, &claims))
	require.NotEmpty(t, claims.SID)
	return claims.SID
}

// fixedKeyHash is the stored form of a key, the hex SHA-256 the sessions
// fixture writes out by literal.
func fixedKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func fixedKeyStoredHash(t *testing.T, sid string) string {
	t.Helper()

	// Closed before returning: on SQLite an open read keeps the sessions table
	// locked, and the next renewal would fail with 500 instead of answering.
	s := db.NewSession()
	defer s.Close()
	rows, err := s.QueryString("SELECT token_hash FROM sessions WHERE id = ?", sid)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the session must still exist")
	return rows[0]["token_hash"]
}

// fixedKeyLeaveTwoKeys gives the session a current key and the previous key it
// replaced, as a renewal before BRA-1768 did.
func fixedKeyLeaveTwoKeys(t *testing.T, sid, current, previous string) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()
	res, err := s.Exec("UPDATE sessions SET token_hash = ?, previous_token_hash = ? WHERE id = ?",
		fixedKeyHash(current), fixedKeyHash(previous), sid)
	require.NoError(t, err)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)
	require.NoError(t, s.Commit())
}

// fixedKeyIsLong reports whether the session holding the key is a long one.
func fixedKeyIsLong(t *testing.T, key string) bool {
	t.Helper()

	s := db.NewSession()
	defer s.Close()
	var row models.Session
	has, err := s.Where("token_hash = ?", fixedKeyHash(key)).Get(&row)
	require.NoError(t, err)
	require.True(t, has, "the key must name a session")
	return row.IsLongSession
}

// fixedKeyUsable requires the access token to authenticate an API call.
func fixedKeyUsable(t *testing.T, e *echo.Echo, access string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// Story 1: the task server's answer to ONE's renewal carries no new key, and the
// session keeps the key issued at sign-in.
func TestBraznOneKeyIsIssuedOnceAtSignIn(t *testing.T) {
	t.Run("ONE renews again and again with the key from sign-in", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		// Guards the stored-key assertion below: the hash written here is the
		// one the sessions fixture states by literal for testtoken_session1.
		require.Equal(t, "ab7459bbf90562c0412ff2b43b55e2a8aa705e243f3959d2ad1ed3d3ee4e164e",
			fixedKeyHash("testtoken_session1"))

		key, sid := fixedKeySignIn(t, e, fixedKeyONE)
		require.Equal(t, fixedKeyHash(key), fixedKeyStoredHash(t, sid))

		// Five renewals, inside the 10-per-minute limit on /api/v1's
		// unauthenticated routes together with the sign-in.
		var access string
		for range 5 {
			access = fixedKeyRenew(t, e, fixedKeyONE, key)
			assert.Equal(t, sid, fixedKeySessionOf(t, access), "a renewal acts on the signed-in session")
		}

		assert.Equal(t, fixedKeyHash(key), fixedKeyStoredHash(t, sid),
			"the session must still hold the key issued at sign-in")
		fixedKeyUsable(t, e, access)
	})

	// The change applies to every OAuth client that uses the refresh grant.
	for _, c := range []fixedKeyClient{
		{"upstream app, JSON, /api/v1", "vikunja", "vikunja-flutter://callback", "/api/v1/oauth/token", "json"},
		{"form, /api/v2", "vikunja", "vikunja-flutter://callback", "/api/v2/oauth/token", "form"},
		{"JSON, /api/v2", "vikunja", "vikunja-flutter://callback", "/api/v2/oauth/token", "json"},
		{"ONE's client, /api/v2", fixedKeyONE.clientID, fixedKeyONE.redirectURI, "/api/v2/oauth/token", "form"},
	} {
		t.Run("every OAuth client's renewal keeps the key: "+c.name, func(t *testing.T) {
			e, err := setupTestEnv()
			require.NoError(t, err)

			key, sid := fixedKeySignIn(t, e, c)
			for range 3 {
				assert.Equal(t, sid, fixedKeySessionOf(t, fixedKeyRenew(t, e, c, key)))
			}
			assert.Equal(t, fixedKeyHash(key), fixedKeyStoredHash(t, sid))
		})
	}
}

// Story 2: once the task server is deployed, every existing installation keeps
// its sign-in through renewals. Before the deploy the server replaced the key at
// each renewal, so an installation holds either the session's current key or,
// when a renewal answer never reached it, the previous key (BRA-1670's grace).
func TestBraznOneInstallationsSignedInBeforeTheDeployKeepTheirSignIn(t *testing.T) {
	for _, held := range []string{"current", "previous"} {
		t.Run("an installation holding the session's "+held+" key", func(t *testing.T) {
			e, err := setupTestEnv()
			require.NoError(t, err)

			signInKey, sid := fixedKeySignIn(t, e, fixedKeyONE)

			// The state the server before the deploy left behind: one renewal
			// replaced the sign-in key with a new current key and kept the one it
			// replaced as the previous key.
			currentKey := strings.Repeat("7d", 128)
			fixedKeyLeaveTwoKeys(t, sid, currentKey, signInKey)

			key := currentKey
			if held == "previous" {
				key = signInKey
			}

			var access string
			for range 5 {
				access = fixedKeyRenew(t, e, fixedKeyONE, key)
				assert.Equal(t, sid, fixedKeySessionOf(t, access))
			}
			fixedKeyUsable(t, e, access)
		})
	}
}

// Story 3: a browser sign-in's key still changes at each renewal, with
// BRA-1670's grace. The sign-in is "remember me", so its session is long like
// ONE's: a change that told the two apart by session length would show here.
func TestBraznBrowserRenewalStillChangesTheKey(t *testing.T) {
	for _, path := range []string{"/api/v1/user/token/refresh", "/api/v2/user/token/refresh"} {
		t.Run(path, func(t *testing.T) {
			e, err := setupTestEnv()
			require.NoError(t, err)

			login := humaRequest(t, e, http.MethodPost, "/api/v2/login",
				`{"username":"user1","password":"12345678","long_token":true}`, "", "")
			require.Equal(t, http.StatusOK, login.Code, login.Body.String())
			signedIn := refreshCookie(login)
			require.NotNil(t, signedIn)
			k0 := signedIn.Value
			require.NotEmpty(t, k0)

			require.True(t, fixedKeyIsLong(t, k0), "the browser session must be long, like ONE's")

			renew := func(key string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(&http.Cookie{Name: auth.RefreshTokenCookieName, Value: key})
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				return rec
			}
			mustChange := func(key string) string {
				t.Helper()
				rec := renew(key)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				next := refreshCookie(rec)
				require.NotNil(t, next, "a browser renewal must set a new key")
				require.NotEmpty(t, next.Value)
				require.NotEqual(t, key, next.Value, "a browser renewal must change the key")
				return next.Value
			}

			mustChange(k0) // this answer is lost
			k1 := mustChange(k0)
			k2 := mustChange(k1)

			assert.Equal(t, http.StatusUnauthorized, renew(k0).Code,
				"k0's successor k1 was used, so k0 must be refused")
			mustChange(k2)
		})
	}
}
