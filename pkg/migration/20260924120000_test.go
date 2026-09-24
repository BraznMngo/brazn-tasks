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

package migration

// BRA-1631 independent QA: migration 20260924120000 on data shaped like a deployment's.
//
// Written by the QA agent. The migration's own pull request lists "the migration against real
// data" as not verified: CI's smoke test runs it on an empty database, and it links each reminder
// that already fired to the notification it wrote by a heuristic — the same owner, the same task or
// the same words, written within fifteen minutes of the firing. This runs it over reminders and
// notifications in the shapes a deployment holds, including the heuristic's edges, and asserts
// what each person is left with, from Sebastian's rule of 24 September 2026 that reminders which
// already exist keep exactly today's behaviour: a reminder whose notification was read is settled
// and does not come back; one whose notification is unread still has its toast waiting; one whose
// notification cannot be found is settled rather than put back in front of anybody.
//
// Rows are written through the engine, configured the way the production engine is, so the stored
// bytes are a deployment's. The pre-migration structs give "created" as a plain datetime rather
// than xorm's created tag, which would overwrite it, so that each notification sits at the moment
// the case needs.

import (
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
	"xorm.io/xorm/names"

	_ "github.com/mattn/go-sqlite3"
)

type reminderBefore20260924120000 struct {
	ID             int64      `xorm:"bigint autoincr not null unique pk"`
	TaskID         int64      `xorm:"bigint not null INDEX"`
	Reminder       time.Time  `xorm:"DATETIME not null INDEX 'reminder'"`
	Created        time.Time  `xorm:"datetime not null 'created'"`
	RelativePeriod int64      `xorm:"bigint null"`
	RelativeTo     string     `xorm:"varchar(50) null"`
	SubjectKind    string     `xorm:"varchar(20) null"`
	Text           string     `xorm:"'reminder_text' longtext null"`
	CreatedByID    int64      `xorm:"bigint null"`
	FiredAt        *time.Time `xorm:"datetime null"`
}

func (reminderBefore20260924120000) TableName() string {
	return "task_reminders"
}

type notificationBefore20260924120000 struct {
	ID           int64      `xorm:"bigint autoincr not null unique pk"`
	NotifiableID int64      `xorm:"bigint not null"`
	Notification string     `xorm:"'notification' longtext not null"`
	Name         string     `xorm:"varchar(250) index not null"`
	SubjectID    int64      `xorm:"bigint null"`
	ReadAt       *time.Time `xorm:"datetime null"`
	Created      time.Time  `xorm:"datetime not null 'created'"`
}

func (notificationBefore20260924120000) TableName() string {
	return "notifications"
}

// reminderAfter20260924120000 reads what the migration left. Pointers keep "no value" and "the zero
// instant" apart, which a plain time.Time would not.
type reminderAfter20260924120000 struct {
	ID          int64      `xorm:"bigint autoincr not null unique pk"`
	FiredAt     *time.Time `xorm:"datetime null"`
	DoneActions []string   `xorm:"json null"`
	SettledAt   *time.Time `xorm:"datetime null"`
}

func (reminderAfter20260924120000) TableName() string {
	return "task_reminders"
}

func migration20260924120000(t *testing.T) *xormigrate.Migration {
	t.Helper()

	for _, m := range migrations {
		if m.ID == "20260924120000" {
			return m
		}
	}
	t.Fatal("migration 20260924120000 is not registered, so nothing would run it on a deployment")
	return nil
}

// Mutation claims, one per case: widening the fifteen-minute window to an hour links "far away" to
// the notification written twenty minutes after it; dropping the owner from the match lets "call
// the garage" take the other person's notification, written closer to its firing; matching on words
// alone for a reminder about a task leaves "renew the lease" unlinked; settling a reminder whose
// notification is unread puts nothing back in front of the person, and fails "call the garage";
// and not settling one whose notification cannot be found fails "nobody wrote me down".
func TestBRA1631MigrationLeavesEachReminderWhereItsNotificationSaysThePersonIs(t *testing.T) {
	// A service timezone well east of UTC, as the BRA-1571 migration's test uses: the engine still
	// stores UTC, and a comparison made in the wrong zone would be wrong by hours here.
	config.InitDefaultConfig()
	previousZone := config.ServiceTimeZone.GetString()
	t.Cleanup(func() { config.ServiceTimeZone.Set(previousZone) })
	config.ServiceTimeZone.Set("Asia/Tokyo")

	engine, err := xorm.NewEngine("sqlite3", "file:bra1631migration?mode=memory&cache=shared&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	gmt, err := time.LoadLocation("GMT")
	require.NoError(t, err)
	engine.SetMapper(names.GonicMapper{})
	engine.SetTZLocation(config.GetTimeZone())
	engine.SetTZDatabase(gmt)
	require.NoError(t, engine.Sync(reminderBefore20260924120000{}, notificationBefore20260924120000{}))

	fired := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	at := func(d time.Duration) *time.Time {
		moment := fired.Add(d)
		return &moment
	}
	// Every reminder here is user 1's. Only a notification carries another owner, which is the
	// case that proves a reminder is never matched to somebody else's.
	remind := func(task int64, words string, firedAt *time.Time) int64 {
		kind := "none"
		if task != 0 {
			kind = "task"
		}
		r := &reminderBefore20260924120000{
			TaskID: task, Reminder: fired.Add(-time.Minute), Created: fired.Add(-24 * time.Hour),
			SubjectKind: kind, Text: words, CreatedByID: 1, FiredAt: firedAt,
		}
		_, err := engine.Insert(r)
		require.NoError(t, err)
		return r.ID
	}
	notify := func(owner int64, written string, created time.Duration, readAt *time.Time) int64 {
		n := &notificationBefore20260924120000{
			NotifiableID: owner, Notification: written, Name: "task.reminder",
			Created: fired.Add(created), ReadAt: readAt,
		}
		_, err := engine.Insert(n)
		require.NoError(t, err)
		return n.ID
	}

	readOne := remind(0, "Water the plants.", at(0))
	readOneBell := notify(1, `{"text":"Water the plants."}`, 5*time.Second, at(time.Hour))

	unread := remind(0, "Call the garage.", at(0))
	unreadBell := notify(1, `{"text":"Call the garage."}`, 3*time.Second, nil)
	theirBell := notify(2, `{"text":"Call the garage."}`, time.Second, nil)

	unwritten := remind(0, "Nobody wrote me down.", at(0))

	onATask := remind(5, "", at(0))
	onATaskBell := notify(1, `{"task":{"id":5,"title":"Renew the lease"}}`, 2*time.Second, nil)

	sameWordsLater := remind(0, "Water the plants.", at(2*time.Hour))
	sameWordsLaterBell := notify(1, `{"text":"Water the plants."}`, 2*time.Hour+time.Second, nil)

	farAway := remind(0, "Far away.", at(0))
	farAwayBell := notify(1, `{"text":"Far away."}`, 20*time.Minute, nil)

	stillToCome := remind(0, "Still to come.", nil)

	require.NoError(t, migration20260924120000(t).Migrate(engine))

	// Each takes the t of the subtest that calls it, so a failure stops that subtest.
	after := func(t *testing.T, id int64) *reminderAfter20260924120000 {
		r := &reminderAfter20260924120000{}
		has, err := engine.ID(id).Get(r)
		require.NoError(t, err)
		require.True(t, has)
		return r
	}
	subjectOf := func(t *testing.T, id int64) int64 {
		n := &notificationBefore20260924120000{}
		has, err := engine.ID(id).Get(n)
		require.NoError(t, err)
		require.True(t, has)
		return n.SubjectID
	}

	t.Run("water the plants: read in the bell, so settled when it was read", func(t *testing.T) {
		r := after(t, readOne)
		require.NotNil(t, r.SettledAt)
		assert.WithinDuration(t, fired.Add(time.Hour), *r.SettledAt, time.Second)
		assert.ElementsMatch(t, []string{"notification", "toast"}, r.DoneActions)
		assert.Equal(t, readOne, subjectOf(t, readOneBell))
	})
	t.Run("call the garage: unread, so its toast is still waiting, and only its own person's notification is its", func(t *testing.T) {
		r := after(t, unread)
		assert.Nil(t, r.SettledAt)
		assert.Equal(t, []string{"notification"}, r.DoneActions)
		assert.Equal(t, unread, subjectOf(t, unreadBell))
		assert.Zero(t, subjectOf(t, theirBell), "another person's notification is never this reminder's")
	})
	t.Run("nobody wrote me down: no notification to be found, so settled when it fired", func(t *testing.T) {
		r := after(t, unwritten)
		require.NotNil(t, r.SettledAt)
		assert.WithinDuration(t, fired, *r.SettledAt, time.Second)
	})
	t.Run("renew the lease: a reminder about a task is found by its task", func(t *testing.T) {
		r := after(t, onATask)
		assert.Nil(t, r.SettledAt)
		assert.Equal(t, onATask, subjectOf(t, onATaskBell))
	})
	t.Run("water the plants, again: the same words two hours later take their own notification", func(t *testing.T) {
		r := after(t, sameWordsLater)
		assert.Nil(t, r.SettledAt)
		assert.Equal(t, sameWordsLater, subjectOf(t, sameWordsLaterBell))
		assert.Equal(t, readOne, subjectOf(t, readOneBell), "and the earlier one keeps its own")
	})
	t.Run("far away: a notification twenty minutes off is not this reminder's", func(t *testing.T) {
		r := after(t, farAway)
		require.NotNil(t, r.SettledAt)
		assert.WithinDuration(t, fired, *r.SettledAt, time.Second)
		assert.Zero(t, subjectOf(t, farAwayBell))
	})
	t.Run("still to come: a reminder that has not fired is left alone", func(t *testing.T) {
		r := after(t, stillToCome)
		assert.Nil(t, r.FiredAt)
		assert.Nil(t, r.SettledAt)
		assert.Empty(t, r.DoneActions)
	})
}
