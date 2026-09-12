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

import (
	"time"

	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

// A reminder can now stand on its own, and it records that it has fired (BRA-1571).
//
// task_id keeps its not-null constraint and carries 0 for a reminder that is
// about nothing, so this migration only adds columns. Relaxing that constraint
// would mean rebuilding the table on SQLite, on live data, for a discriminator
// subject_kind already provides.
//
// fired_at is backfilled for every reminder whose moment has already passed,
// because the sweep now selects anything due and unfired rather than only the
// coming minute. Without the backfill, the first sweep after this migration
// would treat every reminder anybody ever set as one that still has to fire.
type taskReminders20260912120000 struct {
	ID             int64     `xorm:"bigint autoincr not null unique pk"`
	TaskID         int64     `xorm:"bigint not null INDEX"`
	Reminder       time.Time `xorm:"DATETIME not null INDEX 'reminder'"`
	Created        time.Time `xorm:"created not null"`
	RelativePeriod int64     `xorm:"bigint null"`
	RelativeTo     string    `xorm:"varchar(50) null"`
	SubjectKind    string    `xorm:"varchar(20) null"`
	Text           string    `xorm:"'reminder_text' longtext null"`
	CreatedByID    int64     `xorm:"bigint null"`
	FiredAt        time.Time `xorm:"datetime null"`
}

func (taskReminders20260912120000) TableName() string {
	return "task_reminders"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260912120000",
		Description: "let a reminder stand on its own and record that it fired",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Sync(taskReminders20260912120000{}); err != nil {
				return err
			}

			if _, err := tx.Exec("UPDATE task_reminders SET subject_kind = ? WHERE subject_kind IS NULL", "task"); err != nil {
				return err
			}

			// UTC, because this compares against a stored DATETIME rather than
			// going through the ORM, and the ORM stores every datetime column in
			// UTC. Getting the zone wrong here would stamp a reminder that is
			// still to come as already fired, and it would never arrive.
			now := time.Now().UTC().Format("2006-01-02 15:04:05")
			_, err := tx.Exec("UPDATE task_reminders SET fired_at = reminder WHERE fired_at IS NULL AND reminder < ?", now)
			return err
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
