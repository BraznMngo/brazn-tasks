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

	"code.vikunja.io/api/pkg/config"
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
	return canonicalSignedFor(
		organization, strconv.FormatInt(userID, 10), revision, edition, issuedAt,
	)
}

// canonicalSignedFor is canonicalSigned for a subject.user_id that is not a
// local row id. The contract calls the field opaque and constrains it only by
// character class, so a test needs to be able to put a well-formed projection
// naming an unresolvable subject on the wire.
func canonicalSignedFor(organization, subject string, revision int64, edition string, issuedAt time.Time) string {
	return fmt.Sprintf(
		`{"contract_version":"2","issued_at":%q,"revision":%d,`+
			`"state":{"edition":%q,"effective_state":"active","organization_admin":true,"seat_status":"active"},`+
			`"subject":{"organization_id":%q,"user_id":%q}}`,
		issuedAt.UTC().Format(time.RFC3339), revision, edition, organization, subject,
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

// entitlementFor reads a user's projection the way a guarded route does, in its
// own short session.
//
// A session per read rather than one held for the test: these tests interleave
// reads with writes made over HTTP on another connection, and a transaction
// opened before those writes would keep reading the snapshot it started with.
func entitlementFor(t *testing.T, userID int64) (*entitlement.Signed, error) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	return models.GetEntitlement(s, userID)
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

	assert.WithinDuration(t, time.Now(), row.RevisionReceived, time.Minute,
		"the receipt clock must be stamped for audit")

	// The row existing is not the property; the gate being able to use it is.
	// GetEntitlement is the one funnel every guarded route reads through, and it
	// re-verifies the stored bytes, so this also proves the endpoint stored
	// octets that still verify after a round trip through the database.
	signed, err := entitlementFor(t, testuser1.ID)
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

// TestEntitlementIngestAcceptsNothingWithoutAConfiguredKey is the foundation of
// the security argument for a route that authenticates the message and not the
// caller: brazn.entitlementkeys ships empty (pkg/config/config.go), so a
// self-hosted instance of this fork that nobody has configured accepts no
// projection at all and the route is inert rather than open.
//
// This is not the untrusted-signer test wearing a different hat. That one names
// a key id the instance holds and signs with the wrong key; this one holds no
// key at all, which is the state every operator who has never heard of Brazn
// runs in.
func TestEntitlementIngestAcceptsNothingWithoutAConfiguredKey(t *testing.T) {
	env := newManagedEnv(t)

	// Control: with this instance's key trusted, a delivery applies.
	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 1)).Code)
	applied, has := storedProjection(t, testuser1.ID)
	require.True(t, has)

	setConfigForTest(t, config.BraznEntitlementKeys, "")
	require.Empty(t, config.BraznEntitlementKeys.GetString(),
		"the trust store must really be empty, or this proves nothing")

	// Nothing new is stored.
	require.Equal(t, http.StatusBadRequest, env.deliver(env.projection(testuser2.ID, 1)).Code)
	_, has = storedProjection(t, testuser2.ID)
	assert.False(t, has, "an instance trusting no key must store nothing")

	// And nothing already stored can be moved, either.
	require.Equal(t, http.StatusBadRequest,
		env.deliver(env.projection(testuser1.ID, applied.Revision+1)).Code)
	after, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, applied.Revision, after.Revision)
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

// localUserExists reports whether the instance holds a user row, which is the
// precondition the erased-subject case actually turns on. The projections table
// is the wrong place to ask: ApplyEntitlement decides on the USERS table, and a
// test that established its precondition anywhere else would be asserting it
// somewhere other than where it decides the outcome.
func localUserExists(t *testing.T, userID int64) bool {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Table("users").Where("id = ?", userID).Count()
	require.NoError(t, err)
	return count > 0
}

// countProjections counts the WHOLE projections table rather than one subject's
// row, and that distinction is the only thing making the erased-subject test
// mean anything. See the mutation analysis on it.
func countProjections(t *testing.T) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	count, err := s.Table("brazn_entitlement_projections").Count()
	require.NoError(t, err)
	return count
}

// TestEntitlementIngestSucceedsForAnErasedSubject is what keeps account erasure
// from deadlocking, and the failure it prevents has no recovery short of hand
// intervention on production data.
//
// Erasure (BRA-805) is resumable and its acknowledgement guard is fail-closed,
// so a retry after this instance's user row is already gone re-delivers the
// closure projection for a subject nobody holds any more. While that answered
// 400, the producer read a rejection and stopped: the customer's tasks were
// already destroyed, the commercial record was never redacted, and every
// subsequent retry reproduced the same rejection for ever. A refusal here is
// the one answer with no way back, so the contract requires a success.
//
// DELETING THE `if !subjectExists` BRANCH IN ApplyEntitlement MAKES THIS FAIL
// AT THE COUNT, and deliberately not at the status assertion - which is the
// entire reason the count is here. With the guard gone, subjectUserID still
// returns no error for an erased subject, so userID falls through as 0: the
// compare-and-set matches nothing, the read finds nothing, and the INSERT then
// SUCCEEDS, because user_id is `bigint not null unique` with no foreign key
// and 0 satisfies all of that. The endpoint answers 204 exactly as it does
// now, while retaining the erased subject's organization, edition, seat status
// and timestamps in a row at user_id = 0.
//
// So every assertion keyed on 987654321 passes against the mutated code, and
// an earlier version of this test made exactly that mistake. The difference
// has to be asserted on the TABLE, because the broken code writes to a key the
// test never thinks to ask about. Counting the table also catches a row
// written under any other unexpected id, which is the general form of the bug.
//
// The malformed control is the half that would rot quietly. "Succeed whenever
// the subject does not resolve" is one relaxation too far: a user_id that was
// never a local reference at all means a broken producer rather than an erased
// account, and it must still be refused.
func TestEntitlementIngestSucceedsForAnErasedSubject(t *testing.T) {
	env := newManagedEnv(t)

	const erased int64 = 987654321
	require.False(t, localUserExists(t, erased),
		"the subject must really be gone from this instance, or this proves nothing")

	before := countProjections(t)

	closure := env.projection(erased, 1)
	require.Equal(t, strconv.FormatInt(erased, 10), sentSigned(t, closure).Subject.UserID,
		"the delivery must really name the erased user, or this proves nothing")

	require.Equal(t, http.StatusNoContent, env.deliver(closure).Code,
		"a projection for an erased subject must be answered successfully, or erasure deadlocks")

	// A retry is what actually reaches this instance, delivery being
	// at-least-once, so the success has to survive repetition rather than hold
	// once.
	require.Equal(t, http.StatusNoContent, env.deliver(closure).Code)

	assert.Equal(t, before, countProjections(t),
		"an erased subject must leave NO row behind - not under its own id, and not under any other")

	// Control: a subject that is not a local user reference at all is still
	// refused, so the success above is about an erased user and not about
	// leniency toward anything that fails to resolve.
	malformed := env.envelopeAround(canonicalSignedFor(
		managedTestOrganization, "usr_5b1e8c04a927", 1, entitlement.EditionPersonal, time.Now(),
	))
	require.Equal(t, http.StatusBadRequest, env.deliver(malformed).Code,
		"a user_id that was never a local id is a broken producer, not an erased account")
	assert.Equal(t, before, countProjections(t),
		"a refused delivery must be a refusal in the store too, not only in the status")

	// Control: a subject this instance does have still applies, so the endpoint
	// is not simply answering 204 to everything and the count is not simply
	// frozen.
	require.True(t, localUserExists(t, testuser1.ID))
	require.Equal(t, http.StatusNoContent, env.deliver(env.projection(testuser1.ID, 1)).Code)
	row, has := storedProjection(t, testuser1.ID)
	require.True(t, has)
	assert.Equal(t, int64(1), row.Revision)
	assert.Equal(t, before+1, countProjections(t))
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
