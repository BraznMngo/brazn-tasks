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

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personalInboxID is the fixture project used as user1's protected Inbox.
const personalInboxID = 1

func newPersonalEnv(t *testing.T) *managedEnv {
	t.Helper()

	env := newManagedEnv(t)
	env.grant(testuser1.ID, entitlement.EditionPersonal, false)
	env.protect(models.ProtectedKindInbox, personalInboxID, 0)
	return env
}

// TestPersonalPolicyRefusesEveryTopologyChange walks the routes a personal
// account could use to become something other than one protected Inbox.
//
// Each is attempted on both API versions where both exist. A restriction that
// held on one and not the other would be a frontend-shaped restriction wearing
// a server's clothes: Percy and a raw API client do not go through the same
// version the browser does.
func TestPersonalPolicyRefusesEveryTopologyChange(t *testing.T) {
	env := newPersonalEnv(t)

	inbox := fmt.Sprintf("%d", personalInboxID)
	for _, c := range []managedCase{
		{"create a second project (v1)", http.MethodPut, "/api/v1/projects", `{"title":"second"}`, http.StatusForbidden},
		{"create a second project (v2)", http.MethodPost, "/api/v2/projects", `{"title":"second"}`, http.StatusForbidden},
		{"nest a project under the Inbox (v1)", http.MethodPut, "/api/v1/projects", `{"title":"child","parent_project_id":` + inbox + `}`, http.StatusForbidden},
		{"duplicate the Inbox (v1)", http.MethodPut, "/api/v1/projects/" + inbox + "/duplicate", `{}`, http.StatusForbidden},
		{"duplicate the Inbox (v2)", http.MethodPost, "/api/v2/projects/" + inbox + "/duplicate", `{}`, http.StatusForbidden},
		{"rename the Inbox (v1)", http.MethodPost, "/api/v1/projects/" + inbox, `{"title":"not the Inbox"}`, http.StatusForbidden},
		{"rename the Inbox (v2)", http.MethodPatch, "/api/v2/projects/" + inbox, `{"title":"not the Inbox"}`, http.StatusForbidden},
		{"delete the Inbox (v1)", http.MethodDelete, "/api/v1/projects/" + inbox, ``, http.StatusForbidden},
		{"delete the Inbox (v2)", http.MethodDelete, "/api/v2/projects/" + inbox, ``, http.StatusForbidden},
		{"share the Inbox with a user (v1)", http.MethodPut, "/api/v1/projects/" + inbox + "/users", `{"username":"user2"}`, http.StatusForbidden},
		{"share the Inbox with a user (v2)", http.MethodPost, "/api/v2/projects/" + inbox + "/users", `{"username":"user2"}`, http.StatusForbidden},
		{"share the Inbox with a team (v1)", http.MethodPut, "/api/v1/projects/" + inbox + "/teams", `{"team_id":1}`, http.StatusForbidden},
		{"share the Inbox with a team (v2)", http.MethodPost, "/api/v2/projects/" + inbox + "/teams", `{"team_id":1}`, http.StatusForbidden},
		{"publish a link share (v1)", http.MethodPut, "/api/v1/projects/" + inbox + "/shares", `{"permission":0}`, http.StatusForbidden},
		{"publish a link share (v2)", http.MethodPost, "/api/v2/projects/" + inbox + "/shares", `{"permission":0}`, http.StatusForbidden},
		{"create a team (v1)", http.MethodPut, "/api/v1/teams", `{"name":"a team"}`, http.StatusForbidden},
		{"create a team (v2)", http.MethodPost, "/api/v2/teams", `{"name":"a team"}`, http.StatusForbidden},
		{"rename a team (v1)", http.MethodPost, "/api/v1/teams/1", `{"name":"renamed"}`, http.StatusForbidden},
		{"join a team (v1)", http.MethodPut, "/api/v1/teams/1/members", `{"username":"user1"}`, http.StatusForbidden},
		{"join a team (v2)", http.MethodPost, "/api/v2/teams/1/members", `{"username":"user1"}`, http.StatusForbidden},
		{"import from Todoist (v1)", http.MethodPost, "/api/v1/migration/todoist/migrate", `{"code":"x"}`, http.StatusNotFound},
		{"import a Vikunja file (v1)", http.MethodPut, "/api/v1/migration/vikunja-file/migrate", `{}`, http.StatusNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}
}

// TestPersonalPolicyKeepsPercyFeedbackUsable pins the exemption to exactly one
// project.
//
// The two destinations are owned by the same user and carry the same
// permissions, so the only thing that differs is whether the project is
// registered as Percy Feedback. If the exemption ever widened into a general
// "system project" category, the second case would start passing.
func TestPersonalPolicyKeepsPercyFeedbackUsable(t *testing.T) {
	env := newPersonalEnv(t)

	feedback := env.newProject(&testuser1, "Percy Feedback", 0)
	unregistered := env.newProject(&testuser1, "Somewhere else entirely", 0)
	env.protect(models.ProtectedKindFeedback, feedback, 0)

	t.Run("a task can be submitted to Percy Feedback", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("the same move anywhere else is refused", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/2",
			fmt.Sprintf(`{"id":2,"title":"task #2 done","project_id":%d}`, unregistered), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("Percy Feedback itself cannot be renamed or shared", func(t *testing.T) {
		rec := env.request(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d", feedback),
			`{"title":"mine now"}`, &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code)

		rec = env.request(http.MethodPut, fmt.Sprintf("/api/v1/projects/%d/users", feedback),
			`{"username":"user2"}`, &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

// TestPersonalPolicyKeepsAnotherAccountsInboxUnreachable states the isolation
// rule directly. Ownership is what decides it, so no administrator flag and no
// membership can turn this into a yes.
func TestPersonalPolicyKeepsAnotherAccountsInboxUnreachable(t *testing.T) {
	env := newPersonalEnv(t)

	otherInbox := env.newProject(&testuser2, "Inbox", 0)
	env.protect(models.ProtectedKindInbox, otherInbox, 0)

	rec := env.request(http.MethodPost, "/api/v1/tasks/1",
		fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, otherInbox), &testuser1)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// TestPersonalPolicyLeavesOrdinaryTaskWorkAlone is the criterion that stops
// managed mode from becoming a kill switch: a request that does not move a task
// between projects is ordinary work, and it keeps working - including when no
// entitlement can be read at all, which is what an outage looks like from
// inside the instance.
func TestPersonalPolicyLeavesOrdinaryTaskWorkAlone(t *testing.T) {
	env := newPersonalEnv(t)

	t.Run("with a valid projection", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			`{"id":1,"title":"renamed while entitled"}`, &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	// The v1 client sends the whole task back rather than a patch, so this is
	// what an ordinary rename actually looks like on the wire. The project is
	// read rather than hardcoded: naming a different one would make this a
	// genuine move, and the test would pass while pinning the opposite of what
	// it claims.
	t.Run("when the client sends the project back unchanged", func(t *testing.T) {
		currentProject := currentTaskProjectID(t, env.e, 1)

		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"renamed in place","project_id":%d}`, currentProject),
			&testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("with no projection at all", func(t *testing.T) {
		env.revoke(testuser1.ID)

		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			`{"id":1,"title":"renamed during an outage"}`, &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
}

// TestPersonalPolicyFailsClosedWithoutEntitlement is the other half of the
// same coin. Losing entitlement state must never be a way to get more than the
// policy allows.
func TestPersonalPolicyFailsClosedWithoutEntitlement(t *testing.T) {
	env := newPersonalEnv(t)
	feedback := env.newProject(&testuser1, "Percy Feedback", 0)
	env.protect(models.ProtectedKindFeedback, feedback, 0)
	env.revoke(testuser1.ID)

	for _, c := range []managedCase{
		{"create a project", http.MethodPut, "/api/v1/projects", `{"title":"second"}`, http.StatusForbidden},
		{"share the Inbox", http.MethodPut, "/api/v1/projects/1/users", `{"username":"user2"}`, http.StatusForbidden},
		{"publish a link share", http.MethodPut, "/api/v1/projects/1/shares", `{"permission":0}`, http.StatusForbidden},
		{"withdraw a share", http.MethodDelete, "/api/v1/projects/1/users/user2", ``, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := env.request(c.method, c.path, c.body, &testuser1)
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
		})
	}

	t.Run("even a move that policy would otherwise allow", func(t *testing.T) {
		rec := env.request(http.MethodPost, "/api/v1/tasks/1",
			fmt.Sprintf(`{"id":1,"title":"task #1","project_id":%d}`, feedback), &testuser1)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}

// TestPersonalPolicyRejectsATamperedProjection covers the case where the
// database is writable but the projection is not the one Brazn signed: a
// genuine older envelope put back in place of a newer one. The stored revision
// is the anchor, so the signature being valid is not enough.
func TestPersonalPolicyRejectsATamperedProjection(t *testing.T) {
	env := newManagedEnv(t)
	env.protect(models.ProtectedKindInbox, personalInboxID, 0)

	env.grant(testuser1.ID, entitlement.EditionPersonal, false)

	s := dbSessionForTest(t)
	_, err := s.Where("user_id = ?", testuser1.ID).
		Cols("revision").
		Update(&models.EntitlementProjection{Revision: 2})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	rec := env.request(http.MethodDelete, "/api/v1/projects/1/users/user2", ``, &testuser1)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
