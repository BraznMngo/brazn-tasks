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
	"strconv"
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

// managedTestOrganization is the organization every projection these tests
// grant belongs to. Named once because it is half of the contract's subject
// key: the stored row records it, and a delivery for a different organization
// is refused against it.
const managedTestOrganization = "org_test"

// fixtureInboxProjectID is the fixture project user1 owns, used as their
// protected Inbox in managed-mode tests.
const fixtureInboxProjectID = 1

// managedBodyOverflow is comfortably past the bounded prefix the gate reads
// (routes.maxManagedBodyPeek, 64 KiB and unexported). The exact number is not
// the point of any test that uses it: the property is that a body the gate
// could not fully read is refused, at whatever size the caller picked.
const managedBodyOverflow = 96 * 1024

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

	// managedModeEcho has already emptied the managed-mode tables, so this
	// instance starts with no topology and no entitlement of any kind.
	return &managedEnv{t: t, e: e, signingKey: private}
}

// grant writes a correctly signed projection for a user, with no end date -
// the ordinary case of a subscription in good standing.
func (env *managedEnv) grant(userID int64, edition string, organizationAdmin bool) {
	env.t.Helper()

	env.grantUntil(userID, edition, organizationAdmin, nil)
}

// grantUntil is the same with a validity window that ends. `validTo` is a
// pointer because the contract distinguishes "no end recorded" from an end, and
// so does everything downstream of it.
func (env *managedEnv) grantUntil(
	userID int64,
	edition string,
	organizationAdmin bool,
	validTo *time.Time,
) {
	env.t.Helper()

	signed, err := json.Marshal(entitlement.Signed{
		ContractVersion: entitlement.ContractVersion,
		Subject: entitlement.Subject{
			OrganizationID: managedTestOrganization,
			UserID:         strconv.FormatInt(userID, 10),
		},
		Revision: 1,
		IssuedAt: time.Now().UTC(),
		State: entitlement.State{
			Edition:           edition,
			SeatStatus:        "active",
			OrganizationAdmin: organizationAdmin,
			EffectiveState:    "active",
			// Comfortably in the past, so no test below is accidentally about
			// a window that has not opened yet.
			ValidFrom: time.Now().UTC().Add(-24 * time.Hour),
			ValidTo:   validTo,
		},
	})
	require.NoError(env.t, err)

	envelope, err := json.Marshal(map[string]interface{}{
		"signed": json.RawMessage(signed),
		"signature": map[string]string{
			"key_id":    managedTestKeyID,
			"algorithm": "ed25519",
			// Signed over the contract's domain-separated input, the same as
			// the commercial service does. entitlement.SigningInput is the one
			// definition of those bytes, so a signer here and the verifier
			// cannot drift; that the bytes match the contract is pinned
			// separately, against a literal, in the entitlement package's own
			// tests.
			//
			// base64url without padding, which is the encoding the contract
			// fixes for signature.value and the one Percy's producer emits.
			"value": base64.RawURLEncoding.EncodeToString(
				ed25519.Sign(env.signingKey, entitlement.SigningInput(signed))),
		},
	})
	require.NoError(env.t, err)

	env.storeProjection(userID, 1, string(envelope))
}

func (env *managedEnv) storeProjection(userID, revision int64, envelope string) {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	// RevisionReceived is the audit clock, stamped here as the endpoint stamps
	// it so a fixture row looks like a delivered one.
	_, err := s.Insert(&models.EntitlementProjection{
		UserID:           userID,
		OrganizationID:   managedTestOrganization,
		Revision:         revision,
		RevisionReceived: time.Now(),
		Envelope:         envelope,
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

// request signs in as `as` the way the product does, which means the token is
// minted from whatever projection this instance holds at that moment.
//
// The ORDERING is load-bearing for every test below and was not before: grant
// then request carries an entitlement, revoke then request does not, and a test
// that wants to ask what a token outlives has to mint it first and change the
// projection after - see requestWith.
func (env *managedEnv) request(method, path, body string, as *user.User) *httptest.ResponseRecorder {
	env.t.Helper()

	return env.requestWith(method, path, body, env.tokenFor(as))
}

// tokenFor mints the session token a login would hand this user right now,
// through the production path, so a test cannot accidentally assert against a
// token shape the product does not issue.
func (env *managedEnv) tokenFor(as *user.User) string {
	env.t.Helper()

	s := db.NewSession()
	defer s.Close()

	token, err := auth.NewEntitledUserJWTAuthtoken(s, as, "test-session-id")
	require.NoError(env.t, err)
	return token
}

// requestWith makes the request with a token the caller already holds, which is
// how a test separates "what this instance would grant now" from "what this
// token was granted when it was issued".
func (env *managedEnv) requestWith(
	method, path, body, token string,
) *httptest.ResponseRecorder {
	env.t.Helper()

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
