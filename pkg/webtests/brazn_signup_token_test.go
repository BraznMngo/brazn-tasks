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
	"strconv"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/mail"
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

// TestManagedRegistrationIsGatedByASignupToken is BRA-1071 acceptance criteria
// 1, 2, 6 and 7, and Identity-and-Access-Rules.md §11 cases 1, 2 and 7.
//
// EVERY SUBTEST ASSERTS THE ABSENCE OR PRESENCE OF A ROW, not only a status
// code. A refusal that leaves a user behind is the failure this exists to
// catch, and it would answer 403 exactly like a correct one.
//
// WHAT BREAKS IF THE GUARD IS DELETED, reasoned through because it cannot be
// run here. Removing the signup.Redeem call from shared.RegisterUser makes "a
// token the service refuses" fail on both of its assertions: the stub is never
// called, and the user is created and committed. It also makes "a valid token"
// fail, because nothing reports the users.id and stub.calls is 0. Removing the
// CanBeRedeemed pre-check alone changes nothing here that matters - the
// redemption applies the same predicate and refuses an absent token on the
// wire - which is exactly why the pre-check is documented as an optimisation
// and not as the gate.
func TestManagedRegistrationIsGatedByASignupToken(t *testing.T) {
	t.Run("no token creates no user", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("nobody-without-a-token", "nobody-without-a-token@example.com", ""))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, 0, stub.calls, "a token that is not even shaped like one must cost no round trip")
		db.AssertMissing(t, "users", map[string]interface{}{"username": "nobody-without-a-token"})
	})

	t.Run("a token the service refuses creates no user", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("nobody-with-a-dead-token", "nobody-with-a-dead-token@example.com", conformanceSignupToken))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		// The call WAS made, so this is the redemption refusing rather than the
		// shape check refusing before it - and the transaction was rolled back
		// even though the user had already been written inside it.
		assert.Equal(t, 1, stub.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"username": "nobody-with-a-dead-token"})
	})

	t.Run("a valid token creates the user and reports its id", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("somebody-who-paid", "somebody-who-paid@example.com", conformanceSignupToken))

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var created struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
		require.NotZero(t, created.ID)
		db.AssertExists(t, "users", map[string]interface{}{
			"id":       created.ID,
			"username": "somebody-who-paid",
		}, false)

		// AC7, asserted as a JOIN and not at either end. "Some id was reported"
		// and "some user was created" are both true of a build that reports the
		// wrong id, and under Identity-and-Access-Rules.md §3.3 a projection for
		// a subject that does not exist is answered 204 and stored nowhere - so
		// the wrong id is a signup that looks entirely successful and a customer
		// with no entitlement.
		require.Equal(t, 1, stub.calls)
		sent := stub.last(t)
		assert.Equal(t, strconv.FormatInt(created.ID, 10), sent["user_id"],
			"the reported user_id must be the id of the row that now exists, as a decimal string")
		assert.Equal(t, conformanceSignupToken, sent["token"])
		assert.Equal(t, "somebody-who-paid@example.com", sent["email"],
			"the address is required on every redemption, because it is the input to the invitation address binding")
	})

	// The contract's request schema refuses a JSON number for user_id, and a Go
	// int64 field marshals to one. This is the assertion that would have caught
	// that, and it is separate from the join above because it is about the TYPE
	// on the wire rather than the value.
	t.Run("the reported user id is a JSON string and never a number", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("somebody-typed", "somebody-typed@example.com", conformanceSignupToken))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		require.Equal(t, 1, stub.calls)
		sent := stub.last(t)
		require.Contains(t, sent, "user_id")
		assert.IsType(t, "", sent["user_id"], "user_id must be a JSON string")
		// Nothing else may ride along: the schema sets additionalProperties to
		// false, so a fourth member is a malformed request rather than an
		// ignored one - and a password reaching this call would be a leak.
		assert.Len(t, sent, 3)
		assert.NotContains(t, sent, "password")
	})

	// v2 shares shared.RegisterUser with v1, which is the point of asserting it:
	// one guard covers both, and a future handler that grew its own copy would
	// show up here.
	t.Run("v2 is gated by the same guard", func(t *testing.T) {
		e := managedModeEcho(t, true)
		stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

		rec := publicRequest(e, http.MethodPost, "/api/v2/register",
			registrationBody("nobody-on-v2", "nobody-on-v2@example.com", conformanceSignupToken))

		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, 1, stub.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"username": "nobody-on-v2"})
	})
}

// TestManagedRegistrationRefusalsAreIndistinguishable is AC6.
//
// The vocabulary on the wire cannot express why a token is unusable, and this
// asserts the fork does not reintroduce the distinction on its own side: an
// unknown token and a consumed one produce the same status and the same body.
// A helpful message added later - "this link has expired" - fails here.
//
// READ WHAT IT COVERS BEFORE TRUSTING THE NAME. Both requests below are handed
// the SAME token_unusable stub, so this compares two refusals of ONE class. It
// cannot see a refusal that never reached the redemption at all, and it stayed
// green through the whole of BRA-1111 - where an address that already existed
// was answered 400 by user creation long before the token was consulted. The
// property this name promises is asserted by
// TestManagedRegistrationTellsNobodyWhichAddressesExist below; this one only
// pins the wording of a redemption refusal.
func TestManagedRegistrationRefusalsAreIndistinguishable(t *testing.T) {
	e := managedModeEcho(t, true)
	stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

	unknown := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("refusal-one", "refusal-one@example.com", conformanceSignupToken))
	consumed := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("refusal-two", "refusal-two@example.com", conformanceSignupToken))

	require.Equal(t, 2, stub.calls)
	assert.Equal(t, unknown.Code, consumed.Code)
	assert.Equal(t, unknown.Body.String(), consumed.Body.String())
	assert.NotContains(t, strings.ToLower(unknown.Body.String()), "expired")
	assert.NotContains(t, strings.ToLower(unknown.Body.String()), "consumed")
}

// TestManagedRegistrationTellsNobodyWhichAddressesExist is BRA-1111 half one.
//
// Registration creates the user BEFORE it redeems the token, so the duplicate
// check answered first: an address already held returned 400 with code 1002 and
// a free one returned 403 from the redemption. Anyone able to type 43 plausible
// characters could therefore ask this endpoint which addresses and usernames
// hold accounts, without authenticating and without a real token.
//
// WHAT MAKES THIS TEST MEAN ANYTHING is the stub call count. Three requests go
// in and only ONE of them reaches the redemption, which proves the three took
// genuinely different paths through the server - and the assertions then say
// that none of that difference reached the caller. Without that count this
// would be satisfied by three refusals of one class, which is exactly how the
// test above passed through the defect.
//
// DELETING THE GUARD MUST BREAK IT: remove the IsErrUsernameExists /
// IsErrUserEmailExists collapse from shared.RegisterUser and the two taken
// identities answer 400 while the free one answers 403, so both equality
// assertions fail on status and on body.
func TestManagedRegistrationTellsNobodyWhichAddressesExist(t *testing.T) {
	e := managedModeEcho(t, true)
	stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

	// The fixture asserted rather than assumed. If user1 were not really here -
	// a renamed fixture, a table cleared by a neighbour - all three requests
	// would take the free-address path and agree with each other while proving
	// nothing at all.
	db.AssertExists(t, "users", map[string]interface{}{
		"username": "user1",
		"email":    "user1@example.com",
	}, false)
	db.AssertMissing(t, "users", map[string]interface{}{"username": "nobody-at-all"})

	takenEmail := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("a-username-nobody-has", "user1@example.com", conformanceSignupToken))
	takenUsername := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("user1", "an-address-nobody-has@example.com", conformanceSignupToken))
	free := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("nobody-at-all", "nobody-at-all@example.com", conformanceSignupToken))

	require.Equal(t, 1, stub.calls,
		"only the request whose identity is free may get as far as the redemption; "+
			"if all three did, the fixtures are not what this test needs them to be")

	for _, taken := range []struct {
		what string
		rec  *httptest.ResponseRecorder
	}{
		{"an address that already exists", takenEmail},
		{"a username that already exists", takenUsername},
	} {
		assert.Equal(t, free.Code, taken.rec.Code,
			taken.what+" must answer exactly as a free one does")
		assert.Equal(t, free.Body.String(), taken.rec.Body.String(),
			taken.what+" must answer exactly as a free one does")
	}

	// Pinned against a fixed expectation written out here, not against whatever
	// the handler produced. Three responses agreeing with each other are
	// satisfied just as well by three copies of the wrong answer, so the
	// equalities above cannot be the only thing asserted.
	assert.Equal(t, http.StatusForbidden, free.Code)
	assert.Contains(t, free.Body.String(), "This signup link cannot be used.")
	assert.NotContains(t, strings.ToLower(free.Body.String()), "already exists")

	// None of the three may leave a row behind either.
	db.AssertMissing(t, "users", map[string]interface{}{"username": "a-username-nobody-has"})
	db.AssertMissing(t, "users", map[string]interface{}{"username": "nobody-at-all"})
}

// TestManagedRegistrationMailsNobodyItRefuses is BRA-1111 half two.
//
// The confirmation mail was dispatched inline from user.CreateUser, outside the
// transaction, BEFORE the token was redeemed. The redemption then refused and
// the user row was rolled back - the row came back, the mail did not. That is
// unauthenticated mail to any address a caller names, bounded only by the
// no-auth rate limit, on the one verified sending domain the product has.
//
// IT IS OBSERVED AT mail.SendMail, which is the last thing before the SMTP
// queue and so the place the outcome is actually decided - not at the
// notification, which is one hop earlier and would still be recorded by a build
// that only meant to send.
//
// THE FIRST SUBTEST IS THE CONTROL AND IS NOT OPTIONAL. mailer.enabled defaults
// to FALSE, and with it off user.CreateUser returns before it ever builds a
// confirmation - so "no mail was sent" would hold for a completely broken
// build, a correct one, and the defect alike. Proving a completed registration
// DOES mail is what makes the refusal's silence evidence of anything.
func TestManagedRegistrationMailsNobodyItRefuses(t *testing.T) {
	e := managedModeEcho(t, true)
	setConfigForTest(t, config.MailerEnabled, true)

	t.Run("a completed registration sends the confirmation", func(t *testing.T) {
		stub := stubSignupRedemption(t, http.StatusOK, `{"result":"redeemed"}`)
		mail.ResetSent()

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("gets-confirmed", "gets-confirmed@example.com", conformanceSignupToken))

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, 1, stub.calls)

		sent := mail.LastSent()
		require.NotNil(t, sent,
			"a registration that completed must have mailed its confirmation; if it did not, "+
				"the seam this test observes is dead and the refusal subtest below proves nothing")
		assert.Equal(t, "gets-confirmed@example.com", sent.To)
	})

	t.Run("a refused registration sends nothing", func(t *testing.T) {
		stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)
		mail.ResetSent()

		rec := publicRequest(e, http.MethodPost, "/api/v1/register",
			registrationBody("never-confirmed", "never-confirmed@example.com", conformanceSignupToken))

		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		// The refusal has to be the REDEMPTION's. A registration turned away by
		// the shape check never reaches user creation, so it could not have
		// mailed anything however the code were written, and asserting silence
		// on that path would be worthless.
		require.Equal(t, 1, stub.calls,
			"this must be the redemption refusing after the user existed, not the shape check refusing before")
		db.AssertMissing(t, "users", map[string]interface{}{"username": "never-confirmed"})

		assert.Nil(t, mail.LastSent(),
			"a registration that was refused must not have mailed the address it was handed; "+
				"the row is rolled back and the mail cannot be")
	})
}

// TestSelfHostedRegistrationNeedsNoSignupToken is AC4, and it is the criterion
// most easily broken by accident: a self-hosted instance of this fork must
// behave exactly as upstream Vikunja does. No token, nothing new enforced, and
// - the assertion that would catch a guard leaking out of managed mode - no
// call to Percy Cloud at all.
func TestSelfHostedRegistrationNeedsNoSignupToken(t *testing.T) {
	e := managedModeEcho(t, false)
	stub := stubSignupRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)

	rec := publicRequest(e, http.MethodPost, "/api/v1/register",
		registrationBody("a-self-hosted-user", "a-self-hosted-user@example.com", ""))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 0, stub.calls, "a self-hosted instance must never call the commercial service")
	db.AssertExists(t, "users", map[string]interface{}{"username": "a-self-hosted-user"}, false)
}

// TestManagedModeLeavesAccountRecoveryOpen is AC9 and AC10.
//
// Enabling local authentication registers FIVE routes as one block
// (pkg/routes/routes.go), and there is no way to register only some of them.
// This ticket says what each does under managed mode: /register is token-gated
// and asserted above; these four work normally, because under BRA-1069 the fork
// owns authentication and they are the product's real login and recovery paths.
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

// TestV2LoginFollowsTheLocalAuthSwitch is AC8, folded in from
// Identity-and-Access-Rules.md §8 gap T6.
//
// v1 registers POST /login only when a credential backend exists to log in
// against; v2 did not, so an instance with local authentication off still
// answered on /api/v2/login with the whole credential path behind it. It was
// harmless only while no local account could be created - a property of the
// accounts and not of the switch - and this ticket is the moment that stops
// being true, because registration reopens.
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
