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

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedbackOwnerUsername stands in for the Brazn staff account that owns Percy
// Feedback. user10 owns no fixture project and appears in no fixture share, so
// anything this file observes about that account was put there by provisioning
// and not by the fixture set.
const feedbackOwnerUsername = "user10"

func newFeedbackEnv(t *testing.T) *managedEnv {
	t.Helper()

	env := newPersonalEnv(t)
	setConfigForTest(t, config.BraznFeedbackOwner, feedbackOwnerUsername)
	return env
}

// provisionFeedback runs the product's own provisioning for one member and
// returns the project id it bound the exemption to.
func (env *managedEnv) provisionFeedback(member *user.User) int64 {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	require.NoError(env.t, models.ProvisionFeedbackAccess(s, member))

	project, err := models.FeedbackProject(s)
	require.NoError(env.t, err)
	require.NotNil(env.t, project, "provisioning must leave a Percy Feedback project behind")
	require.NoError(env.t, s.Commit())

	return project.ProjectID
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
// properties that make Percy Feedback an exemption rather than a category.
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
		"the instance must start with no Percy Feedback project, or this proves nothing")

	first := env.provisionFeedback(&testuser1)
	second := env.provisionFeedback(&testuser1)

	assert.Equal(t, first, second, "a second registration must find the first project, not make another")
	assert.Equal(t, int64(1), countFeedbackEntities(t), "there must be exactly one Percy Feedback project")

	owner, err := user.GetUserByUsername(dbSessionForTest(t), feedbackOwnerUsername)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, projectOwnerID(t, first),
		"Percy Feedback belongs to Brazn; a customer who owned it would have two owned projects")
	assert.NotEqual(t, testuser1.ID, projectOwnerID(t, first))
}

// TestFeedbackExemptionFollowsTheProjectIDAndNotTheTitle is the leak BRA-764
// exists to prevent, stated as a test.
//
// The decoy carries the EXACT title the product gives Percy Feedback and is
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
		"enrolment must add exactly one membership, and it must be the Percy Feedback one")

	added := map[int64]models.Permission{}
	for _, membership := range after {
		added[membership.ProjectID] = membership.Permission
	}
	for _, membership := range before {
		delete(added, membership.ProjectID)
	}

	require.Len(t, added, 1)
	permission, enrolled := added[feedback]
	require.True(t, enrolled, "the added membership must be on the provisioned Percy Feedback project")
	assert.Equal(t, models.PermissionWrite, permission,
		"the least permission that can submit a task, and no more")
}

// TestFeedbackMembersEndpointIsNotACrossOrganisationDirectory pins BRA-1182
// (A2). Percy Feedback enrols every account on the instance with Write, so its
// members listing - unlike an ordinary project's, whose membership a sharer
// chose deliberately - is a full instance-wide user and email directory to
// anyone merely enrolled, unless this endpoint refuses them the way the
// general users endpoint already does.
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

	feedback := env.provisionFeedback(&testuser1)
	require.Equal(t, feedback, env.provisionFeedback(&testuser2),
		"a second reporter must join the same Percy Feedback project, or this test is not exercising the shared directory")

	owner, err := user.GetUserByUsername(dbSessionForTest(t), feedbackOwnerUsername)
	require.NoError(t, err)

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

// TestFeedbackIsNotProvisionedWithoutAResolvableOwner records the fail-safe
// direction, in both the ways the owner can be absent.
//
// Neither is an error. brazn.feedbackowner is empty on a stock instance and may
// name an account an operator has not created yet, and a customer signing up
// must not fail because of either. Skipping is also the safe direction to be
// wrong in: no project means no access, where refusing here would mean no
// account.
//
// The empty case is additionally what every other managed-mode test in this
// package runs under, so it pins that this change left them alone.
func TestFeedbackIsNotProvisionedWithoutAResolvableOwner(t *testing.T) {
	for _, c := range []struct {
		name  string
		owner string
	}{
		{"no owner configured", ""},
		{"an owner that is not an account here", "brazn-staff-account-that-does-not-exist"},
	} {
		t.Run(c.name, func(t *testing.T) {
			newPersonalEnv(t)
			setConfigForTest(t, config.BraznFeedbackOwner, c.owner)

			before := len(projectMemberships(t, testuser1.ID))

			s := db.NewSession()
			defer s.Close()

			require.NoError(t, models.ProvisionFeedbackAccess(s, &testuser1),
				"an unresolvable owner must not fail the registration it runs inside")
			require.NoError(t, s.Commit())

			assert.Zero(t, countFeedbackEntities(t), "no owner means no project")
			assert.Len(t, projectMemberships(t, testuser1.ID), before, "and no enrolment")
		})
	}
}
