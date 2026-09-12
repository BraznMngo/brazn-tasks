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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReminderSweepSelectsWhatIsDueAndHasNotFired replaces
// TestReminderGetTasksInTheNextMinute, which asserted the semantics BRA-1571 removes.
//
// That test pinned "due" to the coming minute: one reminder found at a fixture moment, none
// found at a later one. Both statements are now wrong on purpose. A reminder whose moment went
// by while nothing was running has to fire on the next pass (acceptance 4), so the sweep looks
// at everything that has come due and not fired rather than at a one-minute window, and
// "nothing is due" is now established by the stamp rather than by the clock having moved on.
//
// The window is not simply widened here. What replaces it is the pair of conditions that
// actually decide the outcome: the moment has passed, and the reminder carries no fired stamp.
func TestReminderSweepSelectsWhatIsDueAndHasNotFired(t *testing.T) {
	// 2018-12-01 01:12:00Z: fixture reminder 6 (task 47) went by in August and never fired,
	// so it is due. Reminder 8 falls on this exact moment but sits on the soft-deleted task
	// 51, and reminders 1 to 5 and 7 are all still to come.
	dueMoment, err := time.Parse(time.RFC3339Nano, "2018-12-01T01:12:00Z")
	require.NoError(t, err)

	t.Run("a moment that has passed is due, however long ago it went by", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		due, err := getTasksWithRemindersDueAndTheirUsers(s, dueMoment)
		require.NoError(t, err)
		require.Len(t, due, 1)
		assert.Equal(t, int64(6), due[0].TaskReminder.ID,
			"the reminder that went by in August is the one that is due")
		assert.Equal(t, int64(47), due[0].Task.ID)
	})

	t.Run("a soft-deleted task's reminder is never due, even at its exact moment", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		due, err := getTasksWithRemindersDueAndTheirUsers(s, dueMoment)
		require.NoError(t, err)
		for _, n := range due {
			assert.NotEqual(t, int64(51), n.TaskReminder.TaskID,
				"reminder 8 sits on the deleted task 51 and falls on this exact moment")
		}
	})

	t.Run("a moment still to come is not due", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		before, err := time.Parse(time.RFC3339Nano, "2018-07-01T00:00:00Z")
		require.NoError(t, err)
		due, err := getTasksWithRemindersDueAndTheirUsers(s, before)
		require.NoError(t, err)
		assert.Empty(t, due, "no fixture reminder has come due by July 2018")
	})

	t.Run("a reminder that already fired is not due again", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("UPDATE task_reminders SET fired_at = reminder WHERE id = ?", 6)
		require.NoError(t, err)

		due, err := getTasksWithRemindersDueAndTheirUsers(s, dueMoment)
		require.NoError(t, err)
		assert.Empty(t, due,
			"the only reminder that was due carries a fired stamp, so nothing is left to fire")
	})
}

func TestGetTaskUsersForTasks(t *testing.T) {
	t.Run("task owner", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 1 is owned by user 1 (created_by_id: 1) in project 1 (owned by user 1)
		taskUsers, err := getTaskUsersForTasks(s, []int64{1}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Should include the task creator
		hasUser1 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 1 && tu.Task.ID == 1 {
				hasUser1 = true
				break
			}
		}
		assert.True(t, hasUser1, "task owner should be included in task users")
	})

	t.Run("project shared directly with user", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 32 is in project 3, which is shared directly with user 1 (users_projects id: 1)
		taskUsers, err := getTaskUsersForTasks(s, []int64{32}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Should include user 1 who has direct share
		hasUser1 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 1 && tu.Task.ID == 32 {
				hasUser1 = true
				break
			}
		}
		assert.True(t, hasUser1, "user with direct project share should be included")
	})

	t.Run("creator who lost project access", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 1 is in project 1 (owned by user 1)
		// Task 1 was created by user 1 (created_by_id: 1)
		// User 13 has no access to project 1
		// Create a scenario by pretending user 13 created the task but has no access

		_, err := s.
			Cols("created_by_id").
			Where("id = ?", 1).
			Update(&Task{CreatedByID: 13})
		require.NoError(t, err)

		taskUsers, err := getTaskUsersForTasks(s, []int64{1}, nil)
		require.NoError(t, err)

		// Should only include users with access
		// User 13 should not be in the results (no access to project 1)
		hasUser13 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 13 && tu.Task.ID == 1 {
				hasUser13 = true
				break
			}
		}
		assert.False(t, hasUser13, "creator without project access should be filtered out")
	})

	t.Run("subscriber who lost project access", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 2 is in project 1 (owned by user 1)
		// Create a subscription for user 13 who has no access to project 1
		subscription := &Subscription{
			EntityType: SubscriptionEntityTask,
			EntityID:   2,
			UserID:     13,
		}
		_, err := s.Insert(subscription)
		require.NoError(t, err)

		taskUsers, err := getTaskUsersForTasks(s, []int64{2}, nil)
		require.NoError(t, err)

		// User 13 should NOT be in the results (subscribed but no access to project 1)
		hasUser13 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 13 && tu.Task.ID == 2 {
				hasUser13 = true
				break
			}
		}
		assert.False(t, hasUser13, "subscriber without project access should be filtered out")
	})

	t.Run("assignees - with and without project access", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 30 has assignees: user 1 and user 2 (task_assignees)
		// Task 30 is in project 1, owned by user 1
		// User 1 has access (owner), user 2 does NOT have access to project 1
		taskUsers, err := getTaskUsersForTasks(s, []int64{30}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Should include user 1 (assignee WITH project access)
		// Should NOT include user 2 (assignee WITHOUT project access)
		hasUser1 := false
		hasUser2 := false
		for _, tu := range taskUsers {
			if tu.Task.ID == 30 {
				if tu.User.ID == 1 {
					hasUser1 = true
				}
				if tu.User.ID == 2 {
					hasUser2 = true
				}
			}
		}
		assert.True(t, hasUser1, "assignee with project access should be included")
		assert.False(t, hasUser2, "assignee without project access should be filtered out")
	})

	t.Run("subscribers - with project access", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 2 has subscription from user 1 (subscriptions id: 1)
		// Task 2 is in project 1, owned by user 1
		// User 1 has access as the owner
		taskUsers, err := getTaskUsersForTasks(s, []int64{2}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Should include the subscriber who has access
		hasUser1 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 1 && tu.Task.ID == 2 {
				hasUser1 = true
				break
			}
		}
		assert.True(t, hasUser1, "subscriber with project access should be included")
	})

	t.Run("no duplicate users", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 30: user 1 is both creator and assignee
		taskUsers, err := getTaskUsersForTasks(s, []int64{30}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Count how many times user 1 appears for task 30
		user1Count := 0
		for _, tu := range taskUsers {
			if tu.User.ID == 1 && tu.Task.ID == 30 {
				user1Count++
			}
		}
		assert.Equal(t, 1, user1Count, "each user should appear only once per task")
	})

	t.Run("empty task list", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		taskUsers, err := getTaskUsersForTasks(s, []int64{}, nil)
		require.NoError(t, err)
		assert.Empty(t, taskUsers)
	})

	t.Run("multiple tasks with various relationships", func(t *testing.T) {
		db.LoadAndAssertFixtures(t)
		s := db.NewSession()
		defer s.Close()

		// Task 1: user 1 is creator and owner
		// Task 2: user 1 is subscriber and owner
		// Task 30: user 1 is assignee and owner, user 2 is assignee without access
		taskUsers, err := getTaskUsersForTasks(s, []int64{1, 2, 30}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, taskUsers)

		// Count unique task IDs in results
		taskIDs := make(map[int64]bool)
		for _, tu := range taskUsers {
			taskIDs[tu.Task.ID] = true
		}
		assert.True(t, taskIDs[1], "should include users for task 1")
		assert.True(t, taskIDs[2], "should include users for task 2")
		assert.True(t, taskIDs[30], "should include users for task 30")

		// Verify user 2 is NOT included for any task (no access to project 1)
		hasUser2 := false
		for _, tu := range taskUsers {
			if tu.User.ID == 2 {
				hasUser2 = true
				break
			}
		}
		assert.False(t, hasUser2, "user without project access should not be included for any task")
	})
}
