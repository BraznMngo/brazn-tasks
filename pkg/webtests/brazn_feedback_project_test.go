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
	"encoding/json"
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The GET /api/v2/brazn/feedback/project read model (BRA-1414), observed
// over real HTTP through the real route table - the same way BRA-1343's
// public-root read model is in brazn_project_topology_test.go.
//
// Before this route existed, the only way anything outside pkg/models found
// this project was its title, which is unique per reporter today but is not
// a contract. These tests are about what the route itself answers, not about
// what a client draws from it.

const feedbackProjectPath = "/api/v2/brazn/feedback/project"

type feedbackProjectResponse struct {
	ProjectID *int64 `json:"project_id"`
}

// TestFeedbackProjectRouteMatchesProvisioning pins the route to the same
// answer the product's own provisioning gives: calling it must not create a
// second sub-project alongside the one ProvisionFeedbackAccess already made,
// and must not hand one reporter another reporter's - and it doubles as the
// "provisions on first use" case, since neither reporter here has been
// provisioned by anything other than the call under test.
func TestFeedbackProjectRouteMatchesProvisioning(t *testing.T) {
	env, _ := newTeamsEnv(t)
	setConfigForTest(t, config.BraznFeedbackOwner, feedbackOwnerUsername)

	expectedA := env.provisionFeedback(&testuser1)
	expectedB := env.provisionFeedback(&testuser6)

	rec := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	bodyA := feedbackProjectResponse{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bodyA))
	require.NotNil(t, bodyA.ProjectID)
	assert.Equal(t, expectedA, *bodyA.ProjectID)

	rec = env.request(http.MethodGet, feedbackProjectPath, "", &testuser6)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	bodyB := feedbackProjectResponse{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bodyB))
	require.NotNil(t, bodyB.ProjectID)
	assert.Equal(t, expectedB, *bodyB.ProjectID)

	assert.NotEqual(t, *bodyA.ProjectID, *bodyB.ProjectID,
		"two reporters must resolve to two different projects, or there is nothing here to isolate")
}

// TestFeedbackProjectRouteIsIdempotent is the property the whole endpoint
// exists to give a client: calling it repeatedly must keep answering with the
// same project, never growing a second one.
func TestFeedbackProjectRouteIsIdempotent(t *testing.T) {
	env := newFeedbackEnv(t)

	first := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	firstBody := feedbackProjectResponse{}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstBody))
	require.NotNil(t, firstBody.ProjectID)

	second := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	secondBody := feedbackProjectResponse{}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondBody))
	require.NotNil(t, secondBody.ProjectID)

	assert.Equal(t, *firstBody.ProjectID, *secondBody.ProjectID)
}

// TestFeedbackProjectRouteAnswersNullWithoutAResolvableOwner is the fail-safe
// direction: an unconfigured or unresolvable brazn.feedbackowner is not an
// error at the model layer (TestFeedbackIsNotProvisionedWithoutAResolvableOwner
// in brazn_feedback_test.go), and this route must carry that through as a
// plain 200 with no project, not surface it as a failure.
func TestFeedbackProjectRouteAnswersNullWithoutAResolvableOwner(t *testing.T) {
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

			rec := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			body := feedbackProjectResponse{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Nil(t, body.ProjectID)
		})
	}
}

// TestFeedbackProjectRouteAnswersNullOutsideManagedMode pins the same
// self-hosted-stays-untouched guarantee CreateNewProjectForUser gives at
// registration (pkg/models/project.go): an operator who has configured
// brazn.feedbackowner but never turned managed mode on must not have this
// route provision Percy Feedback behind their back.
func TestFeedbackProjectRouteAnswersNullOutsideManagedMode(t *testing.T) {
	env := newFeedbackEnv(t)
	setConfigForTest(t, config.BraznManagedMode, false)

	before := len(projectMemberships(t, testuser1.ID))

	rec := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := feedbackProjectResponse{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Nil(t, body.ProjectID)
	assert.Len(t, projectMemberships(t, testuser1.ID), before, "an unmanaged instance must provision nothing")
}
