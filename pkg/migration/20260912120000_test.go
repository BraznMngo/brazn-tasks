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

// BRA-1571's migration backfills a fired stamp onto every reminder whose moment has already
// passed, because the sweep it ships alongside selects everything due and unfired rather than
// only the coming minute. Without that backfill the first pass after deployment would fire
// every reminder anybody ever set.
//
// The boundary has to be right in both directions, and the two directions fail very
// differently:
//
//   - Stamp too much, and a reminder that is still to come never arrives. Nothing reports it;
//     the person simply is not told, and the record says it already happened.
//   - Stamp too little, and reminders that went by long ago all fire at once on the first pass.
//     Noisy, visible, and self-correcting after that pass.
//
// So the test that matters is the first direction, and it is a time-zone test. The cutoff is
// formatted text compared against a stored DATETIME, and the engine stores every datetime
// column in UTC. Formatting the cutoff in the *service's* configured zone instead — which is a
// different setting, and was what this migration did when it was first written — moves the
// cutoff by that zone's offset. East of UTC that stamps everything due within the offset as
// already fired.
//
// This test therefore runs with the service timezone set well east of UTC, which is the
// configuration under which a cutoff in the wrong zone is wrong by hours.

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

// reminderRow20260912120000 reads the table after the migration has shaped it. FiredAt is a
// pointer so that "no stamp" and "the zero instant" stay distinguishable; a plain time.Time
// would read a NULL as the zero value and the two cases would look identical.
type reminderRow20260912120000 struct {
	ID          int64      `xorm:"bigint autoincr not null unique pk"`
	TaskID      int64      `xorm:"bigint not null INDEX"`
	Reminder    time.Time  `xorm:"DATETIME not null INDEX 'reminder'"`
	Created     time.Time  `xorm:"created not null"`
	SubjectKind string     `xorm:"varchar(20) null"`
	FiredAt     *time.Time `xorm:"datetime null"`
}

func (reminderRow20260912120000) TableName() string {
	return "task_reminders"
}

// migration20260912120000 finds the migration in the registered list, rather than calling a
// copy of its body. If it were not registered, nothing would run it on a deployment, and this
// is the assertion that says so.
func migration20260912120000(t *testing.T) *xormigrate.Migration {
	t.Helper()

	for _, m := range migrations {
		if m.ID == "20260912120000" {
			return m
		}
	}
	t.Fatal("migration 20260912120000 is not registered, so nothing would run it on a deployment")
	return nil
}

func TestBRA1571BackfillStampsOnlyWhatIsAlreadyPast(t *testing.T) {
	// A service timezone well east of UTC. The engine still stores UTC; only the service
	// setting moves. A cutoff formatted in this zone would sit nine hours in the future.
	config.InitDefaultConfig()
	previousZone := config.ServiceTimeZone.GetString()
	t.Cleanup(func() { config.ServiceTimeZone.Set(previousZone) })
	config.ServiceTimeZone.Set("Asia/Tokyo")

	engine, err := xorm.NewEngine("sqlite3", "file:bra1571migration?mode=memory&cache=shared&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })

	// The production engine sets both of these. Setting them here is what makes the stored
	// bytes the same bytes a deployment would hold; without the database zone the rows would
	// be written in the host's own zone and this test would measure the host instead of the
	// migration.
	gmt, err := time.LoadLocation("GMT")
	require.NoError(t, err)
	engine.SetMapper(names.GonicMapper{})
	engine.SetTZLocation(config.GetTimeZone())
	engine.SetTZDatabase(gmt)

	run := migration20260912120000(t)

	// First pass shapes the table.
	require.NoError(t, run.Migrate(engine))

	// Three moments either side of the boundary, written through the engine so they are
	// stored exactly as the application stores them.
	now := time.Now().UTC()
	moments := map[string]time.Time{
		"an hour ago":      now.Add(-time.Hour),
		"a minute ago":     now.Add(-time.Minute),
		"in a minute":      now.Add(time.Minute),
		"in three hours":   now.Add(3 * time.Hour),
		"in fifteen hours": now.Add(15 * time.Hour),
	}
	// Written as raw text, in UTC, with subject_kind left NULL. That is what a row created
	// before this change actually looks like, and it is the state the backfill has to cope
	// with: inserting through the ORM would write an empty string instead of NULL, and the
	// backfill's "give every old reminder a subject" statement matches NULL. A fixture that
	// wrote the empty string would report that statement as broken when it is not — and,
	// worse, a fixture that wrote the subject itself would report it as working when it was.
	// The datetime text is UTC because this engine's database zone is set to GMT above,
	// exactly as a deployment sets it.
	ids := map[string]int64{}
	for name, moment := range moments {
		_, err := engine.Exec(
			"INSERT INTO task_reminders (task_id, reminder, created) VALUES (?, ?, ?)",
			1, moment.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
		require.NoError(t, err, name)

		var id int64
		_, err = engine.SQL("SELECT MAX(id) FROM task_reminders").Get(&id)
		require.NoError(t, err)
		ids[name] = id
	}

	// Second pass runs the backfill over rows that already exist, which is the state a real
	// deployment is in.
	require.NoError(t, run.Migrate(engine))

	stamped := map[int64]bool{}
	rows := []*reminderRow20260912120000{}
	require.NoError(t, engine.Find(&rows))
	require.Len(t, rows, len(moments))
	for _, row := range rows {
		stamped[row.ID] = row.FiredAt != nil
		// Every row also gets a subject, or the sweep's "about nothing" branch would
		// treat an ordinary task reminder as a reminder about nothing and skip the
		// done-or-deleted filter for it.
		assert.Equal(t, "task", row.SubjectKind,
			"a reminder that existed before this change is a reminder about a task")
	}

	// Nothing still to come may be stamped. This is the direction that loses a reminder in
	// silence, and the direction a cutoff in the wrong zone gets wrong.
	for _, name := range []string{"in a minute", "in three hours", "in fifteen hours"} {
		assert.False(t, stamped[ids[name]],
			"a reminder due %s must not be stamped as already fired", name)
	}

	// And nothing already past may be left to fire, or the first pass after deployment
	// sends it.
	for _, name := range []string{"an hour ago", "a minute ago"} {
		assert.True(t, stamped[ids[name]],
			"a reminder that was due %s must be stamped, or it fires on the first pass", name)
	}
}
