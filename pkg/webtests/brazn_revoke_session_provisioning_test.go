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
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// revoke_session over the real channel: signed, posted, and asserted against
// what this instance HOLDS afterwards rather than against a handler having
// been reached. The payload is a literal, character for character, for the
// same reason eraseSubjectPayload's is: an interop value both the producer
// and this test imported from one definition would be checked by neither.
func revokeSessionPayload(userID, sessionID string) string {
	return `{"contract_version":"1","operation":"revoke_session","session_id":"` +
		sessionID + `","user_id":"` + userID + `"}`
}

// revocationAccepted asserts the reply a revocation gets: 200, and `{}` -
// matching erasureAccepted's own reasoning. The consumer's transport cannot
// tell an empty 200 from a truncated one and refuses both alike.
func revocationAccepted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{}`, rec.Body.String())
}

// createSessionForTest inserts a session the way a real OAuth exchange would
// (models.CreateSession), so the tests below revoke a row the product
// actually writes rather than one hand-built to merely look like it.
func createSessionForTest(t *testing.T, userID int64) string {
	t.Helper()

	s := dbSessionForTest(t)
	session, err := models.CreateSession(s, userID, "test-device", "127.0.0.1", false, nil)
	require.NoError(t, err)
	require.NoError(t, s.Commit())
	return session.ID
}

func mustParseSubject(t *testing.T, id string) int64 {
	t.Helper()

	parsed, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	return parsed
}

func TestBraznRevokeSessionDeletesTheSession(t *testing.T) {
	env := newManagedEnv(t)

	created := provisioned(t, env.provision(createUserPayload("revoke-subject@example.com")))
	sessionID := createSessionForTest(t, mustParseSubject(t, created.ID))
	db.AssertExists(t, "sessions", map[string]interface{}{"id": sessionID}, false)

	revocationAccepted(t, env.provision(revokeSessionPayload(created.ID, sessionID)))
	db.AssertMissing(t, "sessions", map[string]interface{}{"id": sessionID})
}

// TestBraznRevokeSessionAnswersASessionAlreadyGoneWith200 is the acceptance
// criterion this operation exists to get right, matching
// TestBraznErasureAnswersASubjectAlreadyGoneWith200's own reasoning: the
// commercial service retries a revocation whose response it may have lost,
// and a retry must be able to commit rather than fail against a row this
// instance already removed.
//
// THE GUARD: deleting `if !has { return nil }`'s equivalent - the "zero rows
// affected is not an error" property DeleteSessionForUser documents - would
// make the second and third cases below fail, and nothing else in the suite
// would notice, exactly as the erasure ticket's own comment describes.
func TestBraznRevokeSessionAnswersASessionAlreadyGoneWith200(t *testing.T) {
	env := newManagedEnv(t)

	created := provisioned(t, env.provision(createUserPayload("revoke-repeat@example.com")))
	sessionID := createSessionForTest(t, mustParseSubject(t, created.ID))

	t.Run("the first revocation removes it", func(t *testing.T) {
		revocationAccepted(t, env.provision(revokeSessionPayload(created.ID, sessionID)))
		db.AssertMissing(t, "sessions", map[string]interface{}{"id": sessionID})
	})

	t.Run("a repeat of the same revocation is still a success", func(t *testing.T) {
		revocationAccepted(t, env.provision(revokeSessionPayload(created.ID, sessionID)))
	})

	t.Run("a session that never existed is a success too", func(t *testing.T) {
		revocationAccepted(t, env.provision(
			revokeSessionPayload(created.ID, "00000000-0000-0000-0000-000000000000")))
	})
}

// TestBraznRevokeSessionCannotReachAnotherUsersSession pins the pairing
// DeleteSessionForUser's own comment states: both the session id and the user
// id have to agree for the delete to run, in either direction. Without the
// user id in the same WHERE, a caller naming its own id and a session that
// belongs to somebody else would remove it.
func TestBraznRevokeSessionCannotReachAnotherUsersSession(t *testing.T) {
	env := newManagedEnv(t)

	owner := provisioned(t, env.provision(createUserPayload("revoke-owner@example.com")))
	stranger := provisioned(t, env.provision(createUserPayload("revoke-stranger@example.com")))
	sessionID := createSessionForTest(t, mustParseSubject(t, owner.ID))

	// The stranger's own id, naming the owner's session. Answered 200 - a
	// session that does not belong to the named user is exactly as "not
	// found" as one that never existed - and the row survives it.
	revocationAccepted(t, env.provision(revokeSessionPayload(stranger.ID, sessionID)))
	db.AssertExists(t, "sessions", map[string]interface{}{"id": sessionID}, false)

	revocationAccepted(t, env.provision(revokeSessionPayload(owner.ID, sessionID)))
	db.AssertMissing(t, "sessions", map[string]interface{}{"id": sessionID})
}

// TestBraznRevokeSessionRefusesALeadingZeroUserID pins the fix for the
// aliasing gap ParseInt's leniency opens: "01" parses to the same int64 a
// correct sender's bare "1" would, so without a round-trip check a malformed
// subject does not read as absent, it reads as the REAL user underneath it -
// and here that would delete a session belonging to someone the caller never
// named correctly.
func TestBraznRevokeSessionRefusesALeadingZeroUserID(t *testing.T) {
	env := newManagedEnv(t)

	created := provisioned(t, env.provision(createUserPayload("revoke-leading-zero@example.com")))
	sessionID := createSessionForTest(t, mustParseSubject(t, created.ID))

	rec := env.provision(revokeSessionPayload("0"+created.ID, sessionID))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	db.AssertExists(t, "sessions", map[string]interface{}{"id": sessionID}, false)
}

func TestBraznRevokeSessionRefusesWhatItCannotAccept(t *testing.T) {
	env := newManagedEnv(t)

	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			"a member this build cannot see",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"1","extra":"x"}`,
		},
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"1"}`,
		},
		{
			"a user id that is not one this instance could have minted",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"not-a-number"}`,
		},
		{
			"a session id outside the identifier class",
			`{"contract_version":"1","operation":"revoke_session",` +
				`"session_id":"550e8400 e29b 41d4","user_id":"1"}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			rec := env.provision(refused.payload)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}
