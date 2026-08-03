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

package models

import (
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegisterUserKeepsTheConfirmationStatus pins BRA-1047: a registration that
// sent a confirmation email must leave the account needing confirmation.
//
// This test only means something with the mailer ENABLED, and that is precisely
// why the defect shipped. user.CreateUser writes StatusEmailConfirmationRequired
// only when a mailer exists (user_create.go:101-114). With the mailer off - the
// default, and what every other test in this repository runs with - a new
// account is legitimately Active, so the same assertion holds whether the clobber
// is present or not. Both existing skip_email_confirm tests sit in exactly that
// position, which is why they read as covering this and never did.
func TestRegisterUserKeepsTheConfirmationStatus(t *testing.T) {
	db.LoadAndAssertFixtures(t)

	// Both of these are process-global. Leaving notifications faked makes every
	// later test in the package find an empty notifications table, and this file
	// sorts near the front.
	config.MailerEnabled.Set(true)
	t.Cleanup(func() { config.MailerEnabled.Set(false) })
	notifications.Fake()
	t.Cleanup(notifications.Unfake)

	// CreateNewProjectForUser returns BEFORE its update when the new account
	// already has a default project. A non-zero default here would skip the
	// statement this test exists to cover and the run would prove nothing.
	require.Zero(t, config.DefaultSettingsDefaultProjectID.GetInt64(),
		"a non-zero defaultsettings.default_project_id makes CreateNewProjectForUser "+
			"return before the update this test covers")

	s := db.NewSession()
	defer s.Close()

	registered, err := RegisterUser(s, &user.User{
		Username: "bra1047-confirm",
		Password: "averyl0ngpassword",
		Email:    "bra1047-confirm@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	// Read the row back rather than trusting what RegisterUser returned. That
	// struct is the one CreateUser read at user_create.go:91, before the
	// confirmation status was written; asserting on it would pass either way.
	// Its staleness is the defect itself, not an inconvenience around it.
	rs := db.NewSession()
	defer rs.Close()

	confirmTokens, err := rs.Table("user_tokens").
		Where("user_id = ? AND kind = ?", registered.ID, user.TokenEmailConfirm).
		Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, confirmTokens,
		"CreateUser must have taken its mail branch and issued a confirmation token; "+
			"without one it never set the status and the status assertion is vacuous")

	stored, err := user.GetUserByID(rs, registered.ID)
	require.NoError(t, err)

	require.NotZero(t, stored.DefaultProjectID,
		"CreateNewProjectForUser must have reached its update; that statement is the "+
			"one that used to write the stale status back over the confirmation status")

	assert.Equal(t, user.StatusEmailConfirmationRequired, stored.Status,
		"a confirmation email was sent, so the stored account must still need confirming")
}
