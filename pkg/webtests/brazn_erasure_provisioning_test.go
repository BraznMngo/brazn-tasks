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
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/notifications"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// erase_subject over the real channel: signed, posted, and asserted against what
// this instance HOLDS afterwards rather than against a handler having been
// reached.
//
// The payload below is written out as a LITERAL and is deliberately not built
// from provisioning.OperationEraseSubject or from the payload struct's tags. An
// interop value that both the producer and its test import from one definition
// is checked by neither: a typo would sign, route and decode perfectly here and
// be refused by the only party that matters. What the commercial service emits
// is `{"operation":"erase_subject", ...}` with `organization_id` and `user_id`
// (cloud/service/src/fork.ts, createForkTaskBackendEraser), so that is what is
// typed here, character for character.
//
// Members are sorted by key, which is what the producer emits and therefore what
// the signature is made over.
func eraseSubjectPayload(organization, userID string) string {
	return `{"contract_version":"1","operation":"erase_subject","organization_id":"` +
		organization + `","user_id":"` + userID + `"}`
}

// erasureAccepted asserts the reply an erasure gets: 200, and `{}`.
//
// THE BODY IS CHECKED, not just the status. The consumer's transport cannot tell
// an empty 200 from a truncated one and refuses both alike, so a handler that
// answered 204 or an empty body would be refused by the caller it had just
// succeeded for - and every row assertion in this file would still pass.
func erasureAccepted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{}`, rec.Body.String())
}

// provisionSubjectToErase creates one subject through the channel's own
// create_user operation and gives them an entitlement projection, so the
// erasure below is asserted against rows this instance really wrote.
func provisionSubjectToErase(t *testing.T, env *managedEnv, mailbox string) int64 {
	t.Helper()

	created := provisioned(t, env.provision(createUserPayload(mailbox)))
	id, err := strconv.ParseInt(created.ID, 10, 64)
	require.NoError(t, err)

	env.grant(id, entitlement.EditionPersonal, false)
	db.AssertExists(t, "brazn_entitlement_projections",
		map[string]interface{}{"user_id": id}, false)
	db.AssertExists(t, "brazn_provisioned_users",
		map[string]interface{}{"user_id": id}, false)

	return id
}

func TestBraznErasureDestroysTheSubject(t *testing.T) {
	env := newManagedEnv(t)

	id := provisionSubjectToErase(t, env, "erasure-subject@example.com")

	erasureAccepted(t, env.provision(eraseSubjectPayload(managedTestOrganization,
		strconv.FormatInt(id, 10))))

	db.AssertMissing(t, "users", map[string]interface{}{"id": id})
	// BRA-1103 AC3. Both rows are already in relatedEntities - BRA-933 put the
	// projection there and BRA-1018 the claim - so this is a must-not-regress
	// rather than new work, and it is asserted here because THIS is the call the
	// commercial layer makes. The claim in particular has to go for the mailbox
	// to work again: a surviving row makes every later attempt to provision that
	// address fail forever, so a person who cancels could never come back.
	db.AssertMissing(t, "brazn_entitlement_projections", map[string]interface{}{"user_id": id})
	db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"user_id": id})
}

// TestBraznErasureRefusesALeadingZeroUserID pins the same aliasing gap
// RevokeSessionForSubject's own leading-zero test closes, for the operation
// where getting it wrong is irreversible. strconv.ParseInt("0"+id, ...) parses
// to the exact int64 a correct sender's bare id would, so without
// models.parseSubjectID's round-trip check a malformed subject would not read
// as absent - it would destroy the real, unrelated account underneath it.
func TestBraznErasureRefusesALeadingZeroUserID(t *testing.T) {
	env := newManagedEnv(t)

	id := provisionSubjectToErase(t, env, "erasure-leading-zero@example.com")

	rec := env.provision(eraseSubjectPayload(managedTestOrganization, "0"+strconv.FormatInt(id, 10)))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	db.AssertExists(t, "users", map[string]interface{}{"id": id}, false)
}

// TestBraznErasureAnswersASubjectAlreadyGoneWith200 is the acceptance criterion
// this operation exists to get right, and the one an obvious implementation
// fails.
//
// # WHY THIS TEST DOES NOT RUN UNDER notifications.Fake()
//
// models.DeleteUser is NOT idempotent. For an id with no row it reaches the
// account-deleted notification, whose ShouldNotify looks the user up and returns
// ErrUserDoesNotExist, and user_delete.go returns that BEFORE deleting the users
// row. notifications.Notify's FIRST line short-circuits on the test flag and
// returns nil, so under Fake() that entire chain is skipped and a test asserting
// idempotence would pass against code that has none. Fake() is process-global
// and other files set it, so undoing it here is required rather than tidy.
//
// # WHY IT MATTERS MORE THAN IT LOOKS
//
// On this channel every refusal is one flat 400, and the consumer maps a 400 to
// a non-retryable invalid_state. The erasure sequence is resumable and retries
// from the top, so a 400 here would leave every interrupted erasure failing at
// this step forever - against an Art. 12(3) one-month clock, while every earlier
// step went on succeeding.
//
// # THE GUARD
//
// Deleting `if !has { return nil }` from models.EraseSubject makes the second
// and third cases below fail with 400, and nothing else in the suite change.
// Reasoned rather than run: nothing in this repository may be executed on the
// development host, so CI is the only verifier.
func TestBraznErasureAnswersASubjectAlreadyGoneWith200(t *testing.T) {
	notifications.Unfake()
	t.Cleanup(notifications.Unfake)

	env := newManagedEnv(t)

	id := provisionSubjectToErase(t, env, "erasure-repeat@example.com")
	subject := strconv.FormatInt(id, 10)

	t.Run("the first erasure destroys them", func(t *testing.T) {
		erasureAccepted(t, env.provision(eraseSubjectPayload(managedTestOrganization, subject)))
		db.AssertMissing(t, "users", map[string]interface{}{"id": id})
	})

	t.Run("a repeat of the same erasure is still a success", func(t *testing.T) {
		// This is a resumed sequence retrying from the top after the fork had
		// already carried out step 5. The subject is gone, the call must say so
		// with a 200, and the erasure must be able to finish.
		erasureAccepted(t, env.provision(eraseSubjectPayload(managedTestOrganization, subject)))
	})

	t.Run("a subject that never existed is a success too", func(t *testing.T) {
		// Well-formed, and no row was ever written for it. Indistinguishable
		// from the case above at this seam, and it has to answer the same way.
		erasureAccepted(t, env.provision(eraseSubjectPayload(managedTestOrganization, "987654321")))
	})
}

func TestBraznErasureRefusesWhatItCannotAccept(t *testing.T) {
	env := newManagedEnv(t)

	// Each case is refused with the channel's one flat 400, and none of them is
	// the "already gone" case above.
	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			// decodeExactly refuses a member the payload type does not declare,
			// rather than accepting the request and silently doing something
			// narrower than it says. A team_id on an erasure is the clearest
			// case: it would read as "erase them from this team".
			"a member this build cannot see",
			`{"contract_version":"1","operation":"erase_subject","organization_id":"` +
				managedTestOrganization + `","team_id":"team_x","user_id":"1"}`,
		},
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"erase_subject","organization_id":"` +
				managedTestOrganization + `","user_id":"1"}`,
		},
		{
			// Not a decimal number, so it was never an id this instance could
			// have minted. Refused rather than answered 200: reporting an
			// erasure that could not have happened would mark the sequence
			// complete having destroyed nothing.
			"a subject id that is not one this instance could have minted",
			`{"contract_version":"1","operation":"erase_subject","organization_id":"` +
				managedTestOrganization + `","user_id":"not-a-number"}`,
		},
		{
			"a subject id past the length the contract bounds",
			`{"contract_version":"1","operation":"erase_subject","organization_id":"` +
				managedTestOrganization +
				`","user_id":"111111111111111111111111111111111111111111111111111111111111111111"}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			rec := env.provision(refused.payload)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

// TestBraznErasureIsNotCreatePersonalInbox pins the one property that makes
// erase_subject safe to carry on a channel it shares with three creating
// operations.
//
// The two payloads are field for field identical - one organization, one subject
// - so the operation member is the ONLY thing separating a request that gives
// somebody an Inbox from one that deletes their account. A build that treated
// the two names as interchangeable would route a destruction to a creation, or
// the reverse, and both payloads would decode cleanly either way.
func TestBraznErasureIsNotCreatePersonalInbox(t *testing.T) {
	env := newManagedEnv(t)

	id := provisionSubjectToErase(t, env, "erasure-not-inbox@example.com")
	subject := strconv.FormatInt(id, 10)

	// The same two identifiers, under the creating operation's name. The
	// assertion is that the SUBJECT SURVIVES it, and deliberately not what the
	// creating operation answered: whether an Inbox can be provisioned for this
	// particular account is brazn_topology_provisioning_test.go's question, and
	// asserting it here would couple this test to it for no gain. What must hold
	// is that a request naming create_personal_inbox never destroys anybody.
	env.provision(personalInboxPayload(subject))
	db.AssertExists(t, "users", map[string]interface{}{"id": id}, false)

	// And under the erasing one, the same two identifiers destroy them.
	erasureAccepted(t, env.provision(eraseSubjectPayload(managedTestOrganization, subject)))
	db.AssertMissing(t, "users", map[string]interface{}{"id": id})
}
