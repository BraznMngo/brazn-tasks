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
	"strconv"
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

// ErrUserAlreadyProvisionedForAnotherMailbox means adopting the user this
// mailbox resolves to would give one Brazn Tasks account to two mailboxes.
//
// IT IS AN AUTHORIZATION INVARIANT, not tidiness, and the path to it is
// short. users.email is rewritten in place by openid.getOrCreateUser when a
// provider reports a different address for a subject it already knows - that
// write goes through user.UpdateUser and never reaches UpdateEmail, so it
// skips the uniqueness check UpdateEmail makes. An account whose address has
// moved that way onto a mailbox the commercial service later sells to somebody
// else would be adopted a second time, and the two customers would hold one
// user id. With the provider's email fallback enabled - which it must be for a
// provisioned account to sign in at all - the second customer signs in to the
// first customer's account.
//
// Refusing is the only safe answer: this call cannot tell which of the two the
// account belongs to, and the one thing it must not do is guess.
var ErrUserAlreadyProvisionedForAnotherMailbox = errors.New("the user this mailbox resolves to is already provisioned for another mailbox")

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
	// Email is the mailbox, stored exactly as the commercial service sent it -
	// see provisioning.CreateUser on why nothing here transforms it, and on
	// why how it is COMPARED is the database's collation rather than a
	// property this fork guarantees.
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
	// (docs/Identity-and-Access-Rules.md §1, in the Percy repository) - and it
	// is the only safe default for the accounts already on the development
	// instance, which Google sign-in created before any of this existed.
	// Creating a second user for them instead would strand every task they
	// have, silently and unrecoverably.
	//
	// BRA-1021 owns the deliberate settlement of those accounts and may narrow
	// this; what it must not leave in place is a default that duplicates.
	existing, err := userForMailbox(s, claim.Email)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if err := refuseASecondMailboxForOneUser(s, claim, existing.ID); err != nil {
			return nil, false, err
		}
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

	// Re-read rather than return what RegisterUser handed back. That struct is
	// stale by construction: CreateUser reads the new row at user_create.go:91
	// and then writes to it twice more - the confirmation status, and
	// default_project_id via CreateNewProjectForUser - so what it returns is the
	// row as it was mid-flight. The reply's email_verified comes off the status,
	// and it must be what is STORED.
	//
	// This line USED to be unexercised, and the comment here said so at length.
	// It was unexercised because a defect made the divergence unreachable:
	// CreateNewProjectForUser passed the pre-write struct to user.UpdateUser,
	// whose column list includes "status", writing the stale Active back over
	// StatusEmailConfirmationRequired on every mail-enabled registration. Every
	// account created here was therefore Active whatever the mailer was doing.
	//
	// BRA-1047 fixed that: CreateNewProjectForUser now writes default_project_id
	// alone. So a freshly created account on a mail-enabled instance really is
	// StatusEmailConfirmationRequired, this re-read really does report it, and
	// created:true with email_verified:false is now a reachable reply.
	//
	// Percy Cloud is the consumer that must care, because signUp copies
	// email_verified into email_verified_at and requireVerifiedAccount reads it.
	stored, err := userByID(s, created.ID)
	if err != nil {
		return nil, false, err
	}
	return stored, true, nil
}

// refuseASecondMailboxForOneUser enforces the one invariant the schema cannot:
// user_id is deliberately not unique - see ProvisionedUser on why a unique
// index there would make concurrent claims for DIFFERENT mailboxes collide -
// so nothing but this stops two mailboxes binding to one account.
//
// It is asked only on the adoption path, because that is the only path where a
// user this call did not create can be bound. A freshly created user has no
// other claim by construction.
//
// The row this call has already inserted is excluded by id rather than by
// user_id: it holds 0 until bindClaim runs, and relying on that ordering would
// make this check quietly depend on the order of two statements somewhere else.
func refuseASecondMailboxForOneUser(s *xorm.Session, claim *ProvisionedUser, userID int64) error {
	bound := &ProvisionedUser{}
	has, err := s.Where("user_id = ? AND id != ?", userID, claim.ID).Get(bound)
	if err != nil {
		return err
	}
	if has {
		return ErrUserAlreadyProvisionedForAnotherMailbox
	}
	return nil
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

// MailboxForSubject answers a resolve_mailbox request: the current address of
// the subject it names, or the empty string when this instance has no mailbox
// to report for that subject.
//
// IT READS users.email AND NEVER brazn_provisioned_users.email, which is the one
// thing this function exists to get right. The two are not copies of each other:
// the claim row is the mailbox Percy Cloud provisioned AGAINST and is the
// authoritative map from that mailbox to a user - see ProvisionedUser - while
// the user row carries the address the person has NOW, and the two diverge the
// moment somebody changes theirs. Both callers want the user row: contact has to
// reach the person where they are, and a suppression entry has to name the
// address that would otherwise really be sent to. Reaching for the claim table
// is the natural mistake here, because it is the table this file owns and it is
// already keyed the right way round.
//
// ABSENCE IS ONE ANSWER AND HAS ONE RETURN VALUE. An id this instance never
// minted, and a subject DeleteUser erased, come back the same way - and on this
// path that is structural rather than a rule to remember, because DeleteUser
// takes the claim row holding the erased subject's address away with the user,
// so afterwards nothing is left that COULD tell the two apart. users.id is a
// sequential autoincrement and possession of the provisioning signing key is the
// whole of the authorisation on this channel, so an answer that distinguished
// them would let a key holder walk the keyspace and map which ids were once
// customers.
//
// The empty address is in that set deliberately rather than by oversight.
// users.email is `varchar(250) null` with every bot user carrying the empty
// string, so it is a value this read can really see; it is not a mailbox, and
// the contract's response has no way to report a resolution that names none.
//
// A DISABLED OR LOCKED ACCOUNT STILL HAS A MAILBOX, which the read below gives
// for free by asking the table rather than the accessor: user.GetUserByID
// returns an error for either status, and treating that as absence would leave a
// locked customer unsuppressable. Suppressing their address is exactly as
// necessary as suppressing anyone else's.
func MailboxForSubject(subject string) (string, error) {
	id, err := strconv.ParseInt(subject, 10, 64)
	if err != nil || id < 1 {
		return "", nil
	}

	s := db.NewSession()
	defer s.Close()

	// The row itself, the way userForMailbox reads one, rather than through
	// user.GetUserByID. That is not a shortcut and must not be "simplified" into
	// one: GetUserByID BLANKS Email on the way out, so the obvious call answers
	// with an empty string for every subject that exists - a resolution that
	// resolves nothing, for everybody, while every other assertion about it
	// passes. Its guard against a zero id goes with it, which is why the id is
	// checked above: a Get with no condition answers with some user rather than
	// none.
	found := &user.User{}
	has, err := s.Where("id = ?", id).Get(found)
	if err != nil || !has {
		return "", err
	}
	return found.Email, nil
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
