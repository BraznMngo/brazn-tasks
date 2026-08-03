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
	"encoding/json"
	"strconv"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/files"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

// erasureFixtureTaskID is a fixture task in a project the seeded people do not
// own, so the comment and the unread-status row below are the case upstream
// never reaches: attached to somebody else's project, and therefore not swept up
// by the project cascade.
const erasureFixtureTaskID = 1

// erasureSubject is one seeded person, and the two file ids that have to be
// named individually.
//
// Every other category is asserted by PREDICATE - "no row in this table keyed on
// this person" - which is a stronger statement than "the row I happen to know
// the id of is gone" and catches a fixture row this helper did not write. The
// two files cannot be, because the predicate that finds them is a column on the
// users row and that row is the thing being deleted.
type erasureSubject struct {
	user         *user.User
	avatarFileID int64
	exportFileID int64
}

// seedErasableData writes one row into every table BRA-1104 found surviving a
// user deletion, for one person.
//
// IT SEEDS THE SAME SHAPES FOR THE RETAINED SIBLING TOO, which is the half that
// makes the assertions mean anything. A delete that lost its WHERE clause empties
// the table and satisfies every "the erased person's row is gone" assertion on
// its own; only a surviving sibling row separates a scoped delete from a blanket
// one. This is the same reasoning countProjections in brazn_user_delete_test.go
// is written for.
func seedErasableData(t *testing.T, s *xorm.Session, username string) *erasureSubject {
	t.Helper()

	u := &user.User{
		Username: username,
		Email:    username + "@example.com",
		Password: "irrelevant-to-deletion",
		Issuer:   "local",
	}
	_, err := s.Insert(u)
	require.NoError(t, err)
	require.NotZero(t, u.ID, "the seeded user must have been given an id")

	subject := &erasureSubject{user: u}
	idPart := strconv.FormatInt(u.ID, 10)

	// The two files that belong to the PERSON rather than to a project: their
	// avatar, and their data export. The export is a complete copy of everything
	// they had, which is why item 9 is not a tidiness item.
	avatar := &files.File{Name: "avatar-" + idPart, Size: 10, CreatedByID: u.ID}
	_, err = s.Insert(avatar)
	require.NoError(t, err)
	export := &files.File{Name: "export-" + idPart, Size: 20, CreatedByID: u.ID}
	_, err = s.Insert(export)
	require.NoError(t, err)
	subject.avatarFileID = avatar.ID
	subject.exportFileID = export.ID

	_, err = s.ID(u.ID).Cols("avatar_file_id", "export_file_id").Update(&user.User{
		AvatarFileID: avatar.ID,
		ExportFileID: export.ID,
	})
	require.NoError(t, err)

	// TWO webhooks, because user_id and created_by_id are not the same set and
	// a fix keyed on only one of them would leave the other. The user-level one
	// is deliberately created_by somebody else, so it can only be found through
	// user_id; the project one carries no user_id at all.
	userWebhook := &Webhook{
		UserID:            u.ID,
		TargetURL:         "https://example.com/erasure-user-level-" + idPart,
		Events:            []string{"task.updated"},
		Secret:            "hmac-secret-" + idPart,
		BasicAuthUser:     "basic-user",
		BasicAuthPassword: "basic-password",
		CreatedByID:       2,
	}
	_, err = s.Insert(userWebhook)
	require.NoError(t, err)

	madeWebhook := &Webhook{
		ProjectID:   1,
		TargetURL:   "https://example.com/erasure-project-level-" + idPart,
		Events:      []string{"task.updated"},
		Secret:      "hmac-secret-project-" + idPart,
		CreatedByID: u.ID,
	}
	_, err = s.Insert(madeWebhook)
	require.NoError(t, err)

	// A session: the refresh-token hash and the raw OIDC id token, which together
	// are a working way back into the account.
	//
	// last_active is "not null" and is NOT one of xorm's auto-filled columns, so
	// it has to be set here: a zero time.Time reaches MySQL as
	// '0000-00-00 00:00:00', which strict mode refuses.
	_, err = s.Insert(&Session{
		ID:          "erasure-session-" + idPart,
		UserID:      u.ID,
		TokenHash:   "token-hash-" + idPart,
		DeviceInfo:  "Test Browser",
		IPAddress:   "192.0.2.1",
		LastActive:  testCreatedTime,
		OIDCIDToken: "raw-oidc-id-token-" + idPart,
	})
	require.NoError(t, err)

	_, err = s.Insert(&user.Token{
		UserID: u.ID,
		Token:  "password-reset-token-hash-" + idPart,
		Kind:   user.TokenPasswordReset,
	})
	require.NoError(t, err)

	_, err = s.Insert(&user.TOTP{
		UserID:  u.ID,
		Secret:  "JBSWY3DPEHPK3PX" + idPart,
		Enabled: true,
		URL:     "otpauth://totp/Test:" + username,
	})
	require.NoError(t, err)

	// Inserted directly rather than through Notify: AfterInsert returns early
	// when the in-memory notification and notifiable are nil, and events are
	// faked by TestMain, so this writes the row and sends nothing.
	_, err = s.Insert(&notifications.DatabaseNotification{
		NotifiableID: u.ID,
		Notification: json.RawMessage(`{"seeded":"erasure"}`),
		Name:         "test.notification",
	})
	require.NoError(t, err)

	// A comment on a task in a project somebody else owns, which is the case
	// upstream never reaches: it removes comments only when the task's project
	// is hard-deleted.
	_, err = s.Insert(&TaskComment{
		Comment:  "seeded by the erasure test",
		AuthorID: u.ID,
		TaskID:   erasureFixtureTaskID,
	})
	require.NoError(t, err)

	// Membership of a project shared TO them. Upstream deletes these by
	// project_id and by the unshare endpoint, never by user_id.
	_, err = s.Insert(&ProjectUser{UserID: u.ID, ProjectID: 3, Permission: PermissionRead})
	require.NoError(t, err)

	// A link share they created on somebody else's project: a live
	// unauthenticated URL that outlives them.
	_, err = s.Insert(&LinkSharing{
		Hash:        "erasurehash" + idPart,
		ProjectID:   1,
		Permission:  PermissionRead,
		SharingType: SharingTypeWithoutPassword,
		SharedByID:  u.ID,
	})
	require.NoError(t, err)

	_, err = s.Insert(&OAuthCode{
		UserID:              u.ID,
		Code:                "oauth-code-" + idPart,
		ExpiresAt:           testCreatedTime,
		ClientID:            "test-client",
		RedirectURI:         "https://example.com/callback",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
	})
	require.NoError(t, err)

	_, err = s.Insert(&TaskUnreadStatus{TaskID: erasureFixtureTaskID, UserID: u.ID})
	require.NoError(t, err)

	return subject
}

// TestDeleteUserErasesEveryCategoryOfTheirData is BRA-1104: a deletion that
// leaves a firing webhook, a valid refresh token, a password-reset token and a
// raw TOTP secret behind is not an erasure, and Sebastian's Case 10 says the
// person "is deleted".
//
// EVERY ASSERTION READS THE DATABASE BACK. None of them observes that a delete
// function was called, because the state this ticket fixes is one where the call
// happens and the row stays - an assertion on the call passes against exactly the
// broken code.
//
// Deleting any one entry from relatedEntities in user_delete.go, or the files or
// notifications steps that follow it, makes this test fail on that category's
// AssertMissing and on nothing else. That is the check the brief asks for and it
// is reasoned rather than run: nothing in this repository may be executed on the
// development host, so CI is the only verifier.
func TestDeleteUserErasesEveryCategoryOfTheirData(t *testing.T) {
	// THIS TEST MUST NOT RUN UNDER notifications.Fake(), and undoing it here is
	// required rather than tidy. Fake() sets process-global state and files that
	// sort before this one call it, so without this line the flag arrives already
	// set. Notify's FIRST line short-circuits on it and returns nil, which skips
	// ShouldNotify, notifyDB and the mail entirely - so a faked run cannot show
	// that the notifications delete happens after a real notification, and it is
	// blind to the ErrUserDoesNotExist path BRA-1103 depends on.
	//
	// No mail leaves the process regardless: user.InitTests calls mail.Fake() in
	// TestMain, so notifyMail records instead of sending.
	notifications.Unfake()
	t.Cleanup(notifications.Unfake)

	db.LoadAndAssertFixtures(t)

	s := db.NewSession()
	defer s.Close()

	erased := seedErasableData(t, s, "erasure-subject")
	retained := seedErasableData(t, s, "erasure-sibling")
	require.NoError(t, s.Commit())

	// Re-read the person the way the deletion cron does, rather than passing a
	// bare &user.User{ID: n}. The cron finds full rows, so Email is populated on
	// the value DeleteUser is really given in production; a test that passed only
	// an id would exercise a gentler path than the one that ships - Notify routes
	// the account-deleted mail through u.Email.
	deleteSession := db.NewSession()
	defer deleteSession.Close()

	stored := &user.User{}
	has, err := deleteSession.Where("id = ?", erased.user.ID).Get(stored)
	require.NoError(t, err)
	require.True(t, has, "the seeded subject must exist before the erasure")
	require.NotEmpty(t, stored.Email, "the loaded subject must carry their mailbox, as the cron's does")

	require.NoError(t, DeleteUser(deleteSession, stored))
	require.NoError(t, deleteSession.Commit())

	db.AssertMissing(t, "users", map[string]interface{}{"id": erased.user.ID})

	// Category by category, keyed on the column the erasure has to use. Both
	// halves every time: gone for the erased person, still there for the sibling.
	categories := []struct {
		what   string
		table  string
		column string
	}{
		{"user-level webhooks, with their HMAC secret and target URL", "webhooks", "user_id"},
		{"webhooks they created on a project", "webhooks", "created_by_id"},
		{"sessions, with the refresh-token hash and the raw OIDC id token", "sessions", "user_id"},
		{"user tokens, including password reset and CalDAV", "user_tokens", "user_id"},
		{"the raw TOTP shared secret", "totp", "user_id"},
		{"notifications they received", "notifications", "notifiable_id"},
		{"comments they wrote on other people's tasks", "task_comments", "author_id"},
		{"membership of projects shared to them", "users_projects", "user_id"},
		{"link shares they created", "link_shares", "shared_by_id"},
		{"OAuth authorization codes", "oauth_codes", "user_id"},
		{"per-task unread state", "task_unread_statuses", "user_id"},
	}

	for _, category := range categories {
		t.Run(category.what, func(t *testing.T) {
			db.AssertMissing(t, category.table, map[string]interface{}{
				category.column: erased.user.ID,
			})
			db.AssertExists(t, category.table, map[string]interface{}{
				category.column: retained.user.ID,
			}, false)
		})
	}

	t.Run("their avatar and their data export, row and blob", func(t *testing.T) {
		db.AssertMissing(t, "files", map[string]interface{}{"id": erased.avatarFileID})
		db.AssertMissing(t, "files", map[string]interface{}{"id": erased.exportFileID})
		db.AssertExists(t, "files", map[string]interface{}{"id": retained.avatarFileID}, false)
		db.AssertExists(t, "files", map[string]interface{}{"id": retained.exportFileID}, false)
	})

	t.Run("the notification written by the erasure itself does not survive it", func(t *testing.T) {
		// The account-deleted notification is sent between the related-entity
		// deletes and the users row delete. AccountDeletedNotification.ToDB()
		// returns nil today so no row is written for it, and the assertion above
		// on notifiable_id already covers that. This case states the property
		// that must hold whatever ToDB() returns later: NOTHING keyed on this
		// person is left in the notifications table when the call returns.
		db.AssertMissing(t, "notifications", map[string]interface{}{
			"notifiable_id": erased.user.ID,
		})
	})
}
