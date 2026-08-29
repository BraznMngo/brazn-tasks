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

package user

import (
	"strings"

	"code.vikunja.io/api/pkg/db"

	"xorm.io/xorm"
	"xorm.io/xorm/schemas"
)

// LookupByExactEmail finds at most one user whose email equals the given
// address, ignoring discoverability flags.
//
// BRA-1469: organization administrators must be able to find a colleague by
// typing the full address they already know. Public search (ListUsers /
// SearchUsers) still requires discoverable_by_email; this helper must not be
// exposed on an unscoped route.
//
// Matching is exact and whole-address only — a truncated local-part must not
// hit. Comparison is case-insensitive on every dialect this product runs.
func LookupByExactEmail(s *xorm.Session, email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, nil
	}

	u := &User{}
	var has bool
	var err error
	// Prefer dialect-aware case-insensitive equality where we know it; other
	// dialects use plain equality (MySQL/MariaDB default collations are
	// typically case-insensitive already). Every schemas.DBType case is listed
	// so exhaustive and gocritic/ifElseChain stay green together.
	switch db.Type() {
	case schemas.POSTGRES:
		has, err = s.Where("lower(email) = lower(?)", email).Get(u)
	case schemas.SQLITE:
		has, err = s.Where("email = ? COLLATE NOCASE", email).Get(u)
	case schemas.MYSQL, schemas.MSSQL, schemas.ORACLE, schemas.DAMENG, schemas.GBASE8S:
		has, err = s.Where("email = ?", email).Get(u)
	}
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return u, nil
}
