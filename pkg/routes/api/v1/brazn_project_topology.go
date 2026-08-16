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
	"strconv"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"

	"github.com/labstack/echo/v5"
)

// BraznProjectRootResponse is the one fact the link-share toggle needs (BRA-1343):
// whether the project already sits beneath the organization's Public root, the
// one part of the Teams topology decideTeamsLinkShare lets be shared by link
// (managed_rules_teams.go). Nothing else about the project's place in the
// topology is exposed here - a team's own root and its subprojects, and an
// Inbox, all read as `false`, which is the honest answer for "can this be
// shared by link" without naming what they actually are.
type BraznProjectRootResponse struct {
	UnderPublicRoot bool `json:"under_public_root"`
}

// BraznGetProjectRoot answers whether a project sits beneath the Public root,
// so a client can disable link sharing before asking rather than show it and
// let decideTeamsLinkShare refuse the request.
//
// UNLIKE /brazn/organization THIS IS NOT AN ADMINISTRATOR-ONLY SURFACE. Any
// member deciding whether to share a link needs the answer for a project they
// can already open, so the gate here is ordinary project read access - the
// same check ReadOne makes - not organization administration.
//
// It answers `false` for every project outside the managed Teams topology too
// (self-hosted, community, Personal, or a Teams project nobody protected),
// which is the correct default: nothing about link sharing changes for those
// callers, since useManagedCapabilities never asks this question for them.
//
// @Summary Whether a project sits beneath the organization's Public root
// @Description Used to disable link sharing in the UI before the request reaches the server, for a project the managed Teams topology would otherwise refuse to share by link.
// @tags brazn
// @Produce json
// @Security JWTKeyAuth
// @Param project path int true "Project ID"
// @Success 200 {object} v1.BraznProjectRootResponse "Whether the project is under the Public root."
// @Failure 403 {object} web.HTTPError "The caller cannot read this project."
// @Router /brazn/projects/{project}/public-root [get]
func BraznGetProjectRoot(c *echo.Context) error {
	projectID, err := strconv.ParseInt(c.Param("project"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "The project id could not be read.")
	}

	a, err := auth.GetAuthFromClaims(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "You do not have access to this project.")
	}

	s := db.NewSession()
	defer s.Close()

	project := &models.Project{ID: projectID}
	canRead, _, err := project.CanRead(s, a)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "The project could not be resolved.").Wrap(err)
	}
	if !canRead {
		return echo.NewHTTPError(http.StatusForbidden, "You do not have access to this project.")
	}

	root, err := models.ProtectedRootOf(s, projectID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			"The project's place in the topology could not be resolved.").Wrap(err)
	}

	return c.JSON(http.StatusOK, BraznProjectRootResponse{
		UnderPublicRoot: root != nil && root.Kind == models.ProtectedKindPublicRoot,
	})
}
