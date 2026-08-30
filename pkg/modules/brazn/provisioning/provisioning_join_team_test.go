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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The join_team payloads, written out as literals rather than built from the
// constants under test, for the reason the erase_subject file beside this one
// gives: a test that constructs its input from the code under test can only
// prove that the code agrees with itself.
//
// What the commercial service emits is these five members
// (cloud/service/src/fork.ts, createForkTopologyProvisioner.joinTeam), and the
// contract file cloud/contracts/v1/provisioning/join-team-request.schema.json
// is what fixes them.
const (
	joinTeam = `{"contract_version":"1","operation":"join_team",` +
		`"organization_id":"org_test","user_id":"42","team_id":"team_x"}`

	// THE SAME FIVE MEMBERS UNDER THE CREATING OPERATION'S NAME, which is the
	// whole hazard: a request to JOIN a team and a request to CREATE one are
	// indistinguishable in the bytes.
	createTeamRoots = `{"contract_version":"1","operation":"create_team_roots",` +
		`"organization_id":"org_test","user_id":"42","team_id":"team_x"}`
)

// TestOperationJoinTeamMatchesTheContract pins the constant against the literal
// the producer signs, so a typo here cannot pass every test in this package and
// be refused by the only party that matters.
func TestOperationJoinTeamMatchesTheContract(t *testing.T) {
	assert.Equal(t, "join_team", OperationJoinTeam)
	assert.NotEqual(t, OperationCreateTeamRoots, OperationJoinTeam,
		"the two carry identical payloads, so one shared name would carry out a join as a creation")
}

func TestDecodeJoinTeamReadsAWellFormedRequest(t *testing.T) {
	request, err := DecodeJoinTeam([]byte(joinTeam))
	require.NoError(t, err)

	assert.Equal(t, "org_test", request.OrganizationID)
	// A DECIMAL STRING, never a JSON number: the contract declares it as a
	// string, and a Go implementation marshalling an int64 would send 42.
	assert.Equal(t, "42", request.UserID)
	// The COMMERCIAL team id, matched against the row create_team_roots wrote.
	assert.Equal(t, "team_x", request.TeamID)
}

// TestDecodeJoinTeamRefusesATopologyCreationInDisguise is the direction the
// implementation guards.
//
// Routing cannot confuse the two through the channel, because Verify reads the
// operation out of the same signed bytes the decoder then checks. What this
// guards is an editing mistake in the switch.
//
// DELETE-THE-GUARD: remove `if request.Operation != OperationJoinTeam` from
// DecodeJoinTeam. RUN: this test failed with `expected error ErrInvalidRequest`.
// Guard restored.
func TestDecodeJoinTeamRefusesATopologyCreationInDisguise(t *testing.T) {
	_, err := DecodeJoinTeam([]byte(createTeamRoots))
	require.ErrorIs(t, err, ErrInvalidRequest)
}

// TestDecodeCreateTeamRootsStillAcceptsAJoinInDisguise RECORDS A GAP rather
// than protecting a guard, and it is written to pass so that it does not block
// a merge on its own.
//
// The guard above catches a switch mistake in ONE direction: a create_team_roots
// payload arriving at the join decoder, which if carried out would add an
// administrator to their own team as an ordinary member — untidy and harmless.
//
// THE DIRECTION THAT CARRIES THE HARM IS THE OTHER ONE and nothing checks it. A
// join_team payload reaching DecodeCreateTeamRoots decodes cleanly, because that
// decoder does not compare its operation member, and provisionTeamRoots makes
// its subject the team's CREATOR AND A TEAM ADMIN. The person that payload names
// is the invited member. So the editing mistake that would grant an invited
// member the team management their invitation withholds is the one still
// unguarded, while the comment on OperationJoinTeam says the check exists for
// exactly that reason.
//
// The fix is one line — the same `if request.Operation != OperationCreateTeamRoots`
// comparison, in DecodeCreateTeamRoots. When it lands, this test goes red and
// should be replaced by its mirror image.
func TestDecodeCreateTeamRootsStillAcceptsAJoinInDisguise(t *testing.T) {
	asCreation, err := DecodeCreateTeamRoots([]byte(joinTeam))
	require.NoError(t, err,
		"the join payload decodes cleanly as a create_team_roots one, which is the whole risk")
	assert.Equal(t, "42", asCreation.UserID)
	assert.Equal(t, "team_x", asCreation.TeamID)
}

func TestDecodeJoinTeamRefusesWhatItCannotAccept(t *testing.T) {
	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			// decodeExactly refuses a member this build cannot see rather than
			// dropping it silently. Here that matters because a member the
			// sender believed in and this build ignored would be a scoping rule
			// the sender thinks it applied and the receiver never saw.
			"a member this build cannot see",
			`{"contract_version":"1","operation":"join_team","admin":true,` +
				`"organization_id":"org_test","user_id":"42","team_id":"team_x"}`,
		},
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"join_team",` +
				`"organization_id":"org_test","user_id":"42","team_id":"team_x"}`,
		},
		{
			"a subject id that is not an identifier the contract could have minted",
			`{"contract_version":"1","operation":"join_team",` +
				`"organization_id":"org_test","user_id":"not a number","team_id":"team_x"}`,
		},
		{
			// Every bounded value here reaches a varchar(64) column, so an id
			// past the bound is one a store could truncate into a DIFFERENT
			// organization — which on this operation means putting somebody in
			// another customer's team.
			"an organization past the bound",
			`{"contract_version":"1","operation":"join_team","organization_id":"` +
				`11111111111111111111111111111111111111111111111111111111111111111` +
				`","user_id":"42","team_id":"team_x"}`,
		},
		{
			"a team past the bound",
			`{"contract_version":"1","operation":"join_team","organization_id":"org_test",` +
				`"user_id":"42","team_id":"` +
				`11111111111111111111111111111111111111111111111111111111111111111"}`,
		},
		{
			"an empty team",
			`{"contract_version":"1","operation":"join_team",` +
				`"organization_id":"org_test","user_id":"42","team_id":""}`,
		},
		{
			"an empty organization",
			`{"contract_version":"1","operation":"join_team",` +
				`"organization_id":"","user_id":"42","team_id":"team_x"}`,
		},
		{
			"an empty subject",
			`{"contract_version":"1","operation":"join_team",` +
				`"organization_id":"org_test","user_id":"","team_id":"team_x"}`,
		},
		{
			"content after the payload",
			`{"contract_version":"1","operation":"join_team",` +
				`"organization_id":"org_test","user_id":"42","team_id":"team_x"} {}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			_, err := DecodeJoinTeam([]byte(refused.payload))
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}
}
