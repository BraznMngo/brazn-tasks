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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resolve_mailbox operation (BRA-1099), asserted through the endpoint,
// because everything this operation promises is a property of what goes on the
// wire: which of two stored addresses comes back, whether two absences are
// distinguishable, and which status code carries an absence.
//
// mailboxNeverMinted is an id this instance does not have and never did, which
// is one half of the indistinguishability claim below. 99999 is the value the
// topology tests use for the same purpose.
const mailboxNeverMinted = "99999"

// The vendored conformance fixtures. Constants rather than paths built at call
// time, which is the form gosec resolves and the one golden_test.go already
// uses. See testdata/mailbox/README.md for what they are and why a copy of the
// canonical set lives here.
const (
	mailboxRequestFixture      = "testdata/mailbox/mailbox-resolution-request.valid.conformance.json"
	mailboxResolvedFixture     = "testdata/mailbox/mailbox-resolution-response.valid.conformance-resolved.json"
	mailboxUnresolvableFixture = "testdata/mailbox/mailbox-resolution-response.valid.conformance-unresolvable.json"
)

// mailboxResolution is the reply the consumer is written against
// (cloud/contracts/types.ts, MailboxResolutionResponse). It is redeclared here
// rather than imported so the test asserts the WIRE shape - the same reason
// provisionedUser and teamTopologyRefs are redeclared beside it.
type mailboxResolution struct {
	Result string `json:"result"`
	Email  string `json:"email"`
}

// resolveMailboxPayload is a resolve_mailbox request in canonical JSON - members
// sorted by key, which is what a producer emits and therefore what the signature
// is made over.
func resolveMailboxPayload(userID string) string {
	return `{"contract_version":"1","operation":"resolve_mailbox","organization_id":"` +
		managedTestOrganization + `","user_id":"` + userID + `"}`
}

// resolvedMailbox reads a 200 answer and returns it.
//
// The status is required rather than asserted, because every claim these tests
// make is about the BODY of a 200: an operation that answered 400 would leave
// every assertion below reading a refusal sentence.
func resolvedMailbox(t *testing.T, rec *httptest.ResponseRecorder) mailboxResolution {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := mailboxResolution{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// mailboxMembers reads a reply as the bare set of members it carries, which is
// how the absence of one is asserted. A struct cannot express "and nothing
// else": an `email` this build should never have emitted would land in a
// declared field and read as an ordinary empty string.
func mailboxMembers(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()

	members := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &members))
	return members
}

// mailboxFixtures reads the vendored conformance set as the wire values it
// describes.
//
// The three paths are read from constants at their own call sites rather than
// through a path parameter, which is the form gosec resolves - the same reason
// golden_test.go names its five artifacts as constants.
//
// THE TRAILING NEWLINE IS TRIMMED, and that is not cosmetic. Each file ends in
// one because it is a file; the wire value is the JSON object alone.
// entitlement.VerifyEnvelope slices `signed` out of the envelope from its
// opening brace to its matching close, so a signature made over the bytes
// INCLUDING that newline would cover something the verifier never sees, and the
// conformance request would be refused for a reason that has nothing to do with
// what is under test. Everything INSIDE each object - the indentation and the
// newlines between members - is carried through untouched, which is the point:
// this build has to accept the producer's formatting rather than a canonical
// form of it.
func mailboxFixtures(t *testing.T) (request, resolvedReply, unresolvableReply []byte) {
	t.Helper()

	var err error
	request, err = os.ReadFile(mailboxRequestFixture)
	require.NoError(t, err)
	resolvedReply, err = os.ReadFile(mailboxResolvedFixture)
	require.NoError(t, err)
	unresolvableReply, err = os.ReadFile(mailboxUnresolvableFixture)
	require.NoError(t, err)

	return bytes.TrimSpace(request),
		bytes.TrimSpace(resolvedReply),
		bytes.TrimSpace(unresolvableReply)
}

// assertEmitted compares what this build put on the wire against a fixture.
//
// Both sides are compacted first, because the fixture is indented for a human
// and echo emits compact JSON with a trailing newline. Compaction removes
// insignificant whitespace and NOTHING ELSE - it does not reorder members, drop
// them or change a value - so member order, the exact member set and every
// literal are still compared byte for byte.
func assertEmitted(t *testing.T, fixture []byte, rec *httptest.ResponseRecorder) {
	t.Helper()

	expected := &bytes.Buffer{}
	require.NoError(t, json.Compact(expected, fixture))

	actual := &bytes.Buffer{}
	require.NoError(t, json.Compact(actual, bytes.TrimSpace(rec.Body.Bytes())))

	assert.Equal(t, expected.String(), actual.String(),
		"the contract's fixture is what a conforming consumer is written against")
}

// TestBraznResolveMailboxAnswersWithTheAddressOnTheUserRow is the assertion this
// operation exists to satisfy, and the one a fork-side implementation is most
// likely to fail.
//
// THE FIXTURE MAKES THE TWO STORED ADDRESSES DIFFER, deliberately. This instance
// holds the mailbox in two places - brazn_provisioned_users.email, which is the
// key Percy Cloud provisioned against, and users.email, which is where the
// person actually is now - and they diverge the moment somebody changes their
// address. A fixture where the two happened to agree would pass against an
// implementation reading either table, which is to say it would prove nothing.
//
// Deleting the guard: the answer comes from the users row that MailboxForSubject
// reads. Reading brazn_provisioned_users instead answers with the address the
// customer LEFT - which on the suppression path suppresses a mailbox that is not
// theirs and leaves the erased one reachable - and the equality below fails.
// Reading it through user.GetUserByID instead, which is the obvious call and
// blanks Email on the way out, answers `unresolvable` for a live customer and
// fails in resolvedMailbox. No single change satisfies both.
func TestBraznResolveMailboxAnswersWithTheAddressOnTheUserRow(t *testing.T) {
	env := newManagedEnv(t)

	const provisionedAgainst = "dana-provisioned@example.com"
	const movedTo = "dana-moved@example.com"

	subject := provisioned(t, env.provision(createUserPayload(provisionedAgainst)))
	require.True(t, subject.Created)

	id, err := strconv.ParseInt(subject.ID, 10, 64)
	require.NoError(t, err)

	// The address moves the way openid.getOrCreateUser moves it: straight onto
	// the column, leaving the claim row holding the mailbox that was sold.
	func() {
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("UPDATE users SET email = ? WHERE id = ?", movedTo, id)
		require.NoError(t, err)
		require.NoError(t, s.Commit())
	}()

	// The precondition, asserted rather than assumed. If the claim row did not
	// still hold the OLD address, an implementation reading the wrong table
	// would answer correctly by accident and everything below would be green
	// against the defect it exists to catch.
	db.AssertExists(t, "brazn_provisioned_users",
		map[string]interface{}{"email": provisionedAgainst, "user_id": id}, false)
	db.AssertExists(t, "users", map[string]interface{}{"id": id, "email": movedTo}, false)

	answer := resolvedMailbox(t, env.provision(resolveMailboxPayload(subject.ID)))
	assert.Equal(t, "resolved", answer.Result)
	assert.Equal(t, movedTo, answer.Email,
		"contact has to reach the person where they are now, which is users.email")
	assert.NotEqual(t, provisionedAgainst, answer.Email,
		"brazn_provisioned_users.email is the provisioning key and not the customer's address")
}

// TestBraznResolveMailboxAnswersEveryAbsenceIdentically is the privacy property,
// and it is asserted as byte identity rather than as "both are unresolvable".
//
// users.id is a sequential autoincrement, and possession of the provisioning
// signing key is the whole of the authorisation on this channel - so a holder
// can walk the keyspace. What they must not be able to learn from doing so is
// which of those ids were once customers, because that is precisely the fact
// erasure destroys.
//
// The erasure is REAL: models.DeleteUser is what an erasure runs, and it takes
// the claim row holding the address away with the user, which is why this build
// could not distinguish the two cases even if a later change wanted it to.
//
// Deleting the guard: the two answers come from one value, routes/api/v1's
// noMailbox. Constructing a second reply on the erased path - a third result, a
// `reason`, anything - fails both the body comparison and the exact-member
// assertion. And a build that simply answered `unresolvable` to everything would
// satisfy both of those, which is what the control before the erasure is for.
func TestBraznResolveMailboxAnswersEveryAbsenceIdentically(t *testing.T) {
	// notifications.Fake() sets process-global state and DeleteUser notifies.
	// This file sorts near the front of the package, so leaving it set would
	// make every later test that reads the notifications table find it empty -
	// the trap brazn_user_delete_test.go documents in pkg/models.
	t.Cleanup(notifications.Unfake)
	notifications.Fake()

	env := newManagedEnv(t)

	const mailbox = "erased-subject@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	id, err := strconv.ParseInt(subject.ID, 10, 64)
	require.NoError(t, err)

	// The control, taken before the erasure. Without it, two identical
	// `unresolvable` answers would also be produced by a build that resolved
	// nothing at all - including the live customer whose mailbox an erasure is
	// meant to suppress.
	live := resolvedMailbox(t, env.provision(resolveMailboxPayload(subject.ID)))
	require.Equal(t, mailbox, live.Email, "control: this subject resolves before it is erased")

	func() {
		s := db.NewSession()
		defer s.Close()

		require.NoError(t, models.DeleteUser(s, &user.User{ID: id}))
		require.NoError(t, s.Commit())
	}()

	// Both rows, because the indistinguishability is structural only while both
	// are gone: a surviving claim row would still hold the erased subject's
	// address.
	db.AssertMissing(t, "users", map[string]interface{}{"id": id})
	db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"email": mailbox})

	erased := env.provision(resolveMailboxPayload(subject.ID))
	neverMinted := env.provision(resolveMailboxPayload(mailboxNeverMinted))

	require.Equal(t, http.StatusOK, erased.Code, erased.Body.String())
	require.Equal(t, http.StatusOK, neverMinted.Code, neverMinted.Body.String())
	assert.Equal(t, neverMinted.Body.String(), erased.Body.String(),
		"an erased subject and an id this instance never minted are one answer, byte for byte")

	// And that one answer carries nothing: no address, and no member a reason
	// could be written into. The whole member set is compared, because a struct
	// with a declared Email cannot tell an absent member from an empty one.
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, mailboxMembers(t, erased))
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, mailboxMembers(t, neverMinted))
}

// TestBraznResolveMailboxDoesNotAliasALeadingZeroUserID pins the same aliasing
// gap TestBraznRevokeSessionRefusesALeadingZeroUserID closes from the strict
// side, for the operation that reads a malformed subject as an ordinary
// absence rather than a refusal. strconv.ParseInt("0"+id, ...) parses to the
// exact int64 a correct sender's bare id would, so without
// models.parseSubjectID's round-trip check this would answer with the real
// subject's current address for a subject string nobody sent.
func TestBraznResolveMailboxDoesNotAliasALeadingZeroUserID(t *testing.T) {
	env := newManagedEnv(t)

	const mailbox = "mailbox-leading-zero@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	rec := env.provision(resolveMailboxPayload("0" + subject.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, mailboxMembers(t, rec),
		"a malformed subject must read as absent, not as the real subject a bare id names")
}

// TestBraznResolveMailboxSeparatesAnAbsentSubjectFromARefusal is the status
// contract, and it is the assertion that decides whether an erasure can ever
// finish.
//
// cloud/service/src/fork.ts maps 5xx to a retryable `unavailable` and every
// other non-2xx to a terminal `invalid_state`. Suppression runs at step 4 of six
// and must fail closed, so if "this subject has no mailbox" arrived as the
// channel's flat 400 it would be indistinguishable from a malformed request, and
// a resumed erasure - one that already got past step 5 and now finds the user
// gone - would refuse at step 4 on every retry forever, against a one-month
// statutory clock.
//
// The two codes are written as numbers rather than as http.StatusOK and
// http.StatusBadRequest, because the numbers are what the consumer switches on
// and they are what the contract states.
//
// Deleting the guard: folding the absent case into refuseProvisioning - which is
// what the two topology operations correctly do for their unknown subjects -
// makes the first assertion read 400. Both halves are asserted in one test
// because it is the DIFFERENCE that matters: a build that answered 400 to
// everything, or 200 to everything, fails here and would satisfy either half
// alone.
func TestBraznResolveMailboxSeparatesAnAbsentSubjectFromARefusal(t *testing.T) {
	env := newManagedEnv(t)

	absent := env.provision(resolveMailboxPayload(mailboxNeverMinted))
	assert.Equal(t, 200, absent.Code,
		"an absent subject is an ANSWER, and an erasure that reads it as a refusal never finishes")
	assert.Equal(t, "unresolvable", resolvedMailbox(t, absent).Result)

	// A genuine producer defect on the same operation, which must still get the
	// channel's flat refusal. The request carries an address - the one member
	// this operation answers with and never asks by, because a request naming a
	// mailbox would be a confirmation oracle for "is this person here".
	refused := env.provision(`{"contract_version":"1","email":"dana@acme.example",` +
		`"operation":"resolve_mailbox","organization_id":"` + managedTestOrganization +
		`","user_id":"` + mailboxNeverMinted + `"}`)
	assert.Equal(t, 400, refused.Code,
		"an undeclared member is refused rather than ignored, and a refusal is not an answer")
}

// TestBraznResolveMailboxMatchesTheContractConformanceFixtures runs the pinned
// interop artifacts both repositories run.
//
// The request goes through the WHOLE production path - the signature,
// provisioning.Verify, DecodeResolveMailbox and the switch - as its exact bytes,
// rather than being handed to the decoder alone. env.provision splices the
// payload into the envelope by string concatenation and signs those same bytes,
// so nothing between the file and the verifier re-marshals or compacts them.
//
// The field names and code strings are asserted as LITERALS WRITTEN HERE rather
// than read from the fixture. Per CLAUDE.md, a value both sides take from one
// definition is checked by neither: renaming a member in the fixture and in this
// build together would stay green while Percy's caller rejected every message.
func TestBraznResolveMailboxMatchesTheContractConformanceFixtures(t *testing.T) {
	env := newManagedEnv(t)

	request, resolvedReply, unresolvableReply := mailboxFixtures(t)

	members := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(request, &members))
	assert.Equal(t, "1", members["contract_version"])
	assert.Equal(t, "resolve_mailbox", members["operation"])
	assert.Equal(t, "org_3d77e0c15a84", members["organization_id"])
	// A STRING, not a number. A Go or TypeScript producer marshalling an integer
	// emits 42 rather than "42", which is the single most likely defect on this
	// seam - and a `pattern` check coerces, so only comparing against the quoted
	// form catches it. json.Unmarshal into an interface makes a JSON number a
	// float64, which is not equal to any string.
	assert.Equal(t, "42", members["user_id"])
	assert.Len(t, members, 4, "the request carries these four members and no address")

	// The fixture names user 42, which this instance does not have - so the
	// answer is the unresolvable fixture and the pairing is exact. Asserted
	// rather than assumed: on an instance that happened to hold user 42 this
	// test would compare against the wrong fixture and fail for the wrong reason.
	db.AssertMissing(t, "users", map[string]interface{}{"id": 42})

	answer := env.provision(string(request))
	require.Equal(t, http.StatusOK, answer.Code, answer.Body.String())
	assertEmitted(t, unresolvableReply, answer)

	// The other half of the pair, against a subject that does resolve. The
	// fixture's address is provisioned onto this instance so the emitted bytes
	// can be compared whole, rather than the address being read out and compared
	// on its own - which would leave the member name and the result string
	// unchecked.
	const conformanceMailbox = "dana@acme.example"
	subject := provisioned(t, env.provision(createUserPayload(conformanceMailbox)))
	require.True(t, subject.Created)

	answered := env.provision(resolveMailboxPayload(subject.ID))
	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())
	assertEmitted(t, resolvedReply, answered)
}
