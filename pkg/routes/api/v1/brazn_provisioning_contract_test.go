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

package v1

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The response half of the vendored conformance set. The set itself lives beside
// the decoders it is mostly used by; see
// pkg/modules/brazn/provisioning/testdata/contract/README.md for what it is and
// why a copy of it is in this repository at all.
//
// ONE VENDORED COPY, REFERENCED ACROSS PACKAGES, rather than a second copy under
// this package's own testdata. A frozen artifact that exists twice in one
// repository is one that can drift inside it, which is the failure the whole set
// exists to prevent — at one remove, but the same failure.
//
// Constants rather than paths built at call time, for the reason golden_test.go
// gives in the entitlement package: this is the form gosec resolves.
const (
	contractRoot                 = "../../../modules/brazn/provisioning/testdata/contract/"
	contractUserReplyCreated     = contractRoot + "create-user-response.valid.conformance-created.json"
	contractUserReplyResolved    = contractRoot + "create-user-response.valid.conformance-resolved.json"
	contractTeamRootsReply       = contractRoot + "create-team-roots-response.valid.conformance.json"
	contractAcknowledgementReply = contractRoot + "provisioning-acknowledgement.valid.conformance.json"

	contractUserResolved        = contractRoot + "user-resolution-response.valid.conformance-resolved.json"
	contractUserUnresolvable    = contractRoot + "user-resolution-response.valid.conformance-unresolvable.json"
	contractUserWithAddress     = contractRoot + "user-resolution-response.invalid.address-in-the-answer.json"
	contractUserErasedResult    = contractRoot + "user-resolution-response.invalid.distinguishes-erasure.json"
	contractUserWithoutID       = contractRoot + "user-resolution-response.invalid.resolved-without-user-id.json"
	contractUserWithoutVerified = contractRoot + "user-resolution-response.invalid.resolved-without-verification.json"
	contractUserAbsenceWithID   = contractRoot + "user-resolution-response.invalid.unresolvable-with-user-id.json"
)

// asContractMembers reads a JSON object into a member map.
//
// The comparison is on MEMBERS AND THEIR TYPES rather than on bytes, because the
// fixtures are indented for a human and encoding/json emits compact output — a
// byte comparison would fail on whitespace and prove nothing about the contract.
// What survives the round trip is exactly what the contract constrains: a renamed
// tag changes a key, a dropped field removes one, an added field adds one, and a
// string that became an int64 arrives as a float64 instead of a string. Every one
// of those fails assert.Equal on the map.
func asContractMembers(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	members := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(raw, &members))
	return members
}

// TestContractCreateUserReplyMatchesTheContract is the response-side guard for
// the operation BRA-1106 was about.
//
// ⚠ ID IS A STRING AND IT HAS TO BE. The contract declares it as the decimal
// string form of users.id and validates it against ^[1-9][0-9]{0,18}$; a JSON
// number would pass that pattern by coercion on the consumer and fail the type it
// is declared with. That is why provisionedUserReply declares ID as a string and
// the handler formats it with strconv.FormatInt rather than letting the int64
// marshal itself. This build does that correctly today because one implementer
// read the consumer's code — which is precisely the check this test replaces with
// an artifact.
//
// BOTH PATHS ARE PINNED because created and email_verified move together on one
// of them and not the other: a row this call created microseconds ago has been
// confirmed by nobody, so Percy's transport answers false for it whatever this
// build said, and reports this build's own answer on the resolve path.
func TestContractCreateUserReplyMatchesTheContract(t *testing.T) {
	created, err := json.Marshal(&provisionedUserReply{
		ID:            "42",
		Created:       true,
		EmailVerified: false,
	})
	require.NoError(t, err)

	frozen, err := os.ReadFile(contractUserReplyCreated)
	require.NoError(t, err)
	assert.Equal(t, asContractMembers(t, frozen), asContractMembers(t, created),
		"the create_user reply must carry the members the contract names, in the types it names")

	resolved, err := json.Marshal(&provisionedUserReply{
		ID:            "42",
		Created:       false,
		EmailVerified: true,
	})
	require.NoError(t, err)

	frozenResolved, err := os.ReadFile(contractUserReplyResolved)
	require.NoError(t, err)
	assert.Equal(t, asContractMembers(t, frozenResolved), asContractMembers(t, resolved))
}

// TestContractTeamRootsReplyMatchesTheContract is the response-side guard for the
// two references that become an unrewritable mapping.
//
// Repository.mapTeamTopology on the consumer COALESCES a team's pair in exactly
// once — a later call answers with the stored references rather than repointing
// them — so a reference that arrives in the wrong shape is stored wrong
// permanently, while every log line says the topology was provisioned. Both
// members are decimal strings for the same reason the user id is, and with more
// at stake.
func TestContractTeamRootsReplyMatchesTheContract(t *testing.T) {
	produced, err := json.Marshal(&teamRootsReply{
		TaskTeamRef:    "7",
		TaskProjectRef: "13",
	})
	require.NoError(t, err)

	frozen, err := os.ReadFile(contractTeamRootsReply)
	require.NoError(t, err)
	assert.Equal(t, asContractMembers(t, frozen), asContractMembers(t, produced),
		"both references are decimal STRINGS: a JSON number is refused by the consumer's type")
}

// TestContractAcknowledgementIsAnEmptyObject pins the reply every operation with
// nothing to report gives.
//
// ⚠ AN EMPTY BODY WOULD NOT DO, and that is the channel's contract rather than a
// preference: the consumer cannot tell an empty 200 from a truncated one, so its
// transport refuses both alike — JSON.parse("") throws and becomes a
// non-retryable invalid_state. A handler that answered c.NoContent here would be
// refused by the caller it had just succeeded for, and the customer-visible
// symptom would be a failed erasure or a missing Inbox with a successful write
// sitting behind it.
//
// The emptiness is load-bearing in the other direction too. A destroyed-row count
// would be a fact about this schema that the commercial layer must not depend on,
// and a caller that read one would have to decide what a zero means — which is the
// one question this shape settles by having nothing in it.
func TestContractAcknowledgementIsAnEmptyObject(t *testing.T) {
	produced, err := json.Marshal(&nothingToReport{})
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(produced))

	frozen, err := os.ReadFile(contractAcknowledgementReply)
	require.NoError(t, err)
	assert.Equal(t, asContractMembers(t, frozen), asContractMembers(t, produced),
		"an operation with nothing to report answers {} — never an empty body, never a count")
}

// asCompactContract compacts a fixture to the form encoding/json emits, so the
// two can be compared BYTE FOR BYTE.
//
// The member-map comparison the tests above use cannot see member ORDER, and on
// this operation order is part of what a captured payload is read against.
// Compaction removes insignificant whitespace and nothing else — it does not
// reorder members, drop them or change a value — so what survives is the exact
// member set, in the exact order, with every literal intact.
//
// It takes the BYTES rather than a path, so every fixture is still read from a
// constant at its own call site — the form gosec resolves, which is why the
// files are named as constants above at all.
func asCompactContract(t *testing.T, frozen []byte) string {
	t.Helper()

	compact := &bytes.Buffer{}
	require.NoError(t, json.Compact(compact, frozen))
	return compact.String()
}

// TestContractUserResolutionRepliesMatchTheContract is the response-side guard
// for resolve_user (BRA-1109), compared byte for byte rather than by members.
//
// ⚠ USER_ID IS A STRING AND IT HAS TO BE. The contract declares it as the decimal
// string form of users.id and validates it against ^[1-9][0-9]{0,18}$; a JSON
// number passes that pattern by coercion on the consumer and fails the type it is
// declared with. The response schema calls this the single most likely defect on
// this seam, because a Go handler marshalling an int64 emits 42 rather than "42".
// That is why resolvedUser declares UserID as a string and resolveUser formats it
// with strconv.FormatInt.
//
// The literals here are written into this source rather than read from the
// fixtures, per CLAUDE.md: a value both repositories take from one definition is
// checked by neither.
func TestContractUserResolutionRepliesMatchTheContract(t *testing.T) {
	resolved, err := json.Marshal(&resolvedUser{
		Result:        "resolved",
		UserID:        "9001",
		EmailVerified: true,
	})
	require.NoError(t, err)

	frozenResolved, err := os.ReadFile(contractUserResolved)
	require.NoError(t, err)
	assert.Equal(t, asCompactContract(t, frozenResolved), string(resolved),
		"a resolution carries result, user_id and email_verified, in that order and no others")

	assert.Equal(t, `{"result":"resolved","user_id":"9001","email_verified":true}`, string(resolved),
		"the member names and the result string are the contract's, asserted as literals here")

	absent, err := json.Marshal(noUser)
	require.NoError(t, err)

	frozenAbsent, err := os.ReadFile(contractUserUnresolvable)
	require.NoError(t, err)
	assert.Equal(t, asCompactContract(t, frozenAbsent), string(absent))
	assert.Equal(t, `{"result":"unresolvable"}`, string(absent))
}

// TestContractUserResolutionResolvedNeverDropsAMember is the omitempty trap, and
// it is the reason resolvedUser declares EmailVerified without one.
//
// AN UNCONFIRMED CUSTOMER IS THE CASE THAT BREAKS. `email_verified` is false for
// every account still waiting on its confirmation mail, and omitempty drops a
// false bool — emitting a `resolved` with no verification member, which the
// consumer reads as `undefined`. That is neither true nor false and would refuse
// a confirmed customer or admit an unconfirmed one depending on how the check was
// written, which is why the schema requires the member on this branch rather than
// defaulting it.
//
// Deleting the guard: add `,omitempty` to either tag and the emitted object loses
// a member the frozen invalid fixtures are named for. Both are asserted, because
// a reply missing user_id would make a signup converge on undefined.
func TestContractUserResolutionResolvedNeverDropsAMember(t *testing.T) {
	unconfirmed, err := json.Marshal(&resolvedUser{
		Result:        "resolved",
		UserID:        "9001",
		EmailVerified: false,
	})
	require.NoError(t, err)

	members := asContractMembers(t, unconfirmed)
	assert.Equal(t, map[string]interface{}{
		"result":         "resolved",
		"user_id":        "9001",
		"email_verified": false,
	}, members, "a false verification is a VALUE and must be emitted, never omitted")

	// The three shapes the contract froze as invalid, asserted as things this
	// build does not produce rather than as things it refuses to read.
	withoutVerified, err := os.ReadFile(contractUserWithoutVerified)
	require.NoError(t, err)
	assert.NotEqual(t, asContractMembers(t, withoutVerified), members,
		"an omitempty on email_verified would emit exactly this")

	withoutID, err := os.ReadFile(contractUserWithoutID)
	require.NoError(t, err)
	assert.NotEqual(t, asContractMembers(t, withoutID), members,
		"a resolution with no user_id would make a signup converge on undefined")

	// And no address, ever. This operation exists so that a verification check
	// does not have to pull a mailbox across the seam to look at a boolean, which
	// is more disclosure than the question needs — resolve_mailbox is the other
	// direction and is the only operation that returns one.
	assert.NotContains(t, members, "email",
		"nothing in a user resolution is an address; that is what keeps it off resolve_mailbox")

	withAddress, err := os.ReadFile(contractUserWithAddress)
	require.NoError(t, err)
	assert.NotEqual(t, asContractMembers(t, withAddress), members)
}

// TestContractUserResolutionAbsenceCarriesNothing is the oracle boundary, and it
// is asserted against the type rather than against a code path.
//
// unresolvableUser HAS NO FIELD an id or a verification state could be written
// into, so the emptiness is structural: there is nothing for a later change to
// populate and nothing a `reason` could go in. That is what makes an erased
// subject indistinguishable from one that never existed — models.DeleteUser
// destroys the brazn_provisioned_users row along with the user, so this instance
// holds nothing that could tell them apart even if the vocabulary allowed it.
//
// Deleting the guard: answering absences with resolvedUser and empty members, or
// giving unresolvableUser a third result, fails both comparisons below.
func TestContractUserResolutionAbsenceCarriesNothing(t *testing.T) {
	absent, err := json.Marshal(noUser)
	require.NoError(t, err)

	members := asContractMembers(t, absent)
	assert.Equal(t, map[string]interface{}{"result": "unresolvable"}, members,
		"an unresolvable carries one member, and the emptiness is the guarantee")

	absenceWithID, err := os.ReadFile(contractUserAbsenceWithID)
	require.NoError(t, err)
	assert.NotEqual(t, asContractMembers(t, absenceWithID), members,
		"an unresolvable naming a user_id is refused by the contract, not tolerated")

	// TWO OUTCOMES AND NOT THREE. There is deliberately no member for "erased",
	// because a vocabulary that could express the distinction is one an
	// implementation would eventually be asked to populate.
	erasedResult, err := os.ReadFile(contractUserErasedResult)
	require.NoError(t, err)
	assert.NotEqual(t, asContractMembers(t, erasedResult), members)
	assert.NotEqual(t, "erased", members["result"])
}
