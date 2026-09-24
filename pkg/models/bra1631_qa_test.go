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

// BRA-1631 independent QA: the task server's part.
//
// Written by the QA agent. Its criteria were fixed from the ticket's text before pull request 115's
// diff was read; the tests were then bound to the functions that pull request added. The ticket's
// words are used as it defines them and never for one another:
//
//   - reminder: a trigger held on the task server, which falls due at a time and performs the
//     actions ONE chose for it;
//   - action: what a reminder does when it falls due, from an open set of kinds;
//   - toast: the operating system's popup on the person's machine, which a client shows;
//   - notification: the record on the task server that appears in the bell on the task pages;
//   - checking: the periodic asking a client falls back to while its standing connection is down.
//
// Kinds are written as literals — "notification", "toast", "run-one" — and never through the
// package's own constants, because the kind is the contract with the clients that read it.
//
// Every test states which production line's removal makes it fail, because this repository's
// CLAUDE.md asks for that claim to be written where a reviewer can disprove it. Which of the
// removals were actually run, and where, is recorded in docs/RELEASE-QA-LEDGER.md in
// BraznMngo/one-apps.
//
// The fixture is asHostileAsProduction, from bra1571_reminders_test.go: mail configured, the
// person's email preference on, and no notification or reminder left over from the fixtures.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

// sebastiansExample is the reminder for ONE the ticket asks to be used wherever this is covered, in
// his words.
const sebastiansExample = "remind yourself to run the manual ritual x when an invoice comes in"

// runONE is the kind a reminder for ONE carries. The server does not know it by name, which is the
// point: it is stored for the client that performs it.
const runONE = "run-one"

// qaOtherPerson is user 2, who owns none of the reminders reminderTestUser sets.
func qaOtherPerson() *user.User {
	return &user.User{ID: 2, Username: "user2", Email: "user2@example.com"}
}

// qaSetReminder sets a reminder about nothing that fell due a minute ago.
func qaSetReminder(t *testing.T, s *xorm.Session, who *user.User, words string, actions ...ReminderAction) *TaskReminder {
	t.Helper()

	r, err := CreateStandaloneReminder(s, who, time.Now().UTC().Add(-time.Minute), words, actions...)
	require.NoError(t, err)
	return r
}

// qaDue is what checking reads for one person: each reminder that fell due and is not settled, with
// the kinds not done yet.
func qaDue(t *testing.T, s *xorm.Session, who *user.User) map[int64][]string {
	t.Helper()

	due, total, err := GetDueReminders(s, who, 50, 0)
	require.NoError(t, err)
	require.Len(t, due, int(total), "one page holds everything these tests set")
	listed := make(map[int64][]string, len(due))
	for _, r := range due {
		listed[r.ID] = r.Pending
	}
	return listed
}

// qaUnreadBell counts one person's unread reminder notifications, in raw SQL so that the count
// cannot agree with the code under test by construction.
func qaUnreadBell(t *testing.T, s *xorm.Session, who *user.User) int64 {
	t.Helper()

	return countReminderRows(t, s,
		"SELECT COUNT(*) FROM notifications WHERE notifiable_id = ? AND name = ? AND read_at IS NULL",
		who.ID, "task.reminder")
}

// qaSettled reports whether a reminder is settled, read straight from its row.
func qaSettled(t *testing.T, s *xorm.Session, id int64) bool {
	t.Helper()

	return countReminderRows(t, s,
		"SELECT COUNT(*) FROM task_reminders WHERE id = ? AND settled_at IS NOT NULL", id) == 1
}

// qaBellFor finds the notification a reminder wrote by the words it carries, in raw SQL, rather
// than by the subject the implementation records on it: a lookup through that subject would agree
// with the implementation whatever it recorded.
func qaBellFor(t *testing.T, s *xorm.Session, who *user.User, words string) *notifications.DatabaseNotification {
	t.Helper()

	var id int64
	has, err := s.SQL("SELECT id FROM notifications WHERE notifiable_id = ? AND name = ? AND notification LIKE ?",
		who.ID, "task.reminder", "%"+words+"%").Get(&id)
	require.NoError(t, err)
	require.True(t, has, "the reminder must have written its notification")

	n := &notifications.DatabaseNotification{}
	has, err = s.ID(id).Get(n)
	require.NoError(t, err)
	require.True(t, has)
	return n
}

// qaStoredActions reads a reminder's configuration from its row, as stored.
func qaStoredActions(t *testing.T, s *xorm.Session, id int64) string {
	t.Helper()

	var stored string
	has, err := s.SQL("SELECT actions FROM task_reminders WHERE id = ?", id).Get(&stored)
	require.NoError(t, err)
	require.True(t, has)
	return stored
}

// Story 13: "ONE sets a reminder whose only action is to run ONE. No notification is added to the
// bell on my task pages, because the task server performs only the actions a reminder is
// configured with. If the standing connection is down when it falls due, it still reaches ONE
// through checking."
//
// Mutation claim: dropping the reminderPerforms(..., "notification") condition around
// notifications.Notify in fireDueReminders writes a second notification, and the count fails.
func TestBRA1631Story13AReminderForONEAddsNothingToTheBell(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	forONE := qaSetReminder(t, s, person, sebastiansExample,
		ReminderAction{"kind": runONE, "instruction": sebastiansExample})
	// The control: a reminder that does add a notification, so the count is shown to move and a
	// pass that wrote nothing at all would fail too.
	qaSetReminder(t, s, person, "The bell's own reminder.", ReminderAction{"kind": "notification"})

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"only the reminder configured with a notification adds one to the bell")
	assert.Equal(t, int64(1), countReminderRows(t, s,
		"SELECT COUNT(*) FROM task_reminders WHERE id = ? AND fired_at IS NOT NULL", forONE.ID),
		"the reminder for ONE still falls due")
	assert.Contains(t, qaDue(t, s, person), forONE.ID,
		"and it reaches ONE through checking, which reads the reminders that fell due, not the bell")
}

// Story 12: "A reminder that falls due while [the standing connection] is down still reaches me,
// through checking … This holds for every kind of action, including a reminder that adds nothing
// to the bell." And BRA-1571 item 11: a reminder reaches only the person who set it.
//
// Mutation claims: dropping IsNull{"settled_at"} from unsettledRemindersOf lists the bell-only
// reminder; dropping Eq{"created_by_id"} lists the other person's; listing only reminders that
// wrote a notification — which is what reading the bell amounts to — loses the reminder for ONE.
func TestBRA1631Story12CheckingListsEveryReminderThatFellDueAndIsNotSettled(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()
	other := qaOtherPerson()

	forONE := qaSetReminder(t, s, person, sebastiansExample, ReminderAction{"kind": runONE})
	toast := qaSetReminder(t, s, person, "Call Klaus about the Acme invoice before noon.",
		ReminderAction{"kind": "toast"})
	unconfigured := qaSetReminder(t, s, person, "Water the plants.")
	bellOnly := qaSetReminder(t, s, person, "The bell alone.", ReminderAction{"kind": "notification"})
	later, err := CreateStandaloneReminder(s, person, time.Now().UTC().Add(time.Hour), "Not yet.",
		ReminderAction{"kind": runONE})
	require.NoError(t, err)
	theirs := qaSetReminder(t, s, other, "Somebody else's.", ReminderAction{"kind": runONE})

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	due := qaDue(t, s, person)
	assert.Equal(t, []string{runONE}, due[forONE.ID],
		"a reminder that adds nothing to the bell is listed all the same")
	assert.Equal(t, []string{"toast"}, due[toast.ID])
	assert.Equal(t, []string{"toast"}, due[unconfigured.ID],
		"a reminder with no configuration added its notification when it fell due, and its toast waits")
	assert.NotContains(t, due, bellOnly.ID, "a reminder whose one action the server performed is settled")
	assert.NotContains(t, due, later.ID, "a reminder that has not fallen due is not listed")
	assert.NotContains(t, due, theirs.ID, "a reminder reaches only the person who set it")
	assert.Len(t, due, 3)

	assert.Equal(t, map[int64][]string{theirs.ID: {runONE}}, qaDue(t, s, other),
		"and the other person's checking lists theirs, and nothing of anybody else's")
}

// K2 and story 7, the server's half: "a reminder whose action is to run ONE is settled once ONE has
// acted on it", "whether or not ONE tells the person anything", "and never picked up again".
//
// Mutation claim: making CompleteReminderAction skip the update after recordReminderDone leaves
// the reminder listed, and "never picked up again" fails.
func TestBRA1631K2AReminderForONEIsSettledOnceONEHasActedAndIsNeverPickedUpAgain(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	forONE := qaSetReminder(t, s, person, sebastiansExample,
		ReminderAction{"kind": runONE, "instruction": sebastiansExample})
	theirs := qaSetReminder(t, s, qaOtherPerson(), "Somebody else's.", ReminderAction{"kind": runONE})
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Contains(t, qaDue(t, s, person), forONE.ID, "fallen due, and waiting for ONE")

	// A turn that failed is not ONE having acted: nothing is recorded, the reminder waits, and a
	// later pass neither fires it a second time nor puts it in the bell.
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Contains(t, qaDue(t, s, person), forONE.ID)
	assert.Equal(t, int64(0), reminderNotificationsFor(t, s, person.ID))

	// Nobody settles another person's reminder.
	require.Error(t, CompleteReminderAction(s, person, theirs.ID, runONE))
	assert.Contains(t, qaDue(t, s, qaOtherPerson()), theirs.ID)

	// ONE acted, and told the person nothing: settled all the same. Settled is written to the
	// reminder's own row, read back here in raw SQL, which is what any client reads after it
	// restarts — nothing about it lives in the process that recorded it. (A second session cannot
	// stand in for a restart here: every session is a transaction, and would not see this one's.)
	require.NoError(t, CompleteReminderAction(s, person, forONE.ID, runONE))
	assert.True(t, qaSettled(t, s, forONE.ID))
	assert.NotContains(t, qaDue(t, s, person), forONE.ID, "never picked up again")

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.NotContains(t, qaDue(t, s, person), forONE.ID, "nor after another pass")
}

// The ticket's settling rule: "A reminder is settled once every action it carries is done. Its
// toast is done when the person has dealt with it; running ONE is done once ONE has acted. A
// reminder carrying both is settled only when both are done." Both orders.
//
// Mutation claim: settling a reminder once any one of its actions is done, rather than once
// reminderPending is empty, fails the first half-way assertion in each order.
func TestBRA1631AReminderCarryingAToastAndRunningONEIsSettledOnlyOnceBothAreDone(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	oneFirst := qaSetReminder(t, s, person, "Check whether the Acme invoice has cleared, and tell me.",
		ReminderAction{"kind": "toast"}, ReminderAction{"kind": runONE, "instruction": "check the Acme invoice"})
	toastFirst := qaSetReminder(t, s, person, "The same, the other way round.",
		ReminderAction{"kind": "toast"}, ReminderAction{"kind": runONE, "instruction": "check the Acme invoice"})
	require.NoError(t, fireDueReminders(s, time.Now(), false))

	require.NoError(t, CompleteReminderAction(s, person, oneFirst.ID, runONE))
	assert.False(t, qaSettled(t, s, oneFirst.ID), "ONE has acted, and the person has not dealt with the toast")
	assert.Equal(t, []string{"toast"}, qaDue(t, s, person)[oneFirst.ID])
	require.NoError(t, CompleteReminderAction(s, person, oneFirst.ID, "toast"))
	assert.True(t, qaSettled(t, s, oneFirst.ID))
	assert.NotContains(t, qaDue(t, s, person), oneFirst.ID)

	require.NoError(t, CompleteReminderAction(s, person, toastFirst.ID, "toast"))
	assert.False(t, qaSettled(t, s, toastFirst.ID), "the person has dealt with the toast, and ONE has not acted")
	assert.Equal(t, []string{runONE}, qaDue(t, s, person)[toastFirst.ID])
	require.NoError(t, CompleteReminderAction(s, person, toastFirst.ID, runONE))
	assert.True(t, qaSettled(t, s, toastFirst.ID))
	assert.NotContains(t, qaDue(t, s, person), toastFirst.ID)
}

// Story 2: "A reminder that only informs me shows Done: pressing it settles the reminder, the bell
// on my task pages goes down by one, and I am not reminded again after ONE restarts." And only that
// reminder.
//
// Mutation claim: removing the loop in CompleteReminderAction that reads the reminder's
// notification leaves the bell at two, and "goes down by one" fails.
func TestBRA1631Story2DoneOnTheToastSettlesItAndTakesTheBellDownByOne(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	pressed := qaSetReminder(t, s, person, "The Acme contract renews on Friday.")
	left := qaSetReminder(t, s, person, "A second reminder nobody has dealt with.")
	require.NoError(t, fireDueReminders(s, time.Now(), false))
	require.Equal(t, int64(2), qaUnreadBell(t, s, person))

	require.NoError(t, CompleteReminderAction(s, person, pressed.ID, "toast"))

	assert.Equal(t, int64(1), qaUnreadBell(t, s, person), "the bell goes down by one")
	assert.Equal(t, int64(1), countReminderRows(t, s,
		"SELECT COUNT(*) FROM notifications WHERE notifiable_id = ? AND name = ? AND read_at IS NULL AND notification LIKE ?",
		person.ID, "task.reminder", "%A second reminder nobody has dealt with.%"),
		"and the one still unread is the other reminder's")
	assert.True(t, qaSettled(t, s, pressed.ID))
	due := qaDue(t, s, person)
	assert.NotContains(t, due, pressed.ID, "not reminded again")
	assert.Contains(t, due, left.ID, "and only that reminder is settled")
}

// A toast and a notification in the bell tell the person the same thing, so the two never
// disagree about whether the person has dealt with a reminder: reading the notification on the
// task pages deals with the toast, marking it unread brings the toast back, and reading every
// notification at once deals with every toast — while a reminder that still has ONE to run keeps
// waiting for ONE. The notification is marked the way the notification routes mark one.
//
// Mutation claim: removing notifications.OnReadChanged(reminderToastsFollowTheBell) from the
// package's init leaves the reminder listed after its notification was read.
func TestBRA1631ReadingTheBellDealsWithTheToastAndMarkingItUnreadBringsItBack(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	paid := qaSetReminder(t, s, person, "Pay the Acme invoice.")
	both := qaSetReminder(t, s, person, "Check the Acme invoice and tell me.",
		ReminderAction{"kind": "notification"}, ReminderAction{"kind": "toast"}, ReminderAction{"kind": runONE})
	require.NoError(t, fireDueReminders(s, time.Now(), false))

	bell := qaBellFor(t, s, person, "Pay the Acme invoice.")
	require.NoError(t, (&DatabaseNotifications{DatabaseNotification: *bell, Read: true}).Update(s, person))
	assert.True(t, qaSettled(t, s, paid.ID), "read on the task pages, it does not come back as a toast")
	assert.NotContains(t, qaDue(t, s, person), paid.ID)

	require.NoError(t, (&DatabaseNotifications{DatabaseNotification: *bell, Read: false}).Update(s, person))
	assert.False(t, qaSettled(t, s, paid.ID))
	assert.Equal(t, []string{"toast"}, qaDue(t, s, person)[paid.ID], "marked unread, its toast waits again")

	require.NoError(t, notifications.MarkAllNotificationsAsRead(s, person.ID))
	due := qaDue(t, s, person)
	assert.NotContains(t, due, paid.ID)
	assert.Equal(t, []string{runONE}, due[both.ID],
		"reading the bell deals with the toast, and ONE still has to act")
}

// The open set: "Adding a kind must not change how a reminder is stored, how it falls due, or any
// existing kind", and reaching the person's phone "is not built now, and nothing built now may
// close it off". Judged by what a kind the server has never heard of has to touch — nothing: it is
// accepted, stored exactly as given, handed to clients exactly as given, and the kind the server
// does perform is performed as though it were not there. Under the ticket's settling rule, a kind
// nothing has done yet keeps the reminder waiting.
//
// Mutation claim: validating kinds against a closed list of those known today refuses "phone",
// and the first require fails.
func TestBRA1631OpenSetAKindTheServerHasNeverHeardOfIsKeptAndChangesNothingElse(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	r := qaSetReminder(t, s, person, "Leave for the airport.",
		ReminderAction{"kind": "notification"},
		ReminderAction{"kind": "phone", "device": "sebastians-phone", "sound": "soft"})
	_, err := CreateStandaloneReminder(s, person, time.Now().UTC().Add(time.Hour), "Any short lowercase name is a kind.",
		ReminderAction{"kind": "x-qa-1631.new"})
	require.NoError(t, err, "a kind nobody has built yet must be accepted without changing the server")

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID),
		"the kind the server performs is performed as though the other were not there")
	assert.JSONEq(t,
		`[{"kind":"notification"},{"kind":"phone","device":"sebastians-phone","sound":"soft"}]`,
		qaStoredActions(t, s, r.ID), "stored exactly as given")

	due, _, err := GetDueReminders(s, person, 50, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	handed, err := json.Marshal(due[0].Actions)
	require.NoError(t, err)
	assert.JSONEq(t,
		`[{"kind":"notification"},{"kind":"phone","device":"sebastians-phone","sound":"soft"}]`,
		string(handed), "handed to the client exactly as given")
	assert.Equal(t, []string{"phone"}, due[0].Pending,
		"settled once every action it carries is done, and nothing has done this one")
}

// Story 2 and the model: "A toast's buttons are part of the reminder's configuration … Any other
// button is set on the reminder by ONE, and opens the target its configuration names." The server
// stores each button and what it opens exactly as ONE gave them, and hands them to the client that
// shows the toast.
//
// Mutation claim: storing only each action's kind, and dropping the rest of the object, fails both
// JSON comparisons.
func TestBRA1631Story2AToastsButtonsAndWhatEachOpensAreStoredAsGiven(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	withButtons := ReminderAction{"kind": "toast", "buttons": []any{
		map[string]any{"label": "Open the offer", "opens": "file:///C:/Users/sebastian/Documents/offer-acme.pdf"},
		map[string]any{"label": "Open the task", "opens": "https://tasks.brazn.one/tasks/42"},
	}}
	r := qaSetReminder(t, s, person, "Send Klaus the signed offer today.", withButtons)
	require.NoError(t, fireDueReminders(s, time.Now(), false))

	const asGiven = `[{"kind":"toast","buttons":[` +
		`{"label":"Open the offer","opens":"file:///C:/Users/sebastian/Documents/offer-acme.pdf"},` +
		`{"label":"Open the task","opens":"https://tasks.brazn.one/tasks/42"}]}]`
	assert.JSONEq(t, asGiven, qaStoredActions(t, s, r.ID))

	due, _, err := GetDueReminders(s, person, 50, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	handed, err := json.Marshal(due[0].Actions)
	require.NoError(t, err)
	assert.JSONEq(t, asGiven, string(handed))
}

// Sebastian, 24 September 2026: reminders which already exist keep exactly today's behaviour once
// actions are stored — a toast carrying their words, with Done — and today every reminder also
// adds a notification to the bell. The row is put into exactly the state a row set before the
// migration is in: no configuration at all.
//
// Mutation claim: dropping either kind from defaultReminderActions fails the notification count
// or the pending toast.
func TestBRA1631AReminderSetBeforeRemindersCarriedActionsKeepsTodaysBehaviour(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	r := qaSetReminder(t, s, person, "Water the plants.")
	_, err := s.Exec("UPDATE task_reminders SET actions = NULL, done_actions = NULL, settled_at = NULL WHERE id = ?", r.ID)
	require.NoError(t, err)

	require.NoError(t, fireDueReminders(s, time.Now(), false))

	assert.Equal(t, int64(1), reminderNotificationsFor(t, s, person.ID), "its notification in the bell, as today")
	due, _, err := GetDueReminders(s, person, 50, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "Water the plants.", due[0].Text, "a toast carrying its own words")
	assert.Equal(t, []string{"toast"}, due[0].Pending, "waiting for the person to deal with it")

	require.NoError(t, CompleteReminderAction(s, person, r.ID, "toast"))
	assert.True(t, qaSettled(t, s, r.ID), "and Done settles it")
}

// "It stores each reminder's configuration." A reminder on a task keeps what ONE set it to do when
// the task is saved without saying — as the task pages save a task whose reminder they did not
// change, and as any client does that rewrites a task's reminders without knowing about actions.
//
// Mutation claim: removing the carry-over (does = actions[reminderIdentity(r)]) in updateReminders
// resets the reminder to no configuration, and the JSON comparison fails.
func TestBRA1631SavingATaskWithoutActionsKeepsWhatItsRemindersDo(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()

	moment := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	forONE := []ReminderAction{{"kind": runONE, "instruction": sebastiansExample}}
	require.NoError(t, (&Task{ID: 1, Reminders: []*TaskReminder{{Reminder: moment, Actions: forONE}}}).Update(s, person))

	require.NoError(t, (&Task{ID: 1, Title: "task #1, renamed", Reminders: []*TaskReminder{{Reminder: moment}}}).Update(s, person))

	var id int64
	has, err := s.SQL("SELECT id FROM task_reminders WHERE task_id = ?", 1).Get(&id)
	require.NoError(t, err)
	require.True(t, has)
	assert.JSONEq(t, `[{"kind":"run-one","instruction":"`+sebastiansExample+`"}]`, qaStoredActions(t, s, id))
}

// Story 11, the server's half: "Its toast appears within seconds, because the desktop hears it over
// its standing connection rather than at its next check." A reminder left waiting for a client is
// announced to the person it belongs to as reminder.due, carrying its words and what it does — and
// only once the pass that fired it is committed, so a client that settles it at once finds it
// fallen due. A reminder whose one action the server already performed is settled and not
// announced.
//
// Mutation claim: removing the events.DispatchOnCommit(s, &ReminderDueEvent{...}) call leaves
// nothing announced; dispatching it before the commit, with events.Dispatch, fails the first
// assertion.
func TestBRA1631Story11AReminderThatFellDueIsAnnouncedToItsPersonOnceItsPassIsCommitted(t *testing.T) {
	s := asHostileAsProduction(t)
	person := reminderTestUser()
	events.ClearDispatchedEvents()

	forONE := qaSetReminder(t, s, person, sebastiansExample,
		ReminderAction{"kind": runONE, "instruction": sebastiansExample})
	qaSetReminder(t, s, person, "The bell alone.", ReminderAction{"kind": "notification"})

	require.NoError(t, fireDueReminders(s, time.Now(), false))
	assert.Empty(t, events.GetDispatchedEvents("reminder.due"),
		"nothing is announced before the pass that fired it is committed")

	events.DispatchPending(context.Background(), s)
	announced := events.GetDispatchedEvents("reminder.due")
	require.Len(t, announced, 1, "only the reminder left waiting for a client is announced")
	due, ok := announced[0].(*ReminderDueEvent)
	require.True(t, ok)
	assert.Equal(t, person.ID, due.UserID, "to the person it belongs to")
	require.NotNil(t, due.Reminder)
	assert.Equal(t, forONE.ID, due.Reminder.ID)
	assert.Equal(t, sebastiansExample, due.Reminder.Text)
	assert.Equal(t, []string{runONE}, due.Reminder.Pending)
}
