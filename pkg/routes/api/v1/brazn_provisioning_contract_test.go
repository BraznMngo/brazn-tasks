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
