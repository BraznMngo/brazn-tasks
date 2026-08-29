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
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/brazn/seats"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
)

// The Organization area's API (BRA-917).
//
// WHAT IS DELIBERATELY NOT HERE, because the list is shorter to read than the
// absence is to notice:
//
//   - Inviting and removing members, and transferring administration. A member
//     of an organization IS an entitlement projection, and `organization_admin`
//     and `seat_status` are authoritative from the commercial service - the
//     contract states in as many words that this fork must never grant, infer
//     or repair them locally, because they are the flags that gate every
//     access-expanding operation. A route here that wrote one would be this
//     product deciding who has paid. Membership lifecycle is BRA-786's and the
//     administrator role is BRA-785's; this surface reads the result and links
//     out to perform it.
//   - Buying seats, invoices, price, plan, cadence, renewal, cancellation. AC5,
//     and the reason is the same one: a second copy of an invoice list is how
//     two systems drift apart.
//
// What IS here is what this product is authoritative for: who is in the
// organization according to the projections it holds, how much team capacity
// the purchased seats allow, and the teams themselves - which are its own
// rows, in its own topology.

// braznOrganizationTeamRequest is the whole body of a team creation. A team has
// a name and nothing else at this point; everything about who is in it is
// membership, which is a different route and a different rule.
type braznOrganizationTeamRequest struct {
	Name string `json:"name"`
}

// actingOrganization resolves the caller and the organization they administer,
// or returns the refusal to send.
//
// EVERY handler in this file starts here, and none of them re-derives the
// answer. models.OrganizationFor is the same function the managed rule calls,
// so the check that runs in the middleware and the check that runs in the
// handler cannot come apart - and the GET below needs its own because a read
// route is not classified and therefore never reaches that middleware at all.
func actingOrganization(c *echo.Context) (*user.User, *models.Organization, error) {
	a, err := auth.GetAuthFromClaims(c)
	if err != nil {
		return nil, nil, echo.NewHTTPError(http.StatusForbidden, braznOrganizationRefusal)
	}
	u, isUser := a.(*user.User)
	if !isUser {
		return nil, nil, echo.NewHTTPError(http.StatusForbidden, braznOrganizationRefusal)
	}

	s := db.NewSession()
	defer s.Close()

	organization, err := models.OrganizationFor(s, u.ID)
	if err != nil {
		return nil, nil, echo.NewHTTPError(http.StatusForbidden, braznOrganizationRefusal)
	}
	return u, organization, nil
}

// braznOrganizationRefusal is the one sentence every refusal on this surface
// returns.
//
// It is FLAT ON PURPOSE and it is the same wording for a member, for a personal
// account, for an unentitled one and for an organization whose administrator is
// ambiguous. AC1 sets the bar at discovery: a reply that distinguished "you are
// not the administrator" from "there is no organization here" would answer
// questions about an organization the caller is not in, which is the same
// information the hidden menu entry exists to withhold.
const braznOrganizationRefusal = "This operation is managed by Brazn and is not available for this account."

// BraznGetOrganization returns the organization the caller administers.
//
// @Summary Get the organization you administer
// @Description Returns the organization's members, seat occupancy and team capacity. Only the organization's single administrator may call it.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Success 200 {object} models.Organization "The organization."
// @Failure 403 {object} web.HTTPError "The caller does not administer an organization."
// @Router /brazn/organization [get]
func BraznGetOrganization(c *echo.Context) error {
	_, organization, err := actingOrganization(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, organization)
}

// BraznCreateOrganizationTeam creates one additional team.
//
// THERE IS NO SEAT-CAPACITY REFUSAL ON THIS ROUTE ANY MORE, and its removal is
// the change, not an oversight (BRA-1439 Story 9, decided by Sebastian on
// 2026-08-26): creating a team is never refused for seats. The purchased seat
// count rises instead - after the creation commits, the new team count is
// reported to the commercial service, which raises the count to its own
// formula, opens whatever proration Q7 requires for a paying organization, and
// re-delivers the administrator's projection so the next organization read
// shows the new figure. The report is synchronous so the page's re-read after
// a creation sees the risen count, and IT NEVER FAILS THE CREATION: a team
// that has committed is a fact, and a missed report is converged by the next
// one (the endpoint is a converging ensure).
//
// @Summary Create an additional team
// @Description Creates a team and its protected Team root, and reports the new team count to the commercial service so the purchased seat count can rise.
// @tags brazn
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Success 201 {object} models.Team "The created team."
// @Failure 403 {object} web.HTTPError "The caller does not administer an organization."
// @Router /brazn/organization/teams [put]
func BraznCreateOrganizationTeam(c *echo.Context) error {
	acting, organization, err := actingOrganization(c)
	if err != nil {
		return err
	}

	request := &braznOrganizationTeamRequest{}
	if err := c.Bind(request); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "The team could not be read from the request.")
	}

	s := db.NewSession()
	defer s.Close()

	team, err := models.CreateOrganizationTeam(s, acting, organization, request.Name)
	if err != nil {
		_ = s.Rollback()
		return echo.NewHTTPError(http.StatusBadRequest, "The team could not be created.").Wrap(err)
	}

	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "The team could not be created.").Wrap(err)
	}

	// After the commit, never before: a rolled-back team must not be billed.
	// `TeamsUsed + 1` is exact because this transaction added exactly one.
	// context.WithoutCancel because the creation is already a fact - a report
	// lost to a closed browser tab would leave the seat count lagging until
	// the next creation converges it.
	if err := seats.EnsureOrganizationSeats(
		context.WithoutCancel(c.Request().Context()),
		organization.ID,
		organization.TeamsUsed+1,
	); err != nil {
		log.Errorf("The team was created and the seat rise could not be reported for organization %q: %s",
			organization.ID, err)
	}

	return c.JSON(http.StatusCreated, team)
}

// BraznDeleteOrganizationTeam removes one additional team and the work inside
// it.
//
// @Summary Remove an additional team
// @Description Removes a team, its Team root and everything beneath that root. The primary team cannot be removed.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Param team path int true "Team ID"
// @Success 200 {object} models.Message "The team was removed."
// @Failure 403 {object} web.HTTPError "The caller does not administer an organization."
// @Failure 404 {object} web.HTTPError "This organization has no such team."
// @Failure 409 {object} web.HTTPError "The primary team cannot be removed."
// @Router /brazn/organization/teams/{team} [delete]
func BraznDeleteOrganizationTeam(c *echo.Context) error {
	acting, organization, err := actingOrganization(c)
	if err != nil {
		return err
	}

	teamID, err := strconv.ParseInt(c.Param("team"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "The team id could not be read.")
	}

	s := db.NewSession()
	defer s.Close()

	if err := models.RemoveOrganizationTeam(s, acting, organization, teamID); err != nil {
		_ = s.Rollback()

		if errors.Is(err, models.ErrOrganizationTeamProtected) {
			return echo.NewHTTPError(http.StatusConflict,
				"The primary team cannot be removed. It is a protected root and every member navigates by it.")
		}
		if errors.Is(err, models.ErrOrganizationTeamNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "This organization has no such team.")
		}
		return echo.NewHTTPError(http.StatusBadRequest, "The team could not be removed.").Wrap(err)
	}

	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "The team could not be removed.").Wrap(err)
	}
	return c.JSON(http.StatusOK, models.Message{Message: "The team was removed."})
}

// braznUserLookupResponse is the projection an administrator gets for one
// exact-email hit (BRA-1469). Email is included because the caller already typed
// it; public search deliberately strips addresses.
type braznUserLookupResponse struct {
	User *braznUserLookupBody `json:"user"`
}

type braznUserLookupBody struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

// BraznLookupOrganizationUserByEmail finds a task-server user by full email for
// an organization administrator (BRA-1469).
//
// This is NOT public discoverability. ListUsers / SearchUsers still require
// discoverable_by_email. The gate here is OrganizationFor: only the single
// organization administrator may look, and only by an exact whole address.
// Incomplete addresses (missing "@", empty) return found:null rather than a
// fuzzy match.
//
// @Summary Look up a user by exact email (organization administrator)
// @Description Returns at most one user whose email equals the query. Requires organization administration. Does not enable public email search.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Param email query string true "Full email address"
// @Success 200 {object} braznUserLookupResponse "user is null when nobody matches"
// @Failure 403 {object} web.HTTPError "The caller does not administer an organization."
// @Router /brazn/organization/users/lookup [get]
func BraznLookupOrganizationUserByEmail(c *echo.Context) error {
	_, _, err := actingOrganization(c)
	if err != nil {
		return err
	}

	email := strings.TrimSpace(c.QueryParam("email"))
	s := db.NewSession()
	defer s.Close()

	found, err := user.LookupByExactEmail(s, email)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "The lookup could not be completed.").Wrap(err)
	}
	if found == nil {
		return c.JSON(http.StatusOK, braznUserLookupResponse{User: nil})
	}
	return c.JSON(http.StatusOK, braznUserLookupResponse{User: &braznUserLookupBody{
		ID:       found.ID,
		Username: found.Username,
		Name:     found.Name,
		Email:    found.Email,
	}})
}
