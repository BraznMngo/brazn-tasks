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

package user

import (
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/notifications"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1475 acceptance tests for password reset, written by the reviewing agent
// from the ticket text before the implementation was read.
//
// The fixture accounts are chosen for what the ticket is about rather than for
// convenience, and which is which is stated so a later reader can check it:
// user 1 is a password account (users.yml, issuer: local) and user 14 signs in
// with a provider (users.yml, issuer: https://some.service.com). "Google" in
// the ticket and "an account whose issuer is not local" in the code are the
// same set — user.IsLocalUser() compares the stored issuer to "local" and knows
// nothing about which provider it is.
const (
	bra1475LocalUserID    = 1
	bra1475LocalEmail     = "user1@example.com"
	bra1475ProviderUserID = 14
	bra1475ProviderEmail  = "user15@some.service.com"
	bra1475UnknownEmail   = "nobody-has-this-address@example.com"
)

// bra1475Mailable turns the mailer on and captures notifications for one test.
//
// MailerEnabled matters more than it looks: RequestUserPasswordResetToken
// returns before notifying when the mailer is off, so without this every
// assertion that "no mail was sent" would pass for the wrong reason — the
// reason being that this instance sends no mail at all.
func bra1475Mailable(t *testing.T) {
	t.Helper()

	config.MailerEnabled.Set(true)
	notifications.Fake()
	t.Cleanup(func() {
		notifications.Unfake()
		config.MailerEnabled.Set(false)
	})
}

// TestBRA1475ResetMailPointsStraightAtThePasswordPage is criterion 2.
//
// The address is asserted as a whole literal, written here from the ticket
// rather than assembled the way the code assembles it. Comparing against a
// value the code under test produced is docs/Testing-Rules.md's first shape of
// a test that agrees with itself, and it is the shape that would matter most
// here, because the defect being fixed is entirely in what this string says.
//
// The second assertion is the one that catches a regression rather than a
// rename: the OLD shape — the site root with the token in a query — is the link
// the lockout redirects and therefore destroys, so it must not appear.
func TestBRA1475ResetMailPointsStraightAtThePasswordPage(t *testing.T) {
	const publicURL = "https://tasks.example.test/"
	const token = "a-real-looking-token"

	// The whole address a customer will click, written out from the ticket's
	// "Password — one document with two states" section and Decision 1's table.
	const wantLink = "https://tasks.example.test/one/password.html?userPasswordReset=" + token

	// The shape that is live today and that this ticket exists to end.
	const deadLink = "https://tasks.example.test/?userPasswordReset=" + token

	config.ServicePublicURL.Set(publicURL)
	t.Cleanup(func() { config.ServicePublicURL.Set("") })

	for _, lang := range shippedLanguages {
		t.Run(lang, func(t *testing.T) {
			n := &ResetPasswordNotification{
				User:  newTestUser(),
				Token: &Token{ClearTextToken: token},
			}

			opts, err := notifications.RenderMail(n.ToMail(lang), lang)
			require.NoError(t, err)

			assert.Contains(t, opts.HTMLMessage, wantLink,
				"the customer's button must point at the page that sets a password")
			assert.NotContains(t, opts.HTMLMessage, deadLink,
				"the site root with the token in a query is the link the lockout destroys")
		})
	}
}

// TestBRA1475NoResetMailForAnAccountThatSignsInWithAProvider is criterion 3.
//
// The outcome the criterion names is that the person is not sent a link that
// leads nowhere. Two things are asserted because either alone would pass for
// the wrong reason: no token row is written, and no notification is raised.
// The token is the part that cannot be taken back — it is spent or it expires —
// so a test that only watched the mail would miss a token minted and dropped.
//
// The control matters as much as the case. Without it every assertion here
// would pass just as well if password reset had stopped working for everybody,
// which is a far worse outcome than the one being prevented.
func TestBRA1475NoResetMailForAnAccountThatSignsInWithAProvider(t *testing.T) {
	bra1475Mailable(t)

	t.Run("an account that signs in with a provider", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u, err := GetUserByID(s, bra1475ProviderUserID)
		require.NoError(t, err)
		require.NotEqual(t, IssuerLocal, u.Issuer,
			"this fixture must not be a password account or the test proves nothing")

		err = RequestUserPasswordResetToken(s, u)

		require.Error(t, err, "a reset link for this account leads nowhere and must be refused")
		assert.True(t, IsErrAccountIsNotLocal(err), "got %T: %v", err, err)
		require.NoError(t, s.Commit())

		db.AssertMissing(t, "user_tokens", map[string]interface{}{
			"user_id": bra1475ProviderUserID,
			"kind":    TokenPasswordReset,
		})
		notifications.AssertNotSent(t, &ResetPasswordNotification{})
	})

	t.Run("a password account still gets its link", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u, err := GetUserByID(s, bra1475LocalUserID)
		require.NoError(t, err)
		require.Equal(t, IssuerLocal, u.Issuer)

		require.NoError(t, RequestUserPasswordResetToken(s, u),
			"password reset must keep working, or this change has broken the only way back in")
		require.NoError(t, s.Commit())

		db.AssertExists(t, "user_tokens", map[string]interface{}{
			"user_id": bra1475LocalUserID,
			"kind":    TokenPasswordReset,
		}, false)
		notifications.AssertSent(t, &ResetPasswordNotification{})
	})
}

// TestBRA1475AStrangerSeesTheSameAnswerAsACustomer is the half of criterion 12
// this review owns.
//
// It is here rather than alongside criterion 3 because the two pull against
// each other, and that tension is the thing worth testing. Criterion 3 says an
// account that signs in with a provider gets no mail. Criterion 12 says the
// answer must not depend on what is behind the address. Meeting the first in
// the obvious way — telling the caller the account is not a password account —
// breaks the second, and hands anybody with a list of addresses a way to sort
// it into customers, provider customers and strangers, on an endpoint that
// needs no credentials at all.
//
// So the assertion is that all three cases leave by the same door, and the
// separate assertion that only one of them actually sent anything is what stops
// this passing because reset stopped working entirely.
func TestBRA1475AStrangerSeesTheSameAnswerAsACustomer(t *testing.T) {
	bra1475Mailable(t)

	for _, c := range []struct {
		name       string
		email      string
		wantsAMail bool
	}{
		{"an address with no account", bra1475UnknownEmail, false},
		{"a password account", bra1475LocalEmail, true},
		{"an account that signs in with a provider", bra1475ProviderEmail, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			db.LoadAndAssertFixtures(t)
			notifications.Fake()
			s := db.NewSession()
			defer s.Close()

			err := RequestUserPasswordResetTokenByEmail(s, &PasswordTokenRequest{Email: c.email})

			require.NoError(t, err,
				"every address must leave this endpoint the way success leaves it, or the answer sorts a list of addresses into accounts and non-accounts")
			require.NoError(t, s.Commit())

			if c.wantsAMail {
				notifications.AssertSent(t, &ResetPasswordNotification{})
				return
			}
			notifications.AssertNotSent(t, &ResetPasswordNotification{})
		})
	}
}

// TestBRA1475TheRefusalReadsTheAccountAndNotTheObjectHandedIn is the test that
// binds the guard, and it was added on re-review after the first version of
// this file turned out not to.
//
// WHAT WENT WRONG THE FIRST TIME, recorded because it is the exact shape
// docs/Testing-Rules.md warns about. The refusal below used to read the
// sign-in method off the *User it was handed. Every test in this file passed a
// user read from the database, so the field was always populated and the two
// readings — off the argument, off the stored row — always agreed. The bug was
// invisible to all of them. It was the INHERITED test at
// TestHandleFailedTOTPAuthLockoutCanBeUnlockedByPasswordReset that caught it,
// because it passes a user value carrying only an id, which is the shape the
// sign-in path really produces.
//
// So this case does what none of the others did: it hands the function the
// impoverished object directly, for an account that genuinely is a password
// account, and requires a token to come out. Putting the defect back turns
// this red on its own, without needing ten two-factor attempts to get there.
//
// The three cases after it are the edges the correction introduced, pinned as
// behaviour rather than left as reasoning about the code.
func TestBRA1475TheRefusalReadsTheAccountAndNotTheObjectHandedIn(t *testing.T) {
	bra1475Mailable(t)

	t.Run("a password account named by a bare id still gets its link", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Deliberately NOT read from the database. This is the shape
		// HandleFailedTOTPAuth passes, and the shape that broke.
		bare := &User{ID: bra1475LocalUserID}
		require.Empty(t, bare.Issuer,
			"this object must carry no issuer or the case proves nothing")

		require.NoError(t, RequestUserPasswordResetToken(s, bare),
			"this account is a password account; refusing it a reset link leaves a locked-out customer with no way back in")
		require.NoError(t, s.Commit())

		db.AssertExists(t, "user_tokens", map[string]interface{}{
			"user_id": bra1475LocalUserID,
			"kind":    TokenPasswordReset,
		}, false)
	})

	t.Run("a provider account named by a bare id is still refused", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		err := RequestUserPasswordResetToken(s, &User{ID: bra1475ProviderUserID})

		require.Error(t, err, "reading the row must not weaken the refusal it was introduced for")
		assert.True(t, IsErrAccountIsNotLocal(err), "got %T: %v", err, err)
	})

	t.Run("a locked account still gets a link, because that is what unlocks it", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u, err := GetUserByID(s, bra1475LocalUserID)
		require.NoError(t, err)
		require.NoError(t, u.SetStatus(s, StatusAccountLocked))
		require.NoError(t, s.Commit())

		s2 := db.NewSession()
		defer s2.Close()

		require.NoError(t, RequestUserPasswordResetToken(s2, &User{ID: bra1475LocalUserID}),
			"a lockout is precisely the state a reset gets somebody out of, so reading the row must not refuse it")
		require.NoError(t, s2.Commit())

		db.AssertExists(t, "user_tokens", map[string]interface{}{
			"user_id": bra1475LocalUserID,
			"kind":    TokenPasswordReset,
		}, false)
	})

	t.Run("an id naming nobody mints nothing", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		const nobody = 999999

		require.Error(t, RequestUserPasswordResetToken(s, &User{ID: nobody}),
			"a token minted for an account that could not be read is not a thing to mail")
		require.NoError(t, s.Commit())

		db.AssertMissing(t, "user_tokens", map[string]interface{}{
			"user_id": nobody,
			"kind":    TokenPasswordReset,
		})
	})
}

// TestBRA1475TenFailedTOTPAttemptsStillLockAProviderAccount is not an
// acceptance criterion. It is here because criterion 3's refusal was added to
// the function that mints the token, and that function is also what the TOTP
// lockout calls before it locks an account — so a refusal treated as a failure
// there would take the lock with it, and ten wrong codes against a provider
// account would stop locking it.
//
// That is a security regression this ticket could have introduced without any
// criterion noticing, which is the reason a reviewer writes tests from the
// outcome rather than from the diff.
//
// It does NOT bind the read-the-row correction: this account really is a
// provider account, so both readings of it agree and the defect is invisible
// here. The case above is the one that binds that.
func TestBRA1475TenFailedTOTPAttemptsStillLockAProviderAccount(t *testing.T) {
	bra1475Mailable(t)

	db.LoadAndAssertFixtures(t)
	s := db.NewSession()
	defer s.Close()

	u, err := GetUserByID(s, bra1475ProviderUserID)
	require.NoError(t, err)
	require.NotEqual(t, IssuerLocal, u.Issuer)
	require.NotEqual(t, StatusAccountLocked, u.Status,
		"the fixture must start unlocked or this test proves nothing")
	require.NoError(t, s.Commit())

	for i := 0; i < 10; i++ {
		HandleFailedTOTPAuth(u)
	}

	// Read as a row rather than through GetUserByID, which refuses a locked
	// account with an error and so cannot report the status being asserted.
	db.AssertExists(t, "users", map[string]interface{}{
		"id":     bra1475ProviderUserID,
		"status": StatusAccountLocked,
	}, false)

	// And the link it was refused was never minted, which is criterion 3
	// holding on this door too.
	db.AssertMissing(t, "user_tokens", map[string]interface{}{
		"user_id": bra1475ProviderUserID,
		"kind":    TokenPasswordReset,
	})
}
