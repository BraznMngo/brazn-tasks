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
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/notifications"
	"xorm.io/xorm"
)

// EmailConfirmMaxAge is how long a confirmation link stays usable.
//
// Twenty-four hours [Recorded, Sebastian 2026-08-02; Percy-Account-Path.md §6,
// decision 5b]. The screen states the number as fact, so it is declared once
// here rather than separately in the copy and in the sweep - the two cannot
// then drift apart.
const EmailConfirmMaxAge = 24 * time.Hour

// EmailConfirm holds the token to confirm a mail address
type EmailConfirm struct {
	// The email confirm token sent via email.
	Token string `json:"token"`
}

// EmailConfirmResult reports which of the two successful outcomes happened.
//
// Both are successes. A second click on a link that already worked is not a
// failure, and presenting it as one makes people think they broke something
// (Percy-Account-Path.md §3) - so it is reported here rather than as an error,
// and the screen renders it green.
type EmailConfirmResult struct {
	// AlreadyConfirmed is true when this link had already been used. The
	// address is confirmed either way.
	AlreadyConfirmed bool `json:"already_confirmed" doc:"True when this link had already been used. The address is confirmed either way."`
}

// ConfirmEmail handles the confirmation of an email address
func ConfirmEmail(s *xorm.Session, c *EmailConfirm) (result *EmailConfirmResult, err error) {

	// Check if we have an email confirm token
	if c.Token == "" {
		return nil, ErrInvalidEmailConfirmToken{}
	}

	token, err := getToken(s, c.Token, TokenEmailConfirm)
	if err != nil {
		return nil, err
	}

	if token == nil {
		// Not a live link. It may still be one of ours that was already spent,
		// which is a different sentence to show the person holding it.
		spent, serr := getToken(s, c.Token, TokenEmailConfirmed)
		if serr != nil {
			return nil, serr
		}
		if spent == nil {
			return nil, ErrInvalidEmailConfirmToken{Token: c.Token}
		}
		if time.Since(spent.Created) > EmailConfirmMaxAge {
			return nil, ErrExpiredEmailConfirmToken{}
		}
		return &EmailConfirmResult{AlreadyConfirmed: true}, nil
	}

	// Checked here and not only in the cleanup cron: that runs hourly, so a
	// link swept on the hour would otherwise keep working for up to an hour
	// past the lifetime the screen promised.
	if time.Since(token.Created) > EmailConfirmMaxAge {
		return nil, ErrExpiredEmailConfirmToken{}
	}

	user, err := GetUserByID(s, token.UserID)
	if err != nil && !IsErrAccountLocked(err) {
		return nil, err
	}

	// Every other link outstanding for this user stops working, as it did
	// before. This one survives with a kind nothing accepts, so a second click
	// can be recognised rather than guessed at.
	if err := removeTokensExcept(s, user, TokenEmailConfirm, token.ID); err != nil {
		return nil, err
	}
	if err := changeTokenKind(s, token.ID, TokenEmailConfirmed); err != nil {
		return nil, err
	}

	user.Status = StatusActive
	_, err = s.
		Where("id = ?", user.ID).
		Cols("status").
		Update(user)
	if err != nil {
		return nil, err
	}

	return &EmailConfirmResult{}, nil
}

// EmailConfirmResend is the request for a fresh confirmation link.
type EmailConfirmResend struct {
	// The address to send a new confirmation link to.
	Email string `json:"email" valid:"email,length(0|250)" maxLength:"250"`
}

// ResendEmailConfirmation issues a new confirmation link for an address that is
// waiting on one.
//
// IT TELLS THE CALLER NOTHING ABOUT ANY ACCOUNT, and that is the whole design
// of it. The answer is the same whether the address has an account waiting to
// be confirmed, has one that was confirmed long ago, or has no account at all.
// Anyone can reach this endpoint, so an answer that varied by address would
// turn it into an account-existence oracle (Percy-Account-Path.md §3;
// BRA-1072 AC7). The only person who learns anything is the one who can read
// the mailbox.
//
// It returns nil in every one of those cases. A string that is not an address
// at all is refused by the handlers before it reaches here, which discloses
// nothing: the caller could work that out without asking us.
func ResendEmailConfirmation(s *xorm.Session, r *EmailConfirmResend) (err error) {
	if r.Email == "" {
		return nil
	}

	u, err := GetUserWithEmail(s, &User{Email: r.Email})
	if err != nil {
		// "No such address" and "that account is disabled" are answers, and
		// this endpoint does not give answers - they become the same silence
		// as an address that is simply already confirmed. A locked account is
		// still an account whose address may be waiting on confirmation, so it
		// falls through. Anything else is a real fault and is reported: a
		// failure the operator can see is not an oracle, because it does not
		// vary with the address.
		if IsErrUserDoesNotExist(err) || IsErrAccountDisabled(err) {
			return nil
		}
		if !IsErrAccountLocked(err) {
			return err
		}
	}

	if u == nil || u.Status != StatusEmailConfirmationRequired {
		return nil
	}

	// The earlier links stop working the moment a new one is issued. Otherwise
	// a person who pressed "send it again" three times holds three live links
	// and has no way of telling which of them is the one that counts.
	if err := removeTokens(s, u, TokenEmailConfirm); err != nil {
		return err
	}

	token, err := generateToken(s, u, TokenEmailConfirm)
	if err != nil {
		return err
	}

	if !config.MailerEnabled.GetBool() {
		return nil
	}

	n := &EmailConfirmNotification{
		User:         u,
		IsNew:        false,
		ConfirmToken: token.ClearTextToken,
	}

	return notifications.Notify(u, n, s)
}
