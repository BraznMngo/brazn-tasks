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

package apiv2

import (
	"context"
	"errors"
	"net/http"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/auth/openid"
	"code.vikunja.io/api/pkg/user"

	"github.com/danielgtaylor/huma/v2"
)

func init() { AddRouteRegistrar(RegisterOpenIDRoutes) }

// RegisterOpenIDRoutes wires the OpenID Connect callback endpoint. It is only
// registered when OpenID is enabled; individual providers are still resolved per
// request, so an unknown provider key 404s even when others are configured.
func RegisterOpenIDRoutes(api huma.API) {
	if !config.AuthOpenIDEnabled.GetBool() {
		return
	}

	Register(api, huma.Operation{
		OperationID:   "auth-openid-callback",
		Summary:       "Authenticate with OpenID Connect",
		Description:   "Exchanges the authorization code returned by an OpenID Connect provider for a Brazn Tasks JWT, creating or updating the matching user. A long-lived refresh token is set as an HttpOnly cookie. When the resolved user has 2FA enabled, the call returns 412 and must be retried with totp_passcode set.",
		Method:        http.MethodPost,
		Path:          "/auth/openid/{provider}/callback",
		DefaultStatus: http.StatusOK,
		Tags:          []string{"auth"},
		Security:      publicSecurity,
	}, authOpenIDCallback)

	// Authenticated — no Security override, so it requires the same JWT every
	// other /user/settings/* route does. This is the missing half of what
	// errManagedUsePassword promises a customer who registered with a
	// password: "you can add Google to your account afterwards." That promise
	// is fulfilled as a ONE-WAY SWITCH, not an addition — see linkIdentity's
	// own comment (pkg/modules/auth/openid/openid.go) for why this schema has
	// no way to hold a password and an external identity on one account at
	// once. Unlike the callback above, this never creates or resolves an
	// account by email — the account is already fixed by the caller's own
	// session, and it refuses outright (409) unless that account is currently
	// local.
	Register(api, huma.Operation{
		OperationID: "user-connect-openid",
		Summary:     "Switch the current user from password to OpenID Connect sign-in",
		Description: "Exchanges the authorization code returned by an OpenID Connect provider and switches the authenticated user's account to sign in with that identity instead of a password. Only a local (password) account may do this. Refuses with 409 if the caller's account is not local, or if the identity is already connected to a different account.",
		Method:      http.MethodPost,
		Path:        "/user/settings/connect/openid/{provider}",
		// Attaches an identity to the caller's own account, it creates nothing —
		// keep 200 over the wrapper's POST->201, same as user-change-password.
		DefaultStatus: http.StatusOK,
		Tags:          []string{"user"},
	}, userConnectOpenID)
}

func userConnectOpenID(ctx context.Context, in *struct {
	Provider string          `path:"provider" doc:"The OpenID Connect provider key as returned by the /info endpoint."`
	Body     openid.Callback `doc:"The provider callback, carrying the authorization code."`
}) (*singleBody[userActionMessageBody], error) {
	a, err := authFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	doer, err := user.GetFromAuth(a)
	if err != nil {
		return nil, translateDomainError(err)
	}

	if err := openid.LinkCallback(ctx, &in.Body, in.Provider, doer); err != nil { //nolint:contextcheck // same as authOpenIDCallback: resolves providers from a cached, context-less map.
		return nil, translateOpenIDError(err)
	}

	return &singleBody[userActionMessageBody]{
		Body: &userActionMessageBody{Message: "Your Google account is now connected."},
	}, nil
}

func authOpenIDCallback(ctx context.Context, in *struct {
	Provider string          `path:"provider" doc:"The OpenID Connect provider key as returned by the /info endpoint."`
	Body     openid.Callback `doc:"The provider callback, carrying the authorization code."`
}) (*authTokenBody, error) {
	u, oidcData, err := openid.AuthenticateCallback(ctx, &in.Body, in.Provider) //nolint:contextcheck // resolves providers from a cached, context-less map and runs OIDC discovery on its own background context, like the v1 callback.
	if err != nil {
		return nil, translateOpenIDError(err)
	}

	deviceInfo, ipAddress := requestClientInfo(ctx)
	// OIDC logins are not "remember me" sessions; v1 always issues a short one.
	token, err := auth.IssueUserToken(ctx, u, deviceInfo, ipAddress, false, oidcData)
	if err != nil {
		return nil, translateDomainError(err)
	}

	if ec := echoContextFromCtx(ctx); ec != nil {
		auth.WriteUserAuthCookies(ec, token)
	}

	out := &authTokenBody{CacheControl: "no-store"}
	out.Body.Token = token.AccessToken
	return out, nil
}

// translateOpenIDError maps OIDC callback errors to RFC 9457 responses.
// ErrOpenIDBadRequestWithDetails carries no HTTP semantics of its own (v1 renders
// it with a bespoke {message, details} body), so v2 maps it to a 400 with the
// provider detail attached as a structured error detail rather than porting the
// bespoke shape. Everything else flows through translateDomainError.
func translateOpenIDError(err error) error {
	var detailedErr *models.ErrOpenIDBadRequestWithDetails
	if errors.As(err, &detailedErr) {
		return huma.Error400BadRequest(detailedErr.Message, &huma.ErrorDetail{
			Message: "The identity provider rejected the request.",
			Value:   detailedErr.Details,
		})
	}
	return translateDomainError(err)
}
