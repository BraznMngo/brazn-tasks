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
	contractResolveUserByEmail       = "testdata/contract/user-resolution-request.valid.conformance-by-email.json"
	contractResolveUserByID          = "testdata/contract/user-resolution-request.valid.conformance-by-user-id.json"
	contractResolveUserBoth          = "testdata/contract/user-resolution-request.invalid.both-identifiers.json"
	contractResolveUserNeither       = "testdata/contract/user-resolution-request.invalid.neither-identifier.json"
	contractResolveUserNumericID     = "testdata/contract/user-resolution-request.invalid.numeric-user-id.json"
	contractResolveUserCreateUserOp  = "testdata/contract/user-resolution-request.invalid.create-user-operation.json"
	contractResolveUserAccountKey    = "testdata/contract/user-resolution-request.invalid.provisional-account-key.json"
)

// TestContractOperationNamesAreTheContractsOwn pins this build's operation
// constants against the literals the contract carries.
//
// EVERY OTHER TEST IN THIS FILE WOULD PASS IF ALL OF THESE WERE RENAMED
// TOGETHER, which is the whole reason this one is separate. Most of the decoders
// below do not check the operation member — only DecodeEraseSubject and
// DecodeResolveUser do, each for its own reason — so a payload decodes into its
// type whatever this build calls the operation, and only the route's switch
// would notice. That switch reads these constants, so a rename here silently
// stops matching what Percy sends and every request becomes the flat 400 for an
// operation this build does not define.
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
	assert.Equal(t, "resolve_mailbox", OperationResolveMailbox)
	assert.Equal(t, "erase_subject", OperationEraseSubject)
	assert.Equal(t, "resolve_user", OperationResolveUser)
	// The six are pairwise distinct, and resolve_user against create_user is the
	// pair that matters: their payloads are a superset and a subset, so one name
	// for both would let the switch answer a question about an address by
	// PROVISIONING one — BRA-1106's defect, from this side of the seam.
	assert.NotEqual(t, OperationCreateUser, OperationResolveUser)
	assert.NotEqual(t, OperationResolveMailbox, OperationResolveUser)
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

// TestContractResolveUserRequestDecodes runs both frozen resolve_user payloads
// through the production decoder.
//
// The two forms are one operation because they are one lookup on one row with
// one projection: `email` is recognition, which signUp and registerOrganization
// need, and `user_id` is verification, which requireVerifiedAccount has had no
// source for since BRA-1084 — it arrives holding a bearer and an id and NO
// address, because the commercial layer stores none.
//
// EVERY LITERAL BELOW IS WRITTEN HERE RATHER THAN READ FROM THE FIXTURE, per
// CLAUDE.md. An interop value both sides take from one definition is checked by
// neither: renaming a member in the fixture and in this build together stays
// green while Percy's caller rejects every message.
func TestContractResolveUserRequestDecodes(t *testing.T) {
	byEmail, err := os.ReadFile(contractResolveUserByEmail)
	require.NoError(t, err)

	recognition, err := DecodeResolveUser(byEmail)
	require.NoError(t, err,
		"the frozen recognition payload must decode, or no signup can converge on an existing account")

	assert.Equal(t, ContractVersion, recognition.ContractVersion)
	assert.Equal(t, OperationResolveUser, recognition.Operation)
	// UNTRANSFORMED, for the reason create_user's mailbox is: the commercial
	// layer owns the address and knows which two spellings it treats as one
	// customer, and a fork that folded them would merge two accounts the sender
	// believes are separate.
	assert.Equal(t, "ada@example.com", recognition.Email)
	assert.Empty(t, recognition.UserID, "the recognition form carries no id")

	byID, err := os.ReadFile(contractResolveUserByID)
	require.NoError(t, err)

	verification, err := DecodeResolveUser(byID)
	require.NoError(t, err,
		"the frozen verification payload must decode, or no customer can connect a device")

	assert.Equal(t, ContractVersion, verification.ContractVersion)
	assert.Equal(t, OperationResolveUser, verification.Operation)
	// A DECIMAL STRING. The field is declared as a Go string, so a JSON number
	// fails to unmarshal rather than being coerced — which is the assertion
	// TestContractResolveUserRefusesANumericUserID makes from the other side.
	assert.Equal(t, "9001", verification.UserID)
	assert.Empty(t, verification.Email, "the verification form carries no address")
}

// TestContractResolveUserRefusesBothIdentifiers is the presence rule, and it is
// the one refusal on this operation that NOTHING ELSE CATCHES.
//
// The fixture carries a valid address and a valid id together. Every other check
// in the decoder passes on it: the version matches, the operation matches, the
// address has an "@" and is inside the column width, and the id matches
// commercialID. DisallowUnknownFields cannot see it either — both members are
// declared, so the payload decodes perfectly and means something the contract
// refuses to define.
//
// Deleting the guard: remove the equality between the two emptiness tests in
// DecodeResolveUser and this fixture is accepted, taking the email branch by
// accident of statement order. That is precedence invented at the receiver for a
// caller that asserted a PAIRING rather than asking about one — and neither side
// of this seam should have to decide which member wins.
func TestContractResolveUserRefusesBothIdentifiers(t *testing.T) {
	raw, err := os.ReadFile(contractResolveUserBoth)
	require.NoError(t, err)

	_, err = DecodeResolveUser(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"both identifiers is refused, never resolved by a precedence this build invented")
}

// TestContractResolveUserRefusesNeitherIdentifier is the other half of the
// presence rule: a request that asks nothing.
//
// ⚠ THIS ONE IS REFUSED TWICE OVER AND THE TEST IS NOT PROOF OF THE PRESENCE
// RULE, which is written here so nobody later reads it as one. With the presence
// check deleted, an empty user_id still fails commercialID's length bound, so
// this fixture stays refused by a guard that has nothing to do with the rule
// under test. TestContractResolveUserRefusesBothIdentifiers is the assertion
// that isolates the presence rule; this one pins the observable behaviour.
func TestContractResolveUserRefusesNeitherIdentifier(t *testing.T) {
	raw, err := os.ReadFile(contractResolveUserNeither)
	require.NoError(t, err)

	_, err = DecodeResolveUser(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"a resolve_user naming no subject asks nothing this build could answer")
}

// TestContractResolveUserRefusesANumericUserID is the single most likely defect
// on this seam, per the contract's own response schema.
//
// The fixture carries `"user_id": 9001` — a JSON number. THE GO TYPE IS THE
// GUARD: UserID is declared as a string, so encoding/json fails to unmarshal
// rather than coercing. A `pattern` check on the consumer coerces and would
// accept it, which is exactly why the refusal has to live in a type on this
// side. Deleting the guard means declaring the field as json.Number or
// interface{}, at which point this fixture decodes and one side of the seam is
// silently accepting what the other cannot produce.
func TestContractResolveUserRefusesANumericUserID(t *testing.T) {
	raw, err := os.ReadFile(contractResolveUserNumericID)
	require.NoError(t, err)

	_, err = DecodeResolveUser(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"user_id is a decimal STRING: a JSON number must not decode into it")
}

// TestContractResolveUserRefusesAnotherOperationsName is the arms-are-not-
// interchangeable assertion, and on this pair it carries more than anywhere else
// on the channel.
//
// The fixture is character for character a valid create_user payload — a version,
// an operation and a mailbox — which is precisely the point: resolve_user's
// recognition form is create_user's payload plus an optional id, so the operation
// member is the WHOLE of the difference between asking about an address and
// provisioning one. A switch case pointing one at the other's decoder is the one
// editing error that would matter, and it would turn a read into the write that
// BRA-1106 fixed.
//
// Deleting the guard: remove the Operation comparison in DecodeResolveUser and
// this fixture decodes cleanly — every other check passes on it.
func TestContractResolveUserRefusesAnotherOperationsName(t *testing.T) {
	raw, err := os.ReadFile(contractResolveUserCreateUserOp)
	require.NoError(t, err)

	_, err = DecodeResolveUser(raw)
	require.ErrorIs(t, err, ErrInvalidRequest,
		"a create_user payload must not decode as a resolve_user: the read must never become the write")
}

// TestContractResolveUserAcceptsAnIdPercyWillNeverSend pins a DISAGREEMENT
// between the two sides rather than an agreement, which is why it asserts
// acceptance of a fixture named ".invalid" — the same shape as the
// create_personal_inbox case above, and for the same reason.
//
// The contract states ^[1-9][0-9]{0,18}$ and an account still under its
// provisional `acct_…` key is never sent here at all: it has no fork user, so
// there is nothing to ask about. commercialID admits the shape anyway, on the
// "strict producers, tolerant consumers" split, and NOTHING IS LOST BY THAT HERE
// — models.ResolveUserBySubject cannot parse it as an id and answers
// `unresolvable`, which is a legitimate answer to a question about a subject
// this instance does not have.
//
// It is asserted rather than ignored because whoever later tightens commercialID
// gets a red test and makes the decision deliberately instead of discovering it.
func TestContractResolveUserAcceptsAnIdPercyWillNeverSend(t *testing.T) {
	raw, err := os.ReadFile(contractResolveUserAccountKey)
	require.NoError(t, err)

	request, err := DecodeResolveUser(raw)
	require.NoError(t, err,
		"commercialID is deliberately wider than the contract's user_id; if this now refuses, "+
			"the widening was tightened and cloud/contracts/README.md must be updated with it")
	assert.Equal(t, "acct_2f0a9c31b7e84d5f", request.UserID)
}
