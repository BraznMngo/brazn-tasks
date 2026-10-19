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

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1848: ONE asks GET /brazn/staff before it loads the functions that read
// and answer every customer's feedback. The answer must be exactly the fact the
// permission checks act on: membership of the team that holds the grant on the
// Feedback project.

const staffPath = "/api/v1/brazn/staff"

func staffAnswer(t *testing.T, env *managedEnv) bool {
	t.Helper()
	rec := env.request(http.MethodGet, staffPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Staff *bool `json:"staff"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Staff, "the answer must always carry staff, true or false")
	return *body.Staff
}

// seedStaffTeam runs the product's own seed, which creates the Feedback project
// and the team that holds its grant, and returns that team's id.
func seedStaffTeam(t *testing.T) int64 {
	t.Helper()
	models.SeedInstanceStaff()

	s := db.NewSession()
	defer s.Close()
	root, err := models.FeedbackProject(s)
	require.NoError(t, err)
	require.NotNil(t, root, "the seed must create the Feedback project, or the test proves nothing")
	grant := &models.TeamProject{}
	has, err := s.Where("project_id = ?", root.ProjectID).Get(grant)
	require.NoError(t, err)
	require.True(t, has, "the seed must grant a team access to Feedback")
	return grant.TeamID
}

func TestStaffRouteIsFalseOnAnInstanceWithoutFeedback(t *testing.T) {
	env := newPersonalEnv(t)
	assert.False(t, staffAnswer(t, env))
}

func TestStaffRouteIsFalseForSomebodyOutsideTheStaffTeam(t *testing.T) {
	env := newFeedbackEnv(t)
	seedStaffTeam(t)
	assert.False(t, staffAnswer(t, env),
		"an ordinary account on an instance with Feedback is not staff")
}

func TestStaffRouteIsTrueForAMemberOfTheTeamThatReachesFeedback(t *testing.T) {
	env := newFeedbackEnv(t)
	teamID := seedStaffTeam(t)

	s := db.NewSession()
	defer s.Close()
	_, err := s.Insert(&models.TeamMember{TeamID: teamID, UserID: testuser1.ID})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	assert.True(t, staffAnswer(t, env))
}
