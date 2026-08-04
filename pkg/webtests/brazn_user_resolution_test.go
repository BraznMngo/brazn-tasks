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

// The resolve_user operation (BRA-1114), asserted through the endpoint, because
// everything it promises is a property of what goes on the wire: WHICH of two
// stored addresses it matches, which status code carries an absence, and — the
// one obligation the consumer cannot enforce — that nothing it does writes a row.
//
// userNeverMinted is an id this instance does not have and never did. 99999 is
// the value the topology tests use for the same purpose.
const userNeverMinted = "99999"

// The vendored conformance set, referenced across packages rather than copied
// again beside these tests. Its own README explains why one copy: a frozen
// artifact that exists twice in one repository is one that can drift inside it.
//
// The paths are constants composed of constants, which is the form gosec
// resolves — the same reason brazn_provisioning_contract_test.go names its own
// four this way.
const (
	userContractRoot      = "../modules/brazn/provisioning/testdata/contract/"
	userRequestByEmail    = userContractRoot + "user-resolution-request.valid.conformance-by-email.json"
	userRequestByID       = userContractRoot + "user-resolution-request.valid.conformance-by-user-id.json"
	userReplyResolved     = userContractRoot + "user-resolution-response.valid.conformance-resolved.json"
	userReplyUnresolvable = userContractRoot + "user-resolution-response.valid.conformance-unresolvable.json"
)

// userResolution is the reply the consumer is written against
// (cloud/contracts/v1/user/, UserResolutionResponse). It is redeclared here
// rather than imported so the tests assert the WIRE shape — the same reason
// provisionedUser and mailboxResolution are redeclared beside it. A handler that
// started answering with a JSON number for user_id would still satisfy a Go
// struct that declared one.
type userResolution struct {
	Result        string `json:"result"`
	UserID        string `json:"user_id"`
	EmailVerified bool   `json:"email_verified"`
}

// resolveUserByEmailPayload is the recognition form in canonical JSON — members
// sorted by key, which is what a producer emits and therefore what the signature
// is made over.
func resolveUserByEmailPayload(mailbox string) string {
	return `{"contract_version":"1","email":"` + mailbox + `","operation":"resolve_user"}`
}

// resolveUserBySubjectPayload is the verification form, the one
// requireVerifiedAccount asks by because it holds an id and no address.
func resolveUserBySubjectPayload(userID string) string {
	return `{"contract_version":"1","operation":"resolve_user","user_id":"` + userID + `"}`
}

// resolvedUserAnswer reads a 200 answer and returns it.
//
// The status is required rather than asserted, because every claim these tests
// make is about the BODY of a 200: an operation that answered 400 would leave
// every assertion below reading a refusal sentence.
func resolvedUserAnswer(t *testing.T, rec *httptest.ResponseRecorder) userResolution {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := userResolution{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// answerMembers reads a reply as the bare set of members it carries, which is
// how the absence of one is asserted. A struct cannot express "and nothing
// else": a user_id this build should never have emitted would land in a declared
// field and read as an ordinary empty string.
func answerMembers(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()

	members := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &members))
	return members
}

// provisioningRows counts both tables a resolve must not touch.
//
// BOTH, because either one alone would miss the failure this is here for: a
// fork-side implementation that "helpfully" created the missing user writes to
// users AND to brazn_provisioned_users, and one that only took the mailbox claim
// writes to the second. Counts rather than AssertMissing on a particular row,
// because the assertion is that NOTHING was written — including a row nobody
// thought to name. countRows is this package's own, from huma_testing_test.go.
func provisioningRows(t *testing.T) (users, claims int) {
	t.Helper()

	return countRows(t, "users"), countRows(t, "brazn_provisioned_users")
}

// TestBraznResolveUserRecognisesAProvisionedMailbox is the recognition form's
// happy path — the answer signUp and registerOrganization need to converge a
// repeat signup on the account a customer already has.
func TestBraznResolveUserRecognisesAProvisionedMailbox(t *testing.T) {
	env := newManagedEnv(t)

	const mailbox = "recognised@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	usersBefore, claimsBefore := provisioningRows(t)

	answer := resolvedUserAnswer(t, env.provision(resolveUserByEmailPayload(mailbox)))
	assert.Equal(t, "resolved", answer.Result)
	assert.Equal(t, subject.ID, answer.UserID,
		"a resolve must answer with the id create_user reported, or a signup converges on the wrong account")
	// The row's own status, which for a mail-disabled instance is Active. It is
	// compared against what create_user reported for the same row rather than
	// against a hard-coded true: both readings come off users.status, so a
	// disagreement between them is the defect.
	assert.Equal(t, subject.EmailVerified, answer.EmailVerified)

	usersAfter, claimsAfter := provisioningRows(t)
	assert.Equal(t, usersBefore, usersAfter, "a resolve must not create a user")
	assert.Equal(t, claimsBefore, claimsAfter, "a resolve must not take a mailbox claim")
}

// TestBraznResolveUserAnswersForTheMailboxItWasProvisionedAgainst is the column
// decision, and it is the one assertion a fork-side implementation is most likely
// to fail — because resolve_mailbox, in this same switch, correctly answers the
// OPPOSITE way.
//
// THE FIXTURE MAKES THE TWO STORED ADDRESSES DIFFER, deliberately, exactly as
// the resolve_mailbox test does. This instance holds a mailbox in two places —
// brazn_provisioned_users.email, the key Percy Cloud provisioned against, and
// users.email, where the person is now — and they diverge the moment somebody
// changes their address. A fixture where the two happened to agree would pass
// against an implementation reading either table, which is to say it would prove
// nothing.
//
// Deleting the guard: the answer comes from the claim row that
// models.ResolveUserByMailbox reads. Reading users.email instead answers
// `unresolvable` for the address Percy sold to — which tells the commercial
// layer to open a SECOND account for a customer it already has, BRA-1106's
// defect from this side of the seam — and both assertions below invert.
//
// ⚠ THE SECOND HALF IS CASE 14 AND IS NOT A BUG HERE. Asked by the NEW address
// the answer is `unresolvable`, so a customer who changes their address and later
// signs up again with the new one is not recognised and opens a second
// entitlement. That is bounded the way every unrecognised repeat is — registration
// is single-use per person, so only one entitlement can ever acquire a user — and
// closing it is BRA-1022's, which owns whether an address change updates the claim
// row. It is asserted rather than left implicit so that whoever changes it sees
// this test go red.
func TestBraznResolveUserAnswersForTheMailboxItWasProvisionedAgainst(t *testing.T) {
	env := newManagedEnv(t)

	const provisionedAgainst = "sold-to@example.com"
	const movedTo = "moved-to@example.com"

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
	// still hold the OLD address, an implementation reading the wrong table would
	// answer correctly by accident and everything below would be green against
	// the defect it exists to catch.
	db.AssertExists(t, "brazn_provisioned_users",
		map[string]interface{}{"email": provisionedAgainst, "user_id": id}, false)
	db.AssertExists(t, "users", map[string]interface{}{"id": id, "email": movedTo}, false)

	sold := resolvedUserAnswer(t, env.provision(resolveUserByEmailPayload(provisionedAgainst)))
	assert.Equal(t, "resolved", sold.Result)
	assert.Equal(t, subject.ID, sold.UserID,
		"the id Percy sold to must keep resolving: it is an accounts.user_id, a primary key with no update path")

	moved := env.provision(resolveUserByEmailPayload(movedTo))
	require.Equal(t, http.StatusOK, moved.Code, moved.Body.String())
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, answerMembers(t, moved),
		"Case 14: the new address is not in the claim table, and BRA-1022 owns whether it ever is")
}

// TestBraznResolveUserVerifiesBySubject is the form requireVerifiedAccount asks
// by, and the assertion that email_verified is the ROW'S status.
//
// THE UNCONFIRMED HALF IS THE ONE THAT MATTERS. With the mailer off — the default
// every test in this repository runs with — a provisioned account is legitimately
// Active, so an implementation that hard-coded `true` would satisfy the confirmed
// half and every other test in this file. The status is written directly onto the
// row here, the way brazn_provisioning_test.go does it, so the two answers differ
// in the row and nowhere else.
//
// Deleting the guard: derive email_verified from anything but users.status — a
// constant, or a member of the request — and the second answer stays true.
func TestBraznResolveUserVerifiesBySubject(t *testing.T) {
	env := newManagedEnv(t)

	const mailbox = "verified-by-id@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	id, err := strconv.ParseInt(subject.ID, 10, 64)
	require.NoError(t, err)

	confirmed := resolvedUserAnswer(t, env.provision(resolveUserBySubjectPayload(subject.ID)))
	assert.Equal(t, "resolved", confirmed.Result)
	assert.Equal(t, subject.ID, confirmed.UserID,
		"the verification form echoes the id it was asked about, so a captured reply reads on its own")
	require.True(t, confirmed.EmailVerified,
		"control: with the mailer off this account is Active, or the assertion below proves nothing")

	func() {
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("UPDATE users SET status = ? WHERE id = ?",
			int(user.StatusEmailConfirmationRequired), id)
		require.NoError(t, err)
		require.NoError(t, s.Commit())
	}()

	unconfirmed := resolvedUserAnswer(t, env.provision(resolveUserBySubjectPayload(subject.ID)))
	assert.Equal(t, "resolved", unconfirmed.Result)
	assert.False(t, unconfirmed.EmailVerified,
		"email_verified follows the row, and nothing in the request can move it")

	// The caller cannot assert it either: the member is undeclared on this
	// operation's payload type, so decodeExactly refuses it rather than ignoring
	// it. Without this, "the caller cannot say" would rest on the handler simply
	// not reading a member the transport still accepted.
	claimed := env.provision(`{"contract_version":"1","email_verified":true,` +
		`"operation":"resolve_user","user_id":"` + subject.ID + `"}`)
	assert.Equal(t, http.StatusBadRequest, claimed.Code,
		"a request asserting its own verification is refused, not ignored")
}

// TestBraznResolveUserNeverCreates is the obligation the response schema states
// as the fork's, because the consumer cannot enforce it: an `unresolvable` must
// never be turned into a creation.
//
// A resolve that fell back to creating would reintroduce the total signup outage
// BRA-1106 fixed, from this side of the seam — and it would do it invisibly,
// because the reply would look like an ordinary resolution and the caller would
// bind its account to an id it never asked to have minted.
//
// BOTH TABLES AND BOTH FORMS, and the unknown address is asked TWICE: a build
// that created on the first call would answer `resolved` on the second, so the
// repeat is what separates "never creates" from "creates once and then resolves".
//
// Deleting the guard: point either arm of resolveUser at
// models.CreateOrResolveUserForMailbox — the create_user path, which is one line
// away in the same file — and every count below moves.
func TestBraznResolveUserNeverCreates(t *testing.T) {
	env := newManagedEnv(t)

	const unknown = "never-heard-of@example.com"
	usersBefore, claimsBefore := provisioningRows(t)

	first := env.provision(resolveUserByEmailPayload(unknown))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, answerMembers(t, first),
		"an unknown address is the ordinary answer for every first-time customer, and it carries nothing")

	second := env.provision(resolveUserByEmailPayload(unknown))
	assert.Equal(t, first.Body.String(), second.Body.String(),
		"asking twice must answer twice the same: a build that created on the first call would not")

	absentSubject := env.provision(resolveUserBySubjectPayload(userNeverMinted))
	require.Equal(t, http.StatusOK, absentSubject.Code, absentSubject.Body.String())
	assert.Equal(t, first.Body.String(), absentSubject.Body.String(),
		"an unknown address and an id this instance never minted are one answer, byte for byte")

	usersAfter, claimsAfter := provisioningRows(t)
	assert.Equal(t, usersBefore, usersAfter, "no resolve, in any form, may create a user")
	assert.Equal(t, claimsBefore, claimsAfter, "no resolve, in any form, may take a mailbox claim")

	db.AssertMissing(t, "users", map[string]interface{}{"email": unknown})
	db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"email": unknown})
}

// TestBraznResolveUserAnswersEveryAbsenceIdentically is the privacy property, and
// it is asserted as byte identity rather than as "both are unresolvable".
//
// users.id is a sequential autoincrement and possession of the provisioning
// signing key is the whole of the authorisation on this channel, so a holder can
// walk the keyspace. What they must not learn from doing so is which of those ids
// were once customers, because that is precisely the fact an erasure destroys.
//
// The erasure is REAL: models.DeleteUser is what an erasure runs, and it takes
// the claim row holding the address away with the user — which is why this build
// could not distinguish the two cases even if a later change wanted it to. Both
// directions are checked, because the claim row is what makes the ADDRESS form
// indistinguishable and the user row is what makes the ID form so.
//
// Deleting the guard: the answers come from one value, routes/api/v1's noUser.
// Constructing a second reply on the erased path — a third result, a `reason`,
// anything — fails the comparison. And a build that answered `unresolvable` to
// everything would satisfy that, which is what the control before the erasure is
// for.
func TestBraznResolveUserAnswersEveryAbsenceIdentically(t *testing.T) {
	// notifications.Fake() sets process-global state and DeleteUser notifies.
	// Leaving it set would make every later test that reads the notifications
	// table find it empty — the trap brazn_user_delete_test.go documents.
	t.Cleanup(notifications.Unfake)
	notifications.Fake()

	env := newManagedEnv(t)

	const mailbox = "erased-user@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	id, err := strconv.ParseInt(subject.ID, 10, 64)
	require.NoError(t, err)

	// The control, taken before the erasure. Without it, two identical
	// `unresolvable` answers would also be produced by a build that resolved
	// nothing at all.
	live := resolvedUserAnswer(t, env.provision(resolveUserByEmailPayload(mailbox)))
	require.Equal(t, "resolved", live.Result, "control: this subject resolves before it is erased")

	func() {
		s := db.NewSession()
		defer s.Close()

		require.NoError(t, models.DeleteUser(s, &user.User{ID: id}))
		require.NoError(t, s.Commit())
	}()

	db.AssertMissing(t, "users", map[string]interface{}{"id": id})
	db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"email": mailbox})

	erasedByAddress := env.provision(resolveUserByEmailPayload(mailbox))
	erasedByID := env.provision(resolveUserBySubjectPayload(subject.ID))
	neverMinted := env.provision(resolveUserBySubjectPayload(userNeverMinted))

	require.Equal(t, http.StatusOK, erasedByAddress.Code, erasedByAddress.Body.String())
	require.Equal(t, http.StatusOK, erasedByID.Code, erasedByID.Body.String())
	require.Equal(t, http.StatusOK, neverMinted.Code, neverMinted.Body.String())

	assert.Equal(t, neverMinted.Body.String(), erasedByAddress.Body.String())
	assert.Equal(t, neverMinted.Body.String(), erasedByID.Body.String())
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, answerMembers(t, erasedByID))
}

// TestBraznResolveUserRefusesAnythingButExactlyOneIdentifier is the presence
// rule, on the wire.
//
// BOTH IS REFUSED RATHER THAN RESOLVED BY PRECEDENCE. A caller sending both is
// asserting a pairing rather than asking about one, and neither side of this seam
// should have to decide which member wins — least of all silently, which is what
// a precedence would be. The pairing here is deliberately a TRUE one: the address
// and the id belong to the same account, so an implementation that quietly picked
// either member would answer correctly and pass a weaker test.
//
// Deleting the guard: remove the presence check in DecodeResolveUser and the
// first case answers 200 with a resolution.
func TestBraznResolveUserRefusesAnythingButExactlyOneIdentifier(t *testing.T) {
	env := newManagedEnv(t)

	const mailbox = "one-identifier@example.com"
	subject := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, subject.Created)

	both := env.provision(`{"contract_version":"1","email":"` + mailbox +
		`","operation":"resolve_user","user_id":"` + subject.ID + `"}`)
	assert.Equal(t, http.StatusBadRequest, both.Code,
		"both identifiers is a pairing asserted, not a question asked — and they agree here, so "+
			"a build that picked one would look right")

	neither := env.provision(`{"contract_version":"1","operation":"resolve_user"}`)
	assert.Equal(t, http.StatusBadRequest, neither.Code,
		"a resolve_user naming no subject asks nothing this build could answer")

	// The shape defect the contract calls the single most likely one on this seam.
	// A Go or TypeScript producer marshalling an integer emits 9001 rather than
	// "9001", and a `pattern` check coerces — so the refusal has to come from the
	// Go type, which is why ResolveUser declares UserID as a string.
	usersBefore, claimsBefore := provisioningRows(t)
	numeric := env.provision(`{"contract_version":"1","operation":"resolve_user","user_id":9001}`)
	assert.Equal(t, http.StatusBadRequest, numeric.Code,
		"user_id is a decimal STRING: a JSON number is refused rather than coerced")

	usersAfter, claimsAfter := provisioningRows(t)
	assert.Equal(t, usersBefore, usersAfter, "a refusal writes nothing either")
	assert.Equal(t, claimsBefore, claimsAfter)
}

// TestBraznResolveUserTellsAnUnverifiedCallerNothing is the oracle boundary at
// the door.
//
// This request CARRIES AN ADDRESS, which resolve_mailbox's deliberately does not,
// so reaching this operation at all is a membership answer for whoever holds the
// signing key. The contract meets that rather than inheriting the earlier
// refusal, and the part that is this route's to keep is that an unverifiable
// caller reaches the flat 400 and never a result.
//
// ⚠ WHAT THIS TEST DOES AND DOES NOT PROVE. The signature is verified before the
// lookup because provisioning.Verify runs above the switch — that ordering is
// structural and, for a read with no side effect, is not observable from outside;
// a test that claimed to prove it would be claiming more than it can see. What is
// observable, and is what the oracle argument actually needs, is asserted here:
// an unverified caller gets the same bytes and the same status for an address
// this instance HAS as for one it has never heard of, so nothing it can do with a
// bad key distinguishes them.
func TestBraznResolveUserTellsAnUnverifiedCallerNothing(t *testing.T) {
	env := newManagedEnv(t)

	const known = "known-to-the-instance@example.com"
	const unknown = "unknown-to-the-instance@example.com"

	subject := provisioned(t, env.provision(createUserPayload(known)))
	require.True(t, subject.Created)

	// The control: with a good signature the two really do answer differently, or
	// the identity asserted below would hold for a build that resolved nothing.
	require.Equal(t, "resolved",
		resolvedUserAnswer(t, env.provision(resolveUserByEmailPayload(known))).Result)
	require.Equal(t, "unresolvable",
		resolvedUserAnswer(t, env.provision(resolveUserByEmailPayload(unknown))).Result)

	unsignedKnown := env.postProvisioning(`{"signed":` + resolveUserByEmailPayload(known) + `}`)
	unsignedUnknown := env.postProvisioning(`{"signed":` + resolveUserByEmailPayload(unknown) + `}`)
	assert.Equal(t, http.StatusBadRequest, unsignedKnown.Code)
	assert.Equal(t, unsignedKnown.Body.String(), unsignedUnknown.Body.String(),
		"an unsigned caller learns nothing about which addresses this instance holds")

	// The same key and the same key id: only the domain the signature covers
	// differs, which is the one thing keeping the two channels apart.
	wrongDomainKnown := env.postProvisioning(
		env.signedFor(projectionContractPrefix, resolveUserByEmailPayload(known)))
	wrongDomainUnknown := env.postProvisioning(
		env.signedFor(projectionContractPrefix, resolveUserByEmailPayload(unknown)))
	assert.Equal(t, http.StatusBadRequest, wrongDomainKnown.Code)
	assert.Equal(t, wrongDomainKnown.Body.String(), wrongDomainUnknown.Body.String())
	assert.Equal(t, unsignedKnown.Body.String(), wrongDomainKnown.Body.String(),
		"every refusal on this channel is one flat reply, whatever caused it")
}

// TestBraznResolveUserMatchesTheContractConformanceFixtures runs the pinned
// interop artifacts both repositories run.
//
// The requests go through the WHOLE production path — the signature,
// provisioning.Verify, DecodeResolveUser and the switch — as their exact bytes
// rather than being handed to the decoder alone. env.provision splices the
// payload into the envelope by string concatenation and signs those same bytes,
// so nothing between the file and the verifier re-marshals or compacts them.
//
// The field names and code strings are asserted as LITERALS WRITTEN HERE rather
// than read from the fixtures. Per CLAUDE.md, a value both sides take from one
// definition is checked by neither: renaming a member in the fixture and in this
// build together would stay green while Percy's caller rejected every message.
func TestBraznResolveUserMatchesTheContractConformanceFixtures(t *testing.T) {
	env := newManagedEnv(t)

	// THE TRAILING NEWLINE IS TRIMMED, and that is not cosmetic. Each file ends in
	// one because it is a file; the wire value is the JSON object alone, and
	// entitlement.VerifyEnvelope slices `signed` out of the envelope from its
	// opening brace to its matching close — so a signature made over the bytes
	// INCLUDING that newline would cover something the verifier never sees.
	// Everything INSIDE each object is carried through untouched, which is the
	// point: this build has to accept the producer's formatting rather than a
	// canonical form of it.
	byEmailRaw, err := os.ReadFile(userRequestByEmail)
	require.NoError(t, err)
	byIDRaw, err := os.ReadFile(userRequestByID)
	require.NoError(t, err)
	resolvedRaw, err := os.ReadFile(userReplyResolved)
	require.NoError(t, err)
	unresolvableRaw, err := os.ReadFile(userReplyUnresolvable)
	require.NoError(t, err)

	byEmail := bytes.TrimSpace(byEmailRaw)
	byID := bytes.TrimSpace(byIDRaw)
	resolvedReply := bytes.TrimSpace(resolvedRaw)
	unresolvableReply := bytes.TrimSpace(unresolvableRaw)

	members := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(byEmail, &members))
	assert.Equal(t, "1", members["contract_version"])
	assert.Equal(t, "resolve_user", members["operation"])
	assert.Equal(t, "ada@example.com", members["email"])
	assert.Len(t, members, 3, "the recognition form carries these three members and no id")

	members = map[string]interface{}{}
	require.NoError(t, json.Unmarshal(byID, &members))
	assert.Equal(t, "resolve_user", members["operation"])
	// A STRING, not a number. A Go or TypeScript producer marshalling an integer
	// emits 9001 rather than "9001", which is the single most likely defect on
	// this seam — and a `pattern` check coerces, so only comparing against the
	// quoted form catches it. json.Unmarshal into an interface makes a JSON number
	// a float64, which is not equal to any string.
	assert.Equal(t, "9001", members["user_id"])
	assert.Len(t, members, 3, "the verification form carries these three members and no address")

	// Both fixtures name subjects this instance does not have, so both answer with
	// the unresolvable fixture and the pairing is exact. Asserted rather than
	// assumed: on an instance that happened to hold either, these would compare
	// against the wrong fixture and fail for the wrong reason.
	db.AssertMissing(t, "users", map[string]interface{}{"id": 9001})
	db.AssertMissing(t, "brazn_provisioned_users",
		map[string]interface{}{"email": "ada@example.com"})

	absentByEmail := env.provision(string(byEmail))
	require.Equal(t, http.StatusOK, absentByEmail.Code, absentByEmail.Body.String())
	assertEmitted(t, unresolvableReply, absentByEmail)

	absentByID := env.provision(string(byID))
	require.Equal(t, http.StatusOK, absentByID.Code, absentByID.Body.String())
	assertEmitted(t, unresolvableReply, absentByID)

	// The other half of the pair, against a subject that does resolve. The
	// fixture's address is provisioned onto this instance so the emitted bytes can
	// be compared whole, rather than the id being read out and compared on its own
	// — which would leave the member names, their order and the result string
	// unchecked.
	subject := provisioned(t, env.provision(createUserPayload("ada@example.com")))
	require.True(t, subject.Created)
	require.True(t, subject.EmailVerified,
		"the frozen reply says email_verified true; with the mailer off this account is Active, "+
			"and if that ever changes this comparison must be re-derived rather than relaxed")

	// The fixture's user_id is 9001 and an autoincrement will not mint that, so
	// THE ONE VALUE THAT CANNOT BE FROZEN is substituted and nothing else is: the
	// member names, their order, "resolved" and the boolean all still come from
	// the file. The substituted id is the one create_user reported, which is an
	// independent reading of the same row rather than this reply compared with
	// itself.
	require.Equal(t, 1, bytes.Count(resolvedReply, []byte(`"9001"`)),
		"the placeholder id must appear exactly once, or the substitution below is not the only edit")
	expected := bytes.Replace(resolvedReply, []byte(`"9001"`), []byte(`"`+subject.ID+`"`), 1)

	answered := env.provision(resolveUserByEmailPayload("ada@example.com"))
	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())
	assertEmitted(t, expected, answered)
}
