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
	"encoding/json"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/require"
)

// clearNotifications empties the table so a whole-table count means something.
// The fixture file seeds three rows of its own, and while none of them carries
// a user, leaving them in would put a constant in every expected number for no
// benefit.
func clearNotifications(t *testing.T) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	// id is autoincr and not null, so this matches every row.
	_, err := s.Where("id > 0").Delete(&notifications.DatabaseNotification{})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
}

// storeNotification writes a row the way the application writes one, and the
// marshalling here is the point of the helper rather than an implementation
// detail of it.
//
// notifyDB stores json.Marshal(notification.ToDB()), so this does the same. A
// hand-written JSON literal would test the scan against this file's idea of the
// payload instead of against the bytes the application actually produces - and
// the whole reason the scan walks the tree is that the real payload nests users
// in places no one enumerated. A fixture that only contained the fields the
// test author thought of would hide exactly that.
func storeNotification(t *testing.T, notifiableID int64, n notifications.Notification) int64 {
	t.Helper()

	content, err := json.Marshal(n.ToDB())
	require.NoError(t, err)

	return storeRawNotification(t, notifiableID, n.Name(), string(content))
}

// storeRawNotification writes a row with a payload given verbatim, for the one
// case that has no notification type behind it.
func storeRawNotification(t *testing.T, notifiableID int64, name, payload string) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	row := &notifications.DatabaseNotification{
		NotifiableID: notifiableID,
		Notification: json.RawMessage(payload),
		Name:         name,
	}
	_, err := s.Insert(row)
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	return row.ID
}

// countNotifications counts the WHOLE table, never the erased person's rows.
//
// Every row seeded below sits on a THIRD PARTY's notifiable_id, so a query
// scoped to the erased user would find nothing before the erasure and nothing
// after it, and would report success for code that did nothing at all. The
// count is what separates "their copies went" from "everybody's rows went".
func countNotifications(t *testing.T) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Table("notifications").Count()
	require.NoError(t, err)
	return count
}

// TestDeleteUserErasesCopiesInOtherPeoplesNotifications covers BRA-1117.
//
// Eight notification types return themselves from ToDB(), so the whole struct -
// including every *user.User it reaches, each serialising id, name, username
// and email - is marshalled onto the RECIPIENT's row. DeleteUser's other
// notification delete is keyed on notifiable_id and only ever empties the
// erased person's own inbox, so these copies are the ones nothing reached.
//
// Sebastian ruled they go rather than being scrubbed: the payload is the only
// place they live, and a scrub cannot be shown to have found every place the
// person appears inside it.
//
// ON notifications.Fake(). It is set here because DeleteUser calls Notify for
// AccountDeletedNotification, and without it that call tries to send real mail.
// It does NOT blind these assertions: Notify short-circuits on its first line
// under Fake(), but nothing below goes through Notify - every row is inserted
// directly, and every assertion is about rows that were already there when
// DeleteUser started. It does not even hide the account-deleted row, because
// AccountDeletedNotification.ToDB() returns nil, so no row is written for it
// either way.
func TestDeleteUserErasesCopiesInOtherPeoplesNotifications(t *testing.T) {
	t.Cleanup(notifications.Unfake)

	// The erased subject and a surviving sibling. Every shape below is seeded
	// for both, so a delete that lost its predicate empties the table and fails
	// exactly as loudly as a delete that did nothing.
	erased := &user.User{ID: 6, Username: "user6", Name: "Erased Person", Email: "erased@example.com"}
	sibling := &user.User{ID: 7, Username: "user7", Name: "Surviving Sibling", Email: "sibling@example.com"}
	task := &Task{ID: 1, Title: "A task in a project neither of them owns"}

	t.Run("copies of the erased person go, whoever's row they sit in", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		clearNotifications(t)
		notifications.Fake()

		// NOTE THE notifiable_id ON EVERY ROW BELOW: 1 and 2, never 6. If any
		// of these sat on the erased person's own row the pre-existing
		// notifiable_id delete would remove it and this test would pass
		// against a completely absent implementation.

		// 1. The erased person as Doer - the shape every one of the eight types
		// can produce.
		doerRow := storeNotification(t, 1, &TaskCommentNotification{
			Doer:    erased,
			Task:    task,
			Comment: &TaskComment{ID: 1, Comment: "a comment"},
		})

		// 2. The erased person as ASSIGNEE, with somebody else as the Doer.
		// This is the case an implementation that reads Doer and stops passes
		// every other assertion in this file and still fails.
		assigneeRow := storeNotification(t, 1, &TaskAssignedNotification{
			Doer:     sibling,
			Assignee: erased,
			Task:     task,
		})

		// 3. The erased person reachable ONLY at .team.members[0], with both
		// top-level user fields belonging to somebody else. Team.Members is
		// []*TeamUser and TeamUser embeds user.User, so a member serialises id,
		// name, username and email inline - and GetTeamByID populates Members
		// before this notification is built, so adding anyone to a team writes
		// every existing member into the new member's row. An implementation
		// that reads named top-level fields leaves this one behind.
		nestedRow := storeNotification(t, 2, &TeamMemberAddedNotification{
			Doer:   sibling,
			Member: sibling,
			Team:   &Team{ID: 1, Name: "A team", Members: []*TeamUser{{User: *erased}}},
		})

		// The same three shapes for the sibling, who is not being erased.
		siblingDoerRow := storeNotification(t, 2, &TaskCommentNotification{
			Doer:    sibling,
			Task:    task,
			Comment: &TaskComment{ID: 2, Comment: "another comment"},
		})
		siblingAssigneeRow := storeNotification(t, 1, &TaskAssignedNotification{
			Doer:     sibling,
			Assignee: sibling,
			Task:     task,
		})
		siblingNestedRow := storeNotification(t, 2, &TeamMemberAddedNotification{
			Doer:   sibling,
			Member: sibling,
			Team:   &Team{ID: 1, Name: "A team", Members: []*TeamUser{{User: *sibling}}},
		})

		// A payload carrying no user at all. It shares the erased person's id
		// as a plain number, so a scan that matched on the id alone rather than
		// on a user object would take it.
		noUserRow := storeRawNotification(t, 2, "test.notification", `{"test":"no user here","id":6}`)

		require.EqualValues(t, 7, countNotifications(t), "all seven rows must be in place before the erasure")

		s := db.NewSession()
		defer s.Close()

		require.NoError(t, DeleteUser(s, &user.User{ID: 6}))
		require.NoError(t, s.Commit())

		require.EqualValues(t, 4, countNotifications(t),
			"exactly the three rows naming user 6 must go, and nothing else")

		db.AssertMissing(t, "notifications", map[string]interface{}{"id": doerRow})
		db.AssertMissing(t, "notifications", map[string]interface{}{"id": assigneeRow})
		db.AssertMissing(t, "notifications", map[string]interface{}{"id": nestedRow})

		db.AssertExists(t, "notifications", map[string]interface{}{"id": siblingDoerRow}, false)
		db.AssertExists(t, "notifications", map[string]interface{}{"id": siblingAssigneeRow}, false)
		db.AssertExists(t, "notifications", map[string]interface{}{"id": siblingNestedRow}, false)
		db.AssertExists(t, "notifications", map[string]interface{}{"id": noUserRow}, false)

		db.AssertMissing(t, "users", map[string]interface{}{"id": 6})
	})

	t.Run("erasing a user nobody was ever notified about succeeds", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		clearNotifications(t)
		notifications.Fake()

		// Only the sibling's rows exist. Erasing somebody with no copies
		// anywhere must not be an error and must not touch them, or every
		// erasure of a quiet account would either fail or take other people's
		// history with it.
		siblingRow := storeNotification(t, 2, &TaskCommentNotification{
			Doer:    sibling,
			Task:    task,
			Comment: &TaskComment{ID: 3, Comment: "a comment"},
		})

		s := db.NewSession()
		defer s.Close()

		require.NoError(t, DeleteUser(s, &user.User{ID: 4}))
		require.NoError(t, s.Commit())

		require.EqualValues(t, 1, countNotifications(t))
		db.AssertExists(t, "notifications", map[string]interface{}{"id": siblingRow}, false)
		db.AssertMissing(t, "users", map[string]interface{}{"id": 4})
	})
}
