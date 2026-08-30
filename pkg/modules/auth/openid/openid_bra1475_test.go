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
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1475 criterion 14, written by the reviewing agent from the ticket text
// before the implementation was read.
//
// The two sentences below are transcribed from the ticket's "Decisions already
// made" section, character for character, and are deliberately NOT taken from
// the functions under test. A test that compared the code's string against the
// code's string would agree with itself whatever the code said, which is the
// exact failure docs/Testing-Rules.md opens with — and it is the failure that
// would matter most here, because the entire deliverable of this criterion is
// what these two sentences say to a customer.
const (
	bra1475NoAccountSentence   = "There is no ONE account for this email address. Please subscribe to ONE first."
	bra1475UsePasswordSentence = "This account is not using Google to sign in. Please log in with username and password."
)

// bra1475ManagedMode turns managed mode on for one test.
//
// It has to be set explicitly, and that is worth stating rather than hiding in
// a helper: brazn.managedmode defaults to FALSE in the binary and in
// deploy/vikunja/docker-compose.yml, and decideManagedSignUp returns nil
// immediately when it is off. Neither sentence exists on an instance that has
// not turned it on. Whether production has is a deployment fact and not
// something this test can observe.
func bra1475ManagedMode(t *testing.T) {
	t.Helper()

	config.BraznManagedMode.Set(true)
	t.Cleanup(func() { config.BraznManagedMode.Set(false) })
}

func bra1475MessageOf(t *testing.T, err error) string {
	t.Helper()

	require.Error(t, err)
	var httpErr *echo.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.Code,
		"a refusal a signed-out person reads must be a refusal, not a server error")

	return httpErr.Message
}

// TestBRA1475TheTwoGoogleRefusalsSayWhatTheTicketSays asserts the wording
// itself. It is separated from the routing test below because the two fail for
// different reasons and a reader of a red build should be able to tell "the
// sentence changed" from "the wrong sentence is being shown".
func TestBRA1475TheTwoGoogleRefusalsSayWhatTheTicketSays(t *testing.T) {
	assert.Equal(t, bra1475NoAccountSentence, bra1475MessageOf(t, errManagedNoSignUp()))
	assert.Equal(t, bra1475UsePasswordSentence, bra1475MessageOf(t, errManagedUsePassword()))

	// The promise the ticket says is knowingly dropped. Asserted so that
	// somebody restoring it has to come here and read why it went.
	assert.NotContains(t, bra1475UsePasswordSentence, "add Google",
		"the ticket drops the promise that Google can be added afterwards, knowingly and on its own record")
}

// TestBRA1475EachGoogleRefusalFiresOnItsOwnCondition is the half of criterion
// 14 that matters more, and the half a wording assertion cannot reach.
//
// The criterion is not "these two sentences exist somewhere". It is that a
// person with no account sees the first and a person whose account does not use
// Google sees the second. Two correct sentences wired to one condition would
// pass every assertion above while telling half of the people who hit this the
// wrong thing, and the person told "please subscribe" when they already have an
// account is being told to pay twice.
func TestBRA1475EachGoogleRefusalFiresOnItsOwnCondition(t *testing.T) {
	bra1475ManagedMode(t)

	t.Run("an address with no account here is told to subscribe", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Verified as absent rather than assumed, because if this address did
		// have an account the test would be exercising the other branch and
		// would still pass for the wrong reason.
		existing, err := existingUserForAddress(s, "nobody-has-this-address@example.com")
		require.NoError(t, err)
		require.Nil(t, existing, "this address must have no account or the test proves nothing")

		err = decideManagedSignUp(s, &claims{Email: "nobody-has-this-address@example.com", EmailVerified: true}, "")

		assert.Equal(t, bra1475NoAccountSentence, bra1475MessageOf(t, err))
	})

	t.Run("an address that already has a password account is told to use it", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		existing, err := existingUserForAddress(s, "user1@example.com")
		require.NoError(t, err)
		require.NotNil(t, existing, "this fixture must have an account or the test proves nothing")

		err = decideManagedSignUp(s, &claims{Email: "user1@example.com", EmailVerified: true}, "")

		assert.Equal(t, bra1475UsePasswordSentence, bra1475MessageOf(t, err))
	})

	// The control. Both branches above end in a refusal, so without this the
	// test would pass just as well if this function refused everybody.
	t.Run("a self-hosted instance refuses nobody", func(t *testing.T) {
		config.BraznManagedMode.Set(false)
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		assert.NoError(t, decideManagedSignUp(s, &claims{Email: "user1@example.com", EmailVerified: true}, ""),
			"none of this applies off a managed instance, and this fork must stay usable self-hosted")
		config.BraznManagedMode.Set(true)
	})
}
