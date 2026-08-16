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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/routes"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conformanceSignupToken is the token value from the contract's own conformance
// fixture (cloud/contracts/v1/signup/examples/, vendored under
// pkg/modules/brazn/signup/testdata/contract/). Quoted rather than invented:
// exactly 43 unpadded base64url characters, which is the only shape a token has.
const conformanceSignupToken = "EXAMPLE_signup_token_43_chars_not_a_secret1"

// signupRedemptionStub records what this build sent to Percy Cloud.
type signupRedemptionStub struct {
	calls  int
	bodies []string
}

// last returns the body of the most recent redemption, decoded.
func (s *signupRedemptionStub) last(t *testing.T) map[string]interface{} {
	t.Helper()

	require.NotEmpty(t, s.bodies, "no redemption was attempted")
	var sent map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &sent))
	return sent
}

// stubSignupRedemption points brazn.signupredemptionurl at a server that answers
// exactly what the contract says, and records every call.
//
// It answers a FIXED body rather than one derived from the request, which is
// the point: a stub that echoed what it was sent would let this whole file
// agree with itself.
func stubSignupRedemption(t *testing.T, status int, answer string) *signupRedemptionStub {
	t.Helper()

	stub := &signupRedemptionStub{}
	// assert rather than require inside the handler: this runs on the test
	// server's goroutine, and require calls t.FailNow(), which is only defined
	// on the goroutine running the test. A read failure here still fails the
	// test - at the assertions below, which is where it is meaningful anyway.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		stub.calls++
		stub.bodies = append(stub.bodies, string(body))

		w.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)

	setConfigForTest(t, config.BraznSignupRedemptionURL, server.URL)
	setConfigForTest(t, config.BraznServiceToken, "a-service-credential-for-the-test")
	return stub
}

// publicRequest fires an unauthenticated request through the real route table,
// which is what every route in this file is: registration, confirmation and
// password recovery all happen before anybody has a session.
func publicRequest(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// registrationBody is one registration, with whatever token it carries.
func registrationBody(username, email, token string) string {
	return `{"username":"` + username + `","email":"` + email +
		`","password":"12345678","language":"en","signup_token":"` + token + `"}`
}

// TestManagedRegistrationIsClosedEntirely is BRA-1335's replacement for the
// signup-token gate BRA-1071 built. Percy Cloud now provisions the Brazn Tasks
// account synchronously at checkout, through the brazn provisioning channel's
// create_user_with_password operation (pkg/models/brazn_provisioning.go,
// CreateProvisionedUserWithPassword) - before the customer ever reaches this
// instance - so /register has nothing left to do under managed mode.
// route-classification.json now closes it outright ("service-managed"), the
// same way an instance admin is refused every other account-lifecycle route:
// unconditionally, before the handler runs, whatever token accompanies the
// request.
//
// EVERY SUBTEST ASSERTS THE ABSENCE OF A ROW, not only a status code, matching
// the rigor the retired BRA-1071 tests held this route to.
//
// THE CHEAP CHECK: put "signup-token" back on the two /register entries in
// route-classification.json and this goes red - a request carrying
// conformanceSignupToken would reach shared.RegisterUser, the stub would
// answer `redeemed`, and a user would be created.
func TestManagedRegistrationIsClosedEntirely(t *testing.T) {
	t.Run("v1, with a token the redemption would have accepted", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("closed-with-a-token", "closed-with-a-token@example.com", conformanceSignupToken))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, 0, stub.calls,
			"the gate must refuse before the handler ever asks Percy Cloud about the token")
		db.AssertMissing(t, "users", map[string]interface{}{"username": "closed-with-a-token"})
	})

	t.Run("v2, with no token at all", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v2/register",
			registrationBody("closed-with-no-token", "closed-with-no-token@example.com", ""))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, 0, stub.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"username": "closed-with-no-token"})
	})

	// Naming an address that already has an account changes nothing: this is
	// the gate, not the duplicate-address check BRA-1111 hardened, and the
	// gate refuses before either ever runs.
	t.Run("even with an address that already has an account", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("user1", "user1@example.com", conformanceSignupToken))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, 0, stub.calls)
	})

	t.Run("answers exactly like every other service-managed route", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("same-refusal", "same-refusal@example.com", conformanceSignupToken))

		require.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(),
			"This operation is managed by Brazn and is not available for this account.")
	})
}

// TestSelfHostedRegistrationNeedsNoSignupToken is the criterion most easily
// broken by accident: a self-hosted instance of this fork must behave exactly
// as upstream Vikunja does. No token, nothing new enforced, and - the
// assertion that would catch a guard leaking out of managed mode - no call to
// Percy Cloud at all. BRA-1335 does not touch this path: it only closes the
// route Percy Cloud's own accounts no longer need.
func TestSelfHostedRegistrationNeedsNoSignupToken(t *testing.T) {
	e := managedModeEcho(t, false)
	stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

	rec := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("a-self-hosted-user", "a-self-hosted-user@example.com", ""))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 0, stub.calls, "a self-hosted instance must never call the commercial service")
	db.AssertExists(t, "users", map[string]interface{}{"username": "a-self-hosted-user"}, false)
}

// TestManagedModeLeavesAccountRecoveryOpen is AC9 and AC10 of the ticket that
// first shipped these five routes as one block (pkg/routes/routes.go), and
// there is no way to register only some of them. /register itself is now
// closed under managed mode (TestManagedRegistrationIsClosedEntirely); these
// four still work normally, because under BRA-1069 the fork owns
// authentication and they are the product's real login and recovery paths,
// unaffected by where an account came from.
//
// AC9 exists because the fork sends a confirmation mail pointing at
// /user/confirm, and that route was registered only inside
// `if config.AuthLocalEnabled.GetBool()` while the flag was false in both
// compose files - so the link in a real customer's mail resolved to nothing.
// Asserting the route answers is what closes it.
func TestManagedModeLeavesAccountRecoveryOpen(t *testing.T) {
	t.Run("a confirmation link resolves", func(t *testing.T) {
		e := managedModeEcho(t, true)

		// The link is minted here rather than read out of the fixtures, and it
		// has to be. A confirmation link is only good for 24 hours
		// (user.EmailConfirmMaxAge), so any date written into user_tokens.yml
		// is either already past the deadline or will be tomorrow - the fixture
		// token is dated 2021 and is now permanently expired. A test that
		// depended on it would stop describing this route and start describing
		// the calendar. This one carries its own precondition: the row is as
		// old as the moment the test ran, so no clock can stale it.
		//
		// User 4 is status 1 in the fixtures, waiting on confirmation.
		link := seedConfirmLink(t, 4)

		rec := publicRequest(e, http.MethodPost, "/api/v1/user/confirm",
			`{"token":"`+link+`"}`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"already_confirmed":false`)
	})

	t.Run("a password reset can be requested", func(t *testing.T) {
		e := managedModeEcho(t, true)

		rec := publicRequest(e, http.MethodPost, "/api/v1/user/password/token",
			`{"email":"user1@example.com"}`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("a password reset can be completed", func(t *testing.T) {
		e := managedModeEcho(t, true)

		rec := publicRequest(e, http.MethodPost, "/api/v1/user/password/reset",
			`{"new_password":"12345678","token":"passwordresettesttoken"}`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "The password was updated successfully.")
	})

	t.Run("signing in still works", func(t *testing.T) {
		e := managedModeEcho(t, true)

		rec := publicRequest(e, http.MethodPost, "/api/v1/login",
			`{"username":"user1","password":"12345678"}`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
}

// TestV2LoginFollowsTheLocalAuthSwitch is folded in from
// Identity-and-Access-Rules.md §8 gap T6.
//
// v1 registers POST /login only when a credential backend exists to log in
// against; v2 did not, so an instance with local authentication off still
// answered on /api/v2/login with the whole credential path behind it. It was
// harmless only while no local account could be created - a property of the
// accounts and not of the switch - and BRA-1071 was the moment that stopped
// being true, because registration reopened.
//
// The pair is the assertion: the SAME request answers on one configuration and
// 404s on the other, so the switch is what decides and not the fixture.
func TestV2LoginFollowsTheLocalAuthSwitch(t *testing.T) {
	t.Run("registered when a credential backend exists", func(t *testing.T) {
		e := managedModeEcho(t, true)

		rec := publicRequest(e, http.MethodPost, "/api/v2/login",
			`{"username":"user1","password":"12345678"}`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("not registered at all when neither local nor LDAP is enabled", func(t *testing.T) {
		_, err := setupTestEnv()
		require.NoError(t, err)
		setConfigForTest(t, config.AuthLocalEnabled, false)
		setConfigForTest(t, config.AuthLdapEnabled, false)

		e := routes.NewEcho()
		routes.RegisterRoutes(e)

		rec := publicRequest(e, http.MethodPost, "/api/v2/login",
			`{"username":"user1","password":"12345678"}`)

		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})
}
