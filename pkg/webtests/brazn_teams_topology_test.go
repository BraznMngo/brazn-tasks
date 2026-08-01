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
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestTeamsPolicyResolvesV2ProjectParameters exists because every other v2
// assertion in this file expects a refusal, and a refusal is exactly what a
// gate that resolved no project at all would also produce.
//
// If c.Param("project") came back empty on v2, projectID() would be 0,
// ProtectedRootOf(0) returns (nil, nil), and every one of those tests would
// still pass while the gate protected nothing. So these two ask v2 for
// something policy must ALLOW: they can only pass if the project parameter
// really resolved. Same shape as the ":project" / ":projectid" prefix bug, one
// level up.
func TestTeamsPolicyResolvesV2ProjectParameters(t *testing.T) {
	env, topology := newTeamsEnv(t)

	t.Run("a read-only link share under Public", func(t *testing.T) {
		rec := env.request(http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/shares", topology.underPublic), `{"permission":0}`, &testuser1)
		assertAllowed(t, rec)
	})

	t.Run("sharing team work with a Teams colleague", func(t *testing.T) {
		rec := env.request(http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/users", topology.underTeam), `{"username":"user6"}`, &testuser1)
		assertAllowed(t, rec)
	})
}

// TestTeamsPolicyRefusesABodyItCouldNotRead covers the two Teams rules where an
// unread field used to read as permission.
//
// Both were exploitable the same way: pad the body past the gate's read limit
// and the field it would have refused on simply was not there. The link share
// one is the sharpest - it granted the anonymous internet admin rights on a
// project, which is the precise inverse of "Public and its descendants alone
// permit anonymous read-only links".
func TestTeamsPolicyRefusesABodyItCouldNotRead(t *testing.T) {
	env, topology := newTeamsEnv(t)
	padding := strings.Repeat("a", managedBodyOverflow)

	t.Run("an admin link share hidden behind an oversized body", func(t *testing.T) {
		rec := env.request(http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/shares", topology.underPublic),
			fmt.Sprintf(`{"permission":2,"name":%q}`, padding), &testuser1)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

		// 400 rather than the 413 this deserves, and the response must not
		// mention a file: the shared error handler rewrites every 413 into a
		// file-size error, which is what refuseUnreadableBody sidesteps.
		assert.NotContains(t, rec.Body.String(), "file",
			"there is no file in this request")

		shares := env.request(http.MethodGet,
			fmt.Sprintf("/api/v1/projects/%d/shares", topology.underPublic), ``, &testuser1)
		require.Equal(t, http.StatusOK, shares.Code, shares.Body.String())
		assert.NotContains(t, shares.Body.String(), `"permission":2`,
			"no admin link share may exist on a project the policy protects")
	})

	// This is the load-bearing reparent case, and it is v1 on purpose.
	//
	// The same attempt over v2 PATCH does not discriminate: Huma's autopatch
	// serves a PATCH by merging and re-entering the root router with a PUT,
	// which passes through RequireManagedPolicy a second time. Against the bug
	// the outer PATCH allowed exactly as advertised, the inner PUT then took
	// the non-PATCH branch and refused, and the 403 surfaced - so the test
	// passed while the hole it named was wide open. v1 has no autopatch in
	// front of it, so nothing catches the request on the way back.
	t.Run("a Team root reparented under an Inbox behind an oversized body", func(t *testing.T) {
		rec := env.request(http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d", topology.teamRoot),
			fmt.Sprintf(`{"title":"Delivery","parent_project_id":%d,"description":%q}`,
				topology.inbox, padding), &testuser1)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

		project := env.request(http.MethodGet,
			fmt.Sprintf("/api/v1/projects/%d", topology.teamRoot), ``, &testuser1)
		require.Equal(t, http.StatusOK, project.Code, project.Body.String())
		assert.NotContains(t, project.Body.String(), fmt.Sprintf(`"parent_project_id":%d`, topology.inbox),
			"a Team root must not have been reparented under a member's Inbox")
	})

	t.Run("and the same over v2 PATCH", func(t *testing.T) {
		rec := env.request(http.MethodPatch,
			fmt.Sprintf("/api/v2/projects/%d", topology.underTeam),
			fmt.Sprintf(`{"parent_project_id":%d,"description":%q}`, topology.inbox, padding), &testuser1)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

		project := env.request(http.MethodGet,
			fmt.Sprintf("/api/v1/projects/%d", topology.underTeam), ``, &testuser1)
		require.Equal(t, http.StatusOK, project.Code, project.Body.String())
		assert.Contains(t, project.Body.String(), fmt.Sprintf(`"parent_project_id":%d`, topology.teamRoot),
			"the project must still sit under the Team root it was created in")
	})
}

// TestTeamsPolicyAllowsAnEmptyBodiedLinkShare is the one place the
// empty-versus-unreadable distinction changes an outcome.
//
// An unreadable body is refused because its fields are unknown. A body that is
// genuinely empty is not unknown - nothing can be hidden in zero bytes - and
// the link-share route legitimately takes one, meaning "read-only, no
// password". Collapsing the two would have broken this quietly, and every
// other link-share test sends an explicit permission, so nothing would have
// noticed.
func TestTeamsPolicyAllowsAnEmptyBodiedLinkShare(t *testing.T) {
	env, topology := newTeamsEnv(t)

	rec := env.request(http.MethodPut,
		fmt.Sprintf("/api/v1/projects/%d/shares", topology.underPublic), ``, &testuser1)
	assertAllowed(t, rec)
	assert.Contains(t, rec.Body.String(), `"permission":0`,
		"an unstated permission is read-only, which is what makes the share legal here")
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

// TestTeamsPolicyRefusesSharingAcrossOrganizations is the whole of the
// cross-tenant claim, stated as one pair of requests.
//
// "An active member of a Teams organization" is true of every customer on a
// shared instance, so the edition test alone let an administrator at one of
// them name a user at another and hand over a project. The target passed
// everything; it was a different organization.
//
// THE TWO CASES DIFFER IN EXACTLY ONE FIELD. It is the same account, named the
// same way, over the same route, with the same body, still active and still
// Teams - only the organization on its signed subject moves. That is what makes
// the refusal attributable: an assertion that a request was refused is worth
// nothing if some other guard was doing the refusing, and here the positive
// control is the same request that was just turned down. Nothing in front of
// the organization comparison can be responsible, because all of it passes on
// the second run.
//
// Deleting the comparison in requireEntitledTarget makes the first case fall
// through to nil and be allowed, which fails this test. That is the check that
// matters, and it is the reason the refusal is asserted here rather than by
// message: every managed refusal answers the same flat 403, so the wording
// never reaches the caller and asserting on it would prove nothing.
func TestTeamsPolicyRefusesSharingAcrossOrganizations(t *testing.T) {
	env, topology := newTeamsEnv(t)

	share := fmt.Sprintf("/api/v1/projects/%d/users", topology.underTeam)

	// user6 is granted into the caller's organization by the fixture. Move that
	// one account to a second customer on the same instance, and change nothing
	// else about it.
	env.revoke(testuser6.ID)
	env.grantInOrganization(testuser6.ID, entitlement.EditionTeams, false,
		managedOtherOrganization)

	// Refused first, so this request never reaches the handler and cannot leave
	// a share behind for the positive control to trip over.
	t.Run("an active Teams account at another organization is refused", func(t *testing.T) {
		rec := env.request(http.MethodPut, share, `{"username":"user6"}`, &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("and the identical request succeeds inside one organization", func(t *testing.T) {
		env.revoke(testuser6.ID)
		env.grant(testuser6.ID, entitlement.EditionTeams, false)

		rec := env.request(http.MethodPut, share, `{"username":"user6"}`, &testuser1)
		assertAllowed(t, rec)
	})
}

// TestTeamsPolicyClosesEveryCrossOrganizationRoute walks the alternate routes
// to the same outcome.
//
// requireEntitledTarget guards project sharing, team membership and admin
// promotion across both API versions, so a fix that closed only the route a
// browser happens to use would leave Percy and a raw API client with the hole.
// The promotion route is the one that names its account in the PATH rather than
// the body, which is a second source of the username and therefore a second way
// to get this wrong.
//
// user15 is the control, and it is why these refusals are not simply "policy
// refuses this route". It is granted alongside user6 and differs from it only
// by organization, so the membership route allowing user15 and refusing user6
// is the comparison stated once more on a different door.
func TestTeamsPolicyClosesEveryCrossOrganizationRoute(t *testing.T) {
	env, topology := newTeamsEnv(t)

	env.revoke(testuser6.ID)
	env.grantInOrganization(testuser6.ID, entitlement.EditionTeams, false,
		managedOtherOrganization)
	env.grant(testuser15.ID, entitlement.EditionTeams, false)

	for _, c := range []managedCase{
		{"a project cannot be shared across organizations (v1)", http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/users", topology.underTeam), `{"username":"user6"}`, http.StatusForbidden},
		{"a project cannot be shared across organizations (v2)", http.MethodPost,
			fmt.Sprintf("/api/v2/projects/%d/users", topology.underTeam), `{"username":"user6"}`, http.StatusForbidden},
		{"nor added to a team (v1)", http.MethodPut,
			fmt.Sprintf("/api/v1/teams/%d/members", teamsFixtureTeamID), `{"username":"user6"}`, http.StatusForbidden},
		{"nor added to a team (v2)", http.MethodPost,
			fmt.Sprintf("/api/v2/teams/%d/members", teamsFixtureTeamID), `{"username":"user6"}`, http.StatusForbidden},
		{"nor promoted inside one, where the path names the account", http.MethodPost,
			fmt.Sprintf("/api/v1/teams/%d/members/user6/admin", teamsFixtureTeamID), `{}`, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}

	// The same membership route, the same caller, an account that differs from
	// user6 only by organization. If team membership were refused for a reason
	// of its own, this would be refused too.
	t.Run("but a colleague can still be added to the same team", func(t *testing.T) {
		rec := env.request(http.MethodPut,
			fmt.Sprintf("/api/v1/teams/%d/members", teamsFixtureTeamID),
			`{"username":"user15"}`, &testuser1)
		assertAllowed(t, rec)
	})
}
