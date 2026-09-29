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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth/oauth2server"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1670: a session made by ONE's OAuth sign-in survives a lost refresh
// reply and a few idle days, and still ends when it is deleted, revoked or
// idle past its limit.

// oauthSignIn runs ONE's sign-in (authorize + authorization-code exchange) for
// user 1 and returns the token reply.
func oauthSignIn(t *testing.T, e *echo.Echo) oauth2server.TokenResponse {
	t.Helper()

	verifier := "bra-1670-session-survives-verifier"
	h := sha256.Sum256([]byte(verifier))
	code := getAuthorizationCode(t, e, base64.RawURLEncoding.EncodeToString(h[:]), "")

	rec := doTokenRequest(e, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"client_id":     "vikunja",
		"redirect_uri":  "vikunja-flutter://callback",
		"code_verifier": verifier,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp oauth2server.TokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.RefreshToken)
	return resp
}

func oauthRefresh(e *echo.Echo, refreshToken string) *httptest.ResponseRecorder {
	return doTokenRequest(e, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     "vikunja",
	})
}

// mustOAuthRefresh refreshes and requires success, returning the new reply.
func mustOAuthRefresh(t *testing.T, e *echo.Echo, refreshToken string) oauth2server.TokenResponse {
	t.Helper()

	rec := oauthRefresh(e, refreshToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp oauth2server.TokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.RefreshToken)
	return resp
}

// sessionOf reads the `sid` claim out of an access token, so a test can say
// which session a refresh actually acted on.
func sessionOf(t *testing.T, accessToken string) string {
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

// idleSession sets a session's last activity to `idle` ago.
func idleSession(t *testing.T, sessionID string, idle time.Duration) {
	t.Helper()

	s := dbSessionForTest(t)
	updated, err := s.Where("id = ?", sessionID).
		Cols("last_active").
		Update(&models.Session{LastActive: time.Now().Add(-idle)})
	require.NoError(t, err)
	require.Equal(t, int64(1), updated, "the session to backdate must exist")
	require.NoError(t, s.Commit())
}

func deleteSession(t *testing.T, sessionID string) {
	t.Helper()

	s := dbSessionForTest(t)
	require.NoError(t, models.DeleteSessionForUser(s, sessionID, testuser1.ID))
	require.NoError(t, s.Commit())
}

func shortIdleLimit() time.Duration {
	return time.Duration(config.ServiceJWTTTL.GetInt64()) * time.Second
}

func longIdleLimit() time.Duration {
	return time.Duration(config.ServiceJWTTTLLong.GetInt64()) * time.Second
}

func TestBraznLostRefreshReplyDoesNotEndTheSession(t *testing.T) {
	t.Run("the token whose reply was lost keeps working until its successor is used", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		signIn := oauthSignIn(t, e)
		sid := sessionOf(t, signIn.AccessToken)
		held := signIn.RefreshToken

		// The server rotates, the reply never arrives: ONE still holds `held`.
		lost := mustOAuthRefresh(t, e, held)
		assert.Equal(t, sid, sessionOf(t, lost.AccessToken))

		// ONE retries with what it holds, and again after another lost reply.
		retry := mustOAuthRefresh(t, e, held)
		assert.Equal(t, sid, sessionOf(t, retry.AccessToken))
		retryAgain := mustOAuthRefresh(t, e, held)
		assert.Equal(t, sid, sessionOf(t, retryAgain.AccessToken))

		// This reply arrives, and ONE uses the token it carried.
		next := mustOAuthRefresh(t, e, retryAgain.RefreshToken)
		assert.Equal(t, sid, sessionOf(t, next.AccessToken))

		// Its successor has now been used once, so the old token is refused.
		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, held).Code)

		// A refused replay does not sign the real client out.
		mustOAuthRefresh(t, e, next.RefreshToken)
	})

	t.Run("a token two rotations back is refused and the session carries on", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		r0 := oauthSignIn(t, e).RefreshToken
		r1 := mustOAuthRefresh(t, e, r0).RefreshToken
		r2 := mustOAuthRefresh(t, e, r1).RefreshToken

		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, r0).Code,
			"r0's successor r1 was used, so r0 must be refused")

		r3 := mustOAuthRefresh(t, e, r2).RefreshToken
		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, r1).Code,
			"r1's successor r2 was used, so r1 must be refused")

		mustOAuthRefresh(t, e, r3)
	})
}

func TestBraznOAuthSignInIsALongSession(t *testing.T) {
	t.Run("survives an idle period past the short-session limit", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)
		require.Less(t, shortIdleLimit()+time.Hour, longIdleLimit()-time.Hour,
			"the test needs a gap between the two limits")

		signIn := oauthSignIn(t, e)
		idleSession(t, sessionOf(t, signIn.AccessToken), shortIdleLimit()+time.Hour)

		mustOAuthRefresh(t, e, signIn.RefreshToken)
	})

	t.Run("is ended past the long-session limit", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		signIn := oauthSignIn(t, e)
		idleSession(t, sessionOf(t, signIn.AccessToken), longIdleLimit()+time.Hour)

		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, signIn.RefreshToken).Code)
	})

	// Guards the two above: a short session idle for the same period is
	// refused, so backdating last_active really reaches the idle check.
	t.Run("control: a short session idle past the short limit is refused", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		idleSession(t, sessionUser1A, shortIdleLimit()+time.Hour)

		assert.Equal(t, http.StatusUnauthorized, refreshRequest(e, "testtoken_session1").Code)
	})
}

func TestBraznDeadSessionStaysDead(t *testing.T) {
	// In each case `held` is the previous token, still in its grace (its
	// successor `current` has never been used), so both tokens are live
	// until the session ends.
	oneLostReply := func(t *testing.T, e *echo.Echo) (sid, held, current string) {
		t.Helper()
		signIn := oauthSignIn(t, e)
		return sessionOf(t, signIn.AccessToken), signIn.RefreshToken, mustOAuthRefresh(t, e, signIn.RefreshToken).RefreshToken
	}

	for _, presented := range []string{"previous", "current"} {
		pick := func(held, current string) string {
			if presented == "previous" {
				return held
			}
			return current
		}

		t.Run("deleted session refuses the "+presented+" token", func(t *testing.T) {
			e, err := setupTestEnv()
			require.NoError(t, err)

			sid, held, current := oneLostReply(t, e)
			deleteSession(t, sid)

			assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, pick(held, current)).Code)
		})

		t.Run("idle-expired session refuses the "+presented+" token", func(t *testing.T) {
			e, err := setupTestEnv()
			require.NoError(t, err)

			sid, held, current := oneLostReply(t, e)
			idleSession(t, sid, longIdleLimit()+time.Hour)

			assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, pick(held, current)).Code)
		})
	}

	t.Run("a token from another session never acts on this one", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		sidA, heldA, currentA := oneLostReply(t, e)
		sidB, heldB, _ := oneLostReply(t, e)
		require.NotEqual(t, sidA, sidB)

		deleteSession(t, sidA)

		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, heldA).Code)
		assert.Equal(t, http.StatusUnauthorized, oauthRefresh(e, currentA).Code)

		// B's tokens still work, and only ever on B.
		retried := mustOAuthRefresh(t, e, heldB)
		assert.Equal(t, sidB, sessionOf(t, retried.AccessToken))
		assert.Equal(t, sidB, sessionOf(t, mustOAuthRefresh(t, e, retried.RefreshToken).AccessToken))
	})
}
