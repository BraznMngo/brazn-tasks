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

// The organization a protected entity belongs to (BRA-917).
//
// The seat rule is `purchased seats >= 3 x active teams`, and the second half
// of it is a count of ONE organization's Team roots. Without this column that
// count cannot be taken at all: teams are global in Vikunja, and a protected
// entity recorded only the project and the team.
//
// Existing rows default to the empty string, which is not an organization the
// contract can express - an organization id is constrained to
// ^[A-Za-z0-9_-]{1,64}$ - so an unattributed root counts towards nobody's
// capacity rather than towards everybody's. That is the safe direction for a
// rule that refuses: a root whose organization is unknown cannot be used to
// justify a creation, and it cannot inflate another organization's count
// either. Managed mode has never been switched on anywhere, so in practice
// there are no such rows.
type braznProtectedEntities20260802120000 struct {
	ID             int64     `xorm:"bigint autoincr not null unique pk"`
	Kind           string    `xorm:"varchar(20) not null INDEX"`
	ProjectID      int64     `xorm:"bigint not null default 0 INDEX"`
	TeamID         int64     `xorm:"bigint not null default 0 INDEX"`
	OrganizationID string    `xorm:"varchar(64) not null default '' INDEX"`
	Created        time.Time `xorm:"created not null"`
}

func (braznProtectedEntities20260802120000) TableName() string {
	return "brazn_protected_entities"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260802120000",
		Description: "record which organization a protected entity belongs to, so one organization's teams can be counted",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Sync(braznProtectedEntities20260802120000{})
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
