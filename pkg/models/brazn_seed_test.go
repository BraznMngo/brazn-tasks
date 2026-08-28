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
// The colleague in this test is named in staffUsernames AND has an account on
// the instance, which is the state a real deployment has to reach before
// anybody can read a report. Both halves of that are asserted below: the name
// becomes a membership row, and the membership becomes access.
//
// DELETE-THE-GUARD, and there are three, each failing in its own place:
// remove the grantTeamAccess call from seedInstanceStaff and the last
// assertion fails while every existence check above it still passes - the
// account, the project, the team and the report all exist, and no colleague
// can see any of it, which is the exact shape of the defect this ticket is
// fixing. Remove the ensureStaffMembers call and the membership assertion
// fails first, naming the earlier cause. Point OneAdminEmail at a different
// address and the mailbox assertion fails, which it could not do while it was
// reading a function that blanks the column.
//
// TRACED, because the obvious mutation for the mailbox is the wrong one:
// dropping `Email` from the user handed to RegisterUserConfirmLater does NOT
// redden the mailbox assertion. checkIfUserIsValid refuses an empty address
// with ErrNoUsernamePassword before any row is written, so the seed itself
// fails and runSeed's require stops the test several assertions earlier.
// Changing the address is the mutation this assertion actually guards against,
// and it is also the realistic one.
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

	// THE MAILBOX IS READ BACK THROUGH A DIFFERENT FUNCTION, and that is not a
	// stylistic choice. Every ordinary reader in pkg/user - GetUserByUsername,
	// GetUserByID, GetUsersByCond - funnels into getUser with `withEmail` false,
	// and getUser assigns an EMPTY STRING over the column on its way out
	// (pkg/user/user.go, `if !withEmail { userOut.Email = "" }`). So an
	// assertion on `admin.Email` above cannot pass whatever the database holds,
	// and cannot fail if the seed stopped storing an address either. It is the
	// shape this project calls a test that is true where the difference is not
	// yet decided: the value is normalised away between the row and the
	// assertion. GetUserWithEmail is the one reader that returns the column.
	stored, err := user.GetUserWithEmail(s, &user.User{Username: OneAdminUsername})
	require.NoError(t, err)

	// THE LITERAL, not OneAdminEmail. The address is a decision - a real
	// mailbox somebody monitors - and a constant compared against itself is
	// checked by nobody: renaming the mailbox in brazn_seed.go would move both
	// sides of `assert.Equal(t, OneAdminEmail, ...)` together and stay green.
	// Pinning the text is what makes this an assertion about the decision.
	assert.Equal(t, "admin@braznmngo.com", stored.Email,
		"the staff account must carry the monitored mailbox, because every notification it is sent goes there")
	assert.Equal(t, OneAdminEmail, stored.Email,
		"and the constant the seed writes must be that same address")

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

	// THE COLLEAGUE IS IN THE TEAM, asserted before the reachability below
	// rather than left to be inferred from it. The two are separate failures
	// with the same symptom: a name that never became a membership row, and a
	// membership that never became access. Reading them apart is what says
	// which one broke, and the first is the one that will break, because
	// staffUsernames is edited by hand.
	//
	// The team is found through the grant row on Feedback, which is how
	// ensureStaffTeam identifies it too - never by its title, which is a label
	// anybody could rename.
	grant := &TeamProject{}
	hasGrant, err := dbSession(t).Where("project_id = ?", root.ProjectID).Get(grant)
	require.NoError(t, err)
	require.True(t, hasGrant, "seeding must leave a team grant on Feedback")
	assert.Equal(t, int64(1),
		countRows(t, &TeamMember{}, "team_id = ? AND user_id = ?", grant.TeamID, colleague.ID),
		"a colleague named in staffUsernames who has an account must be put into the staff team")

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
		require.Error(t, user.CheckUserPassword(first, guess),
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

// TestSeedingSkipsAStaffAccountThatCannotBeUsed covers the two refusals that
// are NOT "no such account", and it is here because the cost of getting them
// wrong is out of all proportion to how obscure they look.
//
// A colleague who leaves has their account disabled or locked rather than
// deleted, and their name stays in staffUsernames until somebody removes it.
// user.GetUserByUsername answers that state with ErrAccountDisabled or
// ErrAccountLocked rather than with a user, so TeamMember.Create hands the
// error straight back - and the whole seed runs in ONE TRANSACTION. Before
// this was handled, one former colleague still listed would have rolled back
// the Feedback project, the staff team and the grant on EVERY boot, leaving an
// instance that looked seeded in code and was empty in the database.
//
// Both accounts are listed BEFORE the usable one, because the assertion that
// matters is that the names after a skip are still added.
//
// DELETE-THE-GUARD: remove the ErrAccountDisabled / ErrAccountLocked case from
// ensureStaffMembers and this fails - not on the membership count, but on
// runSeed itself, which is the honest place for it to fail.
func TestSeedingSkipsAStaffAccountThatCannotBeUsed(t *testing.T) {
	seededInstance(t)

	// user17 is disabled and user18 is locked in the shipped fixtures
	// (pkg/db/fixtures/users.yml, status 2 and status 3).
	staffUsernames = []string{"user17", "user18", "user2"}
	t.Cleanup(func() { staffUsernames = []string{OneAdminUsername} })

	runSeed(t)

	admin := seededAdmin(t)
	team := &Team{}
	has, err := dbSession(t).Where("created_by_id = ?", admin.ID).Get(team)
	require.NoError(t, err)
	require.True(t, has, "an unusable staff account must not have rolled the seed back")

	colleague, err := user.GetUserByUsername(dbSession(t), "user2")
	require.NoError(t, err)
	assert.Equal(t, int64(1),
		countRows(t, &TeamMember{}, "team_id = ? AND user_id = ?", team.ID, colleague.ID),
		"a colleague listed after an unusable account must still be added")

	// And the two that were skipped really were skipped, rather than added
	// under an id the count above would never have looked at.
	for _, username := range []string{"user17", "user18"} {
		skipped, err := user.GetUsersByUsername(dbSession(t), []string{username}, false)
		require.NoError(t, err)
		require.Len(t, skipped, 1, "the fixture account %q must exist", username)
		for id := range skipped {
			assert.Equal(t, int64(0),
				countRows(t, &TeamMember{}, "team_id = ? AND user_id = ?", team.ID, id),
				"%q cannot be used, so it must not be in the staff team", username)
		}
	}
}
