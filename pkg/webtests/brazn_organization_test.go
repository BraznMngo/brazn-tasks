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
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Organization area, observed over real HTTP through the real route table
// (BRA-917).
//
// EVERY TEST HERE IS ABOUT THE ROUTE, not about what a frontend renders. AC1's
// bar is discovery, and a menu entry that is not drawn satisfies exactly half
// of it: the other half is that a member who types the URL, keeps a stale tab
// open, or reaches for curl is refused by the server. Only the second half is
// checkable here, and it is the half that is load-bearing.

const organizationPath = "/api/v1/brazn/organization"
const organizationTeamsPath = organizationPath + "/teams"

// otherOrganization is a second customer on the same instance. It exists so
// that "one organization cannot reach another's team" is asserted against a
// team that really belongs to somebody else, rather than against a team id
// nobody ever created - which would pass for the wrong reason.
const otherOrganization = "org_other"

func seats(n int) *int { return &n }

// newOrganizationEnv is an organization with one administrator (testuser1),
// one ordinary member (testuser6), a provisioned primary team root, and a
// personal account elsewhere on the instance.
//
// `seatsPurchased` is a pointer and is passed through untouched, because the
// absent case is one of the things under test.
func newOrganizationEnv(t *testing.T, seatsPurchased *int) (*managedEnv, int64) {
	t.Helper()

	env := newManagedEnv(t)
	env.grantSeats(testuser1.ID, true, seatsPurchased)
	env.grantSeats(testuser6.ID, false, nil)
	env.grant(testuser2.ID, entitlement.EditionPersonal, false)

	// The primary team and its root are provisioned with the organization. It
	// is the team the removal test must refuse, and the one team every
	// capacity sum below starts from.
	primaryRoot := env.newProject(&testuser1, "Nordwind", 0)
	env.protectFor(models.ProtectedKindTeamRoot, primaryRoot, primaryTeamID, managedTestOrganization)

	return env, primaryRoot
}

// primaryTeamID is a team from the fixtures, standing in for the one the
// commercial service provisions with the organization.
const primaryTeamID = 1

// TestOrganizationRoutesRefuseAnOrdinaryMember is BRA-917 AC1, stated as the
// only thing that can actually stop somebody.
//
// THE DIFFERENTIAL IS THE POINT. Each case is run twice against the identical
// route: once as the administrator and once as an ordinary member of the SAME
// organization, on the SAME edition, with a valid active projection in both
// cases. So a refusal cannot be coming from the edition rule, from a missing
// entitlement, from an unregistered route or from authentication - the only
// thing that differs between the two runs is `organization_admin`.
//
// WHAT MAKES IT FAIL: delete the `!acting.State.OrganizationAdmin` clause in
// models.OrganizationFor. The member's 403s become the administrator's answers
// - 200 on the read, and the creation reaching its handler - and every
// sub-test below goes red. The administrator half is what stops the test
// passing for the boring reason that everything is refused.
func TestOrganizationRoutesRefuseAnOrdinaryMember(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(9))

	for _, route := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"reading the organization", http.MethodGet, organizationPath, ""},
		{"creating a team", http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`},
		{"removing a team", http.MethodDelete,
			fmt.Sprintf("%s/%d", organizationTeamsPath, primaryTeamID), ""},
	} {
		t.Run(route.name+", as an ordinary member", func(t *testing.T) {
			rec := env.request(route.method, route.path, route.body, &testuser6)
			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		})
	}

	// The other half of the differential: the administrator reaches the same
	// read route and gets an answer. Without this, deleting the guard would
	// still leave the assertions above meaningful only by luck.
	t.Run("and the administrator reaches the same route", func(t *testing.T) {
		rec := env.request(http.MethodGet, organizationPath, "", &testuser1)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	// A personal account has no organization to administer at all, and is
	// refused on both kinds of route: the read by the handler's own check, the
	// creation by decideByEdition, which has no organization-admin policy
	// registered for the Personal edition and treats unmapped as deny.
	t.Run("and a personal account cannot reach it either", func(t *testing.T) {
		read := env.request(http.MethodGet, organizationPath, "", &testuser2)
		assert.Equal(t, http.StatusForbidden, read.Code, read.Body.String())

		create := env.request(http.MethodPut, organizationTeamsPath, `{"name":"x"}`, &testuser2)
		assert.Equal(t, http.StatusForbidden, create.Code, create.Body.String())
	})
}

// TestTheOrganizationReadModelCountsSeatsAndTeams checks the numbers the
// Overview, Seats and Teams pages all render, in the one place they come from.
//
// WHAT MAKES IT FAIL: change SeatsPerTeam, or make teamsAllowed round the
// other way, and `teams_allowed` stops being 3. The expected values are
// written as literals here - 2 occupied, 9 purchased, 1 team used, 3 allowed -
// derived by hand from the fixture rather than from any function the product
// also uses, so the test cannot agree with a broken implementation.
func TestTheOrganizationReadModelCountsSeatsAndTeams(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(9))

	rec := env.request(http.MethodGet, organizationPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	organization := &models.Organization{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), organization))

	assert.Equal(t, managedTestOrganization, organization.ID)
	// testuser1 and testuser6. The personal account is a different
	// organization's subject and must not be counted into this one.
	assert.Equal(t, 2, organization.SeatsOccupied)
	assert.Len(t, organization.Members, 2)
	require.NotNil(t, organization.SeatsPurchased)
	assert.Equal(t, 9, *organization.SeatsPurchased)
	assert.Equal(t, 1, organization.TeamsUsed)
	require.NotNil(t, organization.TeamsAllowed)
	assert.Equal(t, 3, *organization.TeamsAllowed)
	assert.True(t, organization.CanCreateTeam)
	// The ratio, sent so the surface does not hold its own copy. Pinned here as
	// a literal against product rule 2.3 rather than against models.SeatsPerTeam,
	// which is the value under test - a test importing the constant would agree
	// with whatever it was changed to.
	assert.Equal(t, 3, organization.SeatsPerTeam)

	// The primary flag is what decides whether a removal control is drawn at
	// all, so the server has to be the one that says it - and it has to agree
	// with what RemoveOrganizationTeam refuses, which TestThePrimaryTeamCannot
	// BeRemoved checks from the other side.
	//
	// WHAT MAKES THIS FAIL: change organizationTeams' ordering away from the
	// oldest root, and the primary team stops being the one marked primary.
	require.Len(t, organization.Teams, 1)
	assert.Equal(t, int64(primaryTeamID), organization.Teams[0].TeamID)
	assert.True(t, organization.Teams[0].Primary)

	require.NotNil(t, organization.Administrator)
	assert.Equal(t, testuser1.ID, organization.Administrator.UserID)
}

// TestTeamCreationIsRefusedBeyondPurchasedSeats is AC2's arithmetic, and the
// refusal AC2 asks to be actionable.
//
// The fixture buys SIX seats and already holds one team, so a second team is
// exactly affordable and a third is exactly not. Both edges are asserted,
// because a rule that is off by one passes whichever single case you pick.
//
// WHAT MAKES IT FAIL: make models.CanCreateTeam return true unconditionally,
// and the refusal below becomes a 201.
func TestTeamCreationIsRefusedBeyondPurchasedSeats(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(6))

	t.Run("the second team fits in six seats", func(t *testing.T) {
		rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("and the new team is not the primary one", func(t *testing.T) {
		rec := env.request(http.MethodGet, organizationPath, "", &testuser1)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		organization := &models.Organization{}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), organization))
		require.Len(t, organization.Teams, 2)
		assert.True(t, organization.Teams[0].Primary, "the provisioned team stays primary")
		assert.False(t, organization.Teams[1].Primary, "a team created later never becomes it")
	})

	t.Run("and the third does not", func(t *testing.T) {
		rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Technik"}`, &testuser1)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

		refusal := struct {
			SeatsPurchased *int `json:"seats_purchased"`
			TeamsUsed      int  `json:"teams_used"`
			SeatsNeeded    *int `json:"seats_needed"`
		}{}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))

		// The guidance a customer acts on: they hold 6, they have 2 teams, and
		// a third needs 9. Literal numbers, not a recomputation of the rule.
		require.NotNil(t, refusal.SeatsPurchased)
		assert.Equal(t, 6, *refusal.SeatsPurchased)
		assert.Equal(t, 2, refusal.TeamsUsed)
		require.NotNil(t, refusal.SeatsNeeded)
		assert.Equal(t, 9, *refusal.SeatsNeeded)
	})
}

// TestTeamCreationIsRefusedWhenTheSeatCountIsMissing is the contract decision
// that absence refuses, checked where it bites.
//
// `seats_purchased` is optional, and this is the one member where the
// `valid_to` doctrine inverts: an absent end date read as "no end" would grant
// time nobody bought, so absence there is unsafe - where an absent count read
// as "no limit" would grant capacity nobody bought, so absence here must
// refuse. A projection minted before the member existed does not become
// unenforceable; it becomes a refusal.
//
// WHAT MAKES IT FAIL: change the `seatsPurchased == nil` branch in
// models.CanCreateTeam from false to true, and this 409 becomes a 201.
func TestTeamCreationIsRefusedWhenTheSeatCountIsMissing(t *testing.T) {
	env, _ := newOrganizationEnv(t, nil)

	rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	refusal := struct {
		SeatsPurchased *int `json:"seats_purchased"`
		SeatsNeeded    *int `json:"seats_needed"`
	}{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))

	// Null, not zero. "Nobody told us" and "they bought none" are different
	// facts with different remedies, and a customer sent to buy seats they
	// already own is being sent by a product that guessed.
	assert.Nil(t, refusal.SeatsPurchased)
	assert.Nil(t, refusal.SeatsNeeded)
}

// TestASecondAdministratorStopsTheOrganizationSurface is the fork's half of
// AC3.
//
// This product cannot PERFORM an administrator transfer: `organization_admin`
// is authoritative from the commercial service and the contract forbids
// granting, inferring or repairing it locally. What it can do is refuse to act
// while the answer is ambiguous - so a transfer that went wrong and left two
// administrators stops both of them, instead of letting either carry on and
// letting the two of them make conflicting changes.
//
// WHAT MAKES IT FAIL: delete the `len(administrators) != 1` refusal in
// models.OrganizationFor and this 403 becomes a 200.
func TestASecondAdministratorStopsTheOrganizationSurface(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(9))

	// The same organization now has two subjects claiming administration.
	env.revoke(testuser6.ID)
	env.grantSeats(testuser6.ID, true, seats(9))

	for _, actor := range []struct {
		name string
		as   *user.User
	}{
		{"the original administrator", &testuser1},
		{"and the second one", &testuser6},
	} {
		t.Run(actor.name+" is refused", func(t *testing.T) {
			rec := env.request(http.MethodGet, organizationPath, "", actor.as)
			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		})
	}
}

// TestThePrimaryTeamCannotBeRemoved is the protected root refusing.
//
// WHAT MAKES IT FAIL: delete the `primary.TeamID == teamID` check in
// models.RemoveOrganizationTeam and the primary team is removed with a 200.
func TestThePrimaryTeamCannotBeRemoved(t *testing.T) {
	env, primaryRoot := newOrganizationEnv(t, seats(9))

	rec := env.request(http.MethodDelete,
		fmt.Sprintf("%s/%d", organizationTeamsPath, primaryTeamID), "", &testuser1)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	assert.True(t, env.projectExists(primaryRoot),
		"the primary Team root must still be there after the refusal")
}

// TestRemovingATeamTakesItsOwnWorkAndNothingElse is BRA-917 AC4, reworded
// against BRA-787 because AC4's own citation - BRA-915's "trusted
// reconciliation flow" - names a ticket that was cancelled. What survived is
// the protected topology, and this is the property that topology is for.
//
// The fixture builds the case that separates a correct removal from a
// plausible one: a project that USED to be under the removed team's root and
// was moved out before the removal. A removal that worked from team membership
// or from who had access would take it; a removal that deletes the root's
// subtree does not, because by then it is not in that subtree.
//
// WHAT MAKES IT FAIL: widen models.RemoveOrganizationTeam beyond the root's own
// subtree - delete by the team's project relations, or by organization - and
// the moved-out project and the Public root go with it.
func TestRemovingATeamTakesItsOwnWorkAndNothingElse(t *testing.T) {
	env, primaryRoot := newOrganizationEnv(t, seats(9))

	publicRoot := env.newProject(&testuser1, "Public", 0)
	env.protectFor(models.ProtectedKindPublicRoot, publicRoot, 0, managedTestOrganization)

	created := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	team := &models.Team{}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), team))
	teamRoot := teamRootProject(t, team.ID)

	insideTheTeam := env.newProject(&testuser1, "Sprint 4", teamRoot)
	movedOut := env.newProject(&testuser1, "Handbook", teamRoot)
	env.reparent(movedOut, publicRoot, &testuser1)

	rec := env.request(http.MethodDelete,
		fmt.Sprintf("%s/%d", organizationTeamsPath, team.ID), "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.False(t, env.projectExists(teamRoot), "the team's own root goes")
	assert.False(t, env.projectExists(insideTheTeam), "and the work inside it goes with it")

	assert.True(t, env.projectExists(movedOut),
		"a project moved out of the team before the removal is not the team's work any more")
	assert.True(t, env.projectExists(publicRoot), "the Public root is untouched")
	assert.True(t, env.projectExists(primaryRoot), "the primary team's root is untouched")
	assert.True(t, env.projectExists(fixtureInboxProjectID), "and no member's Inbox is touched")
}

// TestATeamOfAnotherOrganizationCannotBeRemoved is the tenancy half, and it is
// asserted against a team that genuinely belongs to a second customer rather
// than against an id nobody provisioned.
//
// WHAT MAKES IT FAIL: drop `organization_id = ?` from the Team root lookup in
// models.RemoveOrganizationTeam and this 404 becomes a 200, with another
// customer's projects deleted.
func TestATeamOfAnotherOrganizationCannotBeRemoved(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(9))

	const otherTeamID = 2
	env.grantIn(testuser10.ID, otherOrganization, seats(9))
	otherRoot := env.newProject(&testuser10, "Someone else's team", 0)
	env.protectFor(models.ProtectedKindTeamRoot, otherRoot, otherTeamID, otherOrganization)

	rec := env.request(http.MethodDelete,
		fmt.Sprintf("%s/%d", organizationTeamsPath, otherTeamID), "", &testuser1)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	assert.True(t, env.projectExists(otherRoot),
		"another organization's Team root must still be there")
}

// teamRootProject finds the project a team's root was provisioned as. It reads
// the protected entity rather than guessing by title, because two teams may be
// named the same thing and only the id is the binding.
func teamRootProject(t *testing.T, teamID int64) int64 {
	t.Helper()

	s := dbSessionForTest(t)
	root := &models.ProtectedEntity{}
	has, err := s.Where("kind = ? AND team_id = ?", string(models.ProtectedKindTeamRoot), teamID).Get(root)
	require.NoError(t, err)
	require.True(t, has, "creating a team must provision its Team root")
	return root.ProjectID
}
