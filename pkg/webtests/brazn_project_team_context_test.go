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
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projectByID(projects []models.Project, id int64) *models.Project {
	for i := range projects {
		if projects[i].ID == id {
			return &projects[i]
		}
	}
	return nil
}

// TestProjectListTeamContext asserts team-root projects expose team context on
// list, including for descendants whose protected root sits on another page.
func TestProjectListTeamContext(t *testing.T) {
	env := newManagedEnv(t)
	env.grant(testuser1.ID, entitlement.EditionTeams, true)

	require.Equal(t, http.StatusOK,
		env.provision(personalInboxPayload(managedTopologySubject)).Code)

	refs := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))
	teamID := mustParseRef(t, refs.TaskTeamRef)
	rootID := mustParseRef(t, refs.TaskProjectRef)
	childID := env.newProject(&testuser1, "Child under team", rootID)

	rec := env.request(http.MethodGet, "/api/v1/projects", "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var projects []models.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))

	teamRoot := projectByID(projects, rootID)
	require.NotNil(t, teamRoot)
	assert.Equal(t, string(models.ProtectedKindTeamRoot), teamRoot.Role)
	assert.Equal(t, teamID, teamRoot.TeamID)
	assert.Equal(t, models.PrimaryTeamTitle, teamRoot.TeamName)

	child := projectByID(projects, childID)
	require.NotNil(t, child)
	assert.Equal(t, string(models.ProtectedKindTeamRoot), child.Role)
	assert.Equal(t, teamID, child.TeamID)
	assert.Equal(t, models.PrimaryTeamTitle, child.TeamName)
}

// TestProjectListInboxHasEmptyTeamContext asserts protected Inbox projects do
// not carry team-root fields on list.
func TestProjectListInboxHasEmptyTeamContext(t *testing.T) {
	env := newPersonalEnv(t)

	rec := env.request(http.MethodGet, "/api/v1/projects", "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var projects []models.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))

	inbox := projectByID(projects, fixtureInboxProjectID)
	require.NotNil(t, inbox)
	assert.Empty(t, inbox.Role)
	assert.Zero(t, inbox.TeamID)
	assert.Empty(t, inbox.TeamName)
}
