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
	"crypto/rand"
	"encoding/hex"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/xorm"
)

// The staff account this instance runs Feedback as.
//
// THEY ARE CONSTANTS RATHER THAN CONFIGURATION, and that is the whole point of
// BRA-1414. The previous design named the owner in `brazn.feedbackowner`, an
// operator-supplied setting that was never supplied on any deployment, so
// Feedback resolved to nothing and the feature had never once worked in front
// of a customer. A setting that must hold exactly one value on every instance
// we run is not configuration; it is a way for a release to ship broken and
// say nothing.
//
// The username is the identity everything else resolves through, so it is the
// one string here that must not change: it is written into the users table on
// first boot, and a later edit would resolve to no account rather than to the
// account already there. See seedInstanceStaff for what happens then.
const (
	OneAdminUsername = "oneadmin"
	// OneAdminName is the display name other members see on the Feedback
	// project and on anything this account creates.
	OneAdminName = "One Admin"
	// OneAdminEmail is a real, monitored mailbox. It is never used to sign in
	// - see ensureOneAdmin for why the account has no usable password - but
	// user.CreateUser requires an address, and an invented one would bounce
	// every notification the account is ever sent.
	OneAdminEmail = "admin@braznmngo.com"
	// StaffTeamTitle names the team that reaches Feedback. It is a LABEL and
	// never an identity, exactly as every other title in this package is:
	// ensureStaffTeam finds the team through the grant row on the Feedback
	// root and never by reading this string back.
	StaffTeamTitle = "Brazn Staff"
)

// staffUsernames are the accounts put into the staff team on every boot.
//
// EACH ENTRY IS EITHER THE NAME SOMEBODY SIGNS IN WITH OR THEIR EMAIL ADDRESS,
// whichever the person writing the list happens to know. That is not
// convenience. Whoever adds a colleague here knows them as a person and knows
// their address; which of the two strings the users table stores as the
// username is a fact about the database that they would have to go and look
// up, and a wrong guess is skipped in silence and looks exactly like a right
// one. user.GetUserByUsernameOrEmail decides which form it was given.
//
// ADDING A COLLEAGUE IS ONE LINE HERE AND A DEPLOY. That is deliberate and it
// is the cheaper half of a trade: a list in code cannot be edited by anyone
// who can reach the database, and it is read back on every start-up, so a name
// added here reaches the team the next time the server comes up rather than
// needing somebody to remember to run something.
//
// An entry this instance cannot make a member of - no account yet, or an
// account that is disabled or locked - is skipped with a warning rather than
// failing the boot, and the entries after it are still added. Staff accounts
// are created by people signing in for the first time, so an entry will
// routinely be listed here before its account exists; and taking the product
// down to enforce a membership nobody is waiting on would be the more
// expensive failure. See ensureStaffMembers.
//
// WHO CAN READ FEEDBACK, stated precisely, because the honest sentence and the
// comfortable one differ. OneAdminUsername owns the project but is not a person
// and can never sign in (see ensureOneAdmin), so it reads nothing. The address
// below is Sebastian's, and it is the entry that makes a human reader possible
// - BUT ONLY ON AN INSTANCE WHERE THAT ACCOUNT ALREADY EXISTS. Accounts are
// created by signing in, so on an instance he has not yet signed in to, this
// entry resolves to nothing, is skipped with a warning, and the team again has
// no member who can read a customer's report. Whether feedback is readable is
// therefore a fact about the deployment, not about this file, and the log line
// ensureStaffMembers writes on a skip is where that fact is observable.
var staffUsernames = []string{
	OneAdminUsername,
	// Sebastian, who reads customer feedback. This is the address the ONE Apps
	// repository records as the one used for user resolution, in
	// docs/tickets/TICKET-vikunja-pilot-config.md.
	"sebastian@braznmngo.com",
	// Add colleagues here, one per line, as a username or an email address.
}

// SeedInstanceStaff makes sure this instance has the staff account, the
// Feedback project it owns, the staff team, and that team's Admin grant on
// Feedback - creating only what is missing.
//
// IT RUNS AT WEB-SERVER START-UP AND NOWHERE ELSE, and both halves of that
// matter. It cannot be a migration, because on a fresh database this fork's
// migration framework builds the tables directly from the current structs and
// marks every migration applied WITHOUT running any of them: a seed written as
// a migration would be recorded as done on precisely the instances that had
// never run it. And it cannot go into initialize.FullInit, which the restore,
// health check, repair, dump and account commands also call - asking an
// instance whether it is healthy must not write rows to it.
//
// A FAILURE HERE DOES NOT STOP THE SERVER. It is logged and start-up
// continues, because the product's other surfaces work perfectly well without
// Feedback, and refusing to serve a live beta over a staff project nobody is
// looking at yet would be the more expensive failure by a wide margin.
// ProvisionFeedbackAccess already treats an unresolvable owner as "no Feedback
// on this instance" rather than as an error, so the degraded state is one the
// rest of the code already understands.
func SeedInstanceStaff() {
	s := db.NewSession()
	defer s.Close()
	defer events.CleanupPending(s)

	if err := seedInstanceStaff(s); err != nil {
		_ = s.Rollback()
		log.Errorf("Could not seed the staff account and Feedback project: %v", err)
		return
	}
	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		log.Errorf("Could not seed the staff account and Feedback project: %v", err)
		return
	}

	// The events this queued are DISCARDED rather than dispatched, by the
	// deferred CleanupPending above. FullInit starts the event system in a
	// goroutine, so nothing is reliably listening this early in start-up, and
	// dispatching into a pubsub that is still nil logs one error per project
	// and team the seed just made. There is also nothing for a listener to do:
	// a project created on the very first boot has no audience, no watcher and
	// no subscriber, because no account exists yet to be one.
}

// seedInstanceStaff is SeedInstanceStaff's work, in a session the caller owns
// so a test can observe it and roll it back.
//
// THE ORDER IS FORCED. The account has to exist before the project, because
// the project is owned by it; the project has to exist before the grant,
// because the grant names it. Every step reads before it writes, so a second
// run finds four things and creates none.
func seedInstanceStaff(s *xorm.Session) error {
	admin, err := ensureOneAdmin(s)
	if err != nil {
		return err
	}

	rootID, err := ensureFeedbackProject(s, admin)
	if err != nil {
		return err
	}

	teamID, err := ensureStaffTeam(s, admin, rootID)
	if err != nil {
		return err
	}

	if err := grantTeamAccess(s, admin, teamID, rootID); err != nil {
		return err
	}

	return ensureStaffMembers(s, admin, teamID)
}

// ensureOneAdmin returns the staff account, creating it on first boot.
//
// THE ACCOUNT IS NEVER A LOGIN. It is given 32 bytes from crypto/rand as its
// password, which user.CreateUser hashes, and the plaintext goes out of scope
// on the next line - it is neither stored, logged, printed nor returned, so
// nobody, including whoever deployed this, can sign in as it. Staff reach
// Feedback by signing in as themselves and going through the staff team's
// grant, which is also what makes an audit trail possible: a comment left on a
// customer's report carries the name of the person who left it rather than a
// shared account everybody has the key to.
//
// The same pattern, and the same reasoning, is at registerUserForMailbox in
// brazn_provisioning.go. A local issuer is what makes checkIfUserIsValid
// demand a password at all, and a local issuer is required because the OpenID
// fallback that links a Google sign-in to an existing account searches for
// IssuerLocal and nothing else.
//
// RegisterUserConfirmLater rather than RegisterUser, because RegisterUser
// sends the confirmation mail, and the mailer is enabled in production. The
// notification it hands back is dropped: there is no address to confirm to
// that anybody is waiting on, and this account never signs in, so its
// confirmation status decides nothing.
func ensureOneAdmin(s *xorm.Session) (*user.User, error) {
	existing, err := user.GetUserByUsername(s, OneAdminUsername)
	if err == nil {
		return existing, nil
	}
	if !user.IsErrUserDoesNotExist(err) {
		return nil, err
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}

	created, _, err := RegisterUserConfirmLater(s, &user.User{
		Username: OneAdminUsername,
		Name:     OneAdminName,
		Email:    OneAdminEmail,
		Password: hex.EncodeToString(secret),
		Issuer:   user.IssuerLocal,
	})
	if err != nil {
		return nil, err
	}

	// The account is marked active in the same breath it is created. With the
	// mailer enabled, user.CreateUser writes StatusEmailConfirmationRequired
	// over the active status it set a moment earlier, and no confirmation mail
	// will ever be opened for an account that cannot sign in. Leaving it
	// pending would make every staff listing show the account that owns
	// Feedback as one that never finished registering.
	//
	// One column, not the whole row: UpdateUser writes all of
	// baseUserUpdateColumns, which is how email confirmation was made
	// decorative once already (BRA-1047).
	if _, err := s.ID(created.ID).Cols("status").
		Update(&user.User{Status: user.StatusActive}); err != nil {
		return nil, err
	}
	created.Status = user.StatusActive

	log.Infof("Created the %s staff account and its Feedback project", OneAdminUsername)
	return created, nil
}

// ensureStaffTeam returns the id of the team that reaches Feedback, creating
// it on first boot.
//
// IDEMPOTENCE IS DECIDED ON THE GRANT ROW, not on the team's name. A title is
// neither unique nor stable - anyone who can rename a team could otherwise
// decide which team the next boot adopts - and this package refuses
// title-as-identity everywhere else for exactly that reason. The grant is the
// relationship the seed exists to create, so reading it back is asking the
// question that actually matters: does a team already reach Feedback?
//
// The read and the two writes are one transaction, so there is no window in
// which the team exists without its grant and a second boot could make a
// second team.
//
// ONE GRANT ON THE PARENT REACHES EVERY REPORTER'S SUB-PROJECT, with no
// membership row per reporter. checkPermissionsForProjects walks a project's
// ancestors when it resolves the acting user's level and keeps the nearest
// qualifying one, and accessibleProjectIDsSubquery walks the same hierarchy
// downwards when it lists them. That is the same mechanism
// ensureFeedbackSubProject already relies on to give the owner a triage view.
func ensureStaffTeam(s *xorm.Session, admin *user.User, rootID int64) (int64, error) {
	granted := &TeamProject{}
	has, err := s.Where("project_id = ?", rootID).Get(granted)
	if err != nil {
		return 0, err
	}
	if has {
		return granted.TeamID, nil
	}

	team := &Team{Name: StaffTeamTitle}
	if err := team.CreateNewTeam(s, admin, true); err != nil {
		return 0, err
	}
	return team.ID, nil
}

// ensureStaffMembers puts every listed colleague into the staff team.
//
// AN ENTRY THIS INSTANCE CANNOT RESOLVE TO A USABLE ACCOUNT IS SKIPPED, and
// someone already in the team is left alone. Both are ordinary rather than
// exceptional: staffUsernames is edited by hand ahead of people signing in,
// and this runs on every boot, so the second run of an unchanged list must do
// nothing at all.
//
// THREE REFUSALS ARE SKIPPED, NOT ONE, and the two beyond "no such account"
// were not obvious. addStaffMember resolves the entry through
// user.GetUserByUsername, which hands back ErrAccountDisabled or
// ErrAccountLocked for an account that exists and cannot be used - the state a
// colleague's account is left in when they leave. Returning either would abort
// this function, and because the whole seed runs in ONE TRANSACTION, the
// rollback would take the Feedback project and the team's grant with it. One
// former colleague still listed here would therefore have un-seeded the
// instance on every boot, and the only trace would be a line in the log.
//
// The loop continues past a skip deliberately: giving up at the first
// unresolvable entry would look identical to skipping it, while quietly
// dropping every colleague listed after it.
//
// THE WARNING NAMES THE ENTRY AS WRITTEN rather than whatever it resolved to,
// because the reader of that line is somebody looking for which line of the
// list to correct.
func ensureStaffMembers(s *xorm.Session, admin *user.User, teamID int64) error {
	for _, entry := range staffUsernames {
		err := addStaffMember(s, admin, teamID, entry)
		switch {
		case err == nil, IsErrUserIsMemberOfTeam(err):
			continue
		case user.IsErrUserDoesNotExist(err):
			log.Warningf("%q is listed as staff but is not an account on this instance yet; skipping", entry)
			continue
		case user.IsErrAccountDisabled(err), user.IsErrAccountLocked(err):
			log.Warningf("%q is listed as staff but the account is disabled or locked; skipping", entry)
			continue
		default:
			return err
		}
	}
	return nil
}

// addStaffMember makes one listed entry a member of the staff team.
//
// IT RESOLVES THE ENTRY FIRST AND THEN HANDS ON THE USERNAME, which is what
// lets the list hold an address. TeamMember.Create takes a username and only a
// username, so an address given to it directly is simply an account that does
// not exist.
//
// The second lookup inside Create is not waste. GetUserByUsernameOrEmail reads
// the row and says nothing about whether the account can be used;
// GetUserByUsername, which Create calls, is the reader that turns a disabled or
// locked row into an error. Resolving here and letting Create re-read is
// therefore what keeps the two refusals ensureStaffMembers skips on, rather
// than re-implementing that check against a rule that lives in pkg/user.
func addStaffMember(s *xorm.Session, admin *user.User, teamID int64, entry string) error {
	resolved, err := user.GetUserByUsernameOrEmail(s, entry)
	if err != nil {
		return err
	}

	member := &TeamMember{TeamID: teamID, Username: resolved.Username}
	return member.Create(s, admin)
}
