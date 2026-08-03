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

package openid

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/models"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/user"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/labstack/echo/v5"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreateUser(t *testing.T) {
	t.Run("new user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email:             "test@example.com",
			PreferredUsername: "someUserWhoDoesNotExistYet",
		}
		provider := &Provider{}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "users", map[string]interface{}{
			"id":       u.ID,
			"email":    cl.Email,
			"username": "someUserWhoDoesNotExistYet",
		}, false)
	})
	t.Run("new user, no username provided", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email:             "test@example.com",
			PreferredUsername: "",
		}
		provider := &Provider{}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.NotEmpty(t, u.Username)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "users", map[string]interface{}{
			"id":    u.ID,
			"email": cl.Email,
		}, false)
	})
	t.Run("new user, no email address", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email: "",
		}
		provider := &Provider{}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "12345"}

		_, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.Error(t, err)
	})
	t.Run("existing user, different email address", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email: "other-email-address@some.service.com",
		}
		provider := &Provider{}
		idToken := &oidc.IDToken{Issuer: "https://some.service.com", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "users", map[string]interface{}{
			"id":    u.ID,
			"email": cl.Email,
		}, false)
	})
	t.Run("existing user, non existing team", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		team := "new sso team"
		oidcID := "47404"
		cl := &claims{
			Email: "other-email-address@some.service.com",
			VikunjaGroups: []map[string]interface{}{
				{"name": team, "oidcID": oidcID},
			},
		}

		provider := &Provider{Name: "Vikunja Login"}
		idToken := &oidc.IDToken{Issuer: "https://some.service.com", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		teamData := getTeamDataFromToken(cl.VikunjaGroups, nil)
		require.NoError(t, err)
		err = models.SyncExternalTeamsForUser(s, u, teamData, "https://some.issuer", provider.Name)
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "users", map[string]interface{}{
			"id":    u.ID,
			"email": cl.Email,
		}, false)
		db.AssertExists(t, "teams", map[string]interface{}{
			"name":        team + " (" + provider.Name + ")",
			"external_id": oidcID,
			"is_public":   false,
		}, false)
	})

	t.Run("Update IsPublic flag for existing team", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		team := "testteam15"
		oidcID := "15"
		cl := &claims{
			Email: "other-email-address@some.service.com",
			VikunjaGroups: []map[string]interface{}{
				{"name": team, "oidcID": oidcID, "isPublic": true},
			},
		}

		provider := &Provider{Name: "Vikunja Login"}
		idToken := &oidc.IDToken{Issuer: "https://some.service.com", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		teamData := getTeamDataFromToken(cl.VikunjaGroups, nil)
		err = models.SyncExternalTeamsForUser(s, u, teamData, "https://some.issuer", provider.Name)
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "teams", map[string]interface{}{
			"name":        team + " (" + provider.Name + ")",
			"external_id": oidcID,
			"is_public":   true,
		}, false)
	})

	t.Run("existing user, assign to existing team", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		team := "testteam14"
		oidcID := "14"
		cl := &claims{
			Email: "other-email-address@some.service.com",
			VikunjaGroups: []map[string]interface{}{
				{"name": team, "oidcID": oidcID},
			},
		}

		u := &user.User{ID: 10}
		teamData := getTeamDataFromToken(cl.VikunjaGroups, nil)
		err := models.SyncExternalTeamsForUser(s, u, teamData, "https://some.issuer", "Vikunja Login")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertExists(t, "team_members", map[string]interface{}{
			"user_id": u.ID,
		}, false)
	})
	t.Run("existing user, remove from existing team", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email:         "other-email-address@some.service.com",
			VikunjaGroups: []map[string]interface{}{},
		}

		u := &user.User{ID: 10}
		teamData := getTeamDataFromToken(cl.VikunjaGroups, nil)
		err := models.SyncExternalTeamsForUser(s, u, teamData, "https://some.issuer", "Vikunja Login")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		db.AssertMissing(t, "team_members", map[string]interface{}{
			"team_id": 14,
			"user_id": u.ID,
		})
		db.AssertMissing(t, "team_members", map[string]interface{}{
			"team_id": 15,
			"user_id": u.ID,
		})
		// This team is not external and should not be touched
		db.AssertExists(t, "team_members", map[string]interface{}{
			"team_id": 13,
			"user_id": u.ID,
		}, false)
	})
	t.Run("ProviderFallback: Match to existing local user on username", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{}
		provider := &Provider{
			UsernameFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "user11"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.Equal(t, idToken.Subject, u.Username, "subject match username")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")
	})
	t.Run("ProviderFallback: Match to existing local user on preferred_username when sub differs", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			PreferredUsername: "user11",
		}
		provider := &Provider{
			UsernameFallback: true,
		}
		// PocketID-style: the subject is an opaque UUID that does not match any local username.
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "c0ffee00-dead-beef-cafe-000000000011"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		assert.Equal(t, "user11", u.Username, "should link to the local user matching preferred_username")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")

		// No duplicate user must be created for the opaque subject.
		db.AssertMissing(t, "users", map[string]interface{}{
			"subject": idToken.Subject,
		})
	})
	t.Run("ProviderFallback: Falls back to sub when preferred_username is empty", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			PreferredUsername: "",
		}
		provider := &Provider{
			UsernameFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "user11"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.Equal(t, idToken.Subject, u.Username, "subject should match username")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")
	})
	t.Run("ProviderFallback: Match to existing local user on email", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		usersBefore, err := s.Count(&user.User{})
		require.NoError(t, err)

		cl := &claims{
			Email:         "user11@example.com",
			EmailVerified: true,
		}
		provider := &Provider{
			EmailFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "user11"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.Equal(t, cl.Email, u.Email, "email should match")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")

		// The email-only fallback must link the existing user, not create a duplicate.
		usersAfter, err := s.Count(&user.User{})
		require.NoError(t, err)
		assert.Equal(t, usersBefore, usersAfter, "no new user should have been created")
	})
	t.Run("ProviderFallback: unverified email does not link to an existing local user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// GHSA-xv7q-fvmc-jx96: an attacker asserting a victim's email without
		// email_verified must not be linked to the victim's local account.
		cl := &claims{
			Email:             "user11@example.com",
			EmailVerified:     false,
			PreferredUsername: "attackerUser",
		}
		provider := &Provider{
			EmailFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "attacker-subject"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		assert.NotEqual(t, 11, int(u.ID), "must not link to user 11 via an unverified email")
		assert.Equal(t, "https://some.issuer", u.Issuer, "a new separate account should have been created")
		assert.Equal(t, "attacker-subject", u.Subject)
	})
	t.Run("ProviderFallback: verified email links to existing local user despite unknown subject", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email:         "user11@example.com",
			EmailVerified: true,
		}
		provider := &Provider{
			EmailFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "attacker-subject"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
	})
	t.Run("ProviderFallback: empty email claim does not link to an arbitrary local user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		usersBefore, err := s.Count(&user.User{})
		require.NoError(t, err)

		// EmailFallback on, no username fallback, and the IdP sent no email claim. The
		// email-only search must not degenerate to an issuer-only lookup matching an
		// arbitrary local user. With no email there is nothing safe to match on, so the
		// flow falls through to user creation (which then errors because an email is
		// required) rather than silently linking an existing local account.
		cl := &claims{
			Email:             "",
			PreferredUsername: "brandNewOidcUser",
		}
		provider := &Provider{
			EmailFallback: true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "opaque-subject-no-email"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		// Must not have linked an existing local user.
		require.Error(t, err, "an empty email must not silently link an existing local user")
		assert.Nil(t, u, "no existing local user should be returned for an empty email claim")

		usersAfter, err := s.Count(&user.User{})
		require.NoError(t, err)
		assert.Equal(t, usersBefore, usersAfter, "no user should have been linked or created from an empty email claim")
	})
	t.Run("ProviderFallback: Match to existing local user  on username and email", func(t *testing.T) {

		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{
			Email:         "user11@example.com",
			EmailVerified: true,
		}
		provider := &Provider{
			UsernameFallback: true,
			EmailFallback:    true,
		}
		idToken := &oidc.IDToken{Issuer: "https://some.issuer", Subject: "user11"}

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		assert.Equal(t, cl.Email, u.Email, "email should match")
		assert.Equal(t, idToken.Subject, u.Username, "subject match username")
		assert.Equal(t, user.IssuerLocal, u.Issuer, "User should be a local one")
		assert.Equal(t, 11, int(u.ID), "user id 11 expected")
	})
}

// managedModeForTest switches brazn.managedmode for one test and puts it back.
// Viper overrides outlive InitDefaultConfig, so leaving one set would silently
// change every later test in this package.
func managedModeForTest(t *testing.T, managed bool) {
	t.Helper()

	previous := config.BraznManagedMode.Get()
	t.Cleanup(func() { config.BraznManagedMode.Set(previous) })
	config.BraznManagedMode.Set(managed)
}

// unmatchedGoogleSubject is a sign-in that matches NOTHING in the fixtures:
// an issuer and subject no user carries, an address no user has, and a
// provider with both fallbacks off so nothing can link it to an account by
// another route.
//
// It is built once and used by both of the first two subtests below, which is
// the point of it: those two differ in brazn.managedmode and in nothing else.
//
// EmailVerified IS SET, and that is not decoration. Without it the managed
// subtest would be refused by the unverified-address guard instead of by the
// token guard, and would pass while asserting nothing about the token at all -
// a refusal from an unrelated guard is the hardest kind of vacuous test to see
// by reading. The unverified case has its own subtest below.
func unmatchedGoogleSubject() (*claims, *Provider, *oidc.IDToken) {
	cl := &claims{
		Email:             "nobody-here-yet@example.com",
		PreferredUsername: "nobody-here-yet",
		EmailVerified:     true,
	}
	idToken := &oidc.IDToken{
		Issuer:  "https://accounts.google.com",
		Subject: "google-subject-nobody-has",
	}
	return cl, &Provider{Name: "Google"}, idToken
}

// conformanceSignupToken is the token value from the contract's own
// conformance fixture, quoted rather than invented: 43 unpadded base64url
// characters, which is the only shape a token has.
const conformanceSignupToken = "EXAMPLE_signup_token_43_chars_not_a_secret1"

// redemptionRecord is what the stub below saw.
type redemptionRecord struct {
	calls int
	body  string
}

// stubRedemption points brazn.signupredemptionurl at a server answering exactly
// what the contract says, and records what this build actually sent.
func stubRedemption(t *testing.T, status int, answer string) *redemptionRecord {
	t.Helper()

	record := &redemptionRecord{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		record.calls++
		record.body = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, err = w.Write([]byte(answer))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	configForTest(t, config.BraznSignupRedemptionURL, server.URL)
	configForTest(t, config.BraznServiceToken, "a-service-credential-for-the-test")
	return record
}

// configForTest sets a config key for one test and puts it back.
func configForTest(t *testing.T, key config.Key, value interface{}) {
	t.Helper()

	previous := key.Get()
	t.Cleanup(func() { key.Set(previous) })
	key.Set(value)
}

// TestGetOrCreateUserUnderManagedMode is the sign-in / sign-up split (BRA-1018,
// re-answered by BRA-1071, Identity-and-Access-Rules.md §11 cases 1, 2 and 7).
//
// WHY THIS CANNOT PASS FOR THE WRONG REASON, which is the whole reason it is
// written as a set. The first two subtests hand getOrCreateUser byte-identical
// claims, provider and token, load the same fixtures, and differ in exactly one
// thing: brazn.managedmode. One creates a user and one refuses. No guard other
// than the one under test reads that value, so no unrelated refusal can produce
// the difference - and the first subtest is a positive control for the fixture
// itself, because a subject that was somehow already matched would have been
// RESOLVED there rather than created, and the assertion that a new row appeared
// would fail.
//
// Deleting the managed-mode branch in getOrCreateUser makes the second subtest
// fail on require.Error, because the call then does what the first one proves
// it does. Deleting the signup.Redeem call makes "a token the service refuses"
// fail, because a user then survives a refusal.
func TestGetOrCreateUserUnderManagedMode(t *testing.T) {
	t.Run("managed mode off signs the subject up, as every self-hosted instance must", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, false)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.NoError(t, err)
		require.NoError(t, s.Commit())

		db.AssertExists(t, "users", map[string]interface{}{
			"id":      u.ID,
			"email":   cl.Email,
			"subject": idToken.Subject,
		}, false)
	})

	t.Run("managed mode refuses the same subject and stores nothing", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()

		_, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, "")
		require.Error(t, err)
		var refusal *echo.HTTPError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, http.StatusForbidden, refusal.Code)
		require.NoError(t, s.Rollback())

		db.AssertMissing(t, "users", map[string]interface{}{"email": cl.Email})
	})

	// The one this ticket exists for. Same subject, same claims, same managed
	// mode as the refusal above - the only difference is a token the service
	// says is good.
	t.Run("a valid token signs the same subject up and reports the created user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		record := stubRedemption(t, http.StatusOK, `{"result":"redeemed"}`)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()

		u, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, conformanceSignupToken)
		require.NoError(t, err)
		require.NoError(t, s.Commit())

		db.AssertExists(t, "users", map[string]interface{}{
			"id":      u.ID,
			"email":   cl.Email,
			"subject": idToken.Subject,
		}, false)

		// AC7, asserted as a JOIN rather than at either end: the id on the wire
		// has to be the id of the row that now exists, as a decimal STRING.
		// Checking only that some id was sent, or only that some user was
		// created, would pass with the two unrelated.
		require.Equal(t, 1, record.calls)
		var sent map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(record.body), &sent))
		assert.Equal(t, strconv.FormatInt(u.ID, 10), sent["user_id"])
		assert.Equal(t, conformanceSignupToken, sent["token"])
		assert.Equal(t, cl.Email, sent["email"])
	})

	t.Run("a token the service refuses creates no user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		record := stubRedemption(t, http.StatusForbidden, `{"error":"token_unusable"}`)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()

		_, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, conformanceSignupToken)
		require.Error(t, err)
		var refusal *echo.HTTPError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, http.StatusForbidden, refusal.Code)
		require.NoError(t, s.Rollback())

		// The call was made - so this is the redemption refusing, not the shape
		// check refusing before it - and no row survived it.
		assert.Equal(t, 1, record.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"email": cl.Email})
	})

	// Google and a password on one address do not join automatically. user1 in
	// the fixtures holds this address with the local issuer.
	t.Run("an address that already has an account is never adopted", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		record := stubRedemption(t, http.StatusOK, `{"result":"redeemed"}`)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()
		cl.Email = "user1@example.com"

		_, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, conformanceSignupToken)
		require.Error(t, err)
		var refusal *echo.HTTPError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, http.StatusForbidden, refusal.Code)
		require.NoError(t, s.Rollback())

		// A perfectly good token must not be spent on a refusal, so the check
		// has to come first: if the redemption ran here, somebody's token would
		// be gone and they would still have no account.
		assert.Equal(t, 0, record.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"subject": idToken.Subject})
	})

	t.Run("a provider that will not verify the address cannot sign anybody up", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		record := stubRedemption(t, http.StatusOK, `{"result":"redeemed"}`)
		s := db.NewSession()
		defer s.Close()

		cl, provider, idToken := unmatchedGoogleSubject()
		cl.EmailVerified = false

		_, err := getOrCreateUser(context.Background(), s, cl, provider, idToken, conformanceSignupToken)
		require.Error(t, err)
		require.NoError(t, s.Rollback())

		assert.Equal(t, 0, record.calls)
		db.AssertMissing(t, "users", map[string]interface{}{"email": cl.Email})
	})

	t.Run("managed mode signs in a subject this instance already has", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		s := db.NewSession()
		defer s.Close()

		// user 14 in the fixtures, matched on issuer and subject - the path
		// above the split, which must be untouched by any of this.
		cl := &claims{Email: "user15@some.service.com"}
		idToken := &oidc.IDToken{Issuer: "https://some.service.com", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, &Provider{}, idToken, "")
		require.NoError(t, err)
		require.NoError(t, s.Commit())
		assert.Equal(t, int64(14), u.ID)
	})

	// AC3. Carrying a token into a sign-in must change nothing: the person
	// already has an account, so there is nothing to redeem, and redeeming
	// anyway would consume somebody's token and bind it to a user that was
	// never created for it.
	t.Run("a token changes nothing for a subject this instance already has", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		managedModeForTest(t, true)
		record := stubRedemption(t, http.StatusOK, `{"result":"redeemed"}`)
		s := db.NewSession()
		defer s.Close()

		cl := &claims{Email: "user15@some.service.com", EmailVerified: true}
		idToken := &oidc.IDToken{Issuer: "https://some.service.com", Subject: "12345"}

		u, err := getOrCreateUser(context.Background(), s, cl, &Provider{}, idToken, conformanceSignupToken)
		require.NoError(t, err)
		require.NoError(t, s.Commit())
		assert.Equal(t, int64(14), u.ID)
		assert.Equal(t, 0, record.calls)
	})
}

// TestMergeClaims tests the mergeClaims function with different configurations including forceUserInfo
func TestMergeClaims(t *testing.T) {
	t.Run("ForceUserInfo enabled - should use userinfo values", func(t *testing.T) {
		// Setup token claims
		tokenClaims := &claims{
			Email:             "token-email@example.com",
			Name:              "Token Name",
			PreferredUsername: "token_username",
		}

		// Setup userinfo claims
		userinfoClaims := &claims{
			Email:             "userinfo-email@example.com",
			Name:              "UserInfo Name",
			PreferredUsername: "userinfo_username",
		}

		// Test with ForceUserInfo enabled
		err := mergeClaims(tokenClaims, userinfoClaims, true)
		require.NoError(t, err)

		// Verify userinfo data was used
		assert.Equal(t, "userinfo-email@example.com", tokenClaims.Email)
		assert.Equal(t, "UserInfo Name", tokenClaims.Name)
		assert.Equal(t, "userinfo_username", tokenClaims.PreferredUsername)
	})

	t.Run("ForceUserInfo disabled - should use token values if present", func(t *testing.T) {
		// Setup token claims with all values
		tokenClaims := &claims{
			Email:             "token-email@example.com",
			Name:              "Token Name",
			PreferredUsername: "token_username",
		}

		// Setup userinfo claims
		userinfoClaims := &claims{
			Email:             "userinfo-email@example.com",
			Name:              "UserInfo Name",
			PreferredUsername: "userinfo_username",
		}

		// Test with ForceUserInfo disabled
		err := mergeClaims(tokenClaims, userinfoClaims, false)
		require.NoError(t, err)

		// Verify token data was preserved
		assert.Equal(t, "token-email@example.com", tokenClaims.Email)
		assert.Equal(t, "Token Name", tokenClaims.Name)
		assert.Equal(t, "token_username", tokenClaims.PreferredUsername)
	})

	t.Run("Missing values - should use userinfo when token is missing values", func(t *testing.T) {
		// Setup token claims with missing values
		tokenClaims := &claims{
			Email: "token-email@example.com",
			// Missing Name and PreferredUsername
		}

		// Setup userinfo claims
		userinfoClaims := &claims{
			Email:             "userinfo-email@example.com",
			Name:              "UserInfo Name",
			PreferredUsername: "userinfo_username",
		}

		// Test with ForceUserInfo disabled, but missing values in token
		err := mergeClaims(tokenClaims, userinfoClaims, false)
		require.NoError(t, err)

		// Verify token email was kept, but missing fields were filled from userinfo
		assert.Equal(t, "token-email@example.com", tokenClaims.Email)
		assert.Equal(t, "UserInfo Name", tokenClaims.Name)
		assert.Equal(t, "userinfo_username", tokenClaims.PreferredUsername)
	})

	t.Run("Use nickname when preferred_username is missing", func(t *testing.T) {
		// Setup token claims with missing preferred_username
		tokenClaims := &claims{
			Email: "token-email@example.com",
			Name:  "Token Name",
			// Missing PreferredUsername
		}

		// Setup userinfo claims with nickname but no preferred_username
		userinfoClaims := &claims{
			Email:    "userinfo-email@example.com",
			Name:     "UserInfo Name",
			Nickname: "userinfo_nickname",
			// Missing PreferredUsername to test fallback to nickname
		}

		// Test with ForceUserInfo disabled
		err := mergeClaims(tokenClaims, userinfoClaims, false)
		require.NoError(t, err)

		// Verify nickname was used for preferred_username
		assert.Equal(t, "userinfo_nickname", tokenClaims.PreferredUsername)
	})

	t.Run("Error when email is missing", func(t *testing.T) {
		// Setup token claims with missing email
		tokenClaims := &claims{
			// Missing Email
			Name:              "Token Name",
			PreferredUsername: "token_username",
		}

		// Setup userinfo claims also with missing email
		userinfoClaims := &claims{
			// Missing Email
			Name:              "UserInfo Name",
			PreferredUsername: "userinfo_username",
		}

		// Test with ForceUserInfo disabled
		err := mergeClaims(tokenClaims, userinfoClaims, false)

		// Verify error is returned for missing email
		require.Error(t, err)
		var expectedErr *user.ErrNoOpenIDEmailProvided
		assert.ErrorAs(t, err, &expectedErr)
	})
}

func TestEnforceTOTPIfRequired(t *testing.T) {
	// user 10 has TOTP enabled in pkg/db/fixtures/totp.yml with this secret.
	const user10Secret = "JBSWY3DPEHPK3PXP"

	t.Run("user without TOTP - no passcode required", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// user 1 has a totp row but with enabled=false.
		u := &user.User{ID: 1}
		err := enforceTOTPIfRequired(s, u, "")
		require.NoError(t, err)
	})

	t.Run("TOTP enabled - missing passcode returns ErrInvalidTOTPPasscode", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u := &user.User{ID: 10}
		err := enforceTOTPIfRequired(s, u, "")
		require.Error(t, err)
		assert.True(t, user.IsErrInvalidTOTPPasscode(err))
	})

	t.Run("TOTP enabled - invalid passcode returns ErrInvalidTOTPPasscode", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u := &user.User{ID: 10}
		err := enforceTOTPIfRequired(s, u, "000000")
		require.Error(t, err)
		assert.True(t, user.IsErrInvalidTOTPPasscode(err))
	})

	t.Run("TOTP enabled - valid passcode succeeds", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		passcode, err := totp.GenerateCode(user10Secret, time.Now())
		require.NoError(t, err)

		u := &user.User{ID: 10}
		err = enforceTOTPIfRequired(s, u, passcode)
		require.NoError(t, err)
	})
}

func TestSyncUserAvatarFromOpenID(t *testing.T) {
	t.Run("empty picture URL resets openid provider to default", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Use the fixture user that has avatar_provider = "openid"
		u, err := user.GetUserByID(s, 19)
		require.NoError(t, err)
		assert.Equal(t, "openid", u.AvatarProvider, "precondition: user should have openid avatar provider")

		err = syncUserAvatarFromOpenID(s, u, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		// Verify the avatar provider was reset to default in the database
		db.AssertExists(t, "users", map[string]interface{}{
			"id":              19,
			"avatar_provider": "default",
		}, false)
	})

	t.Run("empty picture URL does not reset non-openid provider", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Use a regular user (avatar_provider is empty/"default")
		u, err := user.GetUserByID(s, 1)
		require.NoError(t, err)

		err = syncUserAvatarFromOpenID(s, u, "")
		require.NoError(t, err)
		err = s.Commit()
		require.NoError(t, err)

		// Verify the avatar provider was NOT changed to "default" or anything else
		s2 := db.NewSession()
		defer s2.Close()
		updatedUser, err := user.GetUserByID(s2, 1)
		require.NoError(t, err)
		assert.Empty(t, updatedUser.AvatarProvider, "avatar provider should remain empty for non-openid user")
	})
}

func TestEmailVerifiedClaimDecoding(t *testing.T) {
	// Some OIDC providers emit email_verified as a JSON string; both forms must
	// decode without breaking the whole claims parse (GHSA-xv7q-fvmc-jx96).
	cases := map[string]bool{
		`{"email_verified": true}`:    true,
		`{"email_verified": false}`:   false,
		`{"email_verified": "true"}`:  true,
		`{"email_verified": "false"}`: false,
		`{"email_verified": "1"}`:     true,
		`{"email_verified": "0"}`:     false,
		`{}`:                          false,
	}
	for body, want := range cases {
		t.Run(body, func(t *testing.T) {
			var cl claims
			require.NoError(t, json.Unmarshal([]byte(body), &cl))
			assert.Equal(t, want, bool(cl.EmailVerified))
		})
	}
}
