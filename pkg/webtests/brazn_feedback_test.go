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

package webtests

import (
	"fmt"
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFeedbackEnv is a personal instance that has the staff account Feedback
// resolves through, and nothing else Feedback-shaped.
//
// IT CREATES AN ACCOUNT RATHER THAN POINTING A SETTING AT A FIXTURE USER,
// because BRA-1414 replaced `brazn.feedbackowner` with a constant: the owner
// is now whichever account carries models.OneAdminUsername, and an instance
// without one has no Feedback at all.
//
// It creates the account DIRECTLY rather than running the product's start-up
// seed, deliberately. Every test below asserts on what provisioning creates
// from nothing - the first count in this file requires the instance to hold no
// Feedback project at all - and a seeded project would make those assertions
// pass whatever provisioning did. What the seed itself produces is asserted in
// pkg/models/brazn_seed_test.go, where the whole chain is built.
func newFeedbackEnv(t *testing.T) *managedEnv {
	t.Helper()

	env := newPersonalEnv(t)
	createStaffAccount(t)
	return env
}

// createStaffAccount puts the account models.OneAdminUsername names into the
// users table. It owns no project and appears in no share, so anything these
// tests observe about it was put there by provisioning.
//
// user.CreateUserConfirmLater rather than models.RegisterUser, because
// RegisterUser would create this account's Inbox and - in managed mode - run
// Feedback provisioning for it, which is exactly the state these tests need to
// start without.
func createStaffAccount(t *testing.T) *user.User {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	created, _, err := user.CreateUserConfirmLater(s, &user.User{
		Username: models.OneAdminUsername,
		Name:     models.OneAdminName,
		Email:    models.OneAdminEmail,
		Password: "a password no assertion in this package depends on",
		Issuer:   user.IssuerLocal,
	})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
	return created
}

// provisionFeedback runs the product's own provisioning for one member and
// returns the id of the sub-project it bound their exemption to.
func (env *managedEnv) provisionFeedback(member *user.User) int64 {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	projectID, err := models.ProvisionFeedbackAccess(s, member)
	require.NoError(env.t, err)
	require.NotZero(env.t, projectID, "provisioning must leave a Feedback sub-project behind")
	require.NoError(env.t, s.Commit())

	return projectID
}

func countFeedbackEntities(t *testing.T) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Where("kind = ?", string(models.ProtectedKindFeedback)).Count(&models.ProtectedEntity{})
	require.NoError(t, err)
	return count
}

func projectOwnerID(t *testing.T, projectID int64) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	project, err := models.GetProjectSimpleByID(s, projectID)
	require.NoError(t, err)
	return project.OwnerID
}

func projectMemberships(t *testing.T, userID int64) []*models.ProjectUser {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	memberships := []*models.ProjectUser{}
	require.NoError(t, s.Where("user_id = ?", userID).Find(&memberships))
	return memberships
}

// TestFeedbackProvisioningMakesOneProjectTheCustomerDoesNotOwn covers the two
// properties that make Feedback an exemption rather than a category.
//
// ONE PROJECT: provisioning runs on every registration, so it runs many times
// on any real instance. It has to converge on the single project it made the
// first time. Deleting the existing-project lookup at the top of
// ensureFeedbackProject makes this fail on the count: the second call creates a
// second project, registers a second protected entity, and from then on there
// are two projects wearing an exemption written for one.
//
// NOBODY'S: the owner is the Brazn account, never the customer being enrolled.
// That is what keeps "a personal account has exactly one customer-owned
// project" literally true rather than nearly true. Handing ownership to the
// registering user - the obvious shortcut, because CreateNewProjectForUser
// already has one to hand - fails the owner assertion.
func TestFeedbackProvisioningMakesOneProjectTheCustomerDoesNotOwn(t *testing.T) {
	env := newFeedbackEnv(t)

	require.Zero(t, countFeedbackEntities(t),
		"the instance must start with no Feedback project, or this proves nothing")

	first := env.provisionFeedback(&testuser1)
	second := env.provisionFeedback(&testuser1)

	assert.Equal(t, first, second, "a second registration must find the first project, not make another")
	assert.Equal(t, int64(1), countFeedbackEntities(t), "there must be exactly one Feedback project")

	owner, err := user.GetUserByUsername(dbSessionForTest(t), models.OneAdminUsername)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, projectOwnerID(t, first),
		"Feedback belongs to Brazn; a customer who owned it would have two owned projects")
	assert.NotEqual(t, testuser1.ID, projectOwnerID(t, first))
}

// TestFeedbackOwnerReprovisioningDoesNotDuplicateTheirOwnSubProject is
// TestFeedbackProvisioningMakesOneProjectTheCustomerDoesNotOwn's counterpart
// for the one caller ensureFeedbackSubProject's ordinary idempotence lookup
// can never find a row for: the feedback owner's own account.
//
// ProjectUser.Create refuses to add a project's own owner as a member of it
// (l.OwnerID == lu.UserID), which is exactly right for every OTHER reporter's
// sub-project - the owner already holds Admin on it by ownership - but it
// means the owner's OWN sub-project never gets a users_projects row for the
// join-based lookup every other reporter's repeat call relies on.
//
// DELETE-THE-GUARD: remove ensureFeedbackSubProject's ownership branch and
// this fails on the count - the second call finds nothing via the join and
// creates a second sub-project under the same root for the same account.
func TestFeedbackOwnerReprovisioningDoesNotDuplicateTheirOwnSubProject(t *testing.T) {
	env := newFeedbackEnv(t)

	owner, err := user.GetUserByUsername(dbSessionForTest(t), models.OneAdminUsername)
	require.NoError(t, err)

	first := env.provisionFeedback(owner)
	second := env.provisionFeedback(owner)
	assert.Equal(t, first, second,
		"a repeat call for the owner's own account must find the same sub-project, not make another")

	s := db.NewSession()
	defer s.Close()

	root, err := models.FeedbackProject(s)
	require.NoError(t, err)
	subProjects := []*models.Project{}
	require.NoError(t, s.Where("parent_project_id = ?", root.ProjectID).Find(&subProjects))
	assert.Len(t, subProjects, 1, "the owner's own account must not accumulate a second sub-project")
}

// TestFeedbackSubProjectsUseReporterUsername pins BRA-1479: each reporter's
// sub-project is titled after them, while the instance root keeps the shared
// FeedbackProjectTitle.
func TestFeedbackSubProjectsUseReporterUsername(t *testing.T) {
	env := newFeedbackEnv(t)

	feedbackA := env.provisionFeedback(&testuser1)
	feedbackB := env.provisionFeedback(&testuser2)

	s := db.NewSession()
	defer s.Close()

	projectA, err := models.GetProjectSimpleByID(s, feedbackA)
	require.NoError(t, err)
	projectB, err := models.GetProjectSimpleByID(s, feedbackB)
	require.NoError(t, err)

	assert.Equal(t, models.FeedbackSubProjectTitle(testuser1.Username), projectA.Title)
	assert.Equal(t, models.FeedbackSubProjectTitle(testuser2.Username), projectB.Title)

	root, err := models.FeedbackProject(s)
	require.NoError(t, err)
	rootProject, err := models.GetProjectSimpleByID(s, root.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, models.FeedbackProjectTitle, rootProject.Title)
}

// TestFeedbackOwnerLookupIgnoresCustomerSubProjects is the BRA-1479 guard on
// ensureFeedbackSubProject's owner branch. Every sub-project is owned by the
// staff account, so a lookup on parent plus owner alone can return a customer's
// project; the owner's own sub-project is the one with no users_projects row.
func TestFeedbackOwnerLookupIgnoresCustomerSubProjects(t *testing.T) {
	env := newFeedbackEnv(t)

	customerFeedback := env.provisionFeedback(&testuser1)

	owner, err := user.GetUserByUsername(dbSessionForTest(t), models.OneAdminUsername)
	require.NoError(t, err)

	ownerFeedback := env.provisionFeedback(owner)
	require.NotEqual(t, customerFeedback, ownerFeedback,
		"the owner's sub-project must not be a customer's sub-project")

	s := db.NewSession()
	defer s.Close()

	ownerProject, err := models.GetProjectSimpleByID(s, ownerFeedback)
	require.NoError(t, err)
	assert.Equal(t, models.FeedbackSubProjectTitle(owner.Username), ownerProject.Title)
}

// TestFeedbackRenamesLegacySubProjectTitle pins BRA-1479's migration path:
// sub-projects that still carry FeedbackProjectTitle are renamed on resolve.
func TestFeedbackRenamesLegacySubProjectTitle(t *testing.T) {
	env := newFeedbackEnv(t)

	feedback := env.provisionFeedback(&testuser1)

	s := db.NewSession()
	defer s.Close()

	_, err := s.ID(feedback).Cols("title").Update(&models.Project{Title: models.FeedbackProjectTitle})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	second := env.provisionFeedback(&testuser1)
	assert.Equal(t, feedback, second)

	project, err := models.GetProjectSimpleByID(dbSessionForTest(t), feedback)
	require.NoError(t, err)
	assert.Equal(t, models.FeedbackSubProjectTitle(testuser1.Username), project.Title)
}

// TestFeedbackExemptionFollowsTheProjectIDAndNotTheTitle is the leak BRA-764
// exists to prevent, stated as a test.
//
// The decoy carries the EXACT title the product gives Feedback and is
// owned by the account doing the submitting, so it out-privileges the real
// thing on every axis except the one that decides: there is no
// brazn_protected_entities row pointing at it. Ownership being the customer's
// is deliberate - it removes the permission layer as an explanation, so a
// refusal can only have come from policy.
//
// DELETE-THE-GUARD: replace GetProtectedEntityForProject in
// decidePersonalTaskMove with anything that consults the title - or register
// the exemption against a name rather than an id - and the decoy starts
// accepting submissions while the control still passes. The pair is what makes
// that visible; a test that only tried the decoy would also pass against a
// build that refuses everything.
func TestFeedbackExemptionFollowsTheProjectIDAndNotTheTitle(t *testing.T) {
	env := newFeedbackEnv(t)

	feedback := env.provisionFeedback(&testuser1)
	decoy := env.newProject(&testuser1, models.FeedbackProjectTitle, 0)
	require.NotEqual(t, feedback, decoy)

	t.Run("control: the provisioned project accepts a submission", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("a project that merely shares the title does not", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/2",
			fmt.Sprintf(`{"id":2,"title":"task #2 done","project_id":%d}`, decoy), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

// TestFeedbackExemptionDoesNotExtendToAnotherProtectedKind closes the other way
// the exemption could become general: not by name, but by a project being
// flagged as something the topology knows about.
//
// The decoy here IS part of the managed topology - it holds a real protected
// entity row - and is still refused, because the exemption names one kind and
// not membership of the table. This is the shape the plan of record warned
// about: a "system project" category that later projects join for free.
//
// DELETE-THE-GUARD: weaken `protected.Kind == models.ProtectedKindFeedback` to
// `protected != nil` in decidePersonalTaskMove and this fails while every other
// personal-policy test still passes, because no other test puts a
// non-Feedback, non-Inbox protected project in front of a Personal account.
func TestFeedbackExemptionDoesNotExtendToAnotherProtectedKind(t *testing.T) {
	env := newFeedbackEnv(t)

	feedback := env.provisionFeedback(&testuser1)
	flagged := env.newProject(&testuser1, "Flagged some other way", 0)
	env.protect(models.ProtectedKindPublicRoot, flagged, 0)

	t.Run("control: the provisioned project accepts a submission", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("another protected kind does not", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/2",
			fmt.Sprintf(`{"id":2,"title":"task #2 done","project_id":%d}`, flagged), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

// TestFeedbackEnrolmentGrantsNothingBeyondTheOneProject is BRA-764's fifth
// acceptance criterion measured where it is decided: the membership rows
// provisioning writes.
//
// The delta is asserted rather than the total, because user1 already holds
// fixture shares and a total would be pinning the fixture set instead of the
// change. Write is asserted by value: Read cannot file a task and Admin would
// hand the customer the project, so the exact level is the criterion, not an
// implementation detail.
//
// DELETE-THE-GUARD: bind enrolment to anything broader than
// FeedbackProject's single row - every protected entity, say, or every project
// the owner holds - and the delta stops being one.
func TestFeedbackEnrolmentGrantsNothingBeyondTheOneProject(t *testing.T) {
	env := newFeedbackEnv(t)

	before := projectMemberships(t, testuser1.ID)
	feedback := env.provisionFeedback(&testuser1)
	after := projectMemberships(t, testuser1.ID)

	require.Len(t, after, len(before)+1,
		"enrolment must add exactly one membership, and it must be the Feedback one")

	added := map[int64]models.Permission{}
	for _, membership := range after {
		added[membership.ProjectID] = membership.Permission
	}
	for _, membership := range before {
		delete(added, membership.ProjectID)
	}

	require.Len(t, added, 1)
	permission, enrolled := added[feedback]
	require.True(t, enrolled, "the added membership must be on the provisioned Feedback project")
	assert.Equal(t, models.PermissionWrite, permission,
		"the least permission that can submit a task, and no more")
}

// TestFeedbackMembersEndpointIsNotACrossOrganisationDirectory pins BRA-1182
// (A2), against the one project a members listing can still say anything
// about after BRA-1180 (A1) gave every reporter their own sub-project: the
// root. Two accounts enrolled there directly - which no code path takes after
// A1, but which is exactly the shape an instance that ran the pre-A1
// provisioning still carries for every account already registered before
// this ticket - is the members listing this guard exists for.
//
// Two separate assertions, because they are two separate exposures: an empty
// search enumerates every reporter, and an exact search for a known username
// confirms it exists without enumerating anything. A minimum search length
// alone would close only the first.
//
// DELETE-THE-GUARD: removing the permission check above closes both, and
// leaves the owner's own listing (the control case) working as before -
// exactly the asymmetry a permission-level check gives that a route-wide
// refusal could not.
func TestFeedbackMembersEndpointIsNotACrossOrganisationDirectory(t *testing.T) {
	env := newFeedbackEnv(t)

	// A throwaway third reporter's own provisioning is what brings the root
	// into existence at all; this test's own two reporters are enrolled on
	// that root directly below, simulating the pre-A1 dual enrolment an
	// instance still carries for every account registered before A1 shipped -
	// provisionFeedback gives each reporter their own sub-project now, so this
	// guard's real target has to be built by hand rather than provisioned.
	env.provisionFeedback(&testuser15)

	s := dbSessionForTest(t)
	root, err := models.FeedbackProject(s)
	require.NoError(t, err)
	require.NotNil(t, root)
	feedback := root.ProjectID

	owner, err := user.GetUserByUsername(s, models.OneAdminUsername)
	require.NoError(t, err)

	for _, reporter := range []*user.User{&testuser1, &testuser2} {
		member := &models.ProjectUser{ProjectID: feedback, Username: reporter.Username, Permission: models.PermissionWrite}
		require.NoError(t, member.Create(s, owner))
	}
	require.NoError(t, s.Commit())

	path := fmt.Sprintf("/api/v1/projects/%d/users", feedback)

	t.Run("a reporter cannot enumerate the roster with an empty search", func(t *testing.T) {
		rec := env.request(http.MethodGet, path, "", &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("a reporter cannot confirm another reporter's username by searching for it exactly", func(t *testing.T) {
		rec := env.request(http.MethodGet, path+"?s="+testuser2.Username, "", &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("control: the feedback owner can still read the roster", func(t *testing.T) {
		rec := env.request(http.MethodGet, path, "", owner)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), testuser1.Username)
		assert.Contains(t, rec.Body.String(), testuser2.Username)
	})
}

// TestFeedbackSubProjectsAreIsolatedPerReporter pins BRA-1180 (A1)'s core
// acceptance criterion, over the real route and against a genuine second
// reporter: a reporter reaches only their own Feedback sub-project.
//
// The two provisioned ids being different IS the structural claim this ticket
// makes - isolation is a property of there being two projects, not of a
// permission tweak on one - so it is asserted here rather than only implied
// by the requests below succeeding or failing.
func TestFeedbackSubProjectsAreIsolatedPerReporter(t *testing.T) {
	env := newFeedbackEnv(t)

	feedbackA := env.provisionFeedback(&testuser1)
	feedbackB := env.provisionFeedback(&testuser2)
	require.NotEqual(t, feedbackA, feedbackB,
		"two reporters must land in two different projects, or there is nothing here to isolate")

	t.Run("control: a reporter can file into their own sub-project", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"my own report","project_id":%d}`, feedbackA), &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("a reporter cannot file into another reporter's sub-project", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/2",
			fmt.Sprintf(`{"id":2,"title":"planted report","project_id":%d}`, feedbackB), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("a reporter cannot read another reporter's sub-project members listing", func(t *testing.T) {
		rec := env.request(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/users", feedbackB), "", &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

// TestTeamsFeedbackSubProjectsAreIsolatedPerReporter is
// TestFeedbackSubProjectsAreIsolatedPerReporter's Teams-edition sibling.
//
// decideTeamsTaskMove used to match a Feedback destination by exact id
// (GetProtectedEntityForProject), which only the ROOT carries a
// protected-entity row for - a reporter's own sub-project (BRA-1180/A1) never
// matched, so a Teams member's own feedback submission was refused outright.
// This pins that filing into one's own sub-project is allowed, and - since
// the fix also replaced an unconditional allow with hasFeedbackAccess - that
// another reporter's sub-project still is not.
func TestTeamsFeedbackSubProjectsAreIsolatedPerReporter(t *testing.T) {
	env, _ := newTeamsEnv(t)
	createStaffAccount(t)

	feedbackA := env.provisionFeedback(&testuser1)
	feedbackB := env.provisionFeedback(&testuser6)
	require.NotEqual(t, feedbackA, feedbackB,
		"two reporters must land in two different projects, or there is nothing here to isolate")

	t.Run("control: a member can file into their own sub-project", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"my own report","project_id":%d}`, feedbackA), &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("a member cannot file into another member's sub-project", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/2",
			fmt.Sprintf(`{"id":2,"title":"planted report","project_id":%d}`, feedbackB), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

// TestFeedbackIsNotProvisionedWithoutAResolvableOwner records the fail-safe
// direction on an instance that has not created its staff account.
//
// It used to cover two cases, an empty `brazn.feedbackowner` and a setting
// naming an account nobody had made. BRA-1414 deleted the setting, so there is
// now exactly one way for the owner to be absent - the account is not here -
// and this is it. Nothing was relaxed: the assertions are the ones the second
// case always made, and the case that disappeared is the one that can no
// longer occur.
//
// It is not an error, and a customer registering must not fail because of it.
// An instance can genuinely be in this state: a database restored under a
// build whose web server has not started yet has had no seeding run against
// it. Skipping is also the safe direction to be wrong in: no project means no
// access, where refusing here would mean no account.
//
// It is additionally what every other managed-mode test in this package runs
// under - none of them creates the staff account - so it pins that Feedback
// still leaves them alone.
func TestFeedbackIsNotProvisionedWithoutAResolvableOwner(t *testing.T) {
	newPersonalEnv(t)

	_, err := user.GetUserByUsername(dbSessionForTest(t), models.OneAdminUsername)
	require.True(t, user.IsErrUserDoesNotExist(err),
		"this instance must not have the staff account, or the test proves nothing")

	before := len(projectMemberships(t, testuser1.ID))

	s := db.NewSession()
	defer s.Close()

	projectID, err := models.ProvisionFeedbackAccess(s, &testuser1)
	require.NoError(t, err, "an unresolvable owner must not fail the registration it runs inside")
	require.NoError(t, s.Commit())

	assert.Zero(t, projectID, "no owner means no sub-project either")
	assert.Zero(t, countFeedbackEntities(t), "no owner means no project")
	assert.Len(t, projectMemberships(t, testuser1.ID), before, "and no enrolment")
}
