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
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
)

// BraznFeedbackProjectResponse is the caller's Feedback sub-project
// (BRA-1414), or a defined "not available on this instance" answer when the
// staff account that owns Feedback is not on this instance.
//
// project_id is the id tasks_create / tasks_list must use. Title alone is never
// identity — see models.FeedbackProjectTitle.
type BraznFeedbackProjectResponse struct {
	Available bool   `json:"available"`
	ProjectID int64  `json:"project_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Message   string `json:"message,omitempty"`
}

const braznFeedbackUnavailable = "Feedback is not available on this instance."

// BraznStaffResponse says whether the caller is Brazn staff on this instance:
// a member of the team that reaches every customer's Feedback (BRA-1848).
type BraznStaffResponse struct {
	Staff bool `json:"staff"`
}

// BraznGetStaff answers whether the authenticated user is Brazn staff.
//
// ONE asks this to decide whether to load its admin functions, which read and
// answer every customer's feedback. The answer is the same fact the permission
// checks act on, so it can never offer the functions to somebody who could not
// use them, or withhold them from somebody who could.
//
// @Summary Whether the caller is Brazn staff
// @Description Returns staff=true when the caller is a member of the team that holds the grant on this instance's Feedback project.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Success 200 {object} v1.BraznStaffResponse "Whether the caller is staff."
// @Failure 403 {object} web.HTTPError "The caller is not an authenticated user."
// @Failure 500 {object} web.HTTPError "The answer could not be read."
// @Router /brazn/staff [get]
func BraznGetStaff(c *echo.Context) error {
	a, err := auth.GetAuthFromClaims(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "You are not signed in.")
	}
	u, isUser := a.(*user.User)
	if !isUser {
		// A link share is never staff, and saying so is a plain answer, not a refusal.
		return c.JSON(http.StatusOK, BraznStaffResponse{Staff: false})
	}

	s := db.NewSession()
	defer s.Close()

	staff, err := models.IsFeedbackStaff(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Staff membership could not be read.").Wrap(err)
	}
	return c.JSON(http.StatusOK, BraznStaffResponse{Staff: staff})
}

// BraznGetFeedbackProject resolves the authenticated user's Feedback
// sub-project, ensuring it exists first (idempotent ProvisionFeedbackAccess).
//
// When the staff account that owns Feedback is not on this instance,
// ProvisionFeedbackAccess returns 0 with no error by design. This handler turns
// that into a defined response rather than a bare 0 or a 500.
//
// @Summary Resolve the caller's Feedback project
// @Description Returns the project id ONE / Brazn Tasks clients should file feedback into. Ensures the per-user sub-project exists when this instance has its staff account.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Success 200 {object} v1.BraznFeedbackProjectResponse "Available feedback project, or available=false when the feature is off on this instance."
// @Failure 403 {object} web.HTTPError "The caller is not an authenticated user."
// @Failure 500 {object} web.HTTPError "Provisioning failed."
// @Router /brazn/feedback/project [get]
func BraznGetFeedbackProject(c *echo.Context) error {
	a, err := auth.GetAuthFromClaims(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "You do not have access to Feedback.")
	}
	u, isUser := a.(*user.User)
	if !isUser {
		return echo.NewHTTPError(http.StatusForbidden, "You do not have access to Feedback.")
	}

	s := db.NewSession()
	defer s.Close()

	projectID, err := models.ProvisionFeedbackAccess(s, u)
	if err != nil {
		_ = s.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "Feedback could not be resolved.").Wrap(err)
	}
	if projectID == 0 {
		_ = s.Rollback()
		return c.JSON(http.StatusOK, BraznFeedbackProjectResponse{
			Available: false,
			Message:   braznFeedbackUnavailable,
		})
	}
	project, err := models.GetProjectSimpleByID(s, projectID)
	if err != nil {
		_ = s.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "Feedback could not be loaded.").Wrap(err)
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "Feedback could not be saved.").Wrap(err)
	}

	return c.JSON(http.StatusOK, BraznFeedbackProjectResponse{
		Available: true,
		ProjectID: projectID,
		Title:     project.Title,
	})
}
