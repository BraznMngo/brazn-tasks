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
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

// seededInstance puts this package's tests on an instance that has run
// start-up seeding exactly as the web server does, in MANAGED MODE.
//
// Managed mode is on because that is what production runs, and because it is
// the more hostile setting: with it on, creating the staff account also runs
// CreateNewProjectForUser's Feedback provisioning, so the Feedback root and
// the owner's own sub-project already exist by the time seedInstanceStaff asks
// for them. A test that seeded with managed mode off would never exercise that
// order and would pass against a seed that creates a second root.
//
// The managed-mode tables are emptied first because they are deliberately not
// in the fixture set, so without this a row written by an earlier test in this
// package would still be here and the order tests happened to run in would
// decide the result.
func seededInstance(t *testing.T) {
	t.Helper()

	db.LoadAndAssertFixtures(t)

	config.BraznManagedMode.Set(true)
	t.Cleanup(func() { config.BraznManagedMode.Set(false) })

	s := db.NewSession()
	defer s.Close()
	_, err := s.Exec("DELETE FROM brazn_protected_entities")
	require.NoError(t, err)
	require.NoError(t, s.Commit())
}

// runSeed runs one boot's worth of seeding and commits it.
func runSeed(t *testing.T) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	require.NoError(t, seedInstanceStaff(s))
	require.NoError(t, s.Commit())
}

func seededAdmin(t *testing.T) *user.User {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	admin, err := user.GetUserByUsername(s, OneAdminUsername)
	require.NoError(t, err)
	return admin
}

func countRows(t *testing.T, bean interface{}, where string, args ...interface{}) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Where(where, args...).Count(bean)
	require.NoError(t, err)
	return count
}

// TestSeedingLeavesFeedbackReachableByStaff is the outcome BRA-1414 is named
// for, and it is deliberately asserted as a chain rather than as four separate
// existence checks.
//
// The feature has never worked in front of a customer, and every part of it
// was correct the whole time: the project code, the permission walk and the
// endpoint all did what they said. What was missing was the account at the
// bottom of the chain, so "does each piece exist" is the question that was
// already being answered yes while the feature was dead. The question that
// was not being asked is the one here: starting from nothing, can a member of
// staff read what a customer filed?
//
// DELETE-THE-GUARD: remove the grantTeamAccess call from seedInstanceStaff and
// the last assertion fails while every existence check above it still passes -
// the account, the project, the team and the report all exist, and no
// colleague can see any of it. That is the exact shape of the defect this
// ticket is fixing.
func TestSeedingLeavesFeedbackReachableByStaff(t *testing.T) {
	seededInstance(t)

	// A colleague who is staff but is NOT the account that owns Feedback.
	// Using the owner would prove nothing: the owner reaches every sub-project
	// by owning the root, with or without a team, so a test that only checked
	// them would pass against a build that never granted anything.
	staffUsernames = []string{OneAdminUsername, "user2"}
	t.Cleanup(func() { staffUsernames = []string{OneAdminUsername} })

	runSeed(t)

	s := db.NewSession()
	defer s.Close()

	admin, err := user.GetUserByUsername(s, OneAdminUsername)
	require.NoError(t, err)
	assert.Equal(t, OneAdminName, admin.Name)
	assert.Equal(t, OneAdminEmail, admin.Email)

	root, err := FeedbackProject(s)
	require.NoError(t, err)
	require.NotNil(t, root, "seeding must leave a Feedback project behind")

	rootProject, err := GetProjectSimpleByID(s, root.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, admin.ID, rootProject.OwnerID,
		"Feedback belongs to the staff account, never to a customer")
	assert.Equal(t, FeedbackProjectTitle, rootProject.Title)

	// A customer files a report. This is the ordinary registration path, not a
	// fixture: ProvisionFeedbackAccess is what every account passes through.
	reporter, err := user.GetUserByUsername(s, "user1")
	require.NoError(t, err)
	reported, err := ProvisionFeedbackAccess(s, reporter)
	require.NoError(t, err)
	require.NotZero(t, reported,
		"with the staff account seeded, a customer must get a Feedback sub-project")
	require.NoError(t, s.Commit())

	colleague, err := user.GetUserByUsername(dbSession(t), "user2")
	require.NoError(t, err)

	reportedProject, err := GetProjectSimpleByID(dbSession(t), reported)
	require.NoError(t, err)

	canRead, _, err := reportedProject.CanRead(dbSession(t), colleague)
	require.NoError(t, err)
	assert.True(t, canRead,
		"a member of staff must reach a customer's report through the team's grant on the parent")
}

// dbSession is a fresh committed-state session for one read.
func dbSession(t *testing.T) *xorm.Session {
	t.Helper()

	s := db.NewSession()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestSeedingTwiceLeavesOneOfEachThing is the property that makes it safe to
// run this on every boot, which is the only reason it can be a start-up step
// at all.
//
// Every count is asserted, not just the project's, because the four pieces are
// created by four different mechanisms and only one of them - the Feedback
// project - was idempotent before this ticket. A version that got the project
// right and the team wrong would grow one team and one grant per restart, and
// on a live system that is a table nobody looks at filling up with rows that
// each hand a different team access to customer reports.
//
// DELETE-THE-GUARD: remove the grant lookup at the top of ensureStaffTeam and
// the team and grant counts both become two on the second run. Remove the
// existing-project lookup at the top of ensureFeedbackProject and the project
// count does the same.
func TestSeedingTwiceLeavesOneOfEachThing(t *testing.T) {
	seededInstance(t)

	runSeed(t)
	runSeed(t)

	admin := seededAdmin(t)

	assert.Equal(t, int64(1), countRows(t, &user.User{}, "username = ?", OneAdminUsername),
		"a second boot must find the staff account, not make another")
	assert.Equal(t, int64(1), countRows(t, &ProtectedEntity{}, "kind = ?", string(ProtectedKindFeedback)),
		"a second boot must find the Feedback project, not make another")
	assert.Equal(t, int64(1), countRows(t, &Team{}, "created_by_id = ?", admin.ID),
		"a second boot must find the staff team, not make another")

	root, err := FeedbackProject(dbSession(t))
	require.NoError(t, err)
	require.NotNil(t, root)
	assert.Equal(t, int64(1), countRows(t, &TeamProject{}, "project_id = ?", root.ProjectID),
		"a second boot must find the grant, not write another")
}

// TestTheSeededStaffAccountIsNotALogin pins the decision that this account is
// an owner and never a credential: it is given 32 bytes of crypto/rand as a
// password and the plaintext is dropped in the same expression, so nobody -
// including whoever ran the deploy - can sign in as it. Staff reach Feedback
// by signing in as themselves.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT, stated plainly because the difference
// matters. No test can show that a password is unguessable; the universe of
// strings is not enumerable. What it can show is that the password is not one
// of the values the seed had in its hand when it ran, which is the realistic
// regression - somebody replacing the random secret with something they could
// type, or with the account's own name, to make the account usable.
//
// The second half is a different property and a real one: a re-seed must not
// rewrite the credential. An instance that reset this hash on every restart
// would be quietly re-creating the account rather than finding it, and every
// other idempotence assertion in this file would still pass.
//
// DELETE-THE-GUARD: set Password to any of these values in ensureOneAdmin and
// the first half fails. Remove the existence check at the top of
// ensureOneAdmin and it fails to create the account at all on the second run,
// because the username is taken.
func TestTheSeededStaffAccountIsNotALogin(t *testing.T) {
	seededInstance(t)

	runSeed(t)
	first := seededAdmin(t)
	require.NotEmpty(t, first.Password, "the account must carry a hash, not an empty column")

	for _, guess := range []string{
		"",
		OneAdminUsername,
		OneAdminName,
		OneAdminEmail,
		"password",
		"12345678",
	} {
		assert.Error(t, user.CheckUserPassword(first, guess),
			"the staff account must not accept %q as a password", guess)
	}

	runSeed(t)
	second := seededAdmin(t)
	assert.Equal(t, first.Password, second.Password,
		"a second boot must leave the credential alone rather than minting a new one")
	assert.Equal(t, first.ID, second.ID)
}

// TestSeedingSkipsAStaffNameWithNoAccountYet records the direction this fails
// in, which is a decision rather than an accident.
//
// Staff accounts come into existence when a person signs in for the first
// time, so a name will routinely be listed in code before its account exists.
// Refusing to start the web server over that would take a live product down to
// enforce a membership nobody is waiting on. The colleague who DOES have an
// account is asserted alongside, because a seed that silently gave up at the
// first missing name would otherwise look identical to one that skipped it.
func TestSeedingSkipsAStaffNameWithNoAccountYet(t *testing.T) {
	seededInstance(t)

	staffUsernames = []string{"not-an-account-on-this-instance", "user2"}
	t.Cleanup(func() { staffUsernames = []string{OneAdminUsername} })

	runSeed(t)

	admin := seededAdmin(t)
	team := &Team{}
	has, err := dbSession(t).Where("created_by_id = ?", admin.ID).Get(team)
	require.NoError(t, err)
	require.True(t, has)

	colleague, err := user.GetUserByUsername(dbSession(t), "user2")
	require.NoError(t, err)

	assert.Equal(t, int64(1),
		countRows(t, &TeamMember{}, "team_id = ? AND user_id = ?", team.ID, colleague.ID),
		"a name the seed could not resolve must not stop the names after it")
}
