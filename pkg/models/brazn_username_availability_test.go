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

package models

import (
	"context"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1475: answering whether one exact username is free.
//
// **What has to hold is an AGREEMENT, not a lookup.** The value of this
// operation is entirely that a name it calls free is one the registration will
// accept and a name it calls taken is one the registration will refuse. A test
// that only checked the three words against a fixture would pass while the two
// halves disagreed, and the symptom of that is a form promising a name the
// submission then rejects - which is the defect this operation was added to
// remove, arriving from the other side.

func TestCheckUsernameAvailabilityAnswersTheThreeWords(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	// Held by fixture user 1.
	status, err := CheckUsernameAvailability("user1")
	require.NoError(t, err)
	assert.Equal(t, UsernameTaken, status)

	status, err = CheckUsernameAvailability("nobody-has-ever-held-this-name")
	require.NoError(t, err)
	assert.Equal(t, UsernameAvailable, status)

	// The format rule is user.CheckUsernameFormat and is not reimplemented, so
	// what is asserted here is that this operation DEFERS to it rather than what
	// the rule itself says.
	status, err = CheckUsernameAvailability("has a space")
	require.NoError(t, err)
	assert.Equal(t, UsernameInvalid, status)

	status, err = CheckUsernameAvailability("")
	require.NoError(t, err)
	assert.Equal(t, UsernameInvalid, status)
}

// TestCheckUsernameAvailabilityMatchesTheWHOLENameAndNothingLess is
// docs/Brazn-Tasks-Rules.md §5.1's boundary at the layer that decides it: an
// exact lookup is permitted inside an invitation, a directory is not, and
// anything that matched less than the whole string would be a way to walk the
// name space one character at a time.
func TestCheckUsernameAvailabilityMatchesTheWHOLENameAndNothingLess(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	for _, notTheName := range []string{"user", "user1x", "ser1", "1", "%", "user_", "user%"} {
		status, err := CheckUsernameAvailability(notTheName)
		require.NoError(t, err)
		// DELETE-THE-GUARD: change GetUserByUsername's exact match to a LIKE.
		// RUN: this test failed on "user" and on "user%" — the wildcard forms
		// are the ones that turn an exact answer into an enumeration. Guard
		// restored.
		assert.Equal(t, UsernameAvailable, status,
			"%q is not user1 and must answer free", notTheName)
	}
}

// TestUsernameAvailabilityAgreesWithWhatRegistrationDOES is the one that matters.
//
// It never asserts a spelling of the rule. It drives the SAME name through the
// advice and then through the authority, and requires the two to agree - so it
// keeps holding if either the format rule or the collation changes underneath
// both, and fails the moment they diverge.
func TestUsernameAvailabilityAgreesWithWhatRegistrationDOES(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	const name = "a-name-nobody-holds-yet"

	before, err := CheckUsernameAvailability(name)
	require.NoError(t, err)
	require.Equal(t, UsernameAvailable, before, "the fixture must start with the name free")

	// The authority accepts exactly what the advice called free.
	created, err := CreateProvisionedUserWithPassword(
		context.Background(), "availability@example.com", name, "a-password-12345")
	require.NoError(t, err, "a name reported available must actually be registrable")
	require.NotNil(t, created)

	after, err := CheckUsernameAvailability(name)
	require.NoError(t, err)
	assert.Equal(t, UsernameTaken, after, "and it reads as taken the moment it is held")

	// And the authority refuses exactly what the advice now calls taken.
	_, err = CreateProvisionedUserWithPassword(
		context.Background(), "second@example.com", name, "a-password-12345")
	require.ErrorIs(t, err, ErrPasswordAccountEmailOrUsernameTaken,
		"a name reported taken must actually be refused")
}

// TestUsernameAvailabilityAndRegistrationAgreeOnCASE pins the two halves
// together on the one property most likely to differ between a lookup and an
// insert, and does it WITHOUT asserting which behaviour is correct.
//
// Whether users.username is compared case-sensitively is a property of the
// column's collation, not of this code. What must never happen is the advice
// and the authority disagreeing about it, because that is a form promising a
// name the submission then rejects.
func TestUsernameAvailabilityAndRegistrationAgreeOnCASE(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	const lower = "casesensitivity"
	upper := strings.ToUpper(lower)

	_, err := CreateProvisionedUserWithPassword(
		context.Background(), "case@example.com", lower, "a-password-12345")
	require.NoError(t, err)

	advice, err := CheckUsernameAvailability(upper)
	require.NoError(t, err)

	_, authority := CreateProvisionedUserWithPassword(
		context.Background(), "case2@example.com", upper, "a-password-12345")
	refused := authority != nil

	assert.Equal(t, advice == UsernameTaken, refused,
		"the advice said %q and the registration %s — the two must never disagree",
		advice, map[bool]string{true: "refused", false: "accepted"}[refused])
}

// TestCheckUsernameAvailabilityAnswersInvalidWithoutTouchingStoredData is the
// disclosure property: a malformed value must never become a question about
// what this instance holds.
func TestCheckUsernameAvailabilityAnswersInvalidWithoutTouchingStoredData(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	// A name that is BOTH malformed and, but for the space, a real prefix. If
	// the format rule ran after the lookup, the lookup would still have run.
	status, err := CheckUsernameAvailability("user1 ")
	require.NoError(t, err)
	// DELETE-THE-GUARD: move the CheckUsernameFormat call below the lookup in
	// CheckUsernameAvailability. RUN: this test still passed, because the
	// answer is `invalid` either way — the ORDER is not observable from here,
	// only from the fact that no query is issued. Recorded as a limit of this
	// test rather than as a guard it proves.
	assert.Equal(t, UsernameInvalid, status)
}
