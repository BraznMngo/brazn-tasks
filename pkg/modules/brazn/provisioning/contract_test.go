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

package provisioning

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The vendored conformance set, from the Percy repository. See
// testdata/contract/README.md for what it is and why a copy of it lives here.
//
// Constants rather than paths built at call time, deliberately: this is the form
// gosec resolves, and it is the one golden_test.go already uses in the
// entitlement package for the same reason.
const (
	contractCreateUser               = "testdata/contract/create-user-request.valid.conformance.json"
	contractCreateUserNumericVersion = "testdata/contract/create-user-request.invalid.numeric-contract-version.json"
	contractCreateUserNoAt           = "testdata/contract/create-user-request.invalid.mailbox-without-an-at.json"
	contractInbox                    = "testdata/contract/create-personal-inbox-request.valid.conformance.json"
	contractInboxWithTeamID          = "testdata/contract/create-personal-inbox-request.invalid.carries-a-team-id.json"
	contractInboxOpaqueUserID        = "testdata/contract/create-personal-inbox-request.invalid.opaque-user-id.json"
	contractTeamRoots                = "testdata/contract/create-team-roots-request.valid.conformance.json"
	contractTeamRootsNoTeamID        = "testdata/contract/create-team-roots-request.invalid.missing-team-id.json"
)

// TestContractOperationNamesAreTheContractsOwn pins this build's operation
// constants against the literals the contract carries.
//
// EVERY OTHER TEST IN THIS FILE WOULD PASS IF ALL OF THESE WERE RENAMED
// TOGETHER, which is the whole reason this one is separate. The decoders below
// do not check the operation member — only DecodeEraseSubject does, once
// BRA-1103 lands — so a payload decodes into its type whatever this build calls
// the operation, and only the route's switch would notice. That switch reads
// these constants, so a rename here silently stops matching what Percy sends and
// every request becomes the flat 400 for an operation this build does not
// define.
//
// The literals are written out rather than taken from the fixtures, because a
// fixture is a payload and these are the names the ROUTE dispatches on. Both
// halves are pinned: this test ties the constants to the contract text, and the
// decode tests tie the fixtures to the constants.
func TestContractOperationNamesAreTheContractsOwn(t *testing.T) {
	assert.Equal(t, "1", ContractVersion)
	assert.Equal(t, "create_user", OperationCreateUser)
	assert.Equal(t, "create_personal_inbox", OperationCreatePersonalInbox)
	assert.Equal(t, "create_team_roots", OperationCreateTeamRoots)
	// The signing domain, including its terminating newline. Percy's constant is
	// deliberately not exported on its own side for this exact reason: a test
	// that imported the producer's value would check one definition against
	// itself, and the failure that hides is a typo that signs, verifies and stays
	// green on both halves while the real counterparty rejects everything.
	assert.Equal(t, "percy.provisioning.v1\n", SigningDomain)
}

// TestContractCreateUserRequestDecodes runs the frozen create_user payload
// through the production decoder.
//
// This is the operation BRA-1106 was about, and the reason its fixture is here
// rather than in a table of hand-written cases: the bytes were produced by
// Percy's actual transport and captured off the wire, so nothing about this
// assertion is a conversation with ourselves.
func TestContractCreateUserRequestDecodes(t *testing.T) {
	raw, err := os.ReadFile(contractCreateUser)
	require.NoError(t, err)

	request, err := DecodeCreateUser(raw)
	require.NoError(t, err,
		"the frozen create_user payload must decode, or this build cannot accept a real signup")

	assert.Equal(t, ContractVersion, request.ContractVersion)
	assert.Equal(t, OperationCreateUser, request.Operation)
	// THE MAILBOX IS UNTRANSFORMED. Case folding and plus-address stripping
	// belong to the commercial layer, which owns the mailbox and knows which two
	// spellings it considers one customer; a fork that folded them would merge
	// two accounts the sender believes are separate.
	assert.Equal(t, "dana@acme.example", request.Email)
}

// TestContractCreateUserRequestNegatives are the two refusals the contract
// records for this operation, and neither is decoration.
//
// DELETING THE GUARD MUST MAKE EACH FAIL. The version check is what stops a
// newer contract's payload being decoded by a build that cannot see half its
// members; the "@" check is what stops a non-address becoming a real user's
// address, because user.CreateUser requires the field to be non-empty and
// validates nothing about its shape.
func TestContractCreateUserRequestNegatives(t *testing.T) {
	numericVersion, err := os.ReadFile(contractCreateUserNumericVersion)
	require.NoError(t, err)
	_, err = DecodeCreateUser(numericVersion)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"contract_version is a STRING: a JSON number must not decode into it")

	noAt, err := os.ReadFile(contractCreateUserNoAt)
	require.NoError(t, err)
	_, err = DecodeCreateUser(noAt)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"a mailbox with no @ is not one, and nothing downstream will say so")
}

// TestContractCreatePersonalInboxRequestDecodes runs the frozen
// create_personal_inbox payload through the production decoder.
func TestContractCreatePersonalInboxRequestDecodes(t *testing.T) {
	raw, err := os.ReadFile(contractInbox)
	require.NoError(t, err)

	request, err := DecodeCreatePersonalInbox(raw)
	require.NoError(t, err)

	assert.Equal(t, ContractVersion, request.ContractVersion)
	assert.Equal(t, OperationCreatePersonalInbox, request.Operation)
	assert.Equal(t, "org_3d77e0c15a84", request.OrganizationID)
	assert.Equal(t, "42", request.UserID)
}

// TestContractCreatePersonalInboxRefusesAnUndeclaredMember is the strictness
// guarantee, stated by the contract as a property both sides rely on.
//
// The fixture is a create_personal_inbox carrying a team_id. Accepting it would
// carry the request out as if the team were not there — a silent
// half-application, which is exactly what decodeExactly's DisallowUnknownFields
// exists to convert into a visible failure. Percy's schemas write
// additionalProperties:false as the mirror of this decoder, so if this stopped
// refusing, the two sides would disagree about what a payload MEANS while both
// still parsed it.
func TestContractCreatePersonalInboxRefusesAnUndeclaredMember(t *testing.T) {
	raw, err := os.ReadFile(contractInboxWithTeamID)
	require.NoError(t, err)

	_, err = DecodeCreatePersonalInbox(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"a member this operation does not declare must be refused, never ignored")
}

// TestContractCreatePersonalInboxAcceptsAnIdPercyWillNeverSend pins a
// DISAGREEMENT between the two sides rather than an agreement, which is why it
// asserts acceptance of a fixture named ".invalid".
//
// The contract states ^[1-9][0-9]{0,18}$ for user_id, because the value is this
// instance's own users.id. commercialID here admits ^[A-Za-z0-9_-]{1,64}$ for
// every identifier on the channel, so "acct_3d77e0c15a84" passes. Both are
// correct: Percy's contract set runs on strict producers and tolerant consumers,
// and the narrower rule is the producer's.
//
// IT IS ASSERTED RATHER THAN IGNORED BECAUSE THE GAP IS NOT HARMLESS. For a
// value inside it, erase_subject answers the flat 400 and resolve_mailbox answers
// 200 unresolvable — two functions in this repository giving opposite answers to
// the same malformed id on an identical payload. Whoever tightens commercialID
// gets a red test here and makes that decision deliberately, instead of
// discovering it.
func TestContractCreatePersonalInboxAcceptsAnIdPercyWillNeverSend(t *testing.T) {
	raw, err := os.ReadFile(contractInboxOpaqueUserID)
	require.NoError(t, err)

	request, err := DecodeCreatePersonalInbox(raw)
	require.NoError(t, err,
		"commercialID is deliberately wider than the contract's user_id; if this now refuses, "+
			"the widening was tightened and cloud/contracts/README.md must be updated with it")
	assert.Equal(t, "acct_3d77e0c15a84", request.UserID)
}

// TestContractCreateTeamRootsRequestDecodes runs the frozen create_team_roots
// payload through the production decoder.
//
// TEAM_ID IS THE COMMERCIAL ID AND NEVER THIS INSTANCE'S, and nothing derives one
// from the other in either direction. It is checked in the decoder rather than
// left to the write because it is what a LATER call is matched against: an id
// stored in some other shape than the one that arrives next time would provision
// a second set of roots for one team, and the commercial record coalesces a
// team's pair in exactly once.
func TestContractCreateTeamRootsRequestDecodes(t *testing.T) {
	raw, err := os.ReadFile(contractTeamRoots)
	require.NoError(t, err)

	request, err := DecodeCreateTeamRoots(raw)
	require.NoError(t, err)

	assert.Equal(t, ContractVersion, request.ContractVersion)
	assert.Equal(t, OperationCreateTeamRoots, request.Operation)
	assert.Equal(t, "org_3d77e0c15a84", request.OrganizationID)
	assert.Equal(t, "42", request.UserID)
	assert.Equal(t, "team_9f2c41ab7d30", request.TeamID)
}

// TestContractCreateTeamRootsRefusesAMissingTeamID is the negative for the member
// this operation exists to carry.
//
// An absent team_id decodes to "", which commercialID rejects for its length
// bound. Without that, a payload naming no team would provision roots that no
// later call could match, and the second attempt would mint a second set.
func TestContractCreateTeamRootsRefusesAMissingTeamID(t *testing.T) {
	raw, err := os.ReadFile(contractTeamRootsNoTeamID)
	require.NoError(t, err)

	_, err = DecodeCreateTeamRoots(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"create_team_roots without a team id names nothing a later call could match")
}
