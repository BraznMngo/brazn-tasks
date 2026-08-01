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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const entitlementIngestPath = "/api/v1/brazn/entitlements"

// projectionContractPrefix is written out here rather than taken from
// entitlement.SigningDomain, so what these tests sign is the contract's own
// input and not agreement with our constant. entitlement_test.go pins the two
// against each other; this file assumes neither.
const projectionContractPrefix = "percy.entitlement-projection.v2\n"

// canonicalSigned builds the signed member the way Percy's producer does: RFC
// 8785 canonical JSON, which for this contract's value domain means members
// sorted by key and nothing else. It is a literal template rather than a
// marshalled struct because the wire format is what is under test - Go would
// emit these members in declaration order, which is a different byte string
// that no producer will ever send.
func canonicalSigned(organization string, userID, revision int64, edition string, issuedAt time.Time) string {
	return fmt.Sprintf(
		`{"contract_version":"2","issued_at":%q,"revision":%d,`+
			`"state":{"edition":%q,"effective_state":"active","organization_admin":true,"seat_status":"active"},`+
			`"subject":{"organization_id":%q,"user_id":%q}}`,
		issuedAt.UTC().Format(time.RFC3339), revision, edition, organization,
		strconv.FormatInt(userID, 10),
	)
}

// envelopeSignedBy signs a signed member and splices it into an envelope
// verbatim, which is the one thing the producer must also do: the signature
// covers the octets as sent, so a re-serialization between signing and sending
// produces a message that looks right and verifies against nothing.
func envelopeSignedBy(key ed25519.PrivateKey, signed string) string {
	signature := ed25519.Sign(key, []byte(projectionContractPrefix+signed))
	return `{"signature":{"algorithm":"ed25519","key_id":"` + managedTestKeyID +
		`","value":"` + base64.RawURLEncoding.EncodeToString(signature) +
		`"},"signed":` + signed + `}`
}

func (env *managedEnv) envelopeAround(signed string) string {
	env.t.Helper()

	return envelopeSignedBy(env.signingKey, signed)
}

// projection is the ordinary case: an active Personal seat, issued now.
func (env *managedEnv) projection(userID, revision int64) string {
	env.t.Helper()

	return env.envelopeAround(canonicalSigned(
		managedTestOrganization, userID, revision, entitlement.EditionPersonal, time.Now(),
	))
}

// deliver posts one envelope the way the commercial service would: no session,
// no bearer token, nothing but the signed message.
func (env *managedEnv) deliver(envelope string) *httptest.ResponseRecorder {
	env.t.Helper()

	req := httptest.NewRequest(http.MethodPost, entitlementIngestPath, strings.NewReader(envelope))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

// storedProjection reads back what the endpoint actually wrote. Every
// assertion about a refusal is made against this rather than against a status
// code alone: the status is deliberately the same for every refusal, so the
// store is the only place that says which one happened.
func storedProjection(t *testing.T, userID int64) (*models.EntitlementProjection, bool) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	row := &models.EntitlementProjection{}
	has, err := s.Where("user_id = ?", userID).Get(row)
	require.NoError(t, err)
	return row, has
}

// sentSigned decodes what an envelope actually carries, so a test asserts its
// precondition on the bytes that decide the outcome rather than on the
// variables it built them from.
func sentSigned(t *testing.T, envelope string) *entitlement.Signed {
	t.Helper()

	var carried struct {
		Signed json.RawMessage `json:"signed"`
	}
	require.NoError(t, json.Unmarshal([]byte(envelope), &carried))

	signed := &entitlement.Signed{}
	require.NoError(t, json.Unmarshal(carried.Signed, signed))
	return signed
}

func TestEntitlementIngestAppliesAFirstProjection(t *testing.T) {
	env := newManagedEnv(t)

	_, has := storedProjection(t, testuser1.ID)
	require.False(t, has, "the subject must start with no projection, or this proves nothing")

	envelope := env.projection(testuser1.ID, 1)
	require.Equal(t, http.StatusNoContent, env.deliver(envelope).Code)

	row, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, int64(1), row.Revision)
	assert.Equal(t, managedTestOrganization, row.OrganizationID)
	assert.Equal(t, envelope, row.Envelope, "the envelope must be stored as received")

	// The row existing is not the property; the gate being able to use it is.
	// GetEntitlement is the one funnel every guarded route reads through, and it
	// re-verifies the stored bytes, so this also proves the endpoint stored
	// octets that still verify after a round trip through the database.
	s := dbSessionForTest(t)
	signed, err := models.GetEntitlement(s, testuser1.ID)
	require.NoError(t, err)
	assert.Equal(t, entitlement.EditionPersonal, signed.State.Edition)
	assert.True(t, signed.Active())
}

// TestEntitlementIngestIsIdempotentOnReplay covers the contract's AC1: delivery
// is at-least-once, so the same message arriving twice must leave one effect.
func TestEntitlementIngestIsIdempotentOnReplay(t *testing.T) {
	env := newManagedEnv(t)

	envelope := env.projection(testuser1.ID, 3)
	require.Equal(t, http.StatusNoContent, env.deliver(envelope).Code)

	first, has := storedProjection(t, testuser1.ID)
	require.True(t, has)

	// A replay is the same bytes again, not a fresh mint of the same state: a
	// regenerated envelope would carry a new issued_at and be a different
	// message at the same revision, which the contract forbids the producer.
	require.Equal(t, http.StatusNoContent, env.deliver(envelope).Code,
		"replaying a delivered projection must succeed, not error")

	second, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, first.ID, second.ID, "a replay must not create a second row")
	assert.Equal(t, first.Revision, second.Revision)
	assert.Equal(t, first.Envelope, second.Envelope)
}

// TestEntitlementIngestRefusesADecreasingRevision covers AC2: an older
// revision cannot overwrite newer state, however genuine its signature.
//
// Deleting `revision < ?` from the compare-and-set in ApplyEntitlement makes
// this fail - the lower revision would match the row and overwrite it, so the
// stored revision would come back as 4.
func TestEntitlementIngestRefusesADecreasingRevision(t *testing.T) {
	env := newManagedEnv(t)

	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 5)).Code)
	before, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	require.Equal(t, int64(5), before.Revision)

	older := env.projection(testuser1.ID, 4)
	require.Less(t, sentSigned(t, older).Revision, before.Revision,
		"the delivered revision must really be lower than the stored one, or this proves nothing")

	// Stale is not an error under the contract - the sender is behind, not
	// broken - so the reply says the message was processed. What must not
	// happen is a state change.
	require.Equal(t, http.StatusNoContent, env.deliver(older).Code)

	after, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, int64(5), after.Revision, "the older revision must not have been applied")
	assert.Equal(t, before.Envelope, after.Envelope)
}

// TestEntitlementIngestRefusesASecondOrganization proves the refusal is about
// the organization and nothing else.
//
// The second delivery carries a HIGHER revision than the stored one, so
// monotonicity cannot be what turns it down; and the control repeats that exact
// revision under the original organization and watches it apply. The only
// difference between the refused delivery and the accepted one is the
// organization, which is the whole claim.
//
// Deleting the organization from the compare-and-set's WHERE clause makes this
// fail: the higher revision would match the row and overwrite it, so the stored
// envelope would come back as the other organization's.
func TestEntitlementIngestRefusesASecondOrganization(t *testing.T) {
	env := newManagedEnv(t)

	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 1)).Code)
	before, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	require.Equal(t, managedTestOrganization, before.OrganizationID)

	elsewhere := env.envelopeAround(canonicalSigned(
		"org_somewhere_else", testuser1.ID, 2, entitlement.EditionTeams, time.Now(),
	))
	carried := sentSigned(t, elsewhere)
	require.NotEqual(t, before.OrganizationID, carried.Subject.OrganizationID,
		"the delivery must really name another organization, or this proves nothing")
	require.Greater(t, carried.Revision, before.Revision,
		"the delivery must be applicable on revision alone, or the refusal proves nothing about the organization")

	require.Equal(t, http.StatusBadRequest, env.deliver(elsewhere).Code)

	after, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, managedTestOrganization, after.OrganizationID)
	assert.Equal(t, before.Revision, after.Revision)
	assert.Equal(t, before.Envelope, after.Envelope)

	// Control: the same revision under the original organization applies, so
	// nothing about revision 2 was the problem.
	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 2)).Code)
	control, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	require.Equal(t, int64(2), control.Revision)
}

// TestEntitlementIngestRefusesAProjectionIssuedOutsideTheWindow stops a
// projection that is already stale from retiring a fresher predecessor.
//
// Deleting the Stale check in the handler makes this fail: the envelope is
// correctly signed and carries a higher revision, so nothing else would refuse
// it and the store would end up holding an envelope that reads as expired.
func TestEntitlementIngestRefusesAProjectionIssuedOutsideTheWindow(t *testing.T) {
	env := newManagedEnv(t)

	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 1)).Code)
	before, has := storedProjection(t, testuser1.ID)
	require.True(t, has)

	issuedAt := time.Now().Add(-entitlement.MaxAge()).Add(-24 * time.Hour)
	old := env.envelopeAround(canonicalSigned(
		managedTestOrganization, testuser1.ID, 2, entitlement.EditionPersonal, issuedAt,
	))
	carried := sentSigned(t, old)
	require.True(t, time.Since(carried.IssuedAt) > entitlement.MaxAge(),
		"the delivered projection must really be past the window, or this proves nothing")
	require.Greater(t, carried.Revision, before.Revision,
		"the delivery must be applicable on revision alone, or the refusal proves nothing about its age")

	require.Equal(t, http.StatusBadRequest, env.deliver(old).Code)

	after, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, before.Revision, after.Revision)
	assert.Equal(t, before.Envelope, after.Envelope)
}

// TestStoredProjectionExpiresWithTheFreshnessWindow is the other half of
// staleness, and the half the endpoint cannot cover: a projection that was
// fresh when it arrived and has since aged out.
//
// A valid signature would otherwise be trusted for the rest of the instance's
// life. Past the window a guarded operation must fail exactly as it does when
// no projection was ever applied, which is what ErrNoEntitlement means -
// GetEntitlement is the single funnel every guarded route reads through, so
// refusing here refuses everywhere.
//
// Deleting the Stale check in GetEntitlement makes this fail: the stored
// envelope verifies and its revision matches the row anchor, so nothing else
// would turn it down.
func TestStoredProjectionExpiresWithTheFreshnessWindow(t *testing.T) {
	env := newManagedEnv(t)

	expired := env.envelopeAround(canonicalSigned(
		managedTestOrganization, testuser1.ID, 1, entitlement.EditionPersonal,
		time.Now().Add(-entitlement.MaxAge()).Add(-time.Hour),
	))
	require.True(t, time.Since(sentSigned(t, expired).IssuedAt) > entitlement.MaxAge(),
		"the stored projection must really be past the window, or this proves nothing")
	env.storeProjection(testuser1.ID, 1, expired)

	fresh := env.envelopeAround(canonicalSigned(
		managedTestOrganization, testuser2.ID, 1, entitlement.EditionPersonal, time.Now(),
	))
	env.storeProjection(testuser2.ID, 1, fresh)

	s := dbSessionForTest(t)

	// Control first: the same shape, signed by the same key, differing only in
	// when it was issued.
	signed, err := models.GetEntitlement(s, testuser2.ID)
	require.NoError(t, err, "control: a fresh projection must still be readable")
	require.Equal(t, entitlement.EditionPersonal, signed.State.Edition)

	_, err = models.GetEntitlement(s, testuser1.ID)
	require.ErrorIs(t, err, models.ErrNoEntitlement)
}

// TestEntitlementIngestRefusesAnUndefinedEdition keeps an edition nobody has
// agreed on from being stored and then guessed at by a policy rule.
//
// Deleting the KnownEdition check in the handler makes this fail: the envelope
// is correctly signed, so it would be stored and the assertion that no row
// exists would not hold.
func TestEntitlementIngestRefusesAnUndefinedEdition(t *testing.T) {
	env := newManagedEnv(t)

	undefined := env.envelopeAround(canonicalSigned(
		managedTestOrganization, testuser1.ID, 1, "enterprise-cloud", time.Now(),
	))
	carried := sentSigned(t, undefined)
	require.NotContains(t,
		[]string{entitlement.EditionCommunity, entitlement.EditionPersonal, entitlement.EditionTeams},
		carried.State.Edition,
		"the delivered edition must really be outside the contract's enum, or this proves nothing")

	require.Equal(t, http.StatusBadRequest, env.deliver(undefined).Code)

	_, has := storedProjection(t, testuser1.ID)
	assert.False(t, has, "a projection this build cannot interpret must not be stored")

	// Control: the same envelope shape with an edition the contract defines is
	// accepted, so nothing else about it was the problem.
	require.Equal(t, http.StatusNoContent, env.deliver(env.envelopeAround(canonicalSigned(
		managedTestOrganization, testuser1.ID, 1, entitlement.EditionCommunity, time.Now(),
	))).Code)
	row, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	require.Equal(t, int64(1), row.Revision)
}

// TestEntitlementIngestRefusesASubjectThisInstanceDoesNotHave keeps a row from
// being written against a user id nothing resolves to, which every policy check
// would then read straight past.
func TestEntitlementIngestRefusesASubjectThisInstanceDoesNotHave(t *testing.T) {
	env := newManagedEnv(t)

	const absent int64 = 987654321
	_, has := storedProjection(t, absent)
	require.False(t, has)

	unknown := env.projection(absent, 1)
	require.Equal(t, strconv.FormatInt(absent, 10), sentSigned(t, unknown).Subject.UserID,
		"the delivery must really name the absent user, or this proves nothing")

	require.Equal(t, http.StatusBadRequest, env.deliver(unknown).Code)

	_, has = storedProjection(t, absent)
	assert.False(t, has)

	// Control: the identical delivery for a user this instance does have is
	// applied, so the subject is what was refused.
	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 1)).Code)
}

// TestEntitlementIngestWritesNothingForAnUntrustedSigner is the ordering the
// contract puts first: verification happens before the stored revision is read,
// let alone written, so an unauthenticated caller cannot reach the store at all.
//
// The signature here is genuine and over the right octets - it is simply from a
// key this instance does not trust, which is what makes it an authentication
// test rather than a malformed-input test.
func TestEntitlementIngestWritesNothingForAnUntrustedSigner(t *testing.T) {
	env := newManagedEnv(t)

	signed := canonicalSigned(
		managedTestOrganization, testuser1.ID, 1, entitlement.EditionPersonal, time.Now(),
	)

	// Control: the same signed member, signed by the trusted key, applies.
	require.Equal(t, http.StatusNoContent, env.deliver(env.envelopeAround(signed)).Code)
	before, has := storedProjection(t, testuser1.ID)
	require.True(t, has)

	// A second key pair, generated here and deliberately never configured. It
	// must not go anywhere near the trust store: adding it would make this
	// envelope verify and the test would assert nothing.
	_, untrusted, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	require.NotEqual(t, env.signingKey, untrusted,
		"the two keys must really differ, or this proves nothing")

	// Signed correctly, over the right octets, under the same key id - and by a
	// key this instance has no reason to believe. It also carries a higher
	// revision, so nothing but the signature can be what refuses it.
	forged := envelopeSignedBy(untrusted, canonicalSigned(
		managedTestOrganization, testuser1.ID, 2, entitlement.EditionTeams, time.Now(),
	))
	require.Greater(t, sentSigned(t, forged).Revision, before.Revision,
		"the delivery must be applicable on revision alone, or this proves nothing about the signature")
	require.Equal(t, http.StatusBadRequest, env.deliver(forged).Code)

	after, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, before.Revision, after.Revision, "an untrusted signature must change nothing")
	assert.Equal(t, before.Envelope, after.Envelope)
}
