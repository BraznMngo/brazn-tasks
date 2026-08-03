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

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/routes/api/shared"
	"code.vikunja.io/api/pkg/user"
	"github.com/labstack/echo/v5"
)

// UserConfirmEmail is the handler to confirm a user email
// @Summary Confirm the email of a new user
// @Description Confirms the email of a newly registered user. A link which was already used confirms successfully a second time, with already_confirmed set.
// @tags user
// @Accept json
// @Produce json
// @Param credentials body user.EmailConfirm true "The token."
// @Success 200 {object} user.EmailConfirmResult
// @Failure 412 {object} web.HTTPError "Bad or expired token provided."
// @Failure 500 {object} models.Message "Internal error"
// @Router /user/confirm [post]
func UserConfirmEmail(c *echo.Context) error {
	// Check for Request Content
	var emailConfirm user.EmailConfirm
	if err := c.Bind(&emailConfirm); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "No token provided.").Wrap(err)
	}

	result, err := shared.ConfirmEmail(&emailConfirm)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, result)
}

// UserResendEmailConfirmation is the handler to send a new confirmation link
// @Summary Send a new email confirmation link
// @Description Sends a new confirmation link to an address that is waiting on one. The response is the same for every address, so it cannot be used to find out whether an account exists.
// @tags user
// @Accept json
// @Produce json
// @Param credentials body user.EmailConfirmResend true "The address to send a new link to."
// @Success 200 {object} models.Message
// @Failure 500 {object} models.Message "Internal error"
// @Router /user/confirm/resend [post]
func UserResendEmailConfirmation(c *echo.Context) error {
	var resend user.EmailConfirmResend
	if err := c.Bind(&resend); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "No email address provided.").Wrap(err)
	}

	// Deliberately not validated. c.Validate would answer 400 for an address
	// that does not parse and 200 for one that does, and a caller who can tell
	// those apart is one step from a caller who can tell accounts apart.
	if err := shared.ResendEmailConfirmation(&resend); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, models.Message{Message: "If that address is waiting to be confirmed, a new link is on its way."})
}
