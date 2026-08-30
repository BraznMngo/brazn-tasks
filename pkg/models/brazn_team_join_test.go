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

package models

import (
	"context"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1475, criteria 4, 6 and 7, at the layer that decides them.
//
// **What is being tested is whether an invited person can see the team's shared
// work, and whether they acquire any ability their invitation withholds.** The
// membership row is the mechanism; access to the Team root is the outcome, and
// it is asserted directly below rather than inferred from the row, because
// project access is granted to the TEAM as a group and a row that failed to
// produce access would still read as a successful join.

// provisionedTeam gives one organization its topology under an administrator
// and answers the commercial ids and the fork's own Team root project.
func provisionedTeam(t *testing.T, admin, organizationID, teamID string) *ProtectedEntity {
	t.Helper()
	root, err := ProvisionTeamRoots(context.Background(), admin, organizationID, teamID)
	require.NoError(t, err)
	require.NotNil(t, root)
	return root
}

func teamMemberRow(t *testing.T, teamID, userID int64) *TeamMember {
	t.Helper()
	s := db.NewSession()
	defer s.Close()
	member := &TeamMember{}
	has, err := s.Where("team_id = ? AND user_id = ?", teamID, userID).Get(member)
	require.NoError(t, err)
	if !has {
		return nil
	}
	return member
}

// canReadTeamRoot asks the real permission question, on its own session, and
// closes it. Every helper here closes what it opens: an SQLite session left open
// locks the tables and every following test in the package fails while loading
// its fixtures, which is how this file first ran.
func canReadTeamRoot(t *testing.T, projectID int64, as *user.User) bool {
	t.Helper()
	s := db.NewSession()
	defer s.Close()
	project, err := GetProjectSimpleByID(s, projectID)
	require.NoError(t, err)
	can, _, err := project.CanRead(s, as)
	require.NoError(t, err)
	return can
}

func teamCount(t *testing.T) int64 {
	t.Helper()
	s := db.NewSession()
	defer s.Close()
	total, err := s.Count(&Team{})
	require.NoError(t, err)
	return total
}

// TestJoinProvisionedTeamMakesTheSharedWorkReachable is criterion 4's server
// half: "is working in the team's shared lists".
func TestJoinProvisionedTeamMakesTheSharedWorkReachable(t *testing.T) {
	seededInstance(t)
	root := provisionedTeam(t, "1", "org_join", "team_join")

	joiner := &user.User{ID: 2}

	// Before the join, the invited person cannot read the team's root at all.
	// This is the live defect the ticket exists for, asserted rather than
	// described: a complete, active Teams entitlement and no membership row is
	// an empty product.
	require.False(t, canReadTeamRoot(t, root.ProjectID, joiner),
		"the fixture must start with the person outside the team")

	require.NoError(t, JoinProvisionedTeam(context.Background(), "2", "org_join", "team_join"))

	// DELETE-THE-GUARD: replace JoinProvisionedTeam's `s.Insert(&TeamMember{...})`
	// with `return nil`. RUN: this test failed on the assertion below. Guard
	// restored. Nothing else in this file or in the commercial suite catches
	// that deletion, which is the point of asserting the OUTCOME here.
	assert.True(t, canReadTeamRoot(t, root.ProjectID, joiner),
		"the joined member can reach the team's shared root")
}

// TestJoinProvisionedTeamNeverMakesTheJoinerAnAdministrator is criterion 7's
// core, and the ticket's own prohibition: "Do not grant the accepting person the
// administrator's team-management ability so they can add themselves."
func TestJoinProvisionedTeamNeverMakesTheJoinerAnAdministrator(t *testing.T) {
	seededInstance(t)
	root := provisionedTeam(t, "1", "org_admin", "team_admin")

	require.NoError(t, JoinProvisionedTeam(context.Background(), "2", "org_admin", "team_admin"))

	member := teamMemberRow(t, root.TeamID, 2)
	require.NotNil(t, member, "a membership row must exist")
	// DELETE-THE-GUARD: change `Admin: false` to `Admin: true` in
	// JoinProvisionedTeam. RUN: this test failed here. Guard restored. Team
	// admin is what decides who may add and remove members, so the joiner would
	// have been able to put themselves — and anyone else — into the team.
	assert.False(t, member.Admin,
		"an invited member must never hold the ability to add members")
}

// TestJoinProvisionedTeamNeverCreatesATeam is the refusal the model comment
// argues for, and the reason it matters is the test above: a join that
// helpfully created the missing team would make the JOINER its administrator.
func TestJoinProvisionedTeamNeverCreatesATeam(t *testing.T) {
	seededInstance(t)
	provisionedTeam(t, "1", "org_none", "team_none")
	before := teamCount(t)

	err := JoinProvisionedTeam(context.Background(), "2", "org_none", "team_that_was_never_provisioned")

	// DELETE-THE-GUARD: replace `if root == nil { return ErrProvisioningTeamUnknown }`
	// in JoinProvisionedTeam with a call to provisionTeamRoots for the joining
	// subject. RUN: this test failed on both assertions — a team was minted and
	// the error was nil. Guard restored.
	require.ErrorIs(t, err, ErrProvisioningTeamUnknown)
	assert.Equal(t, before, teamCount(t), "a refused join mints no team")
}

// TestJoinProvisionedTeamCannotCrossBetweenCustomers is criterion 7's other
// half: the team is resolved from the (organization, commercial team) PAIR, so
// a commercial team id belonging to somebody else resolves to nothing.
func TestJoinProvisionedTeamCannotCrossBetweenCustomers(t *testing.T) {
	seededInstance(t)
	ours := provisionedTeam(t, "1", "org_ours", "team_ours")
	theirs := provisionedTeam(t, "3", "org_theirs", "team_theirs")
	require.NotEqual(t, ours.TeamID, theirs.TeamID)

	// Our organization, their team. A commercial team id is minted by a service
	// this fork does not own the namespace of, so without the organization in
	// the key this would resolve to another customer's team.
	err := JoinProvisionedTeam(context.Background(), "2", "org_ours", "team_theirs")

	// DELETE-THE-GUARD: drop `organization_id = ?` from provisionedTeamRoot's
	// WHERE clause. RUN: this test failed — the join succeeded and user 2 was
	// put into another customer's team. Guard restored.
	require.ErrorIs(t, err, ErrProvisioningTeamUnknown)
	assert.Nil(t, teamMemberRow(t, theirs.TeamID, 2),
		"nobody is put into a team belonging to another organization")
}

// TestJoinProvisionedTeamIsIdempotent — the commercial service may reach this
// after an admission it already performed, so a repeat must commit.
func TestJoinProvisionedTeamIsIdempotent(t *testing.T) {
	seededInstance(t)
	root := provisionedTeam(t, "1", "org_again", "team_again")

	require.NoError(t, JoinProvisionedTeam(context.Background(), "2", "org_again", "team_again"))
	require.NoError(t, JoinProvisionedTeam(context.Background(), "2", "org_again", "team_again"))

	s := db.NewSession()
	defer s.Close()
	rows, err := s.Where("team_id = ? AND user_id = ?", root.TeamID, int64(2)).Count(&TeamMember{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows, "a repeat adds no second membership row")
	member := teamMemberRow(t, root.TeamID, 2)
	require.NotNil(t, member)
	assert.False(t, member.Admin, "and a repeat does not promote anybody either")
}

// TestJoinProvisionedTeamRefusesASubjectThisInstanceDoesNotHave — the same
// refusal every operation on this channel makes, so that a producer defect
// arrives as a refusal rather than as a row against nobody.
func TestJoinProvisionedTeamRefusesASubjectThisInstanceDoesNotHave(t *testing.T) {
	seededInstance(t)
	root := provisionedTeam(t, "1", "org_ghost", "team_ghost")

	err := JoinProvisionedTeam(context.Background(), "999999", "org_ghost", "team_ghost")

	require.ErrorIs(t, err, ErrProvisioningSubjectUnknown)
	assert.Nil(t, teamMemberRow(t, root.TeamID, 999999))
}
