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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

const managedTestKeyID = "brazn-test-key"

// managedEnv is an instance running in managed mode, with a signing key this
// test owns, so a projection can be granted or taken away and the policy
// observed from outside - the same way a customer, Percy or a raw API client
// would see it.
type managedEnv struct {
	t          *testing.T
	e          *echo.Echo
	signingKey ed25519.PrivateKey
}

func newManagedEnv(t *testing.T) *managedEnv {
	t.Helper()

	e := managedModeEcho(t, true)

	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	setConfigForTest(t, config.BraznEntitlementKeys,
		managedTestKeyID+":"+base64.StdEncoding.EncodeToString(public))

	env := &managedEnv{t: t, e: e, signingKey: private}
	env.reset()
	return env
}

// reset clears the managed-mode tables. They are deliberately not in the
// fixture set - nothing in the stock product touches them - so each test that
// writes to them empties them first.
func (env *managedEnv) reset() {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Exec("DELETE FROM brazn_protected_entities")
	require.NoError(env.t, err)
	_, err = s.Exec("DELETE FROM brazn_entitlement_projections")
	require.NoError(env.t, err)
	require.NoError(env.t, s.Commit())
}

// grant writes a correctly signed projection for a user.
func (env *managedEnv) grant(userID int64, edition string, organizationAdmin bool) {
	env.t.Helper()

	signed, err := json.Marshal(entitlement.Signed{
		ContractVersion: entitlement.ContractVersion,
		Subject: entitlement.Subject{
			OrganizationID: "org_test",
			UserID:         "usr_test",
		},
		Revision: 1,
		IssuedAt: time.Now().UTC(),
		State: entitlement.State{
			Edition:           edition,
			SeatStatus:        "active",
			OrganizationAdmin: organizationAdmin,
			EffectiveState:    "active",
		},
	})
	require.NoError(env.t, err)

	envelope, err := json.Marshal(map[string]interface{}{
		"signed": json.RawMessage(signed),
		"signature": map[string]string{
			"key_id":    managedTestKeyID,
			"algorithm": "ed25519",
			"value":     base64.StdEncoding.EncodeToString(ed25519.Sign(env.signingKey, signed)),
		},
	})
	require.NoError(env.t, err)

	env.storeProjection(userID, 1, string(envelope))
}

func (env *managedEnv) storeProjection(userID, revision int64, envelope string) {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Insert(&models.EntitlementProjection{
		UserID:   userID,
		Revision: revision,
		Envelope: envelope,
	})
	require.NoError(env.t, err)
	require.NoError(env.t, s.Commit())
}

// revoke removes a user's projection, which is what "entitlement state is
// missing" looks like from the gate's side.
func (env *managedEnv) revoke(userID int64) {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Where("user_id = ?", userID).Delete(&models.EntitlementProjection{})
	require.NoError(env.t, err)
	require.NoError(env.t, s.Commit())
}

// protect gives a project its role in the managed topology.
func (env *managedEnv) protect(kind models.ProtectedKind, projectID, teamID int64) {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	require.NoError(env.t, models.RegisterProtectedProject(s, kind, projectID, teamID))
	require.NoError(env.t, s.Commit())
}

// newProject creates a project the way the product does, so tests measure
// policy against real rows rather than hand-built ones.
func (env *managedEnv) newProject(owner *user.User, title string, parent int64) int64 {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	project := &models.Project{Title: title}
	if parent > 0 {
		project.ParentProjectID = models.Ptr(parent)
	}
	require.NoError(env.t, project.Create(s, owner))
	require.NoError(env.t, s.Commit())
	return project.ID
}

func (env *managedEnv) request(method, path, body string, as *user.User) *httptest.ResponseRecorder {
	env.t.Helper()

	token, err := auth.NewUserJWTAuthtoken(as, "test-session-id")
	require.NoError(env.t, err)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

// dbSessionForTest opens a session that stays usable for the rest of the test.
func dbSessionForTest(t *testing.T) *xorm.Session {
	t.Helper()

	s := db.NewSession()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// managedCase is one attempt and the answer policy must give it.
type managedCase struct {
	name   string
	method string
	path   string
	body   string
	want   int
}
