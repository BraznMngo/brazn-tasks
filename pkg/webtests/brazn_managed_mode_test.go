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
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/routes"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const managedClassificationPath = "../routes/route-classification.json"

type managedClassifiedRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Class   string `json:"class"`
	Managed string `json:"managed"`
}

func (r managedClassifiedRoute) key() string {
	return r.Method + " " + r.Path
}

// managedGuardedRoutes returns every route the classification marks as
// protected-topology or access-expanding.
func managedGuardedRoutes(t *testing.T) []managedClassifiedRoute {
	t.Helper()

	raw, err := os.ReadFile(managedClassificationPath)
	require.NoError(t, err)

	var file struct {
		Routes []managedClassifiedRoute `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))

	guarded := make([]managedClassifiedRoute, 0, 128)
	for _, route := range file.Routes {
		if route.Class == "protected-topology" || route.Class == "access-expanding" {
			guarded = append(guarded, route)
		}
	}
	require.NotEmpty(t, guarded)
	return guarded
}

// managedModeEcho builds the full route table with every route-registering
// feature switched on, so the sweep covers the whole surface an operator could
// expose rather than only the shipped defaults.
func managedModeEcho(t *testing.T, managed bool) *echo.Echo {
	t.Helper()

	_, err := setupTestEnv()
	require.NoError(t, err)

	for _, flag := range []config.Key{
		config.AuthLocalEnabled,
		config.AuthOpenIDEnabled,
		config.MigrationMicrosoftTodoEnable,
		config.MigrationTodoistEnable,
		config.MigrationTrelloEnable,
		config.ServiceEnableLinkSharing,
		config.ServiceEnableRegistration,
		config.ServiceEnableUserDeletion,
		config.WebhooksEnabled,
	} {
		setConfigForTest(t, flag, true)
	}
	setConfigForTest(t, config.ServiceTestingtoken, "managed-mode-harness")
	setConfigForTest(t, config.BraznManagedMode, managed)

	clearManagedTables(t)

	e := routes.NewEcho()
	routes.RegisterRoutes(e)
	return e
}

// setConfigForTest sets a config key and restores it afterwards. Viper
// overrides outlive InitDefaultConfig, so leaving one set would silently change
// the shape of every later test in this package.
func setConfigForTest(t *testing.T, key config.Key, value interface{}) {
	t.Helper()

	previous := key.Get()
	t.Cleanup(func() { key.Set(previous) })
	key.Set(value)
}

// clearManagedTables empties the managed-mode tables. They are deliberately not
// in the fixture set - nothing in the stock product touches them - so without
// this a projection written by one test would still be there for the next one,
// and the order tests happened to run in would decide the result.
func clearManagedTables(t *testing.T) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Exec("DELETE FROM brazn_protected_entities")
	require.NoError(t, err)
	_, err = s.Exec("DELETE FROM brazn_entitlement_projections")
	require.NoError(t, err)
	_, err = s.Exec("DELETE FROM brazn_provisioned_users")
	require.NoError(t, err)
	require.NoError(t, s.Commit())
}

// managedRequest makes a request with a token carrying NO entitlement, which is
// deliberate and is what every caller below wants: these tests are about a
// managed instance holding no projection for the caller.
//
// Since BRA-987 that is expressed by the token rather than by the table - the
// edition claim is stamped in at login, and an instance with no projection for
// a user mints a token without one. Both ends stay empty here (the tables are
// cleared by managedModeEcho), so what these assert has not changed: a guarded
// route refuses, and ordinary task work does not.
func managedRequest(t *testing.T, e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	token, err := auth.NewUserJWTAuthtoken(&testuser1, "test-session-id")
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// concreteURL turns a registered path template into a URL the router matches.
// Task 1 lives in project 1 in the fixtures, so substituting 1 everywhere also
// gives the task-move probes below a real task to reason about.
func concreteURL(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "1"
		}
	}
	return strings.Join(segments, "/")
}

// unreachableProjectID names a project that does not exist, so a request
// carrying it is unambiguously a move to somewhere else.
const unreachableProjectID = 9999

// currentTaskProjectID reads the project a task is actually in.
//
// The move tests restate that project to show restating it is not a move, and
// restating the wrong one would turn the request into a genuine move - the test
// would then pass while asserting the exact opposite of what it claims. A
// hardcoded id cannot notice when a fixture changes underneath it, so this is
// read, never assumed.
func currentTaskProjectID(t *testing.T, e *echo.Echo, taskID int64) int64 {
	t.Helper()

	rec := managedRequest(t, e, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", taskID), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var task struct {
		ProjectID int64 `json:"project_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &task))
	require.NotZero(t, task.ProjectID)
	return task.ProjectID
}

// guardedProbe returns a body that puts a route in its guarded meaning.
//
// Most guarded routes have only one meaning and an empty body is enough. The
// task routes have two - an edit and a move - and only the move is guarded, so
// the sweep has to actually ask for a move or it would be testing the wrong
// half of the route. The bulk route nests its destination and applies only the
// fields it names, so its probe is shaped the way the real API is: a probe that
// cheated by sending a flat project_id would pass even if the gate could not
// read a bulk move at all.
func guardedProbe(route managedClassifiedRoute) string {
	if route.Managed != "task-move" {
		return "{}"
	}
	return taskBody(route, unreachableProjectID, "")
}

// taskBody builds a task update body for either task route shape. A project of
// 0 means "change no project", which is what an ordinary edit looks like.
func taskBody(route managedClassifiedRoute, projectID int64, title string) string {
	if strings.HasSuffix(route.Path, "/bulk") {
		if projectID == 0 {
			return fmt.Sprintf(`{"task_ids":[1],"fields":["title"],"values":{"title":%q}}`, title)
		}
		return fmt.Sprintf(`{"task_ids":[1],"fields":["project_id"],"values":{"project_id":%d}}`, projectID)
	}
	if projectID == 0 {
		return fmt.Sprintf(`{"id":1,"title":%q}`, title)
	}
	return fmt.Sprintf(`{"id":1,"title":%q,"project_id":%d}`, title, projectID)
}

// TestManagedModeReachesEveryGuardedRoute fires a real request at every route
// the classification guards, through the real route table, with no entitlement
// projection present.
//
// It is the half of acceptance criterion 5 that a static check cannot give:
// a route can carry a policy rule and still never be reached, because the
// middleware was not attached to the group it lives on. Every guarded route
// must therefore answer with the gate's own refusal rather than the handler's
// ordinary response.
//
// What is asserted is that the gate *decided* the request - so each route is
// driven with a body that puts it in its guarded meaning, rather than with an
// empty one that would let a route with two meanings answer for the wrong half.
// No route is exempted by name: the routes with the subtlest behaviour are
// exactly the ones a by-name exemption would stop watching.
//
// Two routes per API version are excluded, and only these: login and the
// OpenID callback are pass-through by design (see managed_rules_core.go).
// They share their echo group with /register, which is asserted below, so the
// gate's presence on that group is still covered.
func TestManagedModeReachesEveryGuardedRoute(t *testing.T) {
	e := managedModeEcho(t, true)

	registered := make(map[string]bool)
	for _, route := range e.Router().Routes() {
		method := route.Method
		if method == "echo_route_any" {
			method = "ANY"
		}
		registered[method+" "+route.Path] = true
	}

	for _, route := range managedGuardedRoutes(t) {
		t.Run(route.key(), func(t *testing.T) {
			require.True(t, registered[route.key()],
				"guarded route is not registered under this feature configuration, so the sweep would pass vacuously")

			if route.Managed == "authentication" {
				t.Skip("authentication is pass-through by design; see managed_rules_core.go")
			}
			// Registration is gated by a signup token inside the handler and
			// not by this table (BRA-1071), so the middleware lets it through
			// and this sweep's generic probe cannot say anything useful about
			// it. What the sweep would have covered - the route is reachable
			// and refuses without a token - is covered by
			// TestManagedRegistrationIsGatedByASignupToken, which asserts the
			// stronger form: that no user is created either.
			if route.Managed == "signup-token" {
				t.Skip("signup-token is gated in the handler; see brazn_signup_token_test.go")
			}

			want := http.StatusForbidden
			if route.Managed == "disabled" {
				want = http.StatusNotFound
			}

			rec := managedRequest(t, e, route.Method, concreteURL(route.Path), guardedProbe(route))
			assert.Equalf(t, want, rec.Code,
				"rule %q should answer %d but answered %d - is RequireManagedPolicy attached to the group this route lives on?",
				route.Managed, want, rec.Code)
		})
	}
}

// TestManagedModeSeparatesATaskEditFromATaskMove is the companion assertion,
// and the reason the sweep above does not simply exempt the task routes.
//
// Those routes are the one place a single route carries both an ordinary and a
// guarded meaning, which makes them the likeliest place for an upstream change
// to widen what passes through. So both halves are pinned, driven off the
// classification file rather than a hand-written list: a task-move route added
// later is covered the day it is classified.
//
// Everything here runs with no projection at all. That is the strongest form of
// the claim - ordinary task work never needs entitlement to be readable - and
// it is acceptance criterion 4 stated as a test rather than as an intention.
func TestManagedModeSeparatesATaskEditFromATaskMove(t *testing.T) {
	e := managedModeEcho(t, true)

	for _, route := range managedGuardedRoutes(t) {
		if route.Managed != "task-move" {
			continue
		}

		t.Run(route.key(), func(t *testing.T) {
			currentProject := currentTaskProjectID(t, e, 1)

			t.Run("an edit that names no project passes through", func(t *testing.T) {
				rec := managedRequest(t, e, route.Method, concreteURL(route.Path),
					taskBody(route, 0, "edited with no entitlement at all"))
				assert.NotEqualf(t, http.StatusForbidden, rec.Code,
					"ordinary task work must survive a missing projection: %s", rec.Body.String())
			})

			// The v1 client sends the whole task back, project_id included.
			// If restating where a task already is counted as a move, every
			// edit made from a browser would need entitlement - and this test
			// is the only thing standing between that and a release.
			t.Run("an edit that restates the current project passes through", func(t *testing.T) {
				rec := managedRequest(t, e, route.Method, concreteURL(route.Path),
					taskBody(route, currentProject, "edited in place"))
				assert.NotEqualf(t, http.StatusForbidden, rec.Code,
					"restating the project a task is already in moves nothing: %s", rec.Body.String())
			})

			t.Run("but a move to another project is refused", func(t *testing.T) {
				rec := managedRequest(t, e, route.Method, concreteURL(route.Path),
					taskBody(route, unreachableProjectID, "moved"))
				assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			})
		})
	}
}

// TestManagedModeLeavesTheBodyForTheHandler covers the thing that would break
// silently: the gate reads request bodies to decide, and a body read once is
// gone unless it is put back. A handler that received an empty body would still
// answer, just wrongly, so this asserts the handler saw a field the gate never
// looks at.
func TestManagedModeLeavesTheBodyForTheHandler(t *testing.T) {
	e := managedModeEcho(t, true)

	rec := managedRequest(t, e, http.MethodPost, "/api/v1/tasks/1",
		`{"id":1,"title":"the gate must not eat this","description":"nor this"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the gate must not eat this")
	assert.Contains(t, rec.Body.String(), "nor this")
}

// TestManagedModeFailsClosedWithoutEntitlement states the rule the sweep above
// relies on in its own right: with no projection readable, everything the
// classification guards is refused. Nothing falls through to the handler
// because entitlement state happened to be unavailable.
func TestManagedModeFailsClosedWithoutEntitlement(t *testing.T) {
	e := managedModeEcho(t, true)

	rec := managedRequest(t, e, http.MethodPut, "/api/v1/projects", "{}")
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = managedRequest(t, e, http.MethodPut, "/api/v1/projects/1/shares", "{}")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestManagedModeOffLeavesRoutesAlone is the other side of the same coin: a
// self-hosted instance of this fork must behave exactly like stock Vikunja
// until an operator turns managed mode on. If this fails, the fork has changed
// the product for everyone who is not a Brazn customer.
func TestManagedModeOffLeavesRoutesAlone(t *testing.T) {
	e := managedModeEcho(t, false)

	token, err := auth.NewUserJWTAuthtoken(&testuser1, "test-session-id")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects",
		strings.NewReader(`{"title":"a project managed mode must not block"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}
