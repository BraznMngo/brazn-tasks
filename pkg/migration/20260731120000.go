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

type braznProtectedEntities20260731120000 struct {
	ID        int64     `xorm:"bigint autoincr not null unique pk"`
	Kind      string    `xorm:"varchar(20) not null INDEX"`
	ProjectID int64     `xorm:"bigint not null default 0 INDEX"`
	TeamID    int64     `xorm:"bigint not null default 0 INDEX"`
	Created   time.Time `xorm:"created not null"`
}

func (braznProtectedEntities20260731120000) TableName() string {
	return "brazn_protected_entities"
}

type braznEntitlementProjections20260731120000 struct {
	ID       int64     `xorm:"bigint autoincr not null unique pk"`
	UserID   int64     `xorm:"bigint not null unique"`
	Revision int64     `xorm:"bigint not null"`
	Envelope string    `xorm:"text not null"`
	Created  time.Time `xorm:"created not null"`
	Updated  time.Time `xorm:"updated not null"`
}

func (braznEntitlementProjections20260731120000) TableName() string {
	return "brazn_entitlement_projections"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260731120000",
		Description: "add the Brazn managed-mode tables: protected topology entities and entitlement projections",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Sync(
				braznProtectedEntities20260731120000{},
				braznEntitlementProjections20260731120000{},
			)
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
