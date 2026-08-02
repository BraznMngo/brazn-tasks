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

package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/user"

	petname "github.com/dustinkirkland/golang-petname"
	"xorm.io/xorm"
)

// ErrMailboxProvisioningLost means another caller held the mailbox and then
// gave it up, so this call neither created a user nor found one to return. It
// is a transient state - the winner's transaction rolled back - and the only
// correct response is to try again.
var ErrMailboxProvisioningLost = errors.New("the mailbox was claimed by another provisioning call that did not finish")

// provisionedMailboxConstraint is the fragment every supported database puts in
// its unique-violation message for brazn_provisioned_users.email: MySQL and
// PostgreSQL name the index UQE_brazn_provisioned_users_email, SQLite names the
// column brazn_provisioned_users.email, and the table name is inside both.
const provisionedMailboxConstraint = "brazn_provisioned_users"

// ProvisionedUser binds one mailbox to the Brazn Tasks user Percy Cloud
// provisioned for it.
//
// IT EXISTS BECAUSE users.email IS NOT UNIQUE - it is `varchar(250) null` with
// no index, and one cannot be added, because every bot user carries the empty
// string. The identity contract (cloud/service/src/identity.ts) describes this
// operation as "an insert against the mailbox's unique constraint, reading the
// row back on conflict", and this table is that constraint. Without it there is
// no atomic step available at all: a lookup followed by an insert is
// check-then-act, and the loser of that race mints a second Brazn Tasks user
// for one mailbox - which is the one failure the whole identity model is built
// to make impossible, because a user id is a primary key on the commercial side
// with no update path.
//
// It is the AUTHORITATIVE mailbox-to-user map, not a cache of users.email, and
// the difference shows the moment somebody changes their address here: Percy
// Cloud keeps asking about the mailbox it sold to, and must keep getting the
// same id. Resolving through users.email would answer "no user" and create a
// second one.
type ProvisionedUser struct {
	ID int64 `xorm:"bigint autoincr not null unique pk" json:"id"`
	// Email is the mailbox, stored exactly as the commercial service sent it.
	// See provisioning.CreateUser on why it is never normalised here.
	Email string `xorm:"varchar(250) not null unique" json:"-"`
	// UserID is deliberately NOT unique. A row is inserted with 0 to take the
	// mailbox and updated to the real id in the same transaction, so two
	// concurrent calls for two DIFFERENT mailboxes both hold a 0 for a moment;
	// a unique index here would make them collide with each other.
	UserID  int64     `xorm:"bigint not null default 0" json:"-"`
	Created time.Time `xorm:"created not null" json:"-"`
}

// TableName is the table name for provisioned mailboxes
func (ProvisionedUser) TableName() string {
	return "brazn_provisioned_users"
}

// CreateOrResolveUserForMailbox creates the Brazn Tasks user for one mailbox or
// returns the one that already exists, and reports which of the two happened.
// It owns its own transactions, because the conflict path has to read what
// another transaction committed.
//
// THE ORDER IS THE POINT. The mailbox is claimed by an INSERT before anything
// else happens, so the unique index decides who provisions and who resolves -
// there is no read to lose a race with. Every resolve therefore travels the
// conflict path, which is what makes that path something the tests exercise on
// every ordinary call rather than only under a race nobody can reproduce.
//
// The claim is also why the expensive half is never wasted: a caller that lost
// has done no user creation, sent no confirmation mail and written nothing it
// has to undo.
func CreateOrResolveUserForMailbox(ctx context.Context, email string) (*user.User, bool, error) {
	s := db.NewSession()
	defer s.Close()
	// Discards events queued during a rolled-back transaction (the user
	// creation below); a no-op once DispatchPending has run.
	defer events.CleanupPending(s)

	claim := &ProvisionedUser{Email: email}
	if _, err := s.Insert(claim); err != nil {
		_ = s.Rollback()
		if !db.IsUniqueConstraintError(err, provisionedMailboxConstraint) {
			return nil, false, err
		}
		// Somebody else holds this mailbox. On MySQL and PostgreSQL the insert
		// above waited for them, so by the time it failed their row is
		// committed and readable. The read needs a session of its own: this
		// one has been rolled back, and on PostgreSQL an aborted transaction
		// answers every further statement with an error until it is closed.
		u, err := resolveProvisionedMailbox(email)
		return u, false, err
	}

	u, created, err := provisionUserForClaim(s, claim)
	if err != nil {
		_ = s.Rollback()
		return nil, false, err
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return nil, false, err
	}

	events.DispatchPending(ctx, s)
	return u, created, nil
}

// provisionUserForClaim fills in a mailbox claim this call has just won.
func provisionUserForClaim(s *xorm.Session, claim *ProvisionedUser) (*user.User, bool, error) {
	// A user this instance already has for the mailbox is ADOPTED rather than
	// duplicated. It is what "resolve" means - the mailbox is the identity
	// (Identity-and-Access-Rules.md §1) - and it is the only safe default for
	// the accounts already on the development instance, which Google sign-in
	// created before any of this existed. Creating a second user for them
	// instead would strand every task they have, silently and unrecoverably.
	//
	// BRA-1021 owns the deliberate settlement of those accounts and may narrow
	// this; what it must not leave in place is a default that duplicates.
	existing, err := userForMailbox(s, claim.Email)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if err := bindClaim(s, claim, existing.ID); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}

	created, err := registerUserForMailbox(s, claim.Email)
	if err != nil {
		return nil, false, err
	}
	if err := bindClaim(s, claim, created.ID); err != nil {
		return nil, false, err
	}

	// Re-read rather than return what RegisterUser handed back: CreateUser
	// reads the new row BEFORE it may move the account to
	// StatusEmailConfirmationRequired and send the confirmation mail, so the
	// struct it returns can say Active when the stored row does not. The status
	// is what the reply's email_verified is derived from.
	stored, err := userByID(s, created.ID)
	if err != nil {
		return nil, false, err
	}
	return stored, true, nil
}

// bindClaim points a won claim at the user it provisioned.
func bindClaim(s *xorm.Session, claim *ProvisionedUser, userID int64) error {
	_, err := s.ID(claim.ID).Cols("user_id").Update(&ProvisionedUser{UserID: userID})
	return err
}

// registerUserForMailbox creates the user itself.
//
// RegisterUser rather than user.CreateUser, because a new account is not only a
// row: it needs its Inbox and its default saved filters, and RegisterUser is
// the one place that does all three. auth.CreateUserWithRandomUsername, which
// the OpenID path uses, creates the Inbox but NOT the saved filters, and models
// cannot import it anyway.
func registerUserForMailbox(s *xorm.Session, email string) (*user.User, error) {
	// The issuer must be local, and that is not a default falling through. The
	// OpenID fallback that links a Google sign-in to an account already here
	// searches for `Issuer: user.IssuerLocal` and nothing else
	// (openid.fallbackSearchUsers), so a provisioned user recorded under any
	// other issuer is one their own sign-in can never find.
	//
	// A local issuer means user.checkIfUserIsValid demands a password. It gets
	// one nobody has: 32 bytes of crypto/rand, hashed by CreateUser and
	// dropped here. Local login is off on a managed instance, so the account is
	// reachable only through the identity provider - and if it were ever turned
	// on, this password still authenticates nobody.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}

	u := &user.User{
		Email:    email,
		Password: hex.EncodeToString(secret),
		Issuer:   user.IssuerLocal,
	}
	// The username is random and is retried on collision, exactly as the
	// OpenID path does it. It is deliberately not derived from the mailbox:
	// usernames are visible to other users, so a derived one - even a hashed
	// one - would answer "is this person on this instance" for anyone who could
	// guess the address.
	for {
		u.Username = petname.Generate(3, "-")
		created, err := RegisterUser(s, u)
		if err == nil {
			return created, nil
		}
		if !user.IsErrUsernameExists(err) {
			return nil, err
		}
	}
}

// resolveProvisionedMailbox reads back the winner's row, in a session of its
// own because the losing transaction has been rolled back.
func resolveProvisionedMailbox(email string) (*user.User, error) {
	s := db.NewSession()
	defer s.Close()

	claim := &ProvisionedUser{}
	has, err := s.Where("email = ?", email).Get(claim)
	if err != nil {
		return nil, err
	}
	if !has || claim.UserID == 0 {
		// The winner rolled back after taking the mailbox, so the row is gone
		// (or, on a database that let us read it uncommitted, not yet filled
		// in). Nothing was provisioned and nothing may be invented.
		return nil, ErrMailboxProvisioningLost
	}

	return userByID(s, claim.UserID)
}

// userForMailbox finds an existing user by mailbox, or nil.
//
// users.email is not unique, so this orders by id and takes the first: two
// rows sharing an address must always resolve to the SAME one, whichever it is,
// rather than to whatever the database happened to return first.
func userForMailbox(s *xorm.Session, email string) (*user.User, error) {
	found := &user.User{}
	has, err := s.Where("email = ?", email).OrderBy("id ASC").Limit(1).Get(found)
	if err != nil || !has {
		return nil, err
	}
	return found, nil
}

// userByID reads a user the way this endpoint needs it: a disabled or locked
// account is still the user for that mailbox, and reporting it is what stops a
// second one being created for the same person.
func userByID(s *xorm.Session, id int64) (*user.User, error) {
	u, err := user.GetUserByID(s, id)
	if err != nil && !user.IsErrUserStatusError(err) {
		return nil, err
	}
	return u, nil
}
