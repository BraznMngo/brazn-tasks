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
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

// Every reminder now reaches the person it belongs to and nobody else (BRA-1571), and a
// reminder about a task did not record who that was. Existing ones are given the creator of
// the task they are about: it is the only owner the rows carry evidence for, and leaving them
// with nobody would silently stop reminders people are relying on today.
func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260913120000",
		Description: "give every reminder about a task the person it belongs to",
		Migrate: func(tx *xorm.Engine) error {
			_, err := tx.Exec(`UPDATE task_reminders
SET created_by_id = (SELECT tasks.created_by_id FROM tasks WHERE tasks.id = task_reminders.task_id)
WHERE task_id <> 0 AND (created_by_id IS NULL OR created_by_id = 0)`)
			return err
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
