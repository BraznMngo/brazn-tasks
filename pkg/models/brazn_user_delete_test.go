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
	"testing"
	"time"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/require"
)

// clearProjections empties the table so each case starts from a known count.
// There is no fixture file for brazn_entitlement_projections, and go-testfixtures
// only truncates tables it has a file for, so LoadAndAssertFixtures leaves this
// one alone and rows would otherwise carry from one case into the next.
func clearProjections(t *testing.T) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	// id is autoincr and not null, so this matches every row.
	_, err := s.Where("id > 0").Delete(&EntitlementProjection{})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
}

// storeProjection writes a row as a delivery would have left it. The envelope is
// a placeholder on purpose: nothing on the deletion path parses or verifies it,
// so a real signed one would only suggest this test depends on its contents.
func storeProjection(t *testing.T, userID int64, organization string) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Insert(&EntitlementProjection{
		UserID:           userID,
		OrganizationID:   organization,
		Revision:         1,
		RevisionReceived: time.Now(),
		Envelope:         `{"placeholder":"not read by DeleteUser"}`,
	})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
}

// countProjections counts the WHOLE table, never one subject's row, and that
// distinction is the only thing making the assertions below mean anything.
//
// A change that RELOCATED the row rather than deleting it - blanking user_id,
// moving it under another subject, writing a tombstone - leaves a query on the
// erased user's id finding nothing and reporting success, while that subject's
// organization, edition, seat status and issued-at sit in the table untouched.
// That is the shape of bug this path is most likely to grow, because there is no
// foreign key and no cascade to make the row's absence structural. Counting sees
// it; querying the erased id cannot.
func countProjections(t *testing.T) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Table("brazn_entitlement_projections").Count()
	require.NoError(t, err)
	return count
}

// TestDeleteUserErasesTheEntitlementProjection covers BRA-933: erasure has to
// take the entitlement projection with it.
//
// The row is a statement of what a subject was allowed - organization, edition,
// seat status, validity window - and not a commercial record. No amount, no
// invoice, no tax figure is stored here, and the billing records that retention
// law keeps live in the commercial service, where they are authoritative and
// where this path never reaches. So the projection goes when the user goes, and
// nothing that must be kept is destroyed by its going.
//
// BraznApplyEntitlementProjection already refuses to write a NEW row for a
// subject this instance no longer has. This is the other half: removing one that
// was already there when the erasure ran.
func TestDeleteUserErasesTheEntitlementProjection(t *testing.T) {
	t.Run("the erased user's projection goes, and only theirs", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		clearProjections(t)
		notifications.Fake()

		// Two subjects, so the count afterwards separates a scoped delete from a
		// blanket one. A DELETE carrying no user_id predicate would leave 0 and
		// fail here just as loudly as a surviving row would leave 2.
		storeProjection(t, 6, "org-erased")
		storeProjection(t, 7, "org-retained")
		require.EqualValues(t, 2, countProjections(t), "both rows must be in place before the erasure")

		s := db.NewSession()
		defer s.Close()

		require.NoError(t, DeleteUser(s, &user.User{ID: 6}))
		require.NoError(t, s.Commit())

		require.EqualValues(t, 1, countProjections(t),
			"exactly one projection must remain: user 6's is erased, user 7's is untouched")
		db.AssertExists(t, "brazn_entitlement_projections", map[string]interface{}{"user_id": 7}, false)
		db.AssertMissing(t, "users", map[string]interface{}{"id": 6})
	})

	t.Run("erasing a user with no projection succeeds", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		clearProjections(t)
		notifications.Fake()

		s := db.NewSession()
		defer s.Close()

		// user 4 never had a projection. Deleting nothing must not be an error,
		// or erasure would fail for every subject who never held a seat.
		require.NoError(t, DeleteUser(s, &user.User{ID: 4}))
		require.NoError(t, s.Commit())

		require.EqualValues(t, 0, countProjections(t))
		db.AssertMissing(t, "users", map[string]interface{}{"id": 4})
	})
}
