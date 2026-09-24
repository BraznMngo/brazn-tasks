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
	"fmt"
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollaborationCollaboratorCap (BRA-1064): the primary-team roster holds
// the owner plus max_collaborators outsiders; the next add is refused.
//
// Fills the roster to exactly 1+10 distinct people, then refuses the next via
// the HTTP API. CHEAP CHECK: delete requireCollaboratorCapacity's comparison
// (or the call from decideCollaborationMembership) and that add succeeds.
//
// Grants run only after the fill session commits — SQLite rejects a second
// writer while the fill session is still open ("database table is locked").
func TestCollaborationCollaboratorCap(t *testing.T) {
	env := newManagedEnv(t)

	const maxOutsiders = 10
	ceiling := maxOutsiders
	env.grantCollaboration(testuser1.ID, true, &ceiling)

	teamID := int64(1)

	s := db.NewSession()
	count, err := s.Where("team_id = ?", teamID).Count(&models.TeamMember{})
	require.NoError(t, err)

	var outsiderIDs []int64
	for n := 0; count < int64(1+maxOutsiders); n++ {
		created, err := user.CreateUser(s, &user.User{
			Username: fmt.Sprintf("collab_outsider_%d", n),
			Email:    fmt.Sprintf("collab_outsider_%d@example.com", n),
			Password: "12345678",
			Issuer:   "local",
		})
		require.NoError(t, err)
		_, err = s.Insert(&models.TeamMember{TeamID: teamID, UserID: created.ID})
		require.NoError(t, err)
		outsiderIDs = append(outsiderIDs, created.ID)
		count++
	}
	require.NoError(t, s.Commit())
	s.Close()
	require.Equal(t, int64(1+maxOutsiders), count)

	for _, id := range outsiderIDs {
		env.grantCollaboration(id, false, nil)
	}

	s = db.NewSession()
	extra, err := user.CreateUser(s, &user.User{
		Username: "collab_outsider_eleventh",
		Email:    "collab_outsider_eleventh@example.com",
		Password: "12345678",
		Issuer:   "local",
	})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
	s.Close()
	env.grantCollaboration(extra.ID, false, nil)

	rec := env.request(http.MethodPost,
		fmt.Sprintf("/api/v2/teams/%d/members", teamID),
		fmt.Sprintf(`{"username":%q}`, extra.Username),
		&testuser1)
	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
}

// TestCollaborationPersonalInviteeRefusedBySharingRule: a Personal account
// cannot be added as a collaborator — refused by the entitled-target check,
// not by the roster count (BRA-1064).
func TestCollaborationPersonalInviteeRefusedBySharingRule(t *testing.T) {
	env := newManagedEnv(t)
	ceiling := 10
	env.grantCollaboration(testuser1.ID, true, &ceiling)
	env.grant(testuser2.ID, entitlement.EditionPersonal, false)

	rec := env.request(http.MethodPost, "/api/v2/teams/1/members",
		`{"username":"user2"}`, &testuser1)
	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
}
