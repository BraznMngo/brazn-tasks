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

// Acceptance tests for BRA-1571, the server half: a reminder stands on its own, fires once,
// and reaches the person as a notification record rather than as mail.
//
// These were written from the ticket's acceptance list before either implementation diff was
// read, which is why they call the sweep and count what the person received rather than
// asserting the shape of the query that found it.
//
// Two things about the fixture, both deliberate, because a gentler one would pass against a
// broken implementation:
//
//   - The person's email preference is switched ON and the mailer is configured, for every
//     test in this file. The behaviour being removed was gated on exactly that flag, so a test
//     run with it off would observe "no mail" for the old reason and prove nothing.
//   - Mail is observed at mail.SendMail, the last step before the send queue, so every gate
//     between the notification and the wire has really run. Notifications are NOT faked: the
//     row in the database is the deliverable, and notifications.Fake() would skip writing it.

import (
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/mail"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

// reminderTestUser is user 1, who owns project 1 and most of the fixture tasks.
func reminderTestUser() *user.User {
	return &user.User{ID: 1, Username: "user1", Email: "user1@example.com"}
}

// asHostileAsProduction resets the fixtures and then makes the environment the most
// demanding one the ticket describes: mail fully configured, and the person's own email
// preference on. It also clears the notification table, so a count is a count of what this
// test caused rather than of what the fixture happened to contain.
func asHostileAsProduction(t *testing.T) *xorm.Session {
	t.Helper()

	db.LoadAndAssertFixtures(t)

	// Notifications must not be faked. Another test in this package may have left the
	// package-level flag set, and under it Notify returns before writing anything, so every
	// row count below would be zero and every assertion about "one notification" would
	// pass for the wrong reason.
	notifications.Unfake()

	previousMailer := config.MailerEnabled.GetBool()
	config.MailerEnabled.Set(true)
	t.Cleanup(func() { config.MailerEnabled.Set(previousMailer) })

	mail.Fake()
	mail.ResetSent()

	s := db.NewSession()
	t.Cleanup(func() { _ = s.Close() })

	// The person's own email preference, on. This is the flag the removed behaviour keyed
	// off, so leaving it at the fixture default would make the no-mail assertions vacuous.
	_, err := s.Exec("UPDATE users SET email_reminders_enabled = ? WHERE id = ?", true, reminderTestUser().ID)
	require.NoError(t, err)

	_, err = s.Exec("DELETE FROM notifications")
	require.NoError(t, err)

	// And the fixture reminders go too. Every one of them sits in 2018, 2019 or 2023, so
	// under the catch-up behaviour this ticket introduces they are all due right now and all
	// of them fire on any pass taken at the real clock. Leaving them in place would make
	// every count in this file a count of the fixture plus the test, which is how a test
	// stops being able to say what it caused. The fixture reminders keep their own test,
	// TestReminderSweepSelectsWhatIsDueAndHasNotFired, which works at their own moments.
	_, err = s.Exec("DELETE FROM task_reminders")
	require.NoError(t, err)

	return s
}

// countRows is raw SQL on purpose. Counting through the ORM would apply the same soft-delete
// and column mapping the code under test applies, which is the self-referential shape:
// the count would agree with the implementation whatever the implementation did.
func countReminderRows(t *testing.T, s *xorm.Session, query string, args ...interface{}) int64 {
	t.Helper()

	var n int64
	_, err := s.SQL(query, args...).Get(&n)
	require.NoError(t, err)
	return n
}

func reminderNotificationsFor(t *testing.T, s *xorm.Session, userID int64) int64 {
	t.Helper()

	return countReminderRows(t, s,
		"SELECT COUNT(*) FROM notifications WHERE notifiable_id = ? AND name = ?",
		userID, "task.reminder")
}

func taskCount(t *testing.T, s *xorm.Session) int64 {
	t.Helper()

	return countReminderRows(t, s, "SELECT COUNT(*) FROM tasks")
}

// insertTaskReminder puts a reminder about a task straight into the table.
//
// It goes through the ORM rather than through raw SQL on purpose, and this is not a style
// preference. The test engine does not set the database's own time zone the way the real one
// does, so a datetime written as raw text and read back through the ORM comes back shifted by
// the host's offset on any machine that is not on UTC. Writing it the way the application
// writes it means the round trip is consistent wherever this runs.
// It returns nothing. Every caller establishes a precondition and then asks the sweep what it
// found, so a returned row would be a handle nobody reads; the tests that need the identifier
// read it back from the table, which is also the harsher question to ask.
func insertTaskReminder(t *testing.T, s *xorm.Session, taskID int64, moment time.Time) {
	t.Helper()

	_, err := s.Insert(&TaskReminder{
		TaskID:      taskID,
		Reminder:    moment.UTC(),
		SubjectKind: ReminderSubjectTask,
		CreatedByID: 1,
	})
	require.NoError(t, err)
}

// stampFired puts a reminder into the state a previous pass would have left it in, without
// going through the sweep. Used where the test has to establish "this already fired" as a
// precondition rather than as the thing being proven.
func stampFired(t *testing.T, s *xorm.Session, reminderID int64, at time.Time) {
	t.Helper()

	_, err := s.Exec("UPDATE task_reminders SET fired_at = ? WHERE id = ?",
		at.UTC().Format(dbTimeFormat), reminderID)
	require.NoError(t, err)
}

// Acceptance 1: a reminder created with no subject fires at its moment and produces one
// notification, and no task exists anywhere as a result.
//
// It goes through CreateStandaloneReminder, which is the function the POST /api/v2/reminders
// handler calls, so the path from the published operation to the row is the path under test.
func TestBRA1571AReminderAboutNothingFiresAndCreatesNoTask(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	tasksBefore := taskCount(t, s)

	moment := time.Now().UTC().Add(-5 * time.Minute)
	created, err := CreateStandaloneReminder(s, person, moment, "collect the dry cleaning")
	require.NoError(t, err)
	require.NotZero(t, created.ID)

	// The moment is stored in UTC, whatever zone it arrived in. Asserted by sending a
	// moment in a zone that is not UTC and reading back the instant.
	inAnotherZone := time.FixedZone("UTC+7", 7*60*60)
	alsoCreated, err := CreateStandaloneReminder(s, person, moment.In(inAnotherZone), "and the other thing")
	require.NoError(t, err)
	assert.True(t, alsoCreated.Reminder.Equal(moment),
		"a reminder's moment must survive as the same instant whatever zone it arrived in")

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	assert.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"each reminder about nothing must produce exactly one notification")
	assert.Equal(t, tasksBefore, taskCount(t, s),
		"a reminder about nothing must not create a task anywhere")

	// And it is the person's own reminder that reached them, not somebody else's.
	assert.Equal(t, int64(0), reminderNotificationsFor(t, s, 2),
		"a reminder about nothing must reach only the person it belongs to")
}

// Acceptance 2: a reminder set one day before a task's due date fires one day before that due
// date, and changing the due date moves the firing.
func TestBRA1571ARelativeReminderMovesWithTheDueDate(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	oneDayBefore := int64(-24 * 60 * 60)

	task := &Task{ID: 1, DueDate: due, Reminders: []*TaskReminder{
		{RelativeTo: ReminderRelationDueDate, RelativePeriod: oneDayBefore},
	}}
	require.NoError(t, task.Update(s, person))

	stored := []*TaskReminder{}
	require.NoError(t, s.Where("task_id = ?", 1).Find(&stored))
	require.Len(t, stored, 1)
	assert.True(t, stored[0].Reminder.Equal(due.Add(-24*time.Hour)),
		"a reminder a day before the due date must sit a day before the due date")

	// Nothing fires yet: the moment is two days away.
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Equal(t, int64(0), reminderNotificationsFor(t, s, person.ID),
		"a reminder whose moment is still to come must not fire")

	// Move the due date forward, and the firing must move with it.
	movedDue := due.Add(48 * time.Hour)
	moved := &Task{ID: 1, DueDate: movedDue, Reminders: []*TaskReminder{
		{RelativeTo: ReminderRelationDueDate, RelativePeriod: oneDayBefore},
	}}
	require.NoError(t, moved.Update(s, person))

	stored = []*TaskReminder{}
	require.NoError(t, s.Where("task_id = ?", 1).Find(&stored))
	require.Len(t, stored, 1)
	assert.True(t, stored[0].Reminder.Equal(movedDue.Add(-24*time.Hour)),
		"moving the due date must move the reminder with it")

	// The reminder is now three days out, so a pass at the moment the FIRST arrangement
	// would have fired must still produce nothing. This is the assertion that separates
	// "the stored value moved" from "the firing moved".
	require.NoError(t, fireDueReminders(s, due.Add(-24*time.Hour), false))
	assert.Equal(t, int64(0), reminderNotificationsFor(t, s, person.ID),
		"the old moment must no longer fire once the due date has moved")

	// And it does fire at the new moment.
	require.NoError(t, fireDueReminders(s, movedDue.Add(-24*time.Hour), false))
	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"the reminder must fire at the moment the new due date puts it at")
}

// Acceptance 3: a task reminder whose task is completed before the moment never fires, and the
// same holds when the task is deleted. A reminder about nothing is not subject to that filter.
func TestBRA1571ADoneOrDeletedTasksReminderNeverFires(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	// Task 2 is done in the fixtures; task 51 is soft deleted. Task 1 is neither, and is
	// the control: without it, a sweep that returned nothing at all would pass this test.
	for _, taskID := range []int64{1, 2, 51} {
		insertTaskReminder(t, s, taskID, past)
	}
	// And one about nothing, at the same moment, which no task can complete or delete.
	_, err := CreateStandaloneReminder(s, person, past, "nothing can finish this one for me")
	require.NoError(t, err)

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	// Two: the live task's reminder, and the one about nothing. Not four.
	assert.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"a done task's reminder and a deleted task's reminder must not fire, and the other two must")

	// Which two, established from the stamps rather than from the count, because two counts
	// of two can be made of different pairs.
	unfired := []*TaskReminder{}
	require.NoError(t, s.Where("fired_at IS NULL").Find(&unfired))
	stayedSilent := map[int64]bool{}
	for _, r := range unfired {
		stayedSilent[r.TaskID] = true
	}
	assert.True(t, stayedSilent[2], "the done task's reminder must not be stamped as fired")
	assert.True(t, stayedSilent[51], "the deleted task's reminder must not be stamped as fired")
	assert.False(t, stayedSilent[1], "the live task's reminder must be stamped as fired")
}

// Acceptance 4: the server is stopped across a reminder's moment and restarted. The
// notification is produced on the next pass, once.
//
// There is no way to stop a process inside a unit test, and the thing that made the old
// behaviour lose a reminder was not the stopping — it was that the query only ever looked at
// the coming minute. A moment that went by an hour before this pass is exactly the state a
// restart leaves behind, and it is the state being asserted.
func TestBRA1571AReminderMissedWhileNothingWasRunningStillFires(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	missed := time.Now().UTC().Add(-90 * time.Minute)
	_, err := CreateStandaloneReminder(s, person, missed, "the thing I was going to be told about")
	require.NoError(t, err)

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"a reminder whose moment passed while nothing was running must fire on the next pass")

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"and it must not fire a second time once it has")
}

// Acceptance 5: running the sweep repeatedly over the same due reminder produces exactly one
// notification.
//
// Acceptance 10 is the negative half of this one: deleting the fired-timestamp condition from
// the sweep must make this test fail. That check was run by hand, by removing the condition
// and watching this test go red. See the QA ledger entry for BRA-1571.
func TestBRA1571RepeatedPassesProduceExactlyOneNotification(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	moment := time.Now().UTC().Add(-time.Minute)
	standalone, err := CreateStandaloneReminder(s, person, moment, "say this once")
	require.NoError(t, err)

	insertTaskReminder(t, s, 1, moment)

	for pass := 1; pass <= 3; pass++ {
		require.NoError(t, fireDueReminders(s, time.Now(), false), "pass %d", pass)
	}

	assert.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"three passes over two due reminders must produce two notifications, not six")

	// And the stamp is what makes that true, rather than something upstream having
	// swallowed the later passes: after the passes, nothing due is left unfired.
	assert.Equal(t, int64(0),
		countReminderRows(t, s, "SELECT COUNT(*) FROM task_reminders WHERE fired_at IS NULL AND id = ?", standalone.ID),
		"a reminder that has fired must carry the stamp that stops it firing again")
}

// Acceptance 6 and 7: with mail fully configured and the person's email preference on, a fired
// reminder produces a notification and no mail — and under that same configuration another
// notification kind still sends mail.
//
// The two halves are one test on purpose. Apart, the first can pass because mail is broken
// everywhere, which is the failure mode that matters: switching reminder mail off must not
// switch mail off.
func TestBRA1571AFiredReminderSendsNoMailAndOtherKindsStillDo(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	// The preference this test depends on, read back rather than assumed, because the whole
	// point of the assertion is that it is on.
	assert.Equal(t, int64(1),
		countReminderRows(t, s, "SELECT COUNT(*) FROM users WHERE id = ? AND email_reminders_enabled = ?", person.ID, true),
		"this test is only meaningful with the person's email preference on")

	_, err := CreateStandaloneReminder(s, person, time.Now().UTC().Add(-time.Minute), "no mail for this")
	require.NoError(t, err)
	insertTaskReminder(t, s, 1, time.Now().UTC().Add(-time.Minute))

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	require.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"the reminders must have fired, or the mail assertion below proves nothing")
	assert.Nil(t, mail.LastSent(), "a fired reminder must send no mail")

	// The same configuration, a different notification kind. This one has no database
	// representation, so it takes the inline mail path rather than the after-insert one.
	overdue := &UndoneTaskOverdueNotification{
		User:    person,
		Task:    &Task{ID: 1, Title: "task #1", DueDate: time.Now().UTC().Add(-time.Hour)},
		Project: &Project{ID: 1, Title: "Test1"},
	}
	require.NoError(t, notifications.Notify(person, overdue, s))
	assert.NotNil(t, mail.LastSent(),
		"turning reminder mail off must not turn mail off for every other kind")
}

// Target state 7, and the server half of acceptance 9: whether the person has seen a reminder
// is held on the notification record and nowhere else.
//
// The desktop half of acceptance 9 — that the bell's unseen count falls when ONE marks the
// reminder seen — needs two processes against one instance and is recorded UNPROVEN.
func TestBRA1571SeenIsHeldOnlyOnTheNotificationRecord(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	_, err := CreateStandaloneReminder(s, person, time.Now().UTC().Add(-time.Minute), "have I seen this")
	require.NoError(t, err)
	require.NoError(t, fireDueReminders(s, time.Now(), false))

	unread := countReminderRows(t, s,
		"SELECT COUNT(*) FROM notifications WHERE notifiable_id = ? AND name = ? AND read_at IS NULL",
		person.ID, "task.reminder")
	require.Equal(t, int64(1), unread, "a fired reminder must start out unseen")

	// Firing it did not make it seen. That is target state 8a from the other side: the
	// server never marks a reminder seen on the person's behalf.
	fired := []*TaskReminder{}
	require.NoError(t, s.Where("fired_at IS NOT NULL").Find(&fired))
	require.Len(t, fired, 1)
	assert.Equal(t, int64(1), unread,
		"a reminder that has fired is not thereby seen")

	// Marking the notification read is what makes it seen, and the reminder record itself
	// carries no seen state of its own: the sweep must not find it again either way.
	_, err = s.Exec("UPDATE notifications SET read_at = ? WHERE notifiable_id = ? AND name = ?",
		time.Now().UTC().Format(dbTimeFormat), person.ID, "task.reminder")
	require.NoError(t, err)

	assert.Equal(t, int64(0), countReminderRows(t, s,
		"SELECT COUNT(*) FROM notifications WHERE notifiable_id = ? AND name = ? AND read_at IS NULL",
		person.ID, "task.reminder"),
		"marking the notification read must take it out of the unseen set")

	// No second home for seen state: the reminder table must carry no read/seen column, or
	// the two could disagree and the ticket says there is one place.
	columns, err := s.QueryString("SELECT * FROM task_reminders LIMIT 1")
	require.NoError(t, err)
	require.NotEmpty(t, columns)
	for name := range columns[0] {
		assert.NotContains(t, name, "read",
			"seen state must live on the notification record, not on the reminder")
		assert.NotContains(t, name, "seen",
			"seen state must live on the notification record, not on the reminder")
	}
}

// The sweep must read the reminder's own columns.
//
// This one is not an acceptance criterion. The implementing agent found by reading that the
// query joins to tasks and that the database library selects every column once a join is
// present — and tasks carries id, created and created_by_id under the same names, so the
// task's values landed on the reminder. The consequence is not cosmetic: the fired stamp is
// written by reminder id, so it would have been written against task identifiers, and a
// reminder about nothing would have lost the owner it is delivered to.
//
// It was fixed before this was written and had no assertion. This is the assertion.
func TestBRA1571TheSweepReadsTheRemindersOwnIdentity(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	past := time.Now().UTC().Add(-time.Minute)

	// A task reminder on task 1, whose own id will differ from the task's id, and whose
	// creator column is empty while the task's is not.
	insertTaskReminder(t, s, 1, past)

	var reminderID int64
	_, err := s.SQL("SELECT id FROM task_reminders WHERE task_id = ? ORDER BY id DESC LIMIT 1", 1).Get(&reminderID)
	require.NoError(t, err)
	require.NotEqual(t, int64(1), reminderID,
		"this test needs a reminder whose id differs from its task's, or it cannot tell them apart")

	due, err := getTasksWithRemindersDueAndTheirUsers(s, time.Now())
	require.NoError(t, err)
	require.NotEmpty(t, due)

	var found *TaskReminder
	for _, n := range due {
		if n.TaskReminder != nil && n.TaskReminder.TaskID == 1 {
			found = n.TaskReminder
		}
	}
	require.NotNil(t, found, "the live task's due reminder must come back")
	assert.Equal(t, reminderID, found.ID,
		"the sweep must carry the reminder's own id, not the joined task's")

	// And the stamp lands on the reminder rather than on whatever the join offered.
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(1), countReminderRows(t, s,
		"SELECT COUNT(*) FROM task_reminders WHERE id = ? AND fired_at IS NOT NULL", reminderID),
		"the fired stamp must land on the reminder that fired")

	// A reminder about nothing must keep the owner it is delivered to, which is the other
	// column the join could have overwritten.
	require.NoError(t, func() error {
		_, err := CreateStandaloneReminder(s, person, past, "and I still belong to somebody")
		return err
	}())
	due, err = getTasksWithRemindersDueAndTheirUsers(s, time.Now())
	require.NoError(t, err)
	var mine *ReminderDueNotification
	for _, n := range due {
		if n.Task == nil {
			mine = n
		}
	}
	require.NotNil(t, mine, "a due reminder about nothing must come back")
	require.NotNil(t, mine.User)
	assert.Equal(t, person.ID, mine.User.ID,
		"a reminder about nothing must be delivered to the person who set it")
}

// Target state 9, the server half: creating, listing and removing a reminder about nothing
// works through the same functions the published operations call, and a reminder about a task
// is not reachable that way.
//
// The HTTP layer itself — that the three operations are registered at those addresses, and
// that the body bounds are enforced — is recorded UNPROVEN: this fork has no HTTP test harness
// for the v2 surface, and adding one is a dependency this change is not allowed to add.
func TestBRA1571CreateListAndRemoveAReminderAboutNothing(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()
	somebodyElse := &user.User{ID: 2, Username: "user2", Email: "user2@example.com"}

	soon := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	later := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Second)

	second, err := CreateStandaloneReminder(s, person, later, "the later one")
	require.NoError(t, err)
	first, err := CreateStandaloneReminder(s, person, soon, "the sooner one")
	require.NoError(t, err)
	_, err = CreateStandaloneReminder(s, somebodyElse, soon, "not yours")
	require.NoError(t, err)

	// A reminder with no moment is refused rather than stored as the zero instant, which
	// would be in the past and would fire at once.
	_, err = CreateStandaloneReminder(s, person, time.Time{}, "when?")
	require.Error(t, err, "a reminder with no moment must be refused")

	listed, total, err := GetStandaloneReminders(s, person, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "the list must hold this person's reminders and nobody else's")
	require.Len(t, listed, 2)
	assert.Equal(t, first.ID, listed[0].ID, "soonest first")
	assert.Equal(t, second.ID, listed[1].ID, "soonest first")

	// A reminder about a task is not part of this resource, which is the seam between the
	// two routes. Set one on a task and it must not appear in the list.
	insertTaskReminder(t, s, 1, soon)
	_, total, err = GetStandaloneReminders(s, person, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "a reminder about a task must not appear among the reminders about nothing")

	// Somebody else's reminder cannot be removed, and the refusal is not a silent success.
	require.Error(t, DeleteStandaloneReminder(s, somebodyElse, first.ID),
		"only the person a reminder belongs to may remove it")
	assert.Equal(t, int64(1),
		countReminderRows(t, s, "SELECT COUNT(*) FROM task_reminders WHERE id = ?", first.ID),
		"a refused removal must leave the reminder where it was")

	// Nor can a reminder about a task be removed through this route.
	var taskReminderID int64
	_, err = s.SQL("SELECT id FROM task_reminders WHERE task_id = ? ORDER BY id DESC LIMIT 1", 1).Get(&taskReminderID)
	require.NoError(t, err)
	require.Error(t, DeleteStandaloneReminder(s, person, taskReminderID),
		"a reminder about a task is removed with the task, not through this route")

	require.NoError(t, DeleteStandaloneReminder(s, person, first.ID))
	_, total, err = GetStandaloneReminders(s, person, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "a removed reminder is gone from the list")

	// And a removed reminder does not fire.
	//
	// The words are matched in Go rather than in the query. The payload column holds JSON, and
	// Postgres — which is what production runs — has no text-matching operator for a JSON
	// column, so a query that pattern-matches it is refused outright rather than answering
	// wrongly. Reading the rows and looking at them here asks the same question on every
	// database this project supports.
	require.NoError(t, fireDueReminders(s, soon.Add(time.Minute), false))
	payloads := []string{}
	require.NoError(t, s.
		Table("notifications").
		Cols("notification").
		Where("notifiable_id = ? AND name = ?", person.ID, "task.reminder").
		Find(&payloads))
	// The task's own reminder was due at the same moment and was not removed, so something
	// must have fired. Without this the loop below could pass over nothing at all.
	require.NotEmpty(t, payloads, "the task's reminder was due and must have fired")
	for _, payload := range payloads {
		assert.NotContains(t, payload, "the sooner one",
			"a reminder that was removed must not fire")
	}
}

// A task's reminders are a field that replaces whole, and that is a data-loss hazard rather
// than a detail.
//
// Adding a reminder to a task means sending back every reminder the task already had, plus the
// new one. Anybody who sends only the new one deletes the rest, and nothing reports it: the
// person set a second reminder and quietly lost the first. This is the server-side half of
// that assertion — the table really does replace whole, so a caller really does have to read
// first. The desktop half, that the connector does that reading, is asserted on the other
// branch.
func TestBRA1571ATasksRemindersReplaceWholeSoACallerMustReadFirst(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	first := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	second := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	third := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)

	both := &Task{ID: 1, Reminders: []*TaskReminder{{Reminder: first}, {Reminder: second}}}
	require.NoError(t, both.Update(s, person))
	require.Equal(t, int64(2), countReminderRows(t, s,
		"SELECT COUNT(*) FROM task_reminders WHERE task_id = ?", 1))

	// A caller that reads first, adds, and writes all three keeps all three.
	existing := []*TaskReminder{}
	require.NoError(t, s.Where("task_id = ?", 1).OrderBy("reminder ASC").Find(&existing))
	require.Len(t, existing, 2)
	// Copied rather than appended in place. Appending to a slice read from the database and
	// naming the result something else leaves two names for one array, and whichever of them
	// the assertions below read would depend on whether the append had to grow it.
	all := make([]*TaskReminder, 0, len(existing)+1)
	all = append(all, existing...)
	all = append(all, &TaskReminder{Reminder: third})
	require.NoError(t, (&Task{ID: 1, Reminders: all}).Update(s, person))

	kept := []*TaskReminder{}
	require.NoError(t, s.Where("task_id = ?", 1).OrderBy("reminder ASC").Find(&kept))
	require.Len(t, kept, 3, "adding a third reminder must leave the first two where they were")
	assert.True(t, kept[0].Reminder.Equal(first))
	assert.True(t, kept[1].Reminder.Equal(second))
	assert.True(t, kept[2].Reminder.Equal(third))

	// Removing one means writing the remainder, and the remainder survives.
	remainder := []*TaskReminder{{Reminder: first}, {Reminder: third}}
	require.NoError(t, (&Task{ID: 1, Reminders: remainder}).Update(s, person))

	left := []*TaskReminder{}
	require.NoError(t, s.Where("task_id = ?", 1).OrderBy("reminder ASC").Find(&left))
	require.Len(t, left, 2, "removing one reminder must leave the other two minus the removed one")
	assert.True(t, left[0].Reminder.Equal(first))
	assert.True(t, left[1].Reminder.Equal(third))

	// And this is the failure the whole test exists for, asserted directly rather than left
	// as a warning in a comment: sending only the new reminder takes the others with it.
	require.NoError(t, (&Task{ID: 1, Reminders: []*TaskReminder{{Reminder: second}}}).Update(s, person))
	assert.Equal(t, int64(1), countReminderRows(t, s,
		"SELECT COUNT(*) FROM task_reminders WHERE task_id = ?", 1),
		"the field replaces whole, so a caller that does not read first loses the rest")
}

// Each of these refuses something rather than silently storing or dropping it.
func TestBRA1571ReminderCreationRefusesWhatCannotMeanAnything(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	t.Run("a reminder about nothing has no date to count a period from", func(t *testing.T) {
		// A relative period only means something beside a task's due, start or end date.
		// There is no such date here, so a period cannot be honoured — and storing it and
		// ignoring it would leave the person believing their reminder moves.
		moment := time.Now().UTC().Add(time.Hour)
		created, err := CreateStandaloneReminder(s, person, moment, "no period for me")
		require.NoError(t, err)

		assert.Zero(t, created.RelativePeriod,
			"a reminder about nothing must carry no relative period")
		assert.Empty(t, string(created.RelativeTo),
			"a reminder about nothing must count from nothing")
	})

	t.Run("removing a reminder that is not there is a failure, not a success", func(t *testing.T) {
		require.Error(t, DeleteStandaloneReminder(s, person, 987654),
			"removing a reminder that does not exist must report a failure")
	})
}

// A reminder that has already fired must not fire again because the task it is about was
// edited. Editing a task rewrites its reminder rows, and rewriting them could lose the stamp.
func TestBRA1571EditingATaskDoesNotRefireItsRemindersThatAlreadyWent(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	moment := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	task := &Task{ID: 1, Reminders: []*TaskReminder{{Reminder: moment}}}
	require.NoError(t, task.Update(s, person))

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"the reminder must fire once before this test can say anything about a second time")

	// Edit the task, keeping the same reminder. Its title changes; the reminder does not.
	edited := &Task{ID: 1, Title: "task #1 with a new title", Reminders: []*TaskReminder{{Reminder: moment}}}
	require.NoError(t, edited.Update(s, person))

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"editing a task must not make a reminder that already went off go off again")
}

// The stamp is what stops the second firing, and nothing else is.
//
// This is the third defect shape from the testing rules: a refusal that came from somewhere
// other than the guard the test believes it is exercising. Here the belief is checked
// directly — a reminder whose stamp is cleared, and nothing else changed, fires again.
func TestBRA1571ClearingTheStampMakesAReminderFireAgain(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	moment := time.Now().UTC().Add(-time.Hour)
	reminder, err := CreateStandaloneReminder(s, person, moment, "twice, if the stamp is gone")
	require.NoError(t, err)

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID))

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"the second pass is silent while the stamp is there")

	_, err = s.Exec("UPDATE task_reminders SET fired_at = NULL WHERE id = ?", reminder.ID)
	require.NoError(t, err)

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"with the stamp cleared the same reminder fires again, which is what proves the stamp is the thing stopping it")

	// The precondition for the assertion above, spelled out: the stamp really was set.
	stampFired(t, s, reminder.ID, time.Now())
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Equal(t, int64(2), reminderNotificationsFor(t, s, person.ID),
		"and putting the stamp back stops it again")
}
