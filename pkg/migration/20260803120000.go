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

// The mailbox-to-user map the provisioning channel needs (BRA-1018), and the
// unique index that makes creating a user for a mailbox an atomic step.
//
// The index is the whole reason the table exists. users.email carries no unique
// constraint and cannot be given one - bot users all hold the empty string - so
// without this there is nothing for an insert to conflict against, and
// "create the user for this mailbox or return the existing one" degrades into a
// lookup followed by an insert whose loser mints a second user for one mailbox.
// See models.ProvisionedUser.
//
// Nothing is backfilled. Existing accounts have no claim row, which is correct:
// they were not provisioned, and the first call for one of their mailboxes
// adopts them rather than creating a second user. BRA-1021 owns what happens to
// them when managed mode is switched on.
type braznProvisionedUsers20260803120000 struct {
	ID      int64     `xorm:"bigint autoincr not null unique pk"`
	Email   string    `xorm:"varchar(250) not null unique"`
	UserID  int64     `xorm:"bigint not null default 0"`
	Created time.Time `xorm:"created not null"`
}

func (braznProvisionedUsers20260803120000) TableName() string {
	return "brazn_provisioned_users"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260803120000",
		Description: "record which Brazn Tasks user was provisioned for which mailbox, keyed uniquely by the mailbox",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Sync(braznProvisionedUsers20260803120000{})
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
