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

package openid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/avatar"
	"code.vikunja.io/api/pkg/modules/avatar/upload"
	"code.vikunja.io/api/pkg/modules/brazn/signup"
	"code.vikunja.io/api/pkg/modules/keyvalue"
	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/api/pkg/utils"

	"github.com/coreos/go-oidc/v3/oidc"
	petname "github.com/dustinkirkland/golang-petname"
	"github.com/labstack/echo/v5"
	"golang.org/x/oauth2"
	"xorm.io/xorm"
)

// Callback contains the callback after an auth request was made and redirected
type Callback struct {
	Code        string `query:"code" json:"code"`
	Scope       string `query:"scope" json:"scope"`
	RedirectURL string `json:"redirect_url"`
	// TOTPPasscode is required when the resolved user has TOTP enabled.
	// Clients must restart the OIDC flow and populate this field after
	// receiving a 412 with error code 1017. See GHSA-8jvc-mcx6-r4cg.
	TOTPPasscode string `json:"totp_passcode"`
	// SignupToken carries a the commercial service signup token through the provider
	// round trip, so that registering with Google and registering with a
	// password are gated by the same thing (BRA-1071). It is IGNORED unless
	// this instance is in managed mode, and it is ignored for a sign-in: only
	// the branch that would CREATE a user ever reads it.
	SignupToken string `json:"signup_token"`
}

// Provider is the structure of an OpenID Connect provider
type Provider struct {
	Name                string `json:"name"`
	Key                 string `json:"key"`
	OriginalAuthURL     string `json:"-"`
	AuthURL             string `json:"auth_url"`
	LogoutURL           string `json:"logout_url"`
	ClientID            string `json:"client_id"`
	Scope               string `json:"scope"`
	EmailFallback       bool   `json:"email_fallback"`
	UsernameFallback    bool   `json:"username_fallback"`
	ForceUserInfo       bool   `json:"force_user_info"`
	RequireAvailability bool   `json:"-"`
	ClientSecret        string `json:"-"`
	// RP-Initiated Logout endpoint, cached at init so logout never fetches.
	// Exported so it survives the gob keyvalue round-trip (gob skips unexported
	// fields like openIDProvider); json:"-" keeps it out of /info.
	EndSessionURL  string `json:"-"`
	openIDProvider *oidc.Provider
	Oauth2Config   *oauth2.Config `json:"-"`
}

// boolish decodes a JSON bool or the strings "true"/"false"/"1"/"0" — some
// OIDC providers emit email_verified as a string.
type boolish bool

func (b *boolish) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case bool:
		*b = boolish(v)
	case string:
		*b = boolish(v == "true" || v == "1")
	default:
		*b = false
	}
	return nil
}

type claims struct {
	Email              string                   `json:"email"`
	EmailVerified      boolish                  `json:"email_verified"`
	Name               string                   `json:"name"`
	PreferredUsername  string                   `json:"preferred_username"`
	Nickname           string                   `json:"nickname"`
	VikunjaGroups      []map[string]interface{} `json:"vikunja_groups"`
	Picture            string                   `json:"picture"`
	ExtraSettingsLinks map[string]any           `json:"extra_settings_links"`
}

func init() {
	petname.NonDeterministicMode()
}

func (p *Provider) setOicdProvider() (err error) {
	err = utils.RetryWithBackoff(fmt.Sprintf("OpenID Connect provider '%s'", p.Name), func() error {
		var providerErr error
		p.openIDProvider, providerErr = oidc.NewProvider(context.Background(), p.OriginalAuthURL)
		return providerErr
	})

	if err != nil && p.RequireAvailability {
		log.Fatalf("OpenID Connect provider '%s' is not available and require_availability is enabled: %s", p.Name, err)
	}

	return err
}

func (p *Provider) Issuer() (issuerURL string, err error) {
	type Issuer struct {
		Issuer string `json:"issuer"`
	}

	if p.openIDProvider == nil {
		err = p.setOicdProvider()
		if err != nil {
			return "", err
		}
	}

	iss := &Issuer{}
	err = p.openIDProvider.Claims(iss)
	if err != nil {
		return "", err
	}
	return iss.Issuer, nil
}

// enforceTOTPIfRequired mirrors the TOTP gate from pkg/routes/api/v1/login.go
// for the OIDC flow. Returns nil when the user does not have TOTP enabled.
// See GHSA-8jvc-mcx6-r4cg.
func enforceTOTPIfRequired(s *xorm.Session, u *user.User, totpPasscode string) error {
	totpEnabled, err := user.TOTPEnabledForUser(s, u)
	if err != nil {
		return err
	}
	if !totpEnabled {
		return nil
	}

	if totpPasscode == "" {
		return user.ErrInvalidTOTPPasscode{}
	}

	_, err = user.ValidateTOTPPasscode(s, &user.TOTPPasscode{
		User:     u,
		Passcode: totpPasscode,
	})
	if err != nil {
		return err
	}

	// Reset the counter so old failed attempts don't trip a later lockout.
	if err := keyvalue.Del(u.GetFailedTOTPAttemptsKey()); err != nil {
		return err
	}

	return nil
}

// HandleCallback handles the auth request callback after redirecting from the provider with an auth code
// @Summary Authenticate a user with OpenID Connect
// @Description After a redirect from the OpenID Connect provider to the frontend has been made with the authentication `code`, this endpoint can be used to obtain a jwt token for that user and thus log them in.
// @ID get-token-openid
// @tags auth
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param callback body openid.Callback true "The openid callback"
// @Param provider path int true "The OpenID Connect provider key as returned by the /info endpoint"
// @Success 200 {object} auth.Token
// @Failure 412 {object} models.Message "Invalid totp passcode."
// @Failure 500 {object} models.Message "Internal error"
// @Router /auth/openid/{provider}/callback [post]
func HandleCallback(c *echo.Context) error {
	cb := &Callback{}
	if err := c.Bind(cb); err != nil {
		return &models.ErrOpenIDBadRequest{Message: "Bad data"}
	}

	u, oidcData, err := AuthenticateCallback(c.Request().Context(), cb, c.Param("provider"))
	if err != nil {
		var detailedErr *models.ErrOpenIDBadRequestWithDetails
		if errors.As(err, &detailedErr) {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"message": detailedErr.Message,
				"details": detailedErr.Details,
			})
		}
		return err
	}

	// Create token
	return auth.NewUserAuthTokenResponse(u, c, false, oidcData)
}

// AuthenticateCallback resolves an OpenID Connect callback to an authenticated
// user: it exchanges the auth code, verifies the ID token, creates or updates the
// matching local user, enforces the account-status and TOTP gates, and syncs the
// user's external teams. It is the transport-agnostic core shared by the v1 echo
// handler and the v2 Huma handler; the caller issues the auth token. The
// ErrOpenIDBadRequestWithDetails error keeps its provider detail so v1 can render
// its bespoke body and v2 can map it to RFC 9457.
func AuthenticateCallback(ctx context.Context, cb *Callback, providerKey string) (*user.User, *models.SessionOIDCData, error) {
	// ctx is threaded through only to dispatch the login event; the OIDC token
	// exchange, claim verification and user/avatar sync run on their own
	// background contexts, exactly as the v1 callback always did.
	provider, oauthToken, idToken, rawIDToken, err := exchangeOidcTokens(cb, providerKey) //nolint:contextcheck
	if err != nil {
		return nil, nil, err
	}

	// Stored so logout can replay it as id_token_hint in an RP-Initiated Logout.
	oidcData := &models.SessionOIDCData{
		IDToken:     rawIDToken,
		ProviderKey: providerKey,
	}

	cl, err := getClaims(provider, oauthToken, idToken) //nolint:contextcheck
	if err != nil {
		return nil, nil, err
	}

	s := db.NewSession()
	defer s.Close()
	// Discards events queued during a rolled-back transaction (e.g. user
	// creation); a no-op once DispatchPending has run.
	defer events.CleanupPending(s)

	// Check if we have seen this user before
	u, err := getOrCreateUser(ctx, s, cl, provider, idToken, cb.SignupToken) //nolint:contextcheck
	if err != nil {
		_ = s.Rollback()
		log.Errorf("Error creating new user for provider %s: %v", provider.Name, err)
		return nil, nil, err
	}

	if u.Status == user.StatusDisabled {
		_ = s.Rollback()
		return nil, nil, &user.ErrAccountDisabled{UserID: u.ID}
	}
	if u.Status == user.StatusAccountLocked {
		_ = s.Rollback()
		return nil, nil, &user.ErrAccountLocked{UserID: u.ID}
	}

	// Must run before team sync so a failed 2FA attempt cannot mutate team
	// membership. Commit before HandleFailedTOTPAuth so the getOrCreateUser
	// writes persist and the SQLite write lock is released — its dedicated
	// session needs to acquire its own. See GHSA-fgfv-pv97-6cmj.
	if err := enforceTOTPIfRequired(s, u, cb.TOTPPasscode); err != nil {
		if commitErr := s.Commit(); commitErr != nil {
			log.Errorf("Error committing session after failed OIDC TOTP attempt for user %d: %v", u.ID, commitErr)
		} else {
			// The user creation above was committed, so its events are real.
			events.DispatchPending(ctx, s)
		}
		if user.IsErrInvalidTOTPPasscode(err) {
			user.HandleFailedTOTPAuth(u)
		}
		return nil, nil, err
	}

	teamData := getTeamDataFromToken(cl.VikunjaGroups, provider)

	err = models.SyncExternalTeamsForUser(s, u, teamData, idToken.Issuer, provider.Name)
	if err != nil {
		return nil, nil, err
	}

	err = s.Commit()
	if err != nil {
		_ = s.Rollback()
		log.Errorf("Error creating new team for provider %s: %v", provider.Name, err)
		return nil, nil, err
	}

	events.DispatchPending(ctx, s)

	return u, oidcData, nil
}

// LinkCallback verifies a Google OIDC callback and SWITCHES the CURRENT,
// already-authenticated user from local (password) sign-in to this Google
// identity.
//
// It used to be described here as the missing half of a promise
// errManagedUsePassword made — "you can add Google to your account
// afterwards". BRA-1475 replaced that sentence and dropped the promise, so
// nothing refuses a Google sign-in and then tells the person this exists. The
// capability is unchanged and still reached from the settings page; only the
// refusal stopped advertising it.
//
// "ADD" IS NOT WHAT THIS DOES, AND THAT IS DELIBERATE, NOT A SHORTCUT. This
// schema has one Issuer/Subject slot per account, and IsLocalUser() —
// CheckUserCredentials's own gate on password login — reads that same slot.
// There is no second, non-primary identity a user can hold alongside their
// password here; building one would need a real schema change (a separate
// linked-identity table, plus teaching getOrCreateUser's resolver to read
// it), which is real, larger work than this call makes. So this is a one-way
// migration off passwords, not a second sign-in method layered on top of
// one, and every caller of it — the settings-page copy, the confirmation
// step, the success message — must say so, not "connect" or "add" as if
// both kept working.
//
// linkIdentity refuses outright unless currentUser.IsLocalUser() for exactly
// this reason: an account already on Google switching to a DIFFERENT Google
// identity has no password left to fall back to if that turns out to be a
// mistake, and this ticket exists to give customers a way OFF password
// lock-in, not a way to self-lock-out of the identity they already use.
//
// Unlike AuthenticateCallback, this never looks up or creates an account:
// currentUser is already known from the caller's own session, and this only
// ever writes Issuer/Subject (and avatar sync) onto that one row. It
// deliberately does NOT touch Email or Name from the provider's claims — an
// unverified or mismatched claim has no business overwriting a field on an
// account it did not create.
func LinkCallback(ctx context.Context, cb *Callback, providerKey string, currentUser *user.User) error {
	provider, oauthToken, idToken, _, err := exchangeOidcTokens(cb, providerKey) //nolint:contextcheck
	if err != nil {
		return err
	}

	cl, err := getClaims(provider, oauthToken, idToken) //nolint:contextcheck
	if err != nil {
		return err
	}

	s := db.NewSession()
	defer s.Close()
	defer events.CleanupPending(s)

	// currentUser comes from the caller's JWT via user.GetFromAuth, which —
	// user.GetUserFromClaims — only ever populates ID, Username and IsAdmin
	// from the token. Every other field, Issuer included, is left at its Go
	// zero value (""). IsLocalUser() compares Issuer to "local", so trusting
	// that object here would refuse every account, local or not, before ever
	// reaching the real check: "" never equals "local". Re-fetch by ID for the
	// one field this function actually depends on.
	fullUser, err := user.GetUserByID(s, currentUser.ID)
	if err != nil {
		return err
	}

	if _, err := linkIdentity(s, cl, idToken, fullUser); err != nil { //nolint:contextcheck // avatar sync inside linkIdentity runs on its own background context, same as AuthenticateCallback's.
		return err
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return err
	}

	events.DispatchPending(ctx, s)

	return nil
}

// linkIdentity is LinkCallback's pure logic, split out the same way
// AuthenticateCallback separates its own transport (token exchange, session
// lifecycle) from getOrCreateUser's account logic — this half takes already-
// resolved claims and an ID token, so it is unit-testable without a real or
// mocked OIDC round trip.
func linkIdentity(s *xorm.Session, cl *claims, idToken *oidc.IDToken, currentUser *user.User) (*user.User, error) {
	// Only a local (password) account may make this one-way switch. An
	// account already on Google doing this again would be trading its one
	// working identity for a different one with no password left to fall
	// back to — see LinkCallback's own comment for why this is a switch and
	// not an addition in the first place.
	if !currentUser.IsLocalUser() {
		return nil, errAlreadyUsesProvider()
	}

	// Same principle fallbackSearchUsers applies when matching an existing
	// account (GHSA-xv7q-fvmc-jx96): an unverified claim proves nothing, so it
	// is never trusted to attach a new identity either.
	if !bool(cl.EmailVerified) {
		return nil, errManagedUnverifiedAddress()
	}

	// Refuse rather than reassign: this Google identity may already be
	// somebody else's account. getOrCreateUser's own issuer+subject lookup
	// only ever REUSES whatever it finds there — correct for a sign-in, wrong
	// here, where a match against a DIFFERENT account is exactly the takeover
	// this function exists to refuse, not something to silently paper over.
	existing, err := user.GetUserWithEmail(s, &user.User{Issuer: idToken.Issuer, Subject: idToken.Subject})
	if err != nil && !user.IsErrUserDoesNotExist(err) && !user.IsErrUserStatusError(err) {
		return nil, err
	}
	found := err == nil || user.IsErrUserStatusError(err)
	if found && existing.ID != currentUser.ID {
		return nil, errIdentityAlreadyLinked()
	}

	// A narrow, targeted update — not user.UpdateUser, whose column allowlist
	// (baseUserUpdateColumns) never includes issuer/subject at all: that
	// function exists for a signed-in user's own profile/settings edits,
	// where those two columns are never meant to move. Mirrors SetUserStatus
	// immediately below in pkg/user/user.go, the same narrow-Cols() shape for
	// the same reason: touch exactly the columns this action means to change.
	// THIS IS THE SWITCH: once this commits, IsLocalUser() is false and
	// CheckUserCredentials refuses this account's password — deliberately,
	// per this function's own doc comment, not a defect to guard against.
	if _, err := s.ID(currentUser.ID).Cols("issuer", "subject").Update(&user.User{
		Issuer:  idToken.Issuer,
		Subject: idToken.Subject,
	}); err != nil {
		return nil, err
	}
	// No re-fetch: only Issuer/Subject moved, and both are already known —
	// mutate the in-memory copy the same way getOrCreateUser's own avatar-sync
	// call and SetUserStatus's callers do, rather than spend a SELECT to learn
	// values this function just wrote itself.
	currentUser.Issuer = idToken.Issuer
	currentUser.Subject = idToken.Subject

	// nolint:contextcheck — same as AuthenticateCallback's own avatar sync:
	// deliberately on its own background context, unchanged by this ticket.
	if err := syncUserAvatarFromOpenID(s, currentUser, cl.Picture); err != nil { //nolint:contextcheck
		log.Errorf("Error syncing avatar for user %s: %v", currentUser.Username, err)
	}

	return currentUser, nil
}

func getTeamDataFromToken(groups []map[string]interface{}, provider *Provider) (teamData []*models.Team) {
	teamData = []*models.Team{}
	for _, t := range groups {
		var name string
		var description string
		var oidcID string
		var isPublic bool

		// Read name
		_, exists := t["name"]
		if exists {
			name = t["name"].(string)
		}

		// Read description
		_, exists = t["description"]
		if exists {
			description = t["description"].(string)
		}

		// Read isPublic flag
		_, exists = t["isPublic"]
		if exists {
			isPublic = t["isPublic"].(bool)
		}

		// Read oidcID
		_, exists = t["oidcID"]
		if exists {
			switch id := t["oidcID"].(type) {
			case string:
				oidcID = id
			case int64:
				oidcID = strconv.FormatInt(id, 10)
			case float64:
				oidcID = strconv.FormatFloat(id, 'f', -1, 64)
			default:
				log.Errorf("No oidcID assigned for %v or type %v not supported", t, t)
			}
		}
		if name == "" || oidcID == "" {
			log.Errorf("Claim of your custom scope does not hold name or oidcID for automatic group assignment through oidc provider. Please check %s", provider.Name)
			continue
		}
		teamData = append(teamData, &models.Team{
			Name:        name,
			ExternalID:  oidcID,
			Description: description,
			IsPublic:    isPublic,
		})
	}

	return teamData
}

// Download and store a user's avatar from an OpenID provider
func syncUserAvatarFromOpenID(s *xorm.Session, u *user.User, pictureURL string) (err error) {
	// If no picture URL is provided, reset the avatar provider if it was set to openid
	if pictureURL == "" {
		if u.AvatarProvider == "openid" {
			u.AvatarProvider = "default"
			_, err = s.Where("id = ?", u.ID).Cols("avatar_provider").Update(&user.User{AvatarProvider: "default"})
			if err != nil {
				return fmt.Errorf("error resetting avatar provider: %w", err)
			}
			avatar.FlushAllCaches(u)
		}
		return nil
	}

	log.Debugf("Found avatar URL for user %s: %s", u.Username, pictureURL)

	// Download avatar
	avatarData, err := utils.DownloadImage(pictureURL)
	if err != nil {
		return fmt.Errorf("error downloading avatar: %w", err)
	}

	// Process avatar, ensure 1:1 ratio
	processedAvatar, err := utils.CropAvatarTo1x1(avatarData)
	if err != nil {
		return fmt.Errorf("error processing avatar: %w", err)
	}

	// Set avatar provider to openid
	u.AvatarProvider = "openid"

	// Store avatar and update user
	err = upload.StoreAvatarFile(s, u, bytes.NewReader(processedAvatar))
	if err != nil {
		return fmt.Errorf("error storing avatar: %w", err)
	}

	avatar.FlushAllCaches(u)

	return nil
}

// fallbackSearchUsers builds the ordered list of local-user lookups used to link an OIDC
// login to an existing account when the provider has email and/or username fallback enabled.
// GetUserWithEmail ANDs all non-zero fields, so the email (when set) is combined with each
// username candidate.
func fallbackSearchUsers(cl *claims, provider *Provider, idToken *oidc.IDToken) []*user.User {
	// Only a verified email may link to an existing account — an unverified one lets an
	// attacker asserting a victim's email take over their local account (GHSA-xv7q-fvmc-jx96).
	emailFallbackAllowed := provider.EmailFallback && bool(cl.EmailVerified)

	fallbackEmail := ""
	if emailFallbackAllowed {
		// Used alone, allow for someone to connect from various provider to the same account.
		// Note: mapping on email prevents auto-updating the user email.
		fallbackEmail = cl.Email
	}

	// Try the subject first (keeps working for IdPs where sub == username), then the
	// preferred_username. The latter lets providers with an opaque sub (e.g. a random
	// UUID, like PocketID) still link to an existing local account.
	var searches []*user.User
	if provider.UsernameFallback {
		// Skip empty username candidates: GetUserWithEmail ANDs only non-zero fields, so a
		// {Issuer, Username:"", Email:""} would degenerate to an issuer-only lookup and link
		// an arbitrary local user. idToken.Subject is non-empty per OIDC, but guard anyway.
		if idToken.Subject != "" {
			searches = append(searches, &user.User{Issuer: user.IssuerLocal, Username: idToken.Subject, Email: fallbackEmail})
		}
		preferred := strings.ReplaceAll(cl.PreferredUsername, " ", "-")
		if preferred != "" && preferred != idToken.Subject {
			searches = append(searches, &user.User{Issuer: user.IssuerLocal, Username: preferred, Email: fallbackEmail})
		}
	}
	// Email-only lookup when no username candidates were added. Only with a real,
	// verified email — an empty email would degenerate to an issuer-only lookup and
	// link an arbitrary local user.
	if len(searches) == 0 && emailFallbackAllowed && cl.Email != "" {
		searches = append(searches, &user.User{Issuer: user.IssuerLocal, Email: cl.Email})
	}

	return searches
}

// errManagedNoSignUp is what an unmatched subject gets on a managed instance.
//
// It is an *echo.HTTPError so both APIs answer the same way: v1 lets echo's
// handler render it, and v2 maps it in translateDomainError - without which it
// would surface as a 500 there. 403 rather than 401, because the credentials
// were fine and the answer would not change if they were presented again.
//
// The wording says what the person can do about it and names no rule: whether
// this instance is in managed mode is not something an unauthenticated caller
// needs to be told.
//
// The sentence is BRA-1475's, quoted in the ticket and shipped as written. It
// says "email address" where the previous one said "sign-in", which is what the
// person is actually looking at: they pressed a Google button with one mailbox
// selected, and the address is the thing they can change.
func errManagedNoSignUp() error {
	return echo.NewHTTPError(http.StatusForbidden,
		"There is no ONE account for this email address. Please subscribe to ONE first.")
}

// errManagedUsePassword is what somebody gets when they sign in with Google at
// an address that already has an account here.
//
// GOOGLE AND A PASSWORD ON ONE ADDRESS DO NOT JOIN AUTOMATICALLY (BRA-1071).
// Adopting the existing account would mean whoever controls the mailbox
// controls the account, which is exactly the property a password exists to
// deny. There is no disclosure in saying so: the person has just proved to
// Google that they hold this mailbox, so they are being told something about
// their own address.
//
// The sentence is BRA-1475's, quoted in the ticket and shipped as written. IT
// DROPS A PROMISE THE PREVIOUS ONE MADE, knowingly and on the ticket's own
// record: the old wording ended "you can add Google to your account
// afterwards", and adding Google afterwards is a thing LinkCallback really
// does. The replacement says only what to do now. If that promise is wanted
// back it belongs in the sign-in page's own copy, where it can be read in the
// person's language, rather than in an English-only literal here.
func errManagedUsePassword() error {
	return echo.NewHTTPError(http.StatusForbidden,
		"This account is not using Google to sign in. Please log in with username and password.")
}

// errManagedUnverifiedAddress refuses a sign-up whose provider will not say the
// address is verified.
//
// A Google registration that returns a VERIFIED address needs no confirmation
// mail, because the provider's claim is what proves the mailbox. That is the
// whole of the ruling, and it is conditional on the claim actually being there:
// an unverified claim proves nothing, and accepting it would let a provider
// account assert somebody else's address and reach an active account without
// ever holding the mailbox. This path sends no confirmation mail of its own, so
// there is no weaker outcome available to fall back to - the answer is no.
func errManagedUnverifiedAddress() error {
	return echo.NewHTTPError(http.StatusForbidden,
		"This provider did not confirm your email address, so an account cannot be created from it. Register with an email address and a password instead.")
}

// errIdentityAlreadyLinked is what LinkCallback answers when the Google
// identity being attached already belongs to a different account.
//
// A 409, not the 403 errManagedUsePassword uses: the credentials this caller
// presented (their own session) are fine, and presenting them again would not
// change the answer — the conflict is with a different account's existing
// state, not with this caller's authorization.
func errIdentityAlreadyLinked() error {
	return echo.NewHTTPError(http.StatusConflict,
		"This Google account is already connected to a different ONE account.")
}

// errAlreadyUsesProvider is what linkIdentity answers when the caller's own
// account is not a local (password) account — see linkIdentity's own comment
// for why switching an already-external account to a different provider
// identity is refused rather than performed.
func errAlreadyUsesProvider() error {
	return echo.NewHTTPError(http.StatusConflict,
		"This account already signs in with a connected provider and has no password to fall back to.")
}

// existingUserForAddress reports the account already holding an address, or nil.
//
// The lookup is by address ALONE - no issuer - because that is the question
// being asked: does anybody already have this mailbox. A user.ErrUserDoesNotExist
// is the answer "no" and not a failure; a status error means a row was found,
// which is still "yes".
func existingUserForAddress(s *xorm.Session, email string) (*user.User, error) {
	if email == "" {
		return nil, nil
	}

	existing, err := user.GetUserWithEmail(s, &user.User{Email: email})
	if err != nil && !user.IsErrUserDoesNotExist(err) && !user.IsErrUserStatusError(err) {
		return nil, err
	}
	// getUser answers "nobody" with a zero-valued user rather than a nil one, so
	// the id is what says whether a row was found. A disabled or locked account
	// still counts: it is somebody's, and adopting it would be the same join.
	if existing == nil || existing.ID == 0 {
		return nil, nil
	}
	return existing, nil
}

// decideManagedFallbackMatch refuses to adopt an account that was matched by
// address or username rather than by this provider's subject.
//
// That adoption is the silent join BRA-1071 rules out on a managed instance. It
// stays exactly as it was everywhere else, which is stock behaviour.
//
// It is not the same guard as decideManagedSignUp and neither covers the other:
// the deployed configuration carries emailfallback, so this is the one that
// fires for an address that already has an account, and that one is what closes
// the same hole on a deployment with no fallback configured at all.
func decideManagedFallbackMatch(matched bool) error {
	if matched && config.BraznManagedMode.GetBool() {
		return errManagedUsePassword()
	}
	return nil
}

// decideManagedSignUp answers whether this callback may create a user at all.
// It returns nil on a self-hosted instance, where none of this applies.
//
// THE ORDER OF THE THREE CHECKS IS DELIBERATE. A provider that will not vouch
// for the address is refused first, because everything after it reasons about
// an address; then an address that already has an account here, so a valid
// token can never be spent joining one; and only then the token itself.
//
// The token check is its SHAPE only. Whether it is real is the commercial service's
// answer, and asking costs a user id that does not exist yet - so the refusal
// for a token that is merely absent is given here, and the one that decides is
// redeemManagedSignUp.
func decideManagedSignUp(s *xorm.Session, cl *claims, signupToken string) error {
	if !config.BraznManagedMode.GetBool() {
		return nil
	}

	if !bool(cl.EmailVerified) {
		return errManagedUnverifiedAddress()
	}

	existing, err := existingUserForAddress(s, cl.Email)
	if err != nil {
		return err
	}
	if existing != nil {
		return errManagedUsePassword()
	}

	if !signup.CanBeRedeemed(signupToken) {
		return errManagedNoSignUp()
	}
	return nil
}

// redeemManagedSignUp consumes the token and reports the users.id just created,
// before the caller's session is committed - exactly as the password path does
// it. A refusal returns an error, AuthenticateCallback rolls the session back,
// and no user exists, which is what makes "no token, no user" hold by either
// door rather than only the one somebody remembered.
//
// email is the claim rather than the stored column: user.CreateUser returns the
// row read back by GetUserByID, and that blanks the email on every read.
func redeemManagedSignUp(ctx context.Context, signupToken string, userID int64, email string) error {
	if !config.BraznManagedMode.GetBool() {
		return nil
	}

	if err := signup.Redeem(ctx, signupToken, userID, email); err != nil {
		return signup.HTTPRefusal(err)
	}
	return nil
}

func getOrCreateUser(ctx context.Context, s *xorm.Session, cl *claims, provider *Provider, idToken *oidc.IDToken, signupToken string) (u *user.User, err error) {

	// set defaults
	fallbackMatchFound := false
	alreadyCreatedFromIssuer := false

	// first check if the user already signed up using the provider

	u, err = user.GetUserWithEmail(s, &user.User{
		Issuer:  idToken.Issuer,
		Subject: idToken.Subject,
	})
	if err != nil && !user.IsErrUserDoesNotExist(err) && !user.IsErrUserStatusError(err) {
		return nil, err
	}
	alreadyCreatedFromIssuer = err == nil || user.IsErrUserStatusError(err)

	// If the user exists but is disabled/locked, return early — don't update their profile or sync avatar.
	// HandleCallback will reject the auth attempt.
	if alreadyCreatedFromIssuer && user.IsErrUserStatusError(err) {
		return u, nil
	}

	if !alreadyCreatedFromIssuer && (provider.EmailFallback || provider.UsernameFallback) {

		// try finding the user on fallback mapping properties
		for _, searchUser := range fallbackSearchUsers(cl, provider, idToken) {
			u, err = user.GetUserWithEmail(s, searchUser)
			if err != nil && !user.IsErrUserDoesNotExist(err) && !user.IsErrUserStatusError(err) {
				return nil, err
			}
			fallbackMatchFound = err == nil || user.IsErrUserStatusError(err)

			// Same as above: disabled/locked user found via fallback — return early.
			if fallbackMatchFound && user.IsErrUserStatusError(err) {
				return u, nil
			}
			if fallbackMatchFound {
				break
			}
		}

		if err := decideManagedFallbackMatch(fallbackMatchFound); err != nil {
			return nil, err
		}
	}

	if !alreadyCreatedFromIssuer && !fallbackMatchFound {

		// SIGNING IN AND SIGNING UP PART COMPANY HERE. Everything above this
		// line resolved an existing account and is untouched; everything below
		// it CREATES one, and on a managed instance that is the half the token
		// gates. BRA-1018 refused this branch outright; BRA-1071 changes its
		// answer rather than its shape, because Google is one option to
		// register beside a password and not a second class of account.
		//
		// It cannot be done by the managed gate, and that is not an oversight
		// in the gate. POST /api/v1/auth/openid/:provider/callback is
		// classified "authentication", and that rule returns nil
		// unconditionally (managed_rules_core.go) - correctly, because a
		// login's subject is unknown until the credentials have been checked
		// and a projection that could not be read would otherwise lock every
		// user out of the instance. The distinction between the two things
		// this route does only exists at this line.
		//
		// What managed mode decides here, and in what order, is
		// decideManagedSignUp. It lives outside this function because the three
		// checks it holds put getOrCreateUser over the cyclomatic limit, and
		// because the ordering between them is a property worth stating
		// somewhere it can be read on its own.
		//
		// AuthenticateCallback logs every refusal here through its existing
		// "Error creating new user" line, which is where an operator will read
		// it.
		if err := decideManagedSignUp(s, cl, signupToken); err != nil {
			return nil, err
		}

		// If no user exists, create one with the preferred username if it is not already taken
		uu := &user.User{
			Username:           strings.ReplaceAll(cl.PreferredUsername, " ", "-"),
			Email:              cl.Email,
			Name:               cl.Name,
			Status:             user.StatusActive,
			Issuer:             idToken.Issuer,
			Subject:            idToken.Subject,
			ExtraSettingsLinks: cl.ExtraSettingsLinks,
		}

		u, err = auth.CreateUserWithRandomUsername(s, uu)
		if err != nil {
			return nil, err
		}

		if err := redeemManagedSignUp(ctx, signupToken, u.ID, cl.Email); err != nil {
			return nil, err
		}
	} else if alreadyCreatedFromIssuer {

		// try updating user.Name and/or user.Email if necessary
		if cl.Email != u.Email {
			u.Email = cl.Email
		}
		if cl.Name != u.Name {
			u.Name = cl.Name
		}

		u.ExtraSettingsLinks = cl.ExtraSettingsLinks

		u, err = user.UpdateUser(s, u, false)
		if err != nil {
			return nil, err
		}
	}

	// Try sync avatar if available.
	//
	// nolint:contextcheck — this function took no context before BRA-1071 gave
	// it one for the redemption, so contextcheck now notices that the avatar
	// download runs on its own background context. That is deliberate and
	// unchanged: AuthenticateCallback's own comment says the token exchange,
	// claim verification and avatar sync have always done so. Threading ctx
	// through DownloadImage is a change to shared upstream code with nothing to
	// do with this ticket.
	err = syncUserAvatarFromOpenID(s, u, cl.Picture) //nolint:contextcheck
	if err != nil {
		log.Errorf("Error syncing avatar for user %s: %v", u.Username, err)
	}

	return u, nil
}

// mergeClaims combines claims from token and userinfo based on the ForceUserInfo setting
// cl represents the claims from the token, cl2 represents the claims from userinfo
func mergeClaims(cl *claims, cl2 *claims, forceUserInfo bool) error {
	if (forceUserInfo && cl2.Email != "") || cl.Email == "" {
		cl.Email = cl2.Email
		cl.EmailVerified = cl2.EmailVerified
	}

	if (forceUserInfo && cl2.Name != "") || cl.Name == "" {
		cl.Name = cl2.Name
	}

	if (forceUserInfo && cl2.PreferredUsername != "") || cl.PreferredUsername == "" {
		cl.PreferredUsername = cl2.PreferredUsername
	}

	if cl.PreferredUsername == "" && cl2.Nickname != "" {
		cl.PreferredUsername = cl2.Nickname
	}

	if (forceUserInfo && cl2.Picture != "") || cl.Picture == "" {
		cl.Picture = cl2.Picture
	}

	if (forceUserInfo && len(cl2.VikunjaGroups) > 0) || len(cl.VikunjaGroups) == 0 {
		cl.VikunjaGroups = cl2.VikunjaGroups
	}

	if (forceUserInfo && len(cl2.ExtraSettingsLinks) > 0) || len(cl.ExtraSettingsLinks) == 0 {
		cl.ExtraSettingsLinks = cl2.ExtraSettingsLinks
	}

	if cl.Email == "" {
		return &user.ErrNoOpenIDEmailProvided{}
	}

	return nil
}

func getClaims(provider *Provider, oauth2Token *oauth2.Token, idToken *oidc.IDToken) (*claims, error) {

	cl := &claims{}
	err := idToken.Claims(cl)
	if err != nil {
		log.Errorf("Error getting token claims for provider %s: %v", provider.Name, err)
		return nil, err
	}

	if provider.ForceUserInfo || cl.Email == "" || cl.Name == "" || cl.PreferredUsername == "" || cl.Picture == "" {
		info, err := provider.openIDProvider.UserInfo(context.Background(), provider.Oauth2Config.TokenSource(context.Background(), oauth2Token))
		if err != nil {
			log.Errorf("Error getting userinfo for provider %s: %v", provider.Name, err)
			return nil, err
		}

		cl2 := &claims{}
		err = info.Claims(cl2)
		if err != nil {
			log.Errorf("Error parsing userinfo claims for provider %s: %v", provider.Name, err)
			return nil, err
		}

		err = mergeClaims(cl, cl2, provider.ForceUserInfo)
		if err != nil {
			if user.IsErrNoEmailProvided(err) {
				log.Errorf("Claim does not contain an email address for provider %s", provider.Name)
			}

			return nil, err
		}
	}
	return cl, nil
}

// exchangeOidcTokens resolves the provider, exchanges the callback's auth code,
// and verifies the returned ID token. It takes an already-bound Callback so it
// can be shared by the v1 echo handler (which binds from the request) and the v2
// Huma handler (which binds via its typed body).
func exchangeOidcTokens(cb *Callback, providerKey string) (*Provider, *oauth2.Token, *oidc.IDToken, string, error) {
	provider, err := GetProvider(providerKey)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if provider == nil {
		return nil, nil, nil, "", &models.ErrOpenIDBadRequest{Message: "Provider does not exist"}
	}

	log.Debugf("Trying to authenticate user using provider: %s", provider.Key)

	provider.Oauth2Config.RedirectURL = cb.RedirectURL
	// Parse the access & ID token
	oauth2Token, err := provider.Oauth2Config.Exchange(context.Background(), cb.Code)
	if err != nil {
		var rerr *oauth2.RetrieveError
		if errors.As(err, &rerr) {

			details := make(map[string]interface{})
			if err := json.Unmarshal(rerr.Body, &details); err != nil {
				log.Errorf("Error unmarshalling token for provider %s: %v", provider.Name, err)
				log.Debugf("Raw token value is %s", rerr.Body)
				return nil, nil, nil, "", err
			}

			log.Errorf("Error retrieving token: %s", err)
			log.Debugf("Raw token value is %s", rerr.Body)
			return nil, nil, nil, "", &models.ErrOpenIDBadRequestWithDetails{
				Message: "Could not authenticate against third party.",
				Details: details,
			}
		}

		return nil, nil, nil, "", err
	}

	// Extract the ID Token from OAuth2 token.
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		log.Debugf("Could not get id_token, raw token is %v", oauth2Token)
		return nil, nil, nil, "", &models.ErrOpenIDBadRequest{Message: "Missing token"}
	}

	verifier := provider.openIDProvider.Verifier(&oidc.Config{ClientID: provider.ClientID})

	// Parse and verify ID Token payload.
	idToken, err := verifier.Verify(context.Background(), rawIDToken)
	if err != nil {
		log.Errorf("Error verifying token for provider %s: %v", provider.Name, err)
		return nil, nil, nil, "", err
	}

	return provider, oauth2Token, idToken, rawIDToken, nil
}
