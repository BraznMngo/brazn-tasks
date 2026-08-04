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
	"time"

	"code.vikunja.io/api/pkg/cron"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/files"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/builder"
	"xorm.io/xorm"
)

// User deletion must happen here in this packaage because we want to delete everything associated to this user.
// Because most of these things are managed in the models package, using them has to happen here.

// RegisterUserDeletionCron registers the cron job that actually removes users who are scheduled to delete.
func RegisterUserDeletionCron() {
	err := cron.Schedule("0 * * * *", deleteUsers)
	if err != nil {
		log.Errorf("Could not register deletion cron: %s", err.Error())
	}
}

func deleteUsers() {
	s := db.NewSession()
	users := []*user.User{}
	err := s.Where(builder.Lt{"deletion_scheduled_at": time.Now()}).
		Find(&users)
	s.Close()
	if err != nil {
		log.Errorf("Could not get users scheduled for deletion: %s", err)
		return
	}

	if len(users) == 0 {
		return
	}

	log.Debugf("Found %d users scheduled for deletion", len(users))

	now := time.Now()

	for _, u := range users {
		if !u.DeletionScheduledAt.Before(now) {
			log.Debugf("User %d is not yet scheduled for deletion. Scheduled at %s, now is %s", u.ID, u.DeletionScheduledAt, now)
			continue
		}

		func() {
			us := db.NewSession()
			defer us.Close()

			err = DeleteUser(us, u)
			if err != nil {
				_ = us.Rollback()
				log.Errorf("Could not delete u %d: %s", u.ID, err)
				return
			}

			log.Debugf("Deleted user %d", u.ID)

			err = us.Commit()
			if err != nil {
				log.Errorf("Could not commit transaction: %s", err)
			}
		}()
	}
}

func getProjectsToDelete(s *xorm.Session, u *user.User) (projectsToDelete []*Project, err error) {
	projectsToDelete = []*Project{}
	projects, _, err := getAllProjectsForUser(s, u.ID, &projectOptions{
		page:        0,
		perPage:     -1,
		getArchived: true,
	})
	if err != nil {
		return nil, err
	}

	for _, l := range projects {
		if l.ID < 0 {
			continue
		}

		hadUsers, err := ensureProjectAdminUser(s, l)
		if err != nil {
			return nil, err
		}
		if hadUsers {
			continue
		}
		hadTeams, err := ensureProjectAdminTeam(s, l)
		if err != nil {
			return nil, err
		}

		if hadTeams {
			continue
		}

		projectsToDelete = append(projectsToDelete, l)
	}

	return
}

// migrationStatus is a stand-in for migration.Status, and it exists because
// pkg/modules/migration IMPORTS pkg/models - so importing the real type back
// here is an import cycle, not a style preference.
//
// The columns mirror migration.Status exactly. That is not decoration: the
// models test schema is built by Sync2 (see setup_tests.go, which has to sync
// this table because it is not in GetTables()), and pkg/webtests then syncs
// the real migration.Status over the same table. A narrower stand-in would
// leave that second Sync2 to ALTER TABLE in a NOT NULL column with no default,
// which SQLite refuses. Matching the real struct means the second sync finds
// nothing missing and does nothing.
//
// It is deliberately NOT added to GetTables(): that feeds
// db.RegisteredTableNames(), which pkg/db/dump.go walks one file per name, and
// a second bean for a table migration.GetTables() already registers would put
// migration_status in every dump twice.
type migrationStatus struct {
	ID           int64     `xorm:"bigint autoincr not null unique pk"`
	UserID       int64     `xorm:"bigint not null"`
	MigratorName string    `xorm:"varchar(255)"`
	StartedAt    time.Time `xorm:"not null"`
	FinishedAt   time.Time `xorm:"null"`
}

// TableName is migration_status, matching migration.Status.
func (*migrationStatus) TableName() string {
	return "migration_status"
}

// DeleteUser completely removes a user and all their associated projects and tasks.
// This action is irrevocable.
// Public to allow deletion from the CLI.
func DeleteUser(s *xorm.Session, u *user.User) (err error) {
	// Delete any bot users owned by this user first (cascades their data too).
	var ownedBots []*user.User
	if err = s.Where("bot_owner_id = ?", u.ID).Find(&ownedBots); err != nil {
		return err
	}
	for _, bot := range ownedBots {
		if err = DeleteUser(s, bot); err != nil {
			return err
		}
	}

	projectsToDelete, err := getProjectsToDelete(s, u)
	if err != nil {
		return err
	}

	for _, p := range projectsToDelete {
		if p.parentID() != 0 {
			// Child projects are deleted by p.Delete
			continue
		}
		err = p.Delete(s, u)
		// If the user is the owner of the default project it will be deleted, if they are not the owner
		// we can ignore the error as the project was shared in that case.
		if err != nil && !IsErrCannotDeleteDefaultProject(err) {
			return err
		}
	}

	// Delete all related entities
	relatedEntities := []struct {
		column string
		model  any
	}{
		{"user_id", &TaskAssginee{}},
		{"user_id", &Subscription{}},
		{"user_id", &TeamMember{}},
		{"owner_id", &SavedFilter{}},
		{"user_id", &Reaction{}},
		{"user_id", &Favorite{}},
		{"owner_id", &APIToken{}},
		// Brazn fork (BRA-933). brazn_entitlement_projections has no foreign key
		// on user_id and no cascade, so without this entry the erased subject's
		// row outlives them, still holding their organization, edition, seat
		// status and issued-at. BraznApplyEntitlementProjection already refuses
		// to write a NEW row for a subject this instance no longer has; this is
		// the other half, removing one that was already there.
		//
		// It is an access statement, not a commercial record: no amount, no
		// invoice, no tax figure. The records retention law keeps live in the
		// commercial service, are authoritative there, and are untouched by this
		// path - so erasing the local projection destroys no original.
		//
		// Upstream file: re-apply on merge (patch-surface area 4, entitlement
		// synchronization).
		{"user_id", &EntitlementProjection{}},
		// Brazn fork (BRA-1018). brazn_provisioned_users has no foreign key on
		// user_id and no cascade either, and the argument above applies to it
		// with more force: the row holds the erased subject's EMAIL ADDRESS,
		// which is more of them than an edition and a seat status.
		//
		// It also has to go for the mailbox to work again. The claim is the
		// unique key provisioning inserts against, so a surviving row makes
		// CreateOrResolveUserForMailbox take its conflict branch forever:
		// resolveProvisionedMailbox reads a user_id that is neither zero nor a
		// user, and every attempt to provision that mailbox fails from then on.
		// A person who cancels and comes back would be unable to return.
		//
		// Upstream file: re-apply on merge (patch-surface area 4, entitlement
		// synchronization).
		{"user_id", &ProvisionedUser{}},

		// ------------------------------------------------------------------
		// Brazn fork (BRA-1104). Everything below is data upstream leaves
		// behind, and the first four entries are LIVE CREDENTIALS rather than
		// residue.
		//
		// THERE ARE NO FOREIGN KEYS AND NO CASCADES IN THIS SCHEMA - see
		// pkg/routes/api/shared/testing.go, which says so about this exact
		// problem. So a row this list does not name is not tidied up later by
		// anything. It stays for the life of the database.
		//
		// The nine entries are one flat list rather than the "second,
		// package-crossing phase" the ticket sketched, because the premise for
		// that split does not hold: pkg/models already imports pkg/user,
		// pkg/notifications and pkg/files, none of the three imports pkg/models
		// back, and every type below is exported. A delete keyed on one column
		// belongs here whatever package declares the struct - the loop below
		// only needs a table name and a predicate. Only the two file blobs need
		// their own step, and that is because a blob is not a row.
		// ------------------------------------------------------------------

		// A webhook keeps its HMAC Secret, its Basic-Auth password and its
		// TargetURL, AND IT KEEPS FIRING. Nothing else in the package deletes
		// one except the single-id API handler, and project deletion does not
		// delete by project_id either, so an erased person's webhook outlives
		// both them and, often, the project it was attached to.
		//
		// BOTH COLUMNS, and they are not the same set. user_id is only set on a
		// user-level webhook; a webhook this person put on a PROJECT carries
		// created_by_id and a null user_id. Deleting by user_id alone leaves
		// exactly the case that matters most - a webhook on a project that
		// survives them, still posting that project's contents to an address
		// they chose, signed with a secret only they know.
		{"user_id", &Webhook{}},
		{"created_by_id", &Webhook{}},
		// The refresh-token hash, the RAW OIDC id token, the device and the IP.
		// This is a working way back into the account, and leaving it is the
		// difference between "deleted" and "cannot currently sign in".
		{"user_id", &Session{}},
		// All four kinds of user token: password reset, email confirmation,
		// CalDAV authentication and - with a straight face - account deletion.
		// A password-reset token that outlives the account is a credential for
		// an account that is coming back if the mailbox is ever re-provisioned.
		{"user_id", &user.Token{}},
		// The raw TOTP shared secret. It is stored to generate passcodes from,
		// not hashed, so it is exactly as sensitive as it was the day it was
		// enrolled.
		{"user_id", &user.TOTP{}},
		// Comments the person wrote. Upstream removes these only transitively,
		// when the task's project is hard-deleted, so a comment on somebody
		// else's project survives with a dangling author id.
		//
		// DELETED RATHER THAN ANONYMISED, deliberately: the comment body is
		// free text this person wrote, which is their personal data whoever
		// owns the project it sits in. Blanking author_id would keep the text
		// and lose only the attribution, which is the wrong half.
		{"author_id", &TaskComment{}},
		// Membership of projects shared TO this person. Upstream deletes these
		// by project_id when a project goes and by the unshare endpoint, never
		// by user_id, so every project somebody else shared with them keeps a
		// row naming them.
		{"user_id", &ProjectUser{}},
		// Link shares this person created. Cleaned only when the link's own
		// project is hard-deleted; shared_by_id is not a delete predicate
		// anywhere upstream. Each row is a live unauthenticated URL into a
		// project, some of them password-protected with a hash that also stays.
		{"shared_by_id", &LinkSharing{}},
		// Short-lived by design and therefore the least of these, but "expires
		// soon" is a different claim from "is gone", and the row names the user
		// either way.
		{"user_id", &OAuthCode{}},
		// Per-user read state. No secret in it, and it is here because it is
		// keyed on the user and would otherwise be a row about a person who
		// does not exist.
		{"user_id", &TaskUnreadStatus{}},

		// ------------------------------------------------------------------
		// Brazn fork (BRA-1112). Two more categories BRA-1104's list missed,
		// which its own AC1 forbids leaving unnamed.
		// ------------------------------------------------------------------

		// The person's own work log, and the free text they wrote on it.
		// TimeEntry.Delete is keyed on id and nothing else deletes one:
		// not by user, not by task, and NOT BY PROJECT - Project.Delete
		// removes buckets, views, link shares, project users and team
		// projects, and walks straight past time_entries. So the entries
		// outlive both the person and, often, the project they were logged
		// against.
		//
		// DELETED RATHER THAN ANONYMISED, for the reason the task_comments
		// entry above gives: comment is free text this person wrote, and
		// blanking user_id would keep the text and lose only the attribution.
		{"user_id", &TimeEntry{}},
		// Which import this person ran, and when. No name and no free text in
		// it, so it is the least of these - but it is keyed on somebody who no
		// longer exists, and nothing else would ever remove it.
		{"user_id", &migrationStatus{}},

		// ------------------------------------------------------------------
		// RESOLVED (BRA-1117), and not in this list because it is not a
		// delete keyed on one column. Copies of this person embedded in OTHER
		// PEOPLE'S notification rows - which BRA-1112 recorded here as an open
		// gap - are deleted by deleteNotificationsNamingUser, called below.
		//
		// Sebastian ruled that those rows go rather than being scrubbed: the
		// payload is the only place the copy lives, so retaining it is not
		// lawful, and a scrub cannot be shown to have reached every place the
		// person appears inside it. The reasoning is written out in full on
		// deleteNotificationsNamingUser in brazn_notification_erasure.go.
		// ------------------------------------------------------------------

		// ------------------------------------------------------------------
		// RETAINED, WITH THE REASON, so that none of these is a silent
		// omission (BRA-1104 AC1).
		//
		// teams.created_by_id, buckets.created_by_id, labels.created_by_id,
		// tasks.created_by_id, task_attachments.created_by_id and
		// task_relations.created_by_id are all left alone. They are authorship
		// on objects that BELONG TO A SURVIVING PROJECT - one this person was
		// not the last admin of, so getProjectsToDelete transferred it rather
		// than deleting it. Deleting the rows would destroy other people's
		// tasks, labels and boards to erase one attribution, and blanking the
		// column is a schema decision rather than a deletion one. The
		// attribution is a dangling id and not a name, an address or anything
		// else that identifies them once the users row is gone.
		//
		// files rows for TASK ATTACHMENTS are retained for the same reason and
		// are not the two files handled below: an attachment belongs to a task
		// in a project that survives, and removing its blob would break a
		// document for everybody who can still see the task.
		// ------------------------------------------------------------------
	}

	for _, entity := range relatedEntities {
		_, err = s.Where(entity.column+" = ?", u.ID).Delete(entity.model)
		if err != nil {
			return err
		}
	}

	// Brazn fork (BRA-1104). The person's own two files - their avatar and
	// their data export - and the blobs behind them.
	if err = deleteUserOwnFiles(s, u.ID); err != nil {
		return err
	}

	// Notify before deleting the user row, because ShouldNotify will try to
	// look up the user and fail if the row is already gone.
	err = notifications.Notify(u, &user.AccountDeletedNotification{
		User: u,
	}, s)
	if err != nil {
		return err
	}

	// Brazn fork (BRA-1104). Every notification this person ever received.
	//
	// IT RUNS AFTER Notify ABOVE rather than in relatedEntities with the rest,
	// so that nothing written during the erasure itself outlives it. Today that
	// is belt and braces - AccountDeletedNotification.ToDB() returns nil, so
	// notifyDB writes no row for it and the mail goes out inline - but that is a
	// property of one method in another package. A future ToDB() with a body
	// would otherwise make the erasure's own last act a permanent record of the
	// erased person, and nothing here would look wrong. This ordering makes that
	// impossible rather than merely unlikely.
	if _, err = s.Where("notifiable_id = ?", u.ID).
		Delete(&notifications.DatabaseNotification{}); err != nil {
		return err
	}

	// Brazn fork (BRA-1117). The delete above is keyed on the recipient, so it
	// only ever empties this person's own inbox. This one takes the copies of
	// them sitting inside OTHER PEOPLE'S rows, which nothing else reaches.
	//
	// It runs after that delete for the reason that one runs after Notify - so
	// nothing written during the erasure itself outlives it - and second only
	// because the person's own rows are already gone by then and need not be
	// scanned.
	if err = deleteNotificationsNamingUser(s, u.ID); err != nil {
		return err
	}

	_, err = s.Where("id = ?", u.ID).Delete(&user.User{})
	return err
}

// deleteUserOwnFiles removes the two files that belong to the person rather
// than to a project: their avatar, and their data export.
//
// THE IDS ARE READ FROM THE STORED ROW AND NEVER FROM THE ARGUMENT. DeleteUser
// is called with a bare &user.User{ID: n} by the CLI and by every test in this
// package, and only the deletion cron passes a fully loaded struct - so
// u.AvatarFileID and u.ExportFileID are zero on most calls even when the row
// has them. Trusting the argument would delete the export for one caller and
// silently skip it for the others, which is the shape of bug a test written
// against the cron would never see.
//
// The export file is the reason this matters most: it is a complete copy of
// everything the person had, sitting on the storage backend under a numeric
// name, with the users row that pointed at it about to be deleted. Once that
// pointer is gone nothing can ever find the blob to remove it.
func deleteUserOwnFiles(s *xorm.Session, userID int64) error {
	stored := &user.User{}
	has, err := s.Where("id = ?", userID).Get(stored)
	if err != nil {
		return err
	}
	if !has {
		// No row to read ids from. Nothing to do, and not an error: the caller
		// decides what an absent user means, not this helper.
		return nil
	}

	for _, fileID := range []int64{stored.AvatarFileID, stored.ExportFileID} {
		if fileID == 0 {
			continue
		}
		f := &files.File{ID: fileID}
		err := f.Delete(s)
		// A file the users row still points at but which is already gone is a
		// SUCCESS. Nothing keeps the two in step - there is no foreign key -
		// so a dangling id must not be able to fail an erasure.
		if err != nil && !files.IsErrFileDoesNotExist(err) {
			return err
		}
	}

	return nil
}

func ensureProjectAdminUser(s *xorm.Session, l *Project) (hadUsers bool, err error) {
	projectUsers := []*ProjectUser{}
	err = s.Where("project_id = ?", l.ID).Find(&projectUsers)
	if err != nil {
		return
	}

	if len(projectUsers) == 0 {
		return false, nil
	}

	for _, lu := range projectUsers {
		if lu.Permission == PermissionAdmin {
			// Project already has more than one admin, no need to do anything
			return true, nil
		}
	}

	for _, lu := range projectUsers {
		if lu.Permission == PermissionWrite {
			lu.Permission = PermissionAdmin
			_, err = s.Where("id = ?", lu.ID).
				Cols("permission").
				Update(lu)
			return true, err
		}
	}

	firstUser := projectUsers[0]
	firstUser.Permission = PermissionAdmin
	_, err = s.Where("id = ?", firstUser.ID).
		Cols("permission").
		Update(firstUser)
	if err != nil {
		return true, err
	}

	_, err = s.Where("id = ?", l.ID).
		Cols("owner_id").
		Update(&Project{OwnerID: firstUser.UserID})
	if err != nil {
		return true, err
	}

	return true, err
}

func ensureProjectAdminTeam(s *xorm.Session, l *Project) (hadTeams bool, err error) {
	projectTeams := []*TeamProject{}
	err = s.Where("project_id = ?", l.ID).Find(&projectTeams)
	if err != nil {
		return
	}

	if len(projectTeams) == 0 {
		return false, nil
	}

	for _, lu := range projectTeams {
		if lu.Permission == PermissionAdmin {
			// Project already has more than one admin, no need to do anything
			return true, nil
		}
	}

	for _, lu := range projectTeams {
		if lu.Permission == PermissionWrite {
			lu.Permission = PermissionAdmin
			_, err = s.Where("id = ?", lu.ID).
				Cols("permission").
				Update(lu)
			return true, err
		}
	}

	firstTeam := projectTeams[0]
	firstTeam.Permission = PermissionAdmin
	_, err = s.Where("id = ?", firstTeam.ID).
		Cols("permission").
		Update(firstTeam)
	return true, err
}
