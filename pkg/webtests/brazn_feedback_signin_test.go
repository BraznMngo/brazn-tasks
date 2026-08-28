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
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Signing in is what reaches an account that already exists (BRA-1414).
//
// EVERY OTHER PATH THAT PROVISIONS RUNS ONLY FOR AN ACCOUNT THAT DOES NOT EXIST
// YET - CreateNewProjectForUser at registration, and the commercial
// create_personal_inbox operation the commercial service sends once when it
// makes the account. Every customer this product has signed up before the
// feedback project could be created for anybody, so without something on this
// path, "a customer who signed up before this work also has one" has no
// mechanism at all. The lookup route this ticket adds can provision an existing
// account, but nothing calls it yet - the desktop client still finds the project
// by listing projects and matching its title, and teaching it the route is
// BRA-1415 - so the route alone leaves the outcome waiting on a caller that does
// not exist.

// signIn logs a fixture account in over the real route table, the way the
// product does it.
func signIn(t *testing.T, env *managedEnv, username, password string) {
	t.Helper()

	rec := humaRequest(t, env.e, http.MethodPost, "/api/v1/login",
		`{"username":"`+username+`","password":"`+password+`"}`, "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// feedbackSubProjectOf returns the reporter's own project beneath the shared
// root, resolved the way the product resolves it - by the membership row, never
// by the title.
func feedbackSubProjectOf(t *testing.T, rootID, userID int64) (*models.Project, bool) {
	t.Helper()

	sub := &models.Project{}
	has, err := dbSessionForTest(t).
		Join("INNER", "users_projects", "users_projects.project_id = projects.id").
		Where("projects.parent_project_id = ? AND users_projects.user_id = ?", rootID, userID).
		Get(sub)
	require.NoError(t, err)
	return sub, has
}

// TestSigningInGivesAnExistingAccountItsFeedbackProject is BRA-1414's second
// acceptance criterion, and the fixture account is the point of it: user1 was
// created by the fixture set and has never been through registration in this
// test, so nothing but the sign-in can have provisioned anything for them.
//
// THE CHEAP CHECK: remove the provisionFeedbackOnSignIn call from
// IssueUserToken (pkg/modules/auth/auth.go) and this fails on a nil root - the
// account signs in perfectly well and has nowhere to file feedback, which is
// exactly the state every customer is in today.
func TestSigningInGivesAnExistingAccountItsFeedbackProject(t *testing.T) {
	env := newFeedbackEnv(t)
	require.Zero(t, countFeedbackEntities(t),
		"the instance must start with no feedback project, or this test proves nothing")

	signIn(t, env, "user1", "12345678")

	root, err := models.FeedbackProject(dbSessionForTest(t))
	require.NoError(t, err)
	require.NotNil(t, root, "signing in must have created the shared root")

	sub, has := feedbackSubProjectOf(t, root.ProjectID, testuser1.ID)
	require.True(t, has, "the account that signed in must now have its own project beneath that root")
	assert.Equal(t, models.FeedbackProjectTitle, sub.Title)
}

// TestSigningInTwiceDoesNotGrowASecondFeedbackProject pins the property that
// makes it safe to put this on a path taken on every sign-in.
func TestSigningInTwiceDoesNotGrowASecondFeedbackProject(t *testing.T) {
	env := newFeedbackEnv(t)

	signIn(t, env, "user1", "12345678")
	root, err := models.FeedbackProject(dbSessionForTest(t))
	require.NoError(t, err)
	require.NotNil(t, root)
	first, has := feedbackSubProjectOf(t, root.ProjectID, testuser1.ID)
	require.True(t, has)
	firstID := first.ID

	signIn(t, env, "user1", "12345678")

	assert.Equal(t, int64(1), countFeedbackEntities(t), "a second sign-in must not create a second root")
	second, has := feedbackSubProjectOf(t, root.ProjectID, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, firstID, second.ID, "a second sign-in must resolve to the same project, not a new one")
}

// TestSigningInProvisionsNothingWithoutAResolvableOwner is the fail-safe
// direction on the sign-in path, and it matters more here than anywhere else
// this provisioning runs: this code is now between a customer and their account.
//
// THE CHEAP CHECK: make provisionFeedbackOnSignIn return its error instead of
// logging it, and the sub-test naming an owner that does not exist still passes
// - because an unresolvable owner is deliberately not an error. Make
// ProvisionFeedbackAccess return one, and both of these fail on the sign-in
// itself rather than on anything about feedback.
func TestSigningInProvisionsNothingWithoutAResolvableOwner(t *testing.T) {
	for _, c := range []struct {
		name  string
		owner string
	}{
		{"no owner configured", ""},
		{"an owner that is not an account here", "brazn-staff-account-that-does-not-exist"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newPersonalEnv(t)
			setConfigForTest(t, config.BraznFeedbackOwner, c.owner)

			signIn(t, env, "user1", "12345678")

			assert.Zero(t, countFeedbackEntities(t),
				"a customer must still be able to sign in, and must get no project")
		})
	}
}

// TestSigningInProvisionsNothingOutsideManagedMode keeps a self-hosted instance
// untouched by the sign-in path, exactly as CreateNewProjectForUser keeps it
// untouched by registration.
//
// THE CHEAP CHECK: delete the BraznManagedMode guard in
// provisionFeedbackOnSignIn and this fails - an operator who set an owner but
// never turned managed mode on would find projects appearing under an account of
// theirs on every login.
func TestSigningInProvisionsNothingOutsideManagedMode(t *testing.T) {
	env := newFeedbackEnv(t)
	setConfigForTest(t, config.BraznManagedMode, false)

	signIn(t, env, "user1", "12345678")

	assert.Zero(t, countFeedbackEntities(t), "an unmanaged instance must provision nothing")
}
