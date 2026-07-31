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
	"net/http/httptest"
	"testing"

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
)

// teamsTopology is a provisioned organization: one member Inbox, one Public
// root, one Team root, a subproject under each, and one project that was never
// provisioned - which is what an unmanaged structure looks like from inside.
type teamsTopology struct {
	inbox       int64
	publicRoot  int64
	teamRoot    int64
	underPublic int64
	underTeam   int64
	stray       int64
}

const teamsFixtureTeamID = 1

func newTeamsEnv(t *testing.T) (*managedEnv, teamsTopology) {
	t.Helper()

	env := newManagedEnv(t)
	env.grant(testuser1.ID, entitlement.EditionTeams, true)
	env.grant(testuser6.ID, entitlement.EditionTeams, false)
	// A personal account on the same instance, so "cannot receive an
	// invitation" is measured against a real account rather than a missing one.
	env.grant(testuser2.ID, entitlement.EditionPersonal, false)

	topology := teamsTopology{inbox: fixtureInboxProjectID}
	env.protect(models.ProtectedKindInbox, topology.inbox, 0)

	topology.publicRoot = env.newProject(&testuser1, "Public", 0)
	env.protect(models.ProtectedKindPublicRoot, topology.publicRoot, 0)

	topology.teamRoot = env.newProject(&testuser1, "Delivery", 0)
	env.protect(models.ProtectedKindTeamRoot, topology.teamRoot, teamsFixtureTeamID)

	topology.underPublic = env.newProject(&testuser1, "Handbook", topology.publicRoot)
	topology.underTeam = env.newProject(&testuser1, "Sprint 4", topology.teamRoot)
	topology.stray = env.newProject(&testuser1, "Never provisioned", 0)

	return env, topology
}

// assertAllowed states that policy did not refuse. The exact success code is
// the handler's business; what these tests are about is the decision in front
// of it.
func assertAllowed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	assert.Lessf(t, rec.Code, http.StatusBadRequest, "policy refused: %s", rec.Body.String())
}

// TestTeamsPolicyRefusesUnmanagedTopLevelProjects is the criterion stated
// plainly. A project has to have a home, and the only homes are the roots the
// organization was provisioned with.
func TestTeamsPolicyRefusesUnmanagedTopLevelProjects(t *testing.T) {
	env, topology := newTeamsEnv(t)

	for _, c := range []managedCase{
		{"at the top level (v1)", http.MethodPut, "/api/v1/projects", `{"title":"loose"}`, http.StatusForbidden},
		{"at the top level (v2)", http.MethodPost, "/api/v2/projects", `{"title":"loose"}`, http.StatusForbidden},
		{"under an unprovisioned project", http.MethodPut, "/api/v1/projects",
			fmt.Sprintf(`{"title":"loose","parent_project_id":%d}`, topology.stray), http.StatusForbidden},
		{"under a member's Inbox", http.MethodPut, "/api/v1/projects",
			fmt.Sprintf(`{"title":"loose","parent_project_id":%d}`, topology.inbox), http.StatusForbidden},
		{"by duplicating the Public root", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/duplicate", topology.publicRoot),
			fmt.Sprintf(`{"parent_project_id":%d}`, topology.publicRoot), http.StatusForbidden},
		{"by duplicating to the top level", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/duplicate", topology.underTeam), `{}`, http.StatusForbidden},
		{"by importing", http.MethodPost, "/api/v1/migration/todoist/migrate", `{"code":"x"}`, http.StatusNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}

	t.Run("but beneath a Public root it is allowed", func(t *testing.T) {
		rec := env.request(http.MethodPut, "/api/v1/projects",
			fmt.Sprintf(`{"title":"a page","parent_project_id":%d}`, topology.publicRoot), &testuser1)
		assertAllowed(t, rec)
	})

	t.Run("and beneath a project that already sits under a Team root", func(t *testing.T) {
		rec := env.request(http.MethodPut, "/api/v1/projects",
			fmt.Sprintf(`{"title":"a workstream","parent_project_id":%d}`, topology.underTeam), &testuser1)
		assertAllowed(t, rec)
	})
}

// TestTeamsPolicyProtectsRootsButNotTheWorkUnderThem draws the line the ticket
// draws: a team names its own root and manages its own subprojects, and none of
// the roots can be moved out from under anyone.
func TestTeamsPolicyProtectsRootsButNotTheWorkUnderThem(t *testing.T) {
	env, topology := newTeamsEnv(t)

	t.Run("a team may rename its own root", func(t *testing.T) {
		rec := env.request(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d", topology.teamRoot),
			`{"title":"Delivery and Platform"}`, &testuser1)
		assertAllowed(t, rec)
	})

	t.Run("and rename an ordinary subproject", func(t *testing.T) {
		rec := env.request(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d", topology.underTeam),
			fmt.Sprintf(`{"title":"Sprint 5","parent_project_id":%d}`, topology.teamRoot), &testuser1)
		assertAllowed(t, rec)
	})

	// A full v1 update that omits parent_project_id detaches the project to the
	// top level - upstream treats omitted and 0 alike - so a rename that leaves
	// it out is a move, and policy has to read it as one. Reading absence as
	// "leave it alone" would have let any client walk a project out of the
	// topology just by not mentioning where it lives.
	t.Run("but a rename that would silently detach it is refused", func(t *testing.T) {
		rec := env.request(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d", topology.underTeam),
			`{"title":"Sprint 5"}`, &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	// PATCH is the one method that genuinely merges, so there the same rename
	// keeps the project where it is.
	t.Run("while the same rename over PATCH keeps its place", func(t *testing.T) {
		rec := env.request(http.MethodPatch, fmt.Sprintf("/api/v2/projects/%d", topology.underTeam),
			`{"title":"Sprint 6"}`, &testuser1)
		assertAllowed(t, rec)
	})

	t.Run("and delete an ordinary subproject", func(t *testing.T) {
		rec := env.request(http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d", topology.underPublic),
			``, &testuser1)
		assertAllowed(t, rec)
	})

	for _, c := range []managedCase{
		{"the Public root cannot be renamed", http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.publicRoot), `{"title":"Private now"}`, http.StatusForbidden},
		{"the Inbox cannot be renamed", http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.inbox), `{"title":"Outbox"}`, http.StatusForbidden},
		{"the Team root cannot be moved", http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.teamRoot),
			fmt.Sprintf(`{"title":"Delivery","parent_project_id":%d}`, topology.publicRoot), http.StatusForbidden},
		{"the Public root cannot be deleted", http.MethodDelete,
			fmt.Sprintf("/api/v1/projects/%d", topology.publicRoot), ``, http.StatusForbidden},
		{"the Team root cannot be deleted", http.MethodDelete,
			fmt.Sprintf("/api/v1/projects/%d", topology.teamRoot), ``, http.StatusForbidden},
		{"the Inbox cannot be deleted", http.MethodDelete,
			fmt.Sprintf("/api/v1/projects/%d", topology.inbox), ``, http.StatusForbidden},
		{"a subproject cannot be moved out to the top level", http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.underTeam),
			`{"title":"Sprint 4","parent_project_id":0}`, http.StatusForbidden},
		{"an unprovisioned project stays untouchable", http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.stray), `{"title":"still loose"}`, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}
}

// TestTeamsPolicyLimitsLinkSharesToPublic covers the one place the product
// hands something to the anonymous internet, and how far that reaches.
func TestTeamsPolicyLimitsLinkSharesToPublic(t *testing.T) {
	env, topology := newTeamsEnv(t)

	t.Run("the Public root and its descendants can be shared read-only", func(t *testing.T) {
		rec := env.request(http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.underPublic), `{"permission":0}`, &testuser1)
		assertAllowed(t, rec)
	})

	for _, c := range []managedCase{
		{"but not with write access", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.underPublic), `{"permission":1}`, http.StatusForbidden},
		{"nor a team's work", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.underTeam), `{"permission":0}`, http.StatusForbidden},
		{"nor a Team root", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.teamRoot), `{"permission":0}`, http.StatusForbidden},
		{"nor an Inbox", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.inbox), `{"permission":0}`, http.StatusForbidden},
		{"nor an Inbox on v2 either", http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/shares", topology.inbox), `{"permission":0}`, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}
}

// TestTeamsPolicyKeepsInboxesPrivate is the criterion that no member and no
// organization administrator reaches someone else's Inbox. user1 holds
// organization_admin in this fixture, so every refusal below is one an admin
// received.
func TestTeamsPolicyKeepsInboxesPrivate(t *testing.T) {
	env, topology := newTeamsEnv(t)

	otherInbox := env.newProject(&testuser2, "Inbox", 0)
	env.protect(models.ProtectedKindInbox, otherInbox, 0)

	for _, c := range []managedCase{
		{"an Inbox cannot be shared with a user", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", topology.inbox), `{"username":"user6"}`, http.StatusForbidden},
		{"an Inbox cannot be shared with a team", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/teams", topology.inbox),
			fmt.Sprintf(`{"team_id":%d}`, teamsFixtureTeamID), http.StatusForbidden},
		{"and neither can someone else's", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", otherInbox), `{"username":"user6"}`, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}

	t.Run("a task cannot be pushed into another member's Inbox", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, otherInbox), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("but a member can move their own task into shared work", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, topology.underTeam), &testuser1)
		assertAllowed(t, rec)
	})
}

// TestTeamsPolicyRefusesInvitingAPersonalAccount is the receiving half of "a
// personal account can neither send nor receive shares". The refusal happens
// where the invitation is issued, so no alternate route reaches the account.
func TestTeamsPolicyRefusesInvitingAPersonalAccount(t *testing.T) {
	env, topology := newTeamsEnv(t)

	t.Run("a Teams colleague can be given access", func(t *testing.T) {
		rec := env.request(http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", topology.underTeam), `{"username":"user6"}`, &testuser1)
		assertAllowed(t, rec)
	})

	for _, c := range []managedCase{
		{"a personal account cannot be shared with (v1)", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", topology.underTeam), `{"username":"user2"}`, http.StatusForbidden},
		{"a personal account cannot be shared with (v2)", http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/users", topology.underTeam), `{"username":"user2"}`, http.StatusForbidden},
		{"nor added to a team (v1)", http.MethodPut,
			fmt.Sprintf("/api/v1/teams/%d/members", teamsFixtureTeamID), `{"username":"user2"}`, http.StatusForbidden},
		{"nor added to a team (v2)", http.MethodPost,
			fmt.Sprintf("/api/v2/teams/%d/members", teamsFixtureTeamID), `{"username":"user2"}`, http.StatusForbidden},
		{"nor promoted inside one", http.MethodPost,
			fmt.Sprintf("/api/v1/teams/%d/members/user2/admin", teamsFixtureTeamID), `{}`, http.StatusForbidden},
		{"nor an account with no projection at all", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", topology.underTeam), `{"username":"user10"}`, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}
}
