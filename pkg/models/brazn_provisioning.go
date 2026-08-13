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
// second one. ResolveUserByMailbox is where that sentence is enforced rather
// than only asserted, and it carries what an address change therefore does.
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

// parseSubjectID is every subject-string parse on this channel, in one place.
//
// THE ROUND-TRIP CHECK IS NOT REDUNDANT WITH id >= 1. strconv.ParseInt accepts
// leading zeros - "01" parses to the same 1 a correct sender's bare "1" would -
// so without comparing the reformatted digits back against the original string,
// a malformed subject does not fall into "no such id", it ALIASES a real one.
// RevokeSessionForSubject was the first caller to close this, and its own
// comment explained why at length; this is that same check, lifted here so
// every caller on this channel closes it rather than the one that happened to
// get the comment. MailboxForSubject, ResolveUserBySubject, EraseSubject and
// provisioningSubject all parse the identical subject grammar and all shared
// the identical gap until this was extracted - a leading-zero subject aliased
// a real user's mailbox, verification status, erasure target, or provisioned
// topology exactly as it would have aliased a revoked session.
//
// TOLERANT VS STRICT STAYS WITH THE CALLER, deliberately. What a malformed
// subject MEANS differs by operation - MailboxForSubject and
// ResolveUserBySubject read it as an ordinary absence, while EraseSubject,
// provisioningSubject and RevokeSessionForSubject refuse it outright, because
// answering success/no-op for a request that named no real subject would
// report an action that never happened. Only the parse - and the aliasing gap
// in it - is shared; ok=false is this function's only vocabulary, and each
// caller decides on its own established terms what that means for it.
func parseSubjectID(subject string) (id int64, ok bool) {
	id, err := strconv.ParseInt(subject, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != subject {
		return 0, false
	}
	return id, true
}

func MailboxForSubject(subject string) (string, error) {
	id, ok := parseSubjectID(subject)
	if !ok {
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

// RevokeSessionForSubject deletes one Brazn Tasks session, scoped to the
// subject provisioning names as its owner (BRA-1014).
//
// A session that is already gone - never existed, already revoked, expired
// and swept, or belonged to a different user than the one named - is not a
// refusal. Revocation must be safe to repeat: the commercial service calls
// this before it marks its own device-authorization row revoked, and a retry
// of a call whose response it lost must be able to commit rather than fail
// against a row that no longer needs deleting.
//
// A MALFORMED SUBJECT IS NOT THE SAME CASE, matching EraseSubject's own rule
// and for the same reason: the commercial service validates subject ids
// against ^[1-9][0-9]{0,18}$ before it ever stores one, so this cannot arise
// from a correct sender. Answering success for it would report a revocation
// that could not have happened, so it is refused rather than swallowed as
// nothing-to-revoke. See parseSubjectID for why that check is a round-trip
// and not just id < 1.
func RevokeSessionForSubject(ctx context.Context, subject, sessionID string) error {
	id, ok := parseSubjectID(subject)
	if !ok {
		return ErrProvisioningSubjectUnknown
	}

	return provisionInTransaction(ctx, func(s *xorm.Session) error {
		return DeleteSessionForUser(s, sessionID, id)
	})
}

// UserResolution is what one resolve_user request answers with: the user this
// instance holds for the subject named, and whether its mailbox is confirmed.
//
// A NIL *UserResolution IS THE `unresolvable` ANSWER and the only one. There is
// no reason member and no third outcome, because a vocabulary that could
// express "erased" is one an implementation would eventually be asked to fill
// in - and DeleteUser destroys the brazn_provisioned_users row along with the
// user, so after an erasure this instance holds nothing that could tell an
// erased subject from one it never minted.
type UserResolution struct {
	UserID int64
	// EmailVerified is the ROW'S OWN status and never anything the caller said.
	// The caller is precisely the party with no way to know: the commercial
	// layer reflects a confirmed mailbox and never establishes one, and this
	// field is the reflection's only source. create_user's reply overrides it
	// on the create path because that call reports a row it made microseconds
	// earlier; nothing here creates, so every answer is about a row this
	// instance did not write and the field is its own truth in both directions.
	EmailVerified bool
}

// ResolveUserByMailbox answers the recognition form of a resolve_user request:
// which Brazn Tasks user did Percy Cloud provision for this mailbox, or none.
//
// ⚠ IT READS brazn_provisioned_users.email AND NEVER users.email, which is the
// OPPOSITE of the column MailboxForSubject reads and the one decision this
// function exists to get right. The two are not copies of each other and the
// choice is not a preference:
//
// users.email CANNOT BE A ZERO-OR-ONE LOOKUP. It is varchar(250) null with no
// unique index, and one cannot be added, because every bot user carries the
// empty string - see ProvisionedUser, which exists for that reason. This
// operation answers with one user id or nothing, so it needs a column that can
// only ever match one row; brazn_provisioned_users.email is varchar(250) not
// null unique and IS that constraint. Matching the user row instead would leave
// this call choosing between rows, with the choice unspecified - and worse,
// answering "no user" for a mailbox this instance really did provision, which
// is how the caller would be told to create a second account for one customer.
// That is BRA-1106's defect exactly, arriving from this side of the seam.
//
// IT DOES NOT CONTRADICT MailboxForSubject, because the two answer different
// questions. resolve_mailbox asks where to SEND now, so it must follow the
// person and reads the live user row. This asks which account Percy Cloud SOLD
// to a mailbox, so it must be stable: the id it returns becomes an
// accounts.user_id, a primary key on the commercial side with no update path,
// and an id that moved when somebody edited their profile would repoint a paid
// entitlement. Two operations, two columns, both right; harmonising them would
// break one of the two.
//
// ⚠ WHAT AN ADDRESS CHANGE THEREFORE DOES, stated rather than left implied.
// The two columns diverge the moment somebody changes their address here. After
// that: asked by the OLD address - the one provisioned against - this answers
// the same user id it always did, so every existing commercial record keeps
// resolving; asked by the NEW one, the claim table does not hold it and the
// answer is `unresolvable`, so that customer is not recognised and opens a
// second entitlement. Nothing here invents a mechanism to close that. It is
// Case 14 (docs/Identity-and-Access-Rules.md §11 in the Percy repository),
// which is unsatisfied and owned by BRA-1022 - the ticket that has to decide
// whether an address change updates the claim row.
//
// IT NEVER CREATES, and neither does anything it calls. A resolve that fell
// back to creating would reintroduce the outage BRA-1106 fixed; the contract
// states this as an obligation the consumer cannot enforce, which makes it this
// function's to keep.
func ResolveUserByMailbox(email string) (*UserResolution, error) {
	s := db.NewSession()
	defer s.Close()

	claim := &ProvisionedUser{}
	has, err := s.Where("email = ?", email).Get(claim)
	if err != nil || !has {
		return nil, err
	}
	// A claim taken and not yet bound - the moment between the insert that wins
	// the mailbox and the update that fills in the id. There is no user to
	// report yet, and inventing one is the whole thing this operation must not
	// do, so it is an absence like any other.
	if claim.UserID == 0 {
		return nil, nil
	}
	return userResolutionForID(s, claim.UserID)
}

// ResolveUserBySubject answers the verification form: is this user's mailbox
// confirmed? requireVerifiedAccount asks it, holding a bearer and a users.id
// and no address at all.
//
// IT READS THE USER ROW BY ID AND DOES NOT REQUIRE A CLAIM ROW, deliberately.
// The absence the contract names for this form is "an id this instance never
// minted", which is a fact about users and not about what Percy provisioned;
// and an id that had a user but no claim would otherwise be refused a
// verification answer it can perfectly well be given. It is also what makes an
// erasure indistinguishable either way round: DeleteUser takes both rows.
//
// A subject that is not a decimal number at all is an ANSWER here rather than a
// refusal, exactly as it is for MailboxForSubject - commercialID admits shapes
// the contract's producer never sends, and this is the consumer's tolerant half
// of that split.
func ResolveUserBySubject(subject string) (*UserResolution, error) {
	id, ok := parseSubjectID(subject)
	if !ok {
		return nil, nil
	}

	s := db.NewSession()
	defer s.Close()

	return userResolutionForID(s, id)
}

// userResolutionForID is the one projection both forms answer with, so the two
// cannot report a user differently.
//
// It reads the row the way MailboxForSubject does rather than through
// user.GetUserByID, and that is not a shortcut: GetUserByID returns an error
// for a disabled or a locked account, and treating that as absence would tell
// Percy Cloud that a suspended customer is not a user here - which converges
// nothing and would have their next signup open a second account. A locked
// account is still the account for that mailbox, and its confirmation status is
// still its own.
func userResolutionForID(s *xorm.Session, id int64) (*UserResolution, error) {
	found := &user.User{}
	has, err := s.Where("id = ?", id).Get(found)
	if err != nil || !has {
		return nil, err
	}
	return &UserResolution{
		UserID:        found.ID,
		EmailVerified: found.Status != user.StatusEmailConfirmationRequired,
	}, nil
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
