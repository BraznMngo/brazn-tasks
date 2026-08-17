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
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The GET /brazn/projects/{id}/public-root read model (BRA-1343), observed
// over real HTTP through the real route table, the same way the Organization
// area is in brazn_organization_test.go.
//
// EVERY TEST HERE IS ABOUT WHAT THE ROUTE ANSWERS, not about what a frontend
// draws from it. The frontend's job - disabling the link-share toggle when
// this comes back false - is not checkable from here; what is checkable, and
// load-bearing, is that the answer agrees with decideTeamsLinkShare's own
// criterion (managed_rules_teams.go): only the Public root and its
// descendants may ever be shared by link.

func publicRootPath(projectID int64) string {
	return fmt.Sprintf("/api/v1/brazn/projects/%d/public-root", projectID)
}

type publicRootResponse struct {
	UnderPublicRoot bool `json:"under_public_root"`
}

// TestProjectPublicRootMatchesWhatLinkShareWouldAllow is the property that
// matters: for every place in the Teams topology, this read model must agree
// with decideTeamsLinkShare about whether a link share would be allowed there.
//
// WHAT MAKES IT FAIL: change the `root.Kind == models.ProtectedKindPublicRoot`
// comparison in BraznGetProjectRoot to also accept ProtectedKindTeamRoot (or
// any other kind), and the Team-root cases below go green for the wrong
// reason while a real link-share attempt on them still 403s.
func TestProjectPublicRootMatchesWhatLinkShareWouldAllow(t *testing.T) {
	env, topology := newTeamsEnv(t)

	for _, c := range []struct {
		name      string
		projectID int64
		want      bool
	}{
		{"the Public root itself", topology.publicRoot, true},
		{"a project under the Public root", topology.underPublic, true},
		{"a Team root", topology.teamRoot, false},
		{"a project under a Team root", topology.underTeam, false},
		{"a member's own Inbox", topology.inbox, false},
		{"an unprovisioned project", topology.stray, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(http.MethodGet, publicRootPath(c.projectID), "", &testuser1)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			body := publicRootResponse{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, c.want, body.UnderPublicRoot)
		})
	}
}

// TestProjectPublicRootRefusesSomeoneWithNoAccessToTheProject is the
// permission half: this is an ordinary project read, not an organization-admin
// surface like /brazn/organization, but it is still gated on being able to
// read the project at all. A personal account on the same instance, granted no
// access to the Teams organization's Public root, must not learn anything
// about it - including whether it is public.
//
// WHAT MAKES IT FAIL: drop the `project.CanRead` check in BraznGetProjectRoot,
// and this 403 becomes a 200 revealing `under_public_root` to a caller with no
// standing to ask.
func TestProjectPublicRootRefusesSomeoneWithNoAccessToTheProject(t *testing.T) {
	env, topology := newTeamsEnv(t)

	rec := env.request(http.MethodGet, publicRootPath(topology.publicRoot), "", &testuser2)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// TestProjectPublicRootAnswersFalseForAnUnknownEdition is the default the
// frontend relies on: useManagedCapabilities never asks this question outside
// Teams, but the route itself does not special-case edition either - it just
// reports the topology fact, which is `false` for anyone whose project was
// never registered as a Public root, managed instance or not.
func TestProjectPublicRootAnswersFalseForAnUnknownEdition(t *testing.T) {
	env, topology := newTeamsEnv(t)

	rec := env.request(http.MethodGet, publicRootPath(topology.stray), "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := publicRootResponse{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.False(t, body.UnderPublicRoot)
}
