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
	"time"

	"code.vikunja.io/api/pkg/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

// userAwaitingConfirmation is fixture user 4: status 1, which is
// StatusEmailConfirmationRequired.
const userAwaitingConfirmation = 4

// issueConfirmToken issues a real confirmation link for a user and, when age is
// not zero, moves its creation time that far into the past.
//
// The tokens in this package's tests are ISSUED, not described. A fixture that
// merely looked expired would prove nothing about the deadline: the row would
// carry whatever the fixture author typed, and the code under test would never
// have to agree with it. Here the row is written by the same generateToken the
// product uses, and only its clock is moved.
func issueConfirmToken(t *testing.T, s *xorm.Session, age time.Duration) string {
	u, err := GetUserByID(s, userAwaitingConfirmation)
	require.NoError(t, err)

	token, err := generateToken(s, u, TokenEmailConfirm)
	require.NoError(t, err)

	if age != 0 {
		_, err = s.Exec("UPDATE user_tokens SET created = ? WHERE id = ?", time.Now().Add(-age), token.ID)
		require.NoError(t, err)
	}

	return token.ClearTextToken
}

func TestUserEmailConfirm(t *testing.T) {
	t.Run("an empty token is refused", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		_, err := ConfirmEmail(s, &EmailConfirm{Token: ""})
		require.Error(t, err)
		assert.True(t, IsErrInvalidEmailConfirmToken(err))
	})

	// The link a mail client broke across two lines arrives as something we
	// never issued, and that is a different sentence from "this one ran out".
	t.Run("a token we never issued is invalid, not expired", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		_, err := ConfirmEmail(s, &EmailConfirm{Token: "notATokenThisInstanceEverIssued"})
		require.Error(t, err)
		assert.True(t, IsErrInvalidEmailConfirmToken(err))
		assert.False(t, IsErrExpiredEmailConfirmToken(err))
	})

	t.Run("a fresh token confirms the address", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, 0)

		result, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.AlreadyConfirmed)

		u, err := GetUserByID(s, userAwaitingConfirmation)
		require.NoError(t, err)
		assert.Equal(t, StatusActive, u.Status)
	})

	// The deadline is the one the screen states as fact. Twenty-five hours is
	// on the far side of it; the token is otherwise indistinguishable from the
	// one the test above confirms with.
	t.Run("a token older than the link lifetime is expired", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, EmailConfirmMaxAge+time.Hour)

		_, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.Error(t, err)
		assert.True(t, IsErrExpiredEmailConfirmToken(err))
		assert.False(t, IsErrInvalidEmailConfirmToken(err))

		u, err := GetUserByID(s, userAwaitingConfirmation)
		require.NoError(t, err)
		assert.Equal(t, StatusEmailConfirmationRequired, u.Status)
	})

	t.Run("a token one hour inside the lifetime still works", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, EmailConfirmMaxAge-time.Hour)

		result, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)
		assert.False(t, result.AlreadyConfirmed)
	})

	// THE RULING THIS PROTECTS: a second click is not a failure. The link is
	// really used first - the same call, against the same row - and the second
	// attempt has to come back a success, not an error dressed up in green.
	t.Run("a link that was already used succeeds again and says so", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, 0)

		first, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)
		require.False(t, first.AlreadyConfirmed)

		second, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err, "a second click on a link that worked must not be an error")
		require.NotNil(t, second)
		assert.True(t, second.AlreadyConfirmed)

		u, err := GetUserByID(s, userAwaitingConfirmation)
		require.NoError(t, err)
		assert.Equal(t, StatusActive, u.Status)
	})

	// A spent link is still spent. It is kept only so the sentence above can be
	// said, and it must never confirm anything again - including an account
	// that was put back into confirmation by an email change.
	t.Run("a spent link cannot confirm a second time round", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, 0)
		_, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)

		// The account goes back to waiting, as an email change would leave it.
		_, err = s.Exec("UPDATE users SET status = ? WHERE id = ?", int(StatusEmailConfirmationRequired), userAwaitingConfirmation)
		require.NoError(t, err)

		result, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)
		assert.True(t, result.AlreadyConfirmed)

		u, err := GetUserByID(s, userAwaitingConfirmation)
		require.NoError(t, err)
		assert.Equal(t, StatusEmailConfirmationRequired, u.Status,
			"a spent link reported success but also re-activated the account")
	})

	// After a resend the customer holds two links and has no way of telling
	// which one counts. Using either must retire the other.
	t.Run("using one link retires the others", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		first := issueConfirmToken(t, s, 0)
		second := issueConfirmToken(t, s, 0)

		_, err := ConfirmEmail(s, &EmailConfirm{Token: second})
		require.NoError(t, err)

		_, err = ConfirmEmail(s, &EmailConfirm{Token: first})
		require.Error(t, err)
		assert.True(t, IsErrInvalidEmailConfirmToken(err))
	})

	// A link past the deadline stays past it once it has been used, so somebody
	// coming back to an old mail a week later is told the same thing whether or
	// not they once clicked it.
	t.Run("a spent link past the lifetime reads as expired", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		link := issueConfirmToken(t, s, 0)
		_, err := ConfirmEmail(s, &EmailConfirm{Token: link})
		require.NoError(t, err)

		_, err = s.Exec("UPDATE user_tokens SET created = ? WHERE kind = ?",
			time.Now().Add(-(EmailConfirmMaxAge + time.Hour)), int(TokenEmailConfirmed))
		require.NoError(t, err)

		_, err = ConfirmEmail(s, &EmailConfirm{Token: link})
		require.Error(t, err)
		assert.True(t, IsErrExpiredEmailConfirmToken(err))
	})
}

func TestResendEmailConfirmation(t *testing.T) {
	// The whole point of the endpoint. Three addresses that are three different
	// facts about this instance, and one answer between them.
	t.Run("says nothing about any address", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// user4 is waiting on confirmation, user1 was confirmed long ago, and
		// nobody at all has the third address.
		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "user4@example.com"}))
		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "user1@example.com"}))
		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "nobody@example.com"}))
		// Nothing here validates the shape - the handlers do that, and refusing
		// a string that is not an address discloses nothing. What this asserts
		// is that reaching the domain with one is not a different outcome.
		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "not-even-an-address"}))
	})

	t.Run("issues a new link for an address that is waiting", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u, err := GetUserByID(s, userAwaitingConfirmation)
		require.NoError(t, err)

		before, err := getTokensForKind(s, u, TokenEmailConfirm)
		require.NoError(t, err)

		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "user4@example.com"}))

		after, err := getTokensForKind(s, u, TokenEmailConfirm)
		require.NoError(t, err)
		require.Len(t, after, 1, "a resend must leave exactly one live link")
		if len(before) == 1 {
			assert.NotEqual(t, before[0].Token, after[0].Token, "the earlier link must have been replaced")
		}
	})

	// An address that has nothing waiting must not gain a link it could then be
	// probed with, and must not have an existing one disturbed.
	t.Run("issues nothing for an address that is not waiting", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		u, err := GetUserByID(s, 1)
		require.NoError(t, err)
		require.Equal(t, StatusActive, u.Status)

		require.NoError(t, ResendEmailConfirmation(s, &EmailConfirmResend{Email: "user1@example.com"}))

		tokens, err := getTokensForKind(s, u, TokenEmailConfirm)
		require.NoError(t, err)
		assert.Empty(t, tokens)
	})
}
