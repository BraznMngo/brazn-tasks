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
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
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

func managedRequest(t *testing.T, e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	token, err := auth.NewUserJWTAuthtoken(&testuser1, "test-session-id")
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// concreteURL turns a registered path template into a URL the router matches.
func concreteURL(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "1"
		}
	}
	return strings.Join(segments, "/")
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

			want := http.StatusForbidden
			if route.Managed == "disabled" {
				want = http.StatusNotFound
			}

			rec := managedRequest(t, e, route.Method, concreteURL(route.Path))
			assert.Equalf(t, want, rec.Code,
				"rule %q should answer %d but answered %d - is RequireManagedPolicy attached to the group this route lives on?",
				route.Managed, want, rec.Code)
		})
	}
}

// TestManagedModeFailsClosedWithoutEntitlement states the rule the sweep above
// relies on in its own right: with no projection readable, everything the
// classification guards is refused. Nothing falls through to the handler
// because entitlement state happened to be unavailable.
func TestManagedModeFailsClosedWithoutEntitlement(t *testing.T) {
	e := managedModeEcho(t, true)

	rec := managedRequest(t, e, http.MethodPut, "/api/v1/projects")
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = managedRequest(t, e, http.MethodPut, "/api/v1/projects/1/shares")
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
