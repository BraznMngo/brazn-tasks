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

// The two claims that make Percy Feedback provisioning an atomic step
// (BRA-1414 follow-up), the same reason brazn_provisioned_users exists for
// mailbox claims: ensureFeedbackProject/ensureFeedbackSubProject were
// check-then-insert with nothing for a concurrent second call to conflict
// against, so two callers who both read "not provisioned yet" could each
// create a root or a reporter's sub-project of their own.
//
// brazn_feedback_root has exactly one row, ever, so its unique column
// (Marker) carries a fixed value rather than a natural one - there is nothing
// else to make it unique against. brazn_feedback_reporters has one row per
// reporter, keyed by user id, which is what a second concurrent call for the
// SAME reporter collides on.
//
// NEITHER TABLE'S UNIQUE COLUMN IS ITS PRIMARY KEY, matching
// brazn_provisioned_users' own Email (not its autoincrement ID) rather than
// ProtectedEntity. MySQL and MariaDB name a duplicate-PRIMARY-KEY violation
// only "for key 'PRIMARY'" - db.IsUniqueConstraintError's MySQL branch
// requires the table name to appear, which a PRIMARY KEY violation never
// carries, only a separately named unique index's does.
//
// Nothing is backfilled. An instance with feedback already provisioned before
// this migration has a root and reporter sub-projects with no claim row; the
// first call after upgrading takes the claim then, which is safe because
// ensureFeedbackProject/ensureFeedbackSubProject read for an existing project
// before ever attempting to claim one.
type braznFeedbackRoot20260822140000 struct {
	ID        int64     `xorm:"bigint autoincr not null unique pk"`
	Marker    int64     `xorm:"bigint not null unique"`
	ProjectID int64     `xorm:"bigint not null default 0"`
	Created   time.Time `xorm:"created not null"`
}

func (braznFeedbackRoot20260822140000) TableName() string {
	return "brazn_feedback_root"
}

type braznFeedbackReporters20260822140000 struct {
	ID        int64     `xorm:"bigint autoincr not null unique pk"`
	UserID    int64     `xorm:"bigint not null unique"`
	ProjectID int64     `xorm:"bigint not null default 0"`
	Created   time.Time `xorm:"created not null"`
}

func (braznFeedbackReporters20260822140000) TableName() string {
	return "brazn_feedback_reporters"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260822140000",
		Description: "atomic claims for Percy Feedback's root project and each reporter's sub-project, closing a provisioning race",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Sync(braznFeedbackRoot20260822140000{}); err != nil {
				return err
			}
			return tx.Sync(braznFeedbackReporters20260822140000{})
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
