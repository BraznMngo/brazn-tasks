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

package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/modules/humabridge"
	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/api/pkg/web"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"xorm.io/xorm"
)

// These are all valid auth types
const (
	AuthTypeUnknown int = iota
	AuthTypeUser
	AuthTypeLinkShare
)

// Token represents an authentication token
type Token struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"`
}

const RefreshTokenCookieName = "vikunja_refresh_token" //nolint:gosec // not a credential

// getRefreshTokenCookiePath returns the cookie path for the refresh token,
// derived from service.publicurl.
func getRefreshTokenCookiePath() string {
	refreshURL := "/api/v1/user/token/refresh"

	publicURL := config.ServicePublicURL.GetString()
	u, err := url.Parse(publicURL)
	if err != nil {
		return refreshURL
	}

	// Extract the path component and append the refresh endpoint
	basePath := strings.TrimRight(u.Path, "/")
	return basePath + refreshURL
}

// SetRefreshTokenCookie sets an HttpOnly cookie containing the refresh token.
// The cookie is path-scoped to the refresh endpoint so the browser only sends
// it on refresh requests. HttpOnly prevents JavaScript access (XSS protection).
func SetRefreshTokenCookie(c *echo.Context, token string, maxAge int) {
	secure := strings.HasPrefix(config.ServicePublicURL.GetString(), "https")
	// SameSite=None allows cross-origin sending (needed for the Electron
	// desktop app where the page is on localhost but the API is remote),
	// however browsers require Secure=true for SameSite=None cookies.
	// When running over plain HTTP (e.g. local dev or E2E tests), fall
	// back to Lax so the cookie is still accepted by the browser.
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	c.SetCookie(&http.Cookie{ //nolint:gosec // G124: Secure/SameSite are intentionally conditional on the https scheme (see above); HttpOnly is always set.
		Name:     RefreshTokenCookieName,
		Value:    token,
		Path:     getRefreshTokenCookiePath(),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
}

// ClearRefreshTokenCookie removes the refresh token cookie.
func ClearRefreshTokenCookie(c *echo.Context) {
	SetRefreshTokenCookie(c, "", -1)
}

// IssuedUserToken bundles a freshly minted access token with the matching
// refresh token and the cookie max-age both v1 and v2 use to set the
// HttpOnly refresh cookie.
type IssuedUserToken struct {
	AccessToken  string
	RefreshToken string
	CookieMaxAge int
}

// IssueUserToken creates a session for the user and mints a JWT access token plus
// a refresh token for it. It is the transport-agnostic core both v1 (which writes
// the echo response) and v2 (Huma) call; callers set the refresh cookie and the
// Cache-Control header themselves via WriteUserAuthCookies. Pass oidc for
// OpenID Connect logins to store the logout data; nil otherwise.
func IssueUserToken(ctx context.Context, u *user.User, deviceInfo, ipAddress string, long bool, oidc *models.SessionOIDCData) (*IssuedUserToken, error) {
	s := db.NewSession()
	defer s.Close()

	session, err := models.CreateSession(s, u.ID, deviceInfo, ipAddress, long, oidc)
	if err != nil {
		_ = s.Rollback()
		return nil, err
	}

	t, err := NewEntitledUserJWTAuthtoken(s, u, session.ID)
	if err != nil {
		_ = s.Rollback()
		return nil, err
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return nil, err
	}

	if err := events.DispatchWithContext(ctx, &user.LoginSucceededEvent{User: u}); err != nil {
		log.Errorf("Could not dispatch login succeeded event: %s", err)
	}

	provisionFeedbackOnSignIn(ctx, u)

	cookieMaxAge := int(config.ServiceJWTTTL.GetInt64())
	if long {
		cookieMaxAge = int(config.ServiceJWTTTLLong.GetInt64())
	}

	return &IssuedUserToken{
		AccessToken:  t,
		RefreshToken: session.RefreshToken,
		CookieMaxAge: cookieMaxAge,
	}, nil
}

// provisionFeedbackOnSignIn gives the person who has just signed in their own
// Feedback project, if this instance has an owner configured and they do not
// have one yet (BRA-1414).
//
// WITHOUT IT, "A CUSTOMER WHO SIGNED UP BEFORE THIS WORK ALSO HAS ONE" HAS NO
// MECHANISM, AND EVERY CUSTOMER WE HAVE SIGNED UP BEFORE THIS WORK. The other
// two paths that provision only ever run for an account that does not exist
// yet: CreateNewProjectForUser at registration, and the commercial
// create_personal_inbox operation, which the commercial service sends once when
// it creates the account. The lookup route added by this same ticket can
// provision an existing account, but nothing calls it - wiring the desktop
// client to call it is BRA-1415 and has not been started - so on its own it
// leaves the outcome waiting on a client that does not exist.
//
// IssueUserToken IS THE ONE PLACE EVERY INTERACTIVE SIGN-IN PASSES THROUGH, and
// that is why the call sits here rather than in each handler. Its three callers
// are the v1 response helper (NewUserAuthTokenResponse), the v2 password login
// and the v2 OpenID callback, which between them are every way a person signs
// in. Refreshing a session does NOT come through here - RefreshSession mints its
// own token - so this runs once per sign-in rather than once per token.
//
// IT IS AFTER THE COMMIT AND IT CANNOT FAIL THE SIGN-IN. The session and the
// token are already durable by this line, and a customer must be able to get
// into the product whether or not a feedback project can be made for them:
// ProvisionFeedbackAccess's own comment gives skipping as the safe direction to
// be wrong in, because no project means no feedback channel where a refusal
// here would mean no login. A failure is logged and nothing else.
//
// It is cheap on the ordinary path: ensureFeedbackSubProject looks the reporter
// up before it creates anything, so an account that already has one costs a
// single indexed read per sign-in. An instance with no owner configured costs a
// string comparison, because ProvisionFeedbackAccessRetrying answers on the
// config before it opens a session.
func provisionFeedbackOnSignIn(ctx context.Context, u *user.User) {
	if !config.BraznManagedMode.GetBool() {
		return
	}
	if _, err := models.ProvisionFeedbackAccessRetrying(ctx, u); err != nil {
		log.Errorf("could not provision the Feedback project for user %d on sign-in: %s", u.ID, err)
	}
}

// WriteUserAuthCookies sets the HttpOnly refresh-token cookie and the
// Cache-Control: no-store header on a response. The cookie is path-scoped to the
// refresh endpoint, so the browser only sends it there; JavaScript never sees the
// refresh token, which protects it from XSS. Shared by the v1 echo handlers and
// the v2 Huma handlers (which reach the echo context via the humabridge
// EchoContextKey stash on their request context).
func WriteUserAuthCookies(c *echo.Context, token *IssuedUserToken) {
	SetRefreshTokenCookie(c, token.RefreshToken, token.CookieMaxAge)
	c.Response().Header().Set("Cache-Control", "no-store")
}

// NewUserAuthTokenResponse creates a new user auth token response from a user object.
// Pass oidc for OpenID Connect logins to store the logout data; nil otherwise.
func NewUserAuthTokenResponse(u *user.User, c *echo.Context, long bool, oidc *models.SessionOIDCData) error {
	token, err := IssueUserToken(c.Request().Context(), u, c.Request().UserAgent(), c.RealIP(), long, oidc)
	if err != nil {
		return err
	}

	WriteUserAuthCookies(c, token)
	return c.JSON(http.StatusOK, Token{Token: token.AccessToken})
}

// BraznEditionClaim carries the holder's entitlement edition in the session
// token. Its ABSENCE is meaningful, and it is the refusing case: a token with
// no such claim entitles its holder to nothing beyond ordinary task work,
// whatever the database happens to say. That is what lets a guarded request
// decide without a query - see routes.RequireManagedPolicy.
const BraznEditionClaim = "brazn_edition"

// BraznWriteRestrictedClaim carries the entitlement's `write_access` answer, as
// the one bit the enforcement point needs: true when this holder's writes are
// cut back to the payment method, their own credentials, export and deletion.
//
// ABSENCE IS THE PERMITTING CASE HERE, which is the opposite of the claim above
// and is deliberate. A token minted by a build that predates the member, or for
// a subject whose projection carries no `write_access`, must not be blocked -
// absence means `full` in the contract, and reading it any other way would
// write-block every existing session the moment this build shipped.
//
// It is stamped only when true, so no token that would have been issued before
// this existed changes shape.
const BraznWriteRestrictedClaim = "brazn_write_restricted"

// NewUserJWTAuthtoken generates and signs a new short-lived jwt token for a user.
// The token includes the session UUID as the `sid` claim. This is a global
// function to be able to call it from web tests.
//
// It carries NO entitlement, so in managed mode a guarded route refuses its
// holder. Any caller holding a database session wants
// NewEntitledUserJWTAuthtoken instead; this remains for the callers that have
// none, which are the web tests.
func NewUserJWTAuthtoken(u *user.User, sessionID string) (token string, err error) {
	return newUserJWTAuthtoken(u, sessionID, nil)
}

// NewEntitledUserJWTAuthtoken mints a session token that carries what its
// holder is entitled to and expires no later than that entitlement does.
//
// THIS IS ENFORCEMENT'S EXPENSIVE HALF, and it happens once per token. The
// entitlement is read here, the token is capped here, and afterwards the
// token's own expiry does the work a revocation channel, a refresh protocol or
// a periodic re-check would otherwise have been built to do:
//
//   - Revocation does not exist. Ending a subscription sets the date; this
//     token runs out; the next call here gets nothing back. Bounded by one
//     token lifetime, which is the bound a revocation list would buy.
//   - Refresh does not exist. A renewed token comes back through here and
//     re-runs the same rule, which is not a mechanism - it is issuing a token.
//
// The `expires_in` the login and OAuth responses report is deliberately left as
// the uncapped lifetime. It is advisory, it is only ever an OVERSTATEMENT, and
// the overstatement is harmless: a client that trusts it sees one 401, refreshes
// on it, and gets back a token with no entitlement and a full lifetime - which
// is the correct outcome and the one it would have reached anyway. Threading a
// real expiry through IssuedUserToken, RefreshResult and TokenResponse to
// improve a hint that only misleads inside one ten-minute window per
// subscription is not worth the surface.
func NewEntitledUserJWTAuthtoken(s *xorm.Session, u *user.User, sessionID string) (string, error) {
	return newUserJWTAuthtoken(u, sessionID, models.EntitlementForToken(s, u.ID, time.Now()))
}

func newUserJWTAuthtoken(u *user.User, sessionID string, entitled *entitlement.TokenEntitlement) (token string, err error) {
	t := jwt.New(jwt.SigningMethodHS256)

	var ttl = time.Duration(config.ServiceJWTTTLShort.GetInt64())
	var expires = time.Now().Add(time.Second * ttl)

	claims := t.Claims.(jwt.MapClaims)
	claims["type"] = AuthTypeUser
	claims["id"] = u.ID
	claims["username"] = u.Username
	claims["is_admin"] = u.IsAdmin
	claims["sid"] = sessionID
	claims["jti"] = uuid.New().String()
	if entitled != nil {
		claims[BraznEditionClaim] = entitled.Edition
		// THE CAP, and it only ever shortens. An entitlement running past the
		// normal lifetime changes nothing: a token outliving its own TTL would
		// be a security regression bought for nobody. A zero EndsAt is an
		// entitlement with no recorded end and shortens nothing either.
		//
		// An end already in the past cannot reach here, because ForToken
		// refuses those rather than returning one - so this can never mint a
		// token that expired before it was issued and take a customer's own
		// tasks away along with their subscription.
		if !entitled.EndsAt.IsZero() && entitled.EndsAt.Before(expires) {
			expires = entitled.EndsAt
		}
		// Stamped only when it restricts. A false claim and no claim mean the
		// same thing to every reader, and writing the false one would change the
		// shape of every token this instance has ever issued for no gain.
		if entitled.WriteRestricted {
			claims[BraznWriteRestrictedClaim] = true
		}
	}
	claims["exp"] = expires.Unix()

	return t.SignedString([]byte(config.ServiceSecret.GetString()))
}

// EditionFromToken returns the entitlement edition the request's session token
// carries, and whether it carries one at all.
//
// False is the refusing answer, and it covers every way of not having one: no
// user JWT in the context (an API token or a link share - neither is issued
// through a login, so neither can have been capped), no claim inside it, or an
// empty one. Managed mode already refuses to provision API tokens, CalDAV
// tokens and bots, since their lifecycle belongs to the commercial service, so
// no managed account can hold a credential that reaches here without a claim.
func EditionFromToken(c *echo.Context) (string, bool) {
	jwtinf, isJWT := c.Get("user").(*jwt.Token)
	if !isJWT {
		return "", false
	}
	claims, isClaims := jwtinf.Claims.(jwt.MapClaims)
	if !isClaims {
		return "", false
	}
	edition, isString := claims[BraznEditionClaim].(string)
	if !isString || edition == "" {
		return "", false
	}
	return edition, true
}

// WriteRestrictedFromToken reports whether the request's session token says its
// holder's writes are cut back to settings.
//
// FALSE IS THE PERMITTING ANSWER and it covers every way of not carrying the
// claim, including not being a user JWT at all. That is what makes this safe to
// consult on every mutating request rather than only guarded ones: an API token
// or a link share is not a subject this restriction was minted for, and
// refusing them here would take ordinary reads and shares away from accounts
// that are not restricted at all.
//
// A LINK SHARE IS THEREFORE NOT COVERED BY THIS, and that is a real boundary
// rather than an oversight. A share issued before the restriction began keeps
// whatever write rights it was given, because the token carries no subject
// whose entitlement could be read - resolving share to project to owner to
// organization is the per-request database read the gate was deliberately built
// to stop doing. Creating a NEW share is refused, so the surface cannot grow.
//
// A non-boolean claim restricts, on the same fail-closed reasoning as
// Signed.WriteRestricted: this build not understanding what a token says is not
// a reason to give it more than it asked for.
func WriteRestrictedFromToken(c *echo.Context) bool {
	jwtinf, isJWT := c.Get("user").(*jwt.Token)
	if !isJWT {
		return false
	}
	claims, isClaims := jwtinf.Claims.(jwt.MapClaims)
	if !isClaims {
		return false
	}
	raw, present := claims[BraznWriteRestrictedClaim]
	if !present {
		return false
	}
	restricted, isBool := raw.(bool)
	return !isBool || restricted
}

// NewLinkShareJWTAuthtoken creates a new jwt token from a link share
func NewLinkShareJWTAuthtoken(share *models.LinkSharing) (token string, err error) {
	t := jwt.New(jwt.SigningMethodHS256)

	var ttl = time.Duration(config.ServiceJWTTTL.GetInt64())
	var exp = time.Now().Add(time.Second * ttl).Unix()

	// Set claims
	claims := t.Claims.(jwt.MapClaims)
	claims["type"] = AuthTypeLinkShare
	claims["id"] = share.ID
	claims["hash"] = share.Hash
	claims["project_id"] = share.ProjectID
	claims["permission"] = share.Permission
	claims["sharedByID"] = share.SharedByID
	claims["exp"] = exp

	// Generate encoded token and send it as response.
	return t.SignedString([]byte(config.ServiceSecret.GetString()))
}

// GetAuthFromClaims returns a web.Auth object from jwt claims
func GetAuthFromClaims(c *echo.Context) (a web.Auth, err error) {
	// check if we have a token in context and use it if that's the case
	if c.Get("api_token") != nil {
		apiToken := c.Get("api_token").(*models.APIToken)
		s := db.NewSession()
		defer s.Close()
		u, err := user.GetUserByID(s, apiToken.OwnerID)
		if err != nil {
			return nil, err
		}
		return u, nil
	}

	jwtinf, is := c.Get("user").(*jwt.Token)
	if !is {
		return nil, fmt.Errorf("user in context is not jwt token")
	}
	claims := jwtinf.Claims.(jwt.MapClaims)
	typFloat, is := claims["type"].(float64)
	if !is {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "Invalid JWT token.")
	}
	typ := int(typFloat)
	if typ == AuthTypeLinkShare && config.ServiceEnableLinkSharing.GetBool() {
		s := db.NewSession()
		defer s.Close()
		return models.GetLinkShareFromClaims(s, claims)
	}
	if typ == AuthTypeUser {
		return user.GetUserFromClaims(claims)
	}
	return nil, echo.NewHTTPError(http.StatusBadRequest, "Invalid JWT token.")
}

// ValidateAPITokenString looks up an API token by its raw string, checks expiry,
// and returns the token and its owner. This is the shared validation logic used
// by both the HTTP middleware and WebSocket auth.
func ValidateAPITokenString(tokenString string) (*models.APIToken, *user.User, error) {
	s := db.NewSession()
	defer s.Close()

	token, err := models.GetTokenFromTokenString(s, tokenString)
	if err != nil {
		return nil, nil, err
	}

	if time.Now().After(token.ExpiresAt) {
		return nil, nil, fmt.Errorf("API token %d expired on %s", token.ID, token.ExpiresAt.String())
	}

	u, err := user.GetUserByID(s, token.OwnerID)
	if err != nil {
		if user.IsErrUserStatusError(err) {
			return nil, nil, fmt.Errorf("API token %d owner account is disabled or locked", token.ID)
		}
		return nil, nil, err
	}

	return token, u, nil
}

// GetUserIDFromToken parses a raw JWT token string and returns the user ID.
// Only regular user tokens are accepted (not link shares).
// Returns 0 and an error if the token is invalid.
func GetUserIDFromToken(tokenString string) (int64, error) {
	token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (any, error) {
		return []byte(config.ServiceSecret.GetString()), nil
	})
	if err != nil {
		return 0, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return 0, jwt.ErrTokenInvalidClaims
	}

	typ, ok := claims["type"].(float64)
	if !ok || int(typ) != AuthTypeUser {
		return 0, jwt.ErrTokenInvalidClaims
	}

	userIDFloat, ok := claims["id"].(float64)
	if !ok {
		return 0, jwt.ErrTokenInvalidClaims
	}

	return int64(userIDFloat), nil
}

func CreateUserWithRandomUsername(s *xorm.Session, uu *user.User) (u *user.User, err error) {
	// Check if we actually have a preferred username and generate a random one right away if we don't
	for {
		if uu.Username == "" {
			uu.Username = petname.Generate(3, "-")
		}

		u, err = user.CreateUser(s, uu)
		if err == nil {
			break
		}

		if !user.IsErrUsernameExists(err) {
			return nil, err
		}

		// If their preferred username is already taken, generate a new one
		uu.Username = petname.Generate(3, "-")
	}

	// And create their project
	err = models.CreateNewProjectForUser(s, u)
	return
}

// RefreshResult holds the result of a successful session refresh.
type RefreshResult struct {
	AccessToken     string
	NewRefreshToken string
	ExpiresIn       int64
	IsLongSession   bool
	SessionID       string
}

// RefreshSession looks up a session by its raw refresh token, validates it,
// rotates the refresh token, fetches the user, and generates a new JWT.
// It handles its own DB session (open/commit/rollback).
//
// On user status errors (disabled/locked), the session is deleted before
// returning the error so the caller can handle cleanup (e.g. clearing cookies).
func RefreshSession(rawRefreshToken string) (*RefreshResult, error) {
	s := db.NewSession()
	defer s.Close()

	session, err := models.GetSessionByRefreshToken(s, rawRefreshToken)
	if err != nil {
		_ = s.Rollback()
		if models.IsErrSessionNotFound(err) {
			return nil, echo.NewHTTPError(http.StatusUnauthorized, "Invalid or expired refresh token.")
		}
		return nil, err
	}

	maxAge := time.Duration(config.ServiceJWTTTL.GetInt64()) * time.Second
	if session.IsLongSession {
		maxAge = time.Duration(config.ServiceJWTTTLLong.GetInt64()) * time.Second
	}
	if time.Since(session.LastActive) > maxAge {
		if _, err := s.Where("id = ?", session.ID).Delete(&models.Session{}); err != nil {
			_ = s.Rollback()
			return nil, err
		}
		if err := s.Commit(); err != nil {
			return nil, err
		}
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "Session expired.")
	}

	if err := models.UpdateSessionLastActive(s, session.ID); err != nil {
		_ = s.Rollback()
		return nil, err
	}

	newRawToken, err := models.RotateRefreshToken(s, session)
	if err != nil {
		_ = s.Rollback()
		if models.IsErrSessionNotFound(err) {
			return nil, echo.NewHTTPError(http.StatusUnauthorized, "Refresh token already used.")
		}
		return nil, err
	}

	u, err := user.GetUserByID(s, session.UserID)
	if err != nil {
		if user.IsErrUserStatusError(err) {
			if _, delErr := s.Where("id = ?", session.ID).Delete(&models.Session{}); delErr != nil {
				_ = s.Rollback()
				return nil, delErr
			}
			if commitErr := s.Commit(); commitErr != nil {
				return nil, commitErr
			}
			return nil, err
		}
		_ = s.Rollback()
		return nil, err
	}

	// A renewed token re-runs the capping rule against the entitlement as it
	// stands NOW, which is the entirety of "refresh". An entitlement that ended
	// since the last one was issued returns nothing here, so the session
	// continues for ordinary work and every guarded route starts refusing.
	accessToken, err := NewEntitledUserJWTAuthtoken(s, u, session.ID)
	if err != nil {
		_ = s.Rollback()
		return nil, err
	}

	if err := s.Commit(); err != nil {
		return nil, err
	}

	return &RefreshResult{
		AccessToken:     accessToken,
		NewRefreshToken: newRawToken,
		ExpiresIn:       config.ServiceJWTTTLShort.GetInt64(),
		IsLongSession:   session.IsLongSession,
		SessionID:       session.ID,
	}, nil
}

// SessionIDFromContext reads the session id (the `sid` claim) off the user JWT
// in the echo context. It returns "" when there is no user JWT or no sid claim
// (API tokens and link shares carry no session), which callers treat as a no-op.
func SessionIDFromContext(c *echo.Context) string {
	raw := c.Get("user")
	if raw == nil {
		return ""
	}
	jwtinf, ok := raw.(*jwt.Token)
	if !ok {
		return ""
	}
	claims, ok := jwtinf.Claims.(jwt.MapClaims)
	if !ok {
		return ""
	}
	sid, _ := claims["sid"].(string)
	return sid
}

// GetAuthFromContext retrieves the authenticated web.Auth from a plain
// context.Context, bridging Huma handlers to Vikunja's echo JWT flow. The
// humabridge group middleware stashes the *echo.Context under EchoContextKey
// first.
func GetAuthFromContext(ctx context.Context) (web.Auth, error) {
	ec, ok := ctx.Value(humabridge.EchoContextKey).(*echo.Context)
	if !ok {
		return nil, fmt.Errorf("no echo.Context on request context; are you calling GetAuthFromContext from a Huma handler mounted via humabridge?")
	}
	return GetAuthFromClaims(ec)
}
