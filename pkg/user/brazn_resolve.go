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
	"xorm.io/xorm"
)

// GetUserByUsernameOrEmail finds the account a person is named by, accepting
// EITHER the name they sign in with OR their email address.
//
// It exists because the sign-in path had the only lookup in this package that
// accepts both forms and it was unexported, so every other caller had to know
// in advance which of the two it had been given. Whoever writes a colleague's
// name down does not know that, and should not have to: BRA-1414's staff list
// is edited by hand by somebody who knows the person, not the users table.
//
// IT DOES NOT ANSWER WHETHER THE ACCOUNT CAN BE USED, and that is the one
// thing to carry away from this comment. Every other reader here funnels
// through getUser, which turns a disabled or locked row into ErrAccountDisabled
// or ErrAccountLocked; this reads the row directly and hands back a disabled
// account with no error at all, exactly as the sign-in path needs it to. A
// caller that cares about the account's state must ask separately - the staff
// seed resolves the name here and then goes through TeamMember.Create, which
// re-reads it with GetUserByUsername and so still refuses the states it always
// refused.
//
// AN EMPTY STRING IS AN ABSENCE RATHER THAN A QUERY, and the guard is
// load-bearing rather than defensive. `email = ''` is a row that exists: bot
// accounts are created with no address, so an empty line in an operator's list
// would otherwise resolve to whichever bot the database returned first and make
// it a colleague. GetUserByUsername guards its own argument the same way and
// for a milder version of the same reason.
func GetUserByUsernameOrEmail(s *xorm.Session, usernameOrEmail string) (*User, error) {
	if usernameOrEmail == "" {
		return nil, ErrUserDoesNotExist{}
	}

	return getUserByUsernameOrEmail(s, usernameOrEmail)
}
