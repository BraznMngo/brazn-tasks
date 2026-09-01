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

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const feedbackProjectPath = "/api/v1/brazn/feedback/project"

type feedbackProjectResponse struct {
	Available bool   `json:"available"`
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
}

// TestFeedbackProjectRouteReturnsUnavailableWithoutTheStaffAccount is the
// BRA-1414 contract for an instance whose staff account has not been created:
// the route must answer available=false, not 500 and not a bare zero project
// id.
func TestFeedbackProjectRouteReturnsUnavailableWithoutTheStaffAccount(t *testing.T) {
	env := newPersonalEnv(t)

	_, err := user.GetUserByUsername(dbSessionForTest(t), models.OneAdminUsername)
	require.True(t, user.IsErrUserDoesNotExist(err),
		"this instance must not have the staff account, or the test proves nothing")

	rec := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body feedbackProjectResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.False(t, body.Available)
	assert.Zero(t, body.ProjectID)
	assert.NotEmpty(t, body.Message)
}

// TestFeedbackProjectRouteEnsuresAndReturnsTheCallersSubProject is the happy
// path: with an owner configured, the first call provisions and returns the
// sub-project id; a second call returns the same id.
func TestFeedbackProjectRouteEnsuresAndReturnsTheCallersSubProject(t *testing.T) {
	env := newFeedbackEnv(t)

	rec := env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var first feedbackProjectResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	require.True(t, first.Available)
	require.NotZero(t, first.ProjectID)
	assert.Equal(t, models.FeedbackSubProjectTitle(testuser1.Username), first.Title)

	rec = env.request(http.MethodGet, feedbackProjectPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var second feedbackProjectResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &second))
	assert.Equal(t, first.ProjectID, second.ProjectID,
		"a repeat resolve must find the same sub-project, not make another")
}
