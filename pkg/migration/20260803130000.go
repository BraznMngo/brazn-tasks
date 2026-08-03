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

// The commercial team a Team root was provisioned for (BRA-1050).
//
// create_team_roots has to be idempotent on the team it names, and this is the
// only column that can answer "have I already provisioned THIS team". The
// alternative key - "does this organization have a Team root" - would answer a
// second team's call with the first team's references, and the commercial
// record coalesces a team's pair in exactly once, so that would be permanent.
//
// Existing rows default to the empty string, which is not an id the contract
// can express (^[A-Za-z0-9_-]{1,64}$), so no lookup ever matches one. That is
// correct rather than a gap: a root this fork created for itself was never
// provisioned by the commercial service and has no commercial team to be
// matched against. Managed mode has never been switched on anywhere, so in
// practice there are no rows at all.
type braznProtectedEntities20260803130000 struct {
	ID               int64     `xorm:"bigint autoincr not null unique pk"`
	Kind             string    `xorm:"varchar(20) not null INDEX"`
	ProjectID        int64     `xorm:"bigint not null default 0 INDEX"`
	TeamID           int64     `xorm:"bigint not null default 0 INDEX"`
	OrganizationID   string    `xorm:"varchar(64) not null default '' INDEX"`
	CommercialTeamID string    `xorm:"varchar(64) not null default '' INDEX"`
	Created          time.Time `xorm:"created not null"`
}

func (braznProtectedEntities20260803130000) TableName() string {
	return "brazn_protected_entities"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260803130000",
		Description: "record which commercial team a Team root was provisioned for, so provisioning it twice is idempotent",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Sync(braznProtectedEntities20260803130000{})
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
