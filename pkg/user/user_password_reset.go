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

package user

import (
	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/notifications"
	"xorm.io/xorm"
)

// PasswordReset holds the data to reset a password
type PasswordReset struct {
	// The previously issued reset token.
	Token string `json:"token"`
	// The new password for this user.
	NewPassword string `json:"new_password" valid:"bcrypt_password" minLength:"8" maxLength:"72"`
}

// ResetPassword resets a users password. It returns the ID of the user whose
// password was reset so callers can perform additional cleanup (e.g. session
// invalidation).
func ResetPassword(s *xorm.Session, reset *PasswordReset) (userID int64, err error) {

	// Check if the password is not empty
	if reset.NewPassword == "" {
		return 0, ErrNoUsernamePassword{}
	}

	if reset.Token == "" {
		return 0, ErrNoPasswordResetToken{}
	}

	// Check if we have a token
	token, err := getToken(s, reset.Token, TokenPasswordReset)
	if err != nil {
		return 0, err
	}
	if token == nil {
		return 0, ErrInvalidPasswordResetToken{Token: reset.Token}
	}

	user, err := GetUserByID(s, token.UserID)
	if err != nil && !IsErrAccountLocked(err) {
		return 0, err
	}
	userID = user.ID

	// Hash the password
	user.Password, err = HashPassword(reset.NewPassword)
	if err != nil {
		return
	}

	err = removeTokens(s, user, TokenPasswordReset)
	if err != nil {
		return
	}

	if user.Status == StatusAccountLocked || user.Status == StatusEmailConfirmationRequired {
		user.Status = StatusActive
	}
	_, err = s.
		Cols("password", "status").
		Where("id = ?", user.ID).
		Update(user)
	if err != nil {
		return
	}

	// Dont send a mail if no mailer is configured
	if !config.MailerEnabled.GetBool() {
		return
	}

	// Send a mail to the user to notify it his password was changed.
	n := &PasswordChangedNotification{
		User: user,
	}

	err = notifications.Notify(user, n, s)
	return
}

// PasswordTokenRequest defines the request format for password reset resqest
type PasswordTokenRequest struct {
	Email string `json:"email" valid:"email,length(0|250)" maxLength:"250"`
}

// RequestUserPasswordResetTokenByEmail inserts a random token to reset a users password into the databsse
func RequestUserPasswordResetTokenByEmail(s *xorm.Session, tr *PasswordTokenRequest) (err error) {
	if tr.Email == "" {
		return ErrNoUsernamePassword{}
	}

	// Check if the user exists
	user, err := GetUserWithEmail(s, &User{Email: tr.Email})
	if err != nil {
		// BRA-1101: this operation publishes "the response is the same whether
		// or not an account exists" and then answered 1005 for an address with
		// no account and 1020 for a disabled one, which is the same oracle
		// /login has just been stopped from being: anyone with a list of
		// addresses could sort it into customers and non-customers, on an
		// endpoint that needs no credentials at all. Neither gets a token, and
		// neither is told so — both leave here the way success leaves here.
		if IsErrUserDoesNotExist(err) || IsErrAccountDisabled(err) {
			return nil //nolint:nilerr // saying nothing is the published contract
		}
		// A locked account is the one refusal that still proceeds: a lockout
		// comes from failed sign-ins, and a reset is how its owner gets out of
		// one.
		if !IsErrAccountLocked(err) {
			return err
		}
	}

	// BRA-1475: an account that signs in with a provider gets no reset mail,
	// and the person asking is not told that is why. RequestUserPasswordResetToken
	// refuses it, and the refusal is swallowed here for the same reason the two
	// above are: this endpoint needs no credentials, and any answer that differs
	// from the ordinary one sorts a list of addresses into accounts and
	// non-accounts. Nothing is sent, and the caller leaves the way success
	// leaves.
	err = RequestUserPasswordResetToken(s, user)
	if IsErrAccountIsNotLocal(err) {
		return nil //nolint:nilerr // saying nothing is the published contract
	}

	return err
}

// RequestUserPasswordResetToken sends a user a password reset email.
func RequestUserPasswordResetToken(s *xorm.Session, user *User) (err error) {
	// BRA-1475: AN ACCOUNT THAT SIGNS IN WITH A PROVIDER MUST NOT BE SENT A
	// RESET LINK, because the link leads nowhere and leaves the person worse
	// off than before they asked. Nothing checked this. ResetPassword writes a
	// password hash onto whichever account the token names and, for an account
	// awaiting confirmation, marks it active in the same write
	// (user_password_reset.go:73-79). None of that gives the account a password
	// to sign in with: CheckUserCredentials refuses a non-local account outright
	// (user.go:410-412), so the person sets a password, is told it worked, and
	// is refused at the sign-in page with a message about a different subject.
	//
	// Here rather than at the four call sites, because this is the function
	// that mints the token and sends the mail, so a door added later is covered
	// without anybody remembering. RequestPasswordResetAsAdmin already refuses
	// the same case ahead of this (pkg/models/admin_user_actions.go:144-146),
	// so nothing changes for an administrator; the CLI and the invalid-TOTP
	// lockout mail now get the same refusal instead of sending a dead link.
	//
	// The self-service door swallows this error rather than reporting it — see
	// RequestUserPasswordResetTokenByEmail, where saying "that account signs in
	// with Google" to an unauthenticated stranger is the oracle BRA-1101 closed.
	if !user.IsLocalUser() {
		return &ErrAccountIsNotLocal{UserID: user.ID}
	}

	token, err := generateToken(s, user, TokenPasswordReset)
	if err != nil {
		return
	}

	// Dont send a mail if no mailer is configured
	if !config.MailerEnabled.GetBool() {
		return
	}

	n := &ResetPasswordNotification{
		User:  user,
		Token: token,
	}

	err = notifications.Notify(user, n, s)
	return
}
