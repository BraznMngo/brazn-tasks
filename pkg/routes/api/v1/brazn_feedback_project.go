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

// BraznFeedbackProjectResponse is what a client needs in order to file into
// or list from the caller's own Percy Feedback sub-project (BRA-1414).
type BraznFeedbackProjectResponse struct {
	// ProjectID is null when Percy Feedback is not provisioned on this
	// instance - brazn.feedbackowner unset, or naming an account that does
	// not exist here. A caller should read that as "the feature does not
	// exist on this instance", not retry it.
	ProjectID *int64 `json:"project_id"`
}

// BraznGetFeedbackProject resolves the caller's own Percy Feedback
// sub-project, provisioning it on first use so an account created before
// brazn.feedbackowner was configured is not left without one.
//
// UNTIL THIS ROUTE, THE ONLY WAY ANYTHING OUTSIDE pkg/models HAS EVER FOUND
// THIS PROJECT IS ITS TITLE (see ensureFeedbackSubProject's own comment) -
// which happens to be unique per reporter today, but is not a contract: a
// customer project sharing that title would be indistinguishable from it by
// name alone. This route is the first real lookup, and callers should move
// off title-matching onto it.
//
// @Summary Resolve the caller's Percy Feedback sub-project
// @Description Returns the id of the project a "file feedback" or "list my feedback" client should read and write, provisioning it first if this account has none yet. Null when Percy Feedback is not configured on this instance.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Success 200 {object} v1.BraznFeedbackProjectResponse "The caller's feedback project, or null if the feature is unavailable here."
// @Router /brazn/feedback/project [get]
func BraznGetFeedbackProject(c *echo.Context) error {
	a, err := auth.GetAuthFromClaims(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "You are not signed in.")
	}
	u, isUser := a.(*user.User)
	if !isUser {
		return echo.NewHTTPError(http.StatusForbidden, "Percy Feedback is available to accounts, not to link shares or API tokens.")
	}

	s := db.NewSession()
	defer s.Close()

	projectID, err := models.ProvisionFeedbackAccess(s, u)
	if err != nil {
		_ = s.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "Your Percy Feedback project could not be resolved.").Wrap(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Your Percy Feedback project could not be resolved.").Wrap(err)
	}

	if projectID == 0 {
		return c.JSON(http.StatusOK, BraznFeedbackProjectResponse{ProjectID: nil})
	}
	return c.JSON(http.StatusOK, BraznFeedbackProjectResponse{ProjectID: &projectID})
}
