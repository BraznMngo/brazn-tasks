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

// The entitlement subject is the (organization_id, user_id) pair, and until now
// only half of it was stored. The apply rule in models.ApplyEntitlement decides
// on the organization inside its UPDATE statement, so it has to be a column: a
// projection for a second organization must lose the compare-and-set rather
// than be caught by a check made before it.
//
// Existing rows default to the empty string, which matches no organization the
// contract can express, so any row written before this migration stops
// accepting deliveries and says so. That is the safe direction and it costs
// nothing in practice: managed mode has never been switched on, because until
// BRA-913 there was no endpoint that could write one of these rows at all.
type braznEntitlementProjections20260801120000 struct {
	ID             int64     `xorm:"bigint autoincr not null unique pk"`
	UserID         int64     `xorm:"bigint not null unique"`
	OrganizationID string    `xorm:"varchar(64) not null default ''"`
	Revision       int64     `xorm:"bigint not null"`
	Envelope       string    `xorm:"text not null"`
	Created        time.Time `xorm:"created not null"`
	Updated        time.Time `xorm:"updated not null"`
}

func (braznEntitlementProjections20260801120000) TableName() string {
	return "brazn_entitlement_projections"
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260801120000",
		Description: "record the organization half of the entitlement subject on each projection",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Sync(braznEntitlementProjections20260801120000{})
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
