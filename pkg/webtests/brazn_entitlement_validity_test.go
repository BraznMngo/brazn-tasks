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
	"fmt"
	"net/http"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The entitlement's validity window, observed where it actually acts: on the
// session token, over real HTTP, through the routes a customer uses.
//
// The whole design is that the token is the enforcement. The entitlement is
// read once when a token is issued, the token is capped at the entitlement's
// end, and no guarded request reads the entitlement again. So there are exactly
// two things to challenge, and both are here: does the cap really shorten a
// token, and does the gate really decide from the token rather than from the
// database.

// tokenExpiry returns the instant a session token expires, read out of the
// token itself rather than out of anything that produced it.
func tokenExpiry(t *testing.T, token string) time.Time {
	t.Helper()

	parsed, err := jwt.Parse(token, func(_ *jwt.Token) (any, error) {
		return []byte(config.ServiceSecret.GetString()), nil
	})
	require.NoError(t, err, "the issued token must parse and must not already be expired")

	claims, isClaims := parsed.Claims.(jwt.MapClaims)
	require.True(t, isClaims)
	exp, isNumber := claims["exp"].(float64)
	require.True(t, isNumber, "a session token must carry an exp claim")
	return time.Unix(int64(exp), 0)
}

func tokenEdition(t *testing.T, token string) (string, bool) {
	t.Helper()

	parsed, err := jwt.Parse(token, func(_ *jwt.Token) (any, error) {
		return []byte(config.ServiceSecret.GetString()), nil
	})
	require.NoError(t, err)

	claims, isClaims := parsed.Claims.(jwt.MapClaims)
	require.True(t, isClaims)
	edition, isString := claims[auth.BraznEditionClaim].(string)
	return edition, isString && edition != ""
}

// normalTokenLifetime is what a session token gets when nothing caps it. Read
// from configuration, which is this test's input, so the assertions below
// compare the issued token against the setting rather than against anything the
// capping code derived.
func normalTokenLifetime() time.Duration {
	return time.Duration(config.ServiceJWTTTLShort.GetInt64()) * time.Second
}

// TestEntitlementEndCapsTheSessionToken is the assertion the whole design rests
// on: a token issued for an entitlement that ends sooner than the token
// normally would expires when the entitlement does, and not when the clock says
// it may.
//
// DELETE THE CAP - the `entitled.EndsAt.Before(expires)` branch in
// newUserJWTAuthtoken - and the token gets the full lifetime, so `capped` moves
// roughly eight minutes later and the first assertion fails.
//
// The `require` on the fixture is not decoration. If a future edit moved the
// end date past the normal lifetime, every assertion here would still pass with
// the capping code deleted, because there would be nothing to cap. That is the
// exact shape of a test that cannot fail, so the precondition is asserted
// rather than assumed.
func TestEntitlementEndCapsTheSessionToken(t *testing.T) {
	env := newManagedEnv(t)
	setConfigForTest(t, config.BraznEntitlementGrace, "0s")

	endsAt := time.Now().UTC().Add(2 * time.Minute)
	require.Less(t, 2*time.Minute, normalTokenLifetime(),
		"the fixture must end the entitlement SOONER than the token would expire, or there is nothing to cap")

	env.grantUntil(testuser1.ID, entitlement.EditionPersonal, false, &endsAt)
	capped := tokenExpiry(t, env.tokenFor(&testuser1))

	assert.WithinDuration(t, endsAt, capped, 2*time.Second,
		"the token must expire when the entitlement does")
	assert.True(t, capped.Before(time.Now().UTC().Add(normalTokenLifetime())),
		"a capped token must be shorter-lived than an uncapped one")
}

// TestEntitlementBeyondTheTokenCapsNothing is the other half of the pair, and
// it is deliberately one a deleted cap would also pass. It is here to stop the
// cap from becoming "shorten every token": an entitlement running well past the
// token's own lifetime must change nothing, because a token that outlived its
// TTL would be a security regression bought for nobody.
func TestEntitlementBeyondTheTokenCapsNothing(t *testing.T) {
	env := newManagedEnv(t)
	setConfigForTest(t, config.BraznEntitlementGrace, "0s")

	endsAt := time.Now().UTC().Add(30 * 24 * time.Hour)
	env.grantUntil(testuser1.ID, entitlement.EditionPersonal, false, &endsAt)

	expiry := tokenExpiry(t, env.tokenFor(&testuser1))
	assert.WithinDuration(t, time.Now().UTC().Add(normalTokenLifetime()), expiry, 5*time.Second)
}

// TestEntitlementGraceIsConfiguration pins that the grace period is read from
// configuration and added to the end date, rather than being a constant or
// being ignored.
//
// Two runs of one fixture differing only in the setting, so the assertion is
// about the setting and not about arithmetic that happens to come out right
// once. Delete the `.Add(grace)` and both runs produce the same expiry, which
// the last assertion refuses.
func TestEntitlementGraceIsConfiguration(t *testing.T) {
	endsAt := time.Now().UTC().Add(2 * time.Minute)
	require.Less(t, 2*time.Minute+5*time.Minute, normalTokenLifetime(),
		"both runs must stay inside the normal lifetime, or the cap is what differs rather than the grace")

	withGrace := func(grace string) time.Time {
		env := newManagedEnv(t)
		setConfigForTest(t, config.BraznEntitlementGrace, grace)
		env.grantUntil(testuser1.ID, entitlement.EditionPersonal, false, &endsAt)
		return tokenExpiry(t, env.tokenFor(&testuser1))
	}

	none := withGrace("0s")
	generous := withGrace("5m")

	assert.WithinDuration(t, endsAt, none, 2*time.Second)
	assert.WithinDuration(t, endsAt.Add(5*time.Minute), generous, 2*time.Second)
	assert.True(t, none.Before(generous), "a longer grace must produce a longer-lived token")
}

// TestEndedEntitlementLeavesOrdinaryWorkAlone is the case that is easiest to
// get backwards and most expensive to get wrong.
//
// An entitlement whose end has already passed must produce a token with NO
// entitlement and a full lifetime - never a token capped to a moment in the
// past. The same token authenticates ordinary task work, which the contract's
// failure policy says continues, so capping to a past instant would take a
// customer's own tasks away from them along with the subscription they stopped
// paying for.
//
// Delete the refusal in Signed.ForToken and this fails twice over: the token
// would carry an expiry an hour in the past, so tokenExpiry's parse fails on an
// expired token, and the ordinary edit answers 401 instead of 200.
func TestEndedEntitlementLeavesOrdinaryWorkAlone(t *testing.T) {
	env := newManagedEnv(t)
	setConfigForTest(t, config.BraznEntitlementGrace, "0s")

	ended := time.Now().UTC().Add(-time.Hour)
	env.grantUntil(testuser1.ID, entitlement.EditionPersonal, false, &ended)
	env.protect(models.ProtectedKindInbox, fixtureInboxProjectID, 0)

	token := env.tokenFor(&testuser1)

	// A full lifetime, in the future, and no entitlement in it.
	expiry := tokenExpiry(t, token)
	assert.True(t, expiry.After(time.Now().UTC()), "an ended entitlement must not expire the token itself")
	assert.WithinDuration(t, time.Now().UTC().Add(normalTokenLifetime()), expiry, 5*time.Second)
	_, entitled := tokenEdition(t, token)
	assert.False(t, entitled, "an entitlement that has ended must not be stamped into a token")

	// Ordinary task work continues: an edit that names no destination is not a
	// move, so it never reaches a policy rule.
	rec := env.requestWith(http.MethodPost, "/api/v1/tasks/1",
		`{"id":1,"title":"still my task"}`, token)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// And the guarded operation is refused.
	feedback := env.newProject(&testuser1, "Feedback", 0)
	env.protect(models.ProtectedKindFeedback, feedback, 0)
	rec = env.requestWith(http.MethodPost, "/api/v1/tasks/1",
		fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), token)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// TestGuardedRequestDecidesFromTheTokenNotTheDatabase is the assertion that the
// per-request read is actually gone, and it is the one that cannot pass while
// it is still there.
//
// A token is minted while a valid personal projection exists, then the
// projection row is DELETED, then a guarded operation the personal rule permits
// is attempted with that same token. It succeeds, because the token says
// personal-cloud and nothing consults the row any more.
//
// Restore models.GetEntitlement in decideByEdition and this answers 403: the
// row is gone, so ErrNoEntitlement comes back and the gate refuses. There is no
// way to write this test such that both implementations pass it.
func TestGuardedRequestDecidesFromTheTokenNotTheDatabase(t *testing.T) {
	env := newManagedEnv(t)
	env.grant(testuser1.ID, entitlement.EditionPersonal, false)
	feedback := env.newProject(&testuser1, "Feedback", 0)
	env.protect(models.ProtectedKindFeedback, feedback, 0)

	// Issued while the entitlement is on record.
	token := env.tokenFor(&testuser1)
	edition, entitled := tokenEdition(t, token)
	require.True(t, entitled, "the fixture must produce an entitled token, or it tests nothing")
	require.Equal(t, entitlement.EditionPersonal, edition)

	// The database no longer knows anything about this subject.
	env.revoke(testuser1.ID)
	require.NoError(t, entitlementRowIsGone(t, testuser1.ID))

	rec := env.requestWith(http.MethodPost, "/api/v1/tasks/1",
		fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), token)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestTokenWithoutEntitlementIsRefusedDespiteTheDatabase is the mirror image,
// and together with the test above it pins the direction: the token decides,
// both when it is more generous than the database and when it is less.
//
// A perfectly good projection is on record, and the request carries a token
// minted without one. It is refused. Reintroduce a database fallback for "the
// token carries no edition" - the tempting way to be lenient during an upgrade
// - and this answers 200 instead.
func TestTokenWithoutEntitlementIsRefusedDespiteTheDatabase(t *testing.T) {
	env := newManagedEnv(t)
	env.grant(testuser1.ID, entitlement.EditionPersonal, false)
	feedback := env.newProject(&testuser1, "Feedback", 0)
	env.protect(models.ProtectedKindFeedback, feedback, 0)

	// The same request with an entitled token is allowed, which is what makes
	// the refusal below about the token rather than about the operation.
	allowed := env.requestWith(http.MethodPost, "/api/v1/tasks/1",
		fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), env.tokenFor(&testuser1))
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())

	plain, err := auth.NewUserJWTAuthtoken(&testuser1, "test-session-id")
	require.NoError(t, err)
	_, entitled := tokenEdition(t, plain)
	require.False(t, entitled, "the fixture must really carry no entitlement")

	rec := env.requestWith(http.MethodPost, "/api/v1/tasks/2",
		fmt.Sprintf(`{"id":2,"title":"task #2 done","project_id":%d}`, feedback), plain)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// entitlementRowIsGone confirms the fixture above really removed the row, so
// "the database no longer knows" is observed rather than assumed.
func entitlementRowIsGone(t *testing.T, userID int64) error {
	t.Helper()

	s := dbSessionForTest(t)
	_, err := models.GetEntitlement(s, userID)
	if err == nil {
		return fmt.Errorf("the projection for user %d is still readable", userID)
	}
	return nil
}
