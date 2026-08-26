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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
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

// organizationDisplayName is the name the fixture organization registered with
// the commercial service - the value `state.organization_name` carries on the
// administrator's projection (BRA-1439 Story 2). A literal here, so the
// round-trip assertion cannot agree with a broken read model.
const organizationDisplayName = "Nordwind Logistik"

func displayName() *string {
	name := organizationDisplayName
	return &name
}

// newOrganizationEnv is an organization with one administrator (testuser1),
// one ordinary member (testuser6), a provisioned primary team root, and a
// personal account elsewhere on the instance.
//
// `seatsPurchased` is a pointer and is passed through untouched, because the
// absent case is one of the things under test.
func newOrganizationEnv(t *testing.T, seatsPurchased *int) (*managedEnv, int64) {
	t.Helper()

	env := newManagedEnv(t)
	env.grantSeatsNamed(testuser1.ID, true, seatsPurchased, displayName())
	env.grantSeats(testuser6.ID, false, nil)

	// The personal account is granted into a DIFFERENT organization, and that
	// is load-bearing rather than tidy. env.grant defaults to
	// managedTestOrganization, so a personal account granted the ordinary way
	// would be a member of the very organization whose roster the read-model
	// test counts - it would count three, the assertion would say two, and the
	// comment explaining why would be false. A subject in nobody else's
	// organization is what "a personal account on this instance" actually is.
	env.grantProjection(testuser2.ID, entitlement.EditionPersonal, false, nil, nil, nil, otherOrganization, nil)

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
	// The registered display name, carried on the administrator's projection
	// and served so no screen has to show the org_ identifier (BRA-1439
	// Story 2). Asserted against the fixture literal, not against anything the
	// product computed.
	require.NotNil(t, organization.Name)
	assert.Equal(t, organizationDisplayName, *organization.Name)
	// testuser1 and testuser6, and NOT the personal account - which holds an
	// active projection on this same instance, for a different organization.
	// That is what makes this an assertion about the organization filter rather
	// than about how many projections exist.
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

// TestTeamCreationIsNeverRefusedForSeats is Sebastian's decision of
// 2026-08-26 (BRA-1439 Story 9), asserted where the old rule used to bite.
// The fixture holds SIX seats and one team; under the removed rule a second
// team was exactly affordable and a third exactly not, so the third creation
// is the edge that proves the refusal is gone rather than merely moved.
//
// WHAT MAKES IT FAIL: put the old `purchased >= 3 x (teams + 1)` refusal back
// into models.CreateOrganizationTeam, and the third creation below answers
// 409 again.
func TestTeamCreationIsNeverRefusedForSeats(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(6))

	t.Run("the second team is created", func(t *testing.T) {
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

	t.Run("and so is the third, which the old seat rule refused", func(t *testing.T) {
		rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Technik"}`, &testuser1)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("and the read model keeps saying a team can be created", func(t *testing.T) {
		// Three teams on six seats - the old allowance was two - and the server
		// still answers true, because the page renders the server's decision
		// and no screen may refuse a team for want of seats.
		rec := env.request(http.MethodGet, organizationPath, "", &testuser1)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		organization := &models.Organization{}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), organization))
		assert.Equal(t, 3, organization.TeamsUsed)
		assert.True(t, organization.CanCreateTeam)
	})
}

// TestTeamCreationSucceedsWhenTheSeatCountIsMissing pins the other half of the
// same decision. The old doctrine read an absent count as a refusal because a
// capacity decision was taken against it; there is no capacity decision any
// more, so a projection minted before `seats_purchased` existed no longer
// stops a team. The count is display and billing input now - the commercial
// service computes the rise from its own records either way.
//
// WHAT MAKES IT FAIL: re-introduce any refusal of a nil seat count on the
// creation path.
func TestTeamCreationSucceedsWhenTheSeatCountIsMissing(t *testing.T) {
	env, _ := newOrganizationEnv(t, nil)

	rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
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
//
// It opens and CLOSES its own session rather than using dbSessionForTest,
// whose session stays open until the test ends. Every caller here writes
// afterwards - another project, the removal request - and SQLite deadlocks a
// write against a read transaction that is still open. That is the same hazard
// decideByEdition documents for the request path, and it is why the session is
// scoped to this lookup.
func teamRootProject(t *testing.T, teamID int64) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	root := &models.ProtectedEntity{}
	has, err := s.Where("kind = ? AND team_id = ?", string(models.ProtectedKindTeamRoot), teamID).Get(root)
	require.NoError(t, err)
	require.True(t, has, "creating a team must provision its Team root")
	return root.ProjectID
}

// TestTheOrganizationNameIsNullUntilTheProducerSendsOne pins the read model's
// answer for a projection minted before `state.organization_name` existed:
// null, never an invented name and never the identifier dressed up as one. The
// page renders its own "no name recorded" sentence for null (BRA-1439
// Story 2), so a value fabricated here would ship to a screen.
//
// WHAT MAKES IT FAIL: derive a fallback name in models.OrganizationFor - from
// the organization id, from the administrator's username, from anything.
func TestTheOrganizationNameIsNullUntilTheProducerSendsOne(t *testing.T) {
	env := newManagedEnv(t)
	env.grantSeats(testuser1.ID, true, seats(9))

	rec := env.request(http.MethodGet, organizationPath, "", &testuser1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	organization := &models.Organization{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), organization))
	assert.Nil(t, organization.Name)
}

// TestATeamCreationReportsTheNewTeamCountForTheSeatRise is the fork's half of
// the BRA-1439 Story 9 seam: after a creation commits, the new team count is
// reported to the commercial seat-ensure endpoint with the shared service
// credential, and the body carries exactly the two members the commercial half
// declared on the ticket - the organization id and the count AFTER the
// creation.
//
// WHAT MAKES IT FAIL: delete the seats.EnsureOrganizationSeats call from
// BraznCreateOrganizationTeam - the creation still answers 201 and nothing
// ever reaches the endpoint, which is precisely the "guard whose input has no
// producer" shape this repository keeps finding, caught here from the
// producing side.
func TestATeamCreationReportsTheNewTeamCountForTheSeatRise(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(3))

	type report struct {
		authorization string
		body          string
		// readErr crosses back to the test goroutine and is asserted THERE:
		// testifylint's go-require rule forbids require inside an http handler,
		// because a require failure calls t.FailNow from the wrong goroutine.
		readErr error
	}
	received := make(chan report, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		received <- report{
			authorization: r.Header.Get("Authorization"),
			body:          string(body),
			readErr:       err,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"organization_id":"` + managedTestOrganization + `","outcome":"changed"}`))
	}))
	defer server.Close()

	setConfigForTest(t, config.BraznSeatsEnsureURL, server.URL)
	setConfigForTest(t, config.BraznServiceToken, "test-service-credential")

	rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	select {
	case got := <-received:
		require.NoError(t, got.readErr)
		// The shared service credential, as a bearer - the same one the signup
		// redemption presents, per the seam contract on the ticket.
		assert.Equal(t, "Bearer test-service-credential", got.authorization)
		// The whole body: the fixture held one team, the creation added one, so
		// the count reported is 2. JSONEq so a third member sneaking in fails
		// too - the endpoint accepts nothing else.
		assert.JSONEq(t, `{"organization_id":"`+managedTestOrganization+`","active_teams":2}`, got.body)
	case <-time.After(5 * time.Second):
		t.Fatal("the team was created and nothing reported the new count to the seat-ensure endpoint")
	}
}

// TestATeamCreationSurvivesAnUnreachableSeatEndpoint is the other half of the
// same decision: the report never gates the creation. A team that has
// committed is a fact; a missed report is logged and converged by the next
// one, because the endpoint is a converging ensure.
//
// WHAT MAKES IT FAIL: make BraznCreateOrganizationTeam return an error when
// seats.EnsureOrganizationSeats does - the creation below answers something
// other than 201, which is a seat-shaped refusal wearing a transport failure.
func TestATeamCreationSurvivesAnUnreachableSeatEndpoint(t *testing.T) {
	env, _ := newOrganizationEnv(t, seats(3))

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	endpoint := server.URL
	// Closed BEFORE the creation, so the configured URL points at a port
	// nothing listens on any more - the transport failure, not a slow answer.
	server.Close()

	setConfigForTest(t, config.BraznSeatsEnsureURL, endpoint)
	setConfigForTest(t, config.BraznServiceToken, "test-service-credential")

	rec := env.request(http.MethodPut, organizationTeamsPath, `{"name":"Vertrieb"}`, &testuser1)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
