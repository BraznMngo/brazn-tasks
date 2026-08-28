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

package webtests

import (
	"context"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/builder"
)

// The two claim tables (BRA-1414 follow-up) that make Feedback
// provisioning an atomic step, and ProvisionFeedbackAccessRetrying's own
// recovery path when a claim is already taken.
//
// NEITHER TEST BELOW USES TWO REAL GOROUTINES RACING AN INSERT, for the same
// reason TestTheMailboxClaimCannotBeHeldTwice does not: the test database is
// SQLite, which serialises writers, so two concurrent calls would very likely
// just run one after the other and prove nothing about the guarantee. What
// actually makes concurrent callers safe is the unique constraint refusing a
// second claim, so that is what is asserted directly.

// TestFeedbackRootClaimCannotBeHeldTwice pins the constraint
// ensureFeedbackProject relies on: two claims on the one root can never both
// be stored.
//
// THE FIRST CLAIM IS TAKEN BY THE PRODUCT AND NOT BY THIS TEST, deliberately,
// and that is the whole difference between this and the version it replaces.
// That version stored two rows carrying the same explicit ID and asserted the
// refusal came from the PRIMARY KEY - a collision ensureFeedbackProject can
// never have, because it inserts with the id left to autoincrement and relies
// on Marker alone. It also asserted precisely what the migration's own comment
// rules out: MySQL and MariaDB name a duplicate-primary-key violation only
// "for key 'PRIMARY'", so IsUniqueConstraintError's MySQL branch would never
// have matched it. On SQLite, which is what the suite runs on, it passed
// anyway. Provisioning first means the row this collides with is the row
// production writes, carrying the marker production chose.
func TestFeedbackRootClaimCannotBeHeldTwice(t *testing.T) {
	env := newFeedbackEnv(t)
	env.provisionFeedback(&testuser1)

	s := db.NewSession()
	defer s.Close()

	// The same fixed marker every caller uses - a second concurrent
	// ensureFeedbackProject that also read "no root yet" builds exactly this
	// row. Written as a literal rather than read back from the row above, so a
	// marker the product stopped setting cannot make this agree with itself.
	_, err := s.Insert(&models.FeedbackRootClaim{Marker: 1, ProjectID: 20})
	require.Error(t, err, "there is one root for the instance, and a second claim on it must not be stored")
	assert.True(t, db.IsUniqueConstraintError(err, "brazn_feedback_root"),
		"the refusal must come from the unique marker, not from something else: %v", err)
}

// TestFeedbackReporterClaimCannotBeHeldTwice is the same property for one
// reporter's own sub-project, and it takes its shape from the root test above
// for the same reason.
//
// THE FIRST CLAIM IS TAKEN BY THE PRODUCT. The version this replaces stored
// both rows itself, so it exercised the unique index and nothing else: deleting
// ensureFeedbackSubProject's own claim insert left the whole suite green, which
// made the reporter half of the race fix pinned by nothing at all. The comment
// on TestProvisionFeedbackAccessRetryingRecoversFromAConflict claimed a
// compile-time backstop here, and that was wrong too - it only bites if the
// FeedbackReporterClaim TYPE is deleted, which removing the call does not do.
//
// Provisioning first means the row this collides with is the row
// ensureFeedbackSubProject wrote, so deleting that insert makes this fail on
// the collision that never comes.
func TestFeedbackReporterClaimCannotBeHeldTwice(t *testing.T) {
	env := newFeedbackEnv(t)
	env.provisionFeedback(&testuser1)

	s := db.NewSession()
	defer s.Close()

	// What a second concurrent call for this same reporter builds, having also
	// read "no sub-project yet". The user id is the literal the product would
	// have written for testuser1, not a value read back out of the claim above,
	// so a product that stopped keying the claim on the reporter cannot make
	// this agree with itself.
	_, err := s.Insert(&models.FeedbackReporterClaim{UserID: testuser1.ID, ProjectID: 20})
	require.Error(t, err, "one reporter's claim is keyed by their own user id, and a second must not be stored")
	assert.True(t, db.IsUniqueConstraintError(err, "brazn_feedback_reporters"),
		"the refusal must come from the unique index, not from something else: %v", err)
}

// TestProvisionFeedbackAccessRetryingRecoversFromAConflict constructs the
// state a genuinely concurrent caller would leave behind - a fully committed
// reporter sub-project this call did not create - directly on the tables,
// since two real goroutines cannot be trusted to reproduce it (see the file
// comment above). It then asserts the retrying call resolves to that existing
// project rather than erroring or creating a second one.
//
// THE CHEAP CHECK, AND WHAT IT IS NOT: revert ensureFeedbackSubProject to
// check-then-insert and this test still passes on its own, because the
// constructed state already holds a sub-project its own read would find. So
// this test does not pin the claim, and an earlier version of this comment
// claimed a compile-time backstop that does not exist - removing the claim
// INSERT leaves the FeedbackReporterClaim type in place and every reference to
// it compiling. TestFeedbackReporterClaimCannotBeHeldTwice above is what
// actually pins it, by provisioning through the product first so that deleting
// the insert removes the row this collides with.
func TestProvisionFeedbackAccessRetryingRecoversFromAConflict(t *testing.T) {
	env := newFeedbackEnv(t)
	owner := env.provisioningOwner(t)

	env.provisionFeedback(&testuser6) // a different reporter, just to have a root to hang ours off
	root, err := models.FeedbackProject(dbSessionForTest(t))
	require.NoError(t, err)
	require.NotNil(t, root)

	// Hand-plant testuser1's claim and sub-project directly, simulating a
	// concurrent call that already won by the time
	// ProvisionFeedbackAccessRetrying below is asked.
	s := db.NewSession()
	sub := &models.Project{Title: models.FeedbackProjectTitle, ParentProjectID: models.Ptr(root.ProjectID)}
	require.NoError(t, sub.Create(s, owner))
	member := &models.ProjectUser{ProjectID: sub.ID, Username: testuser1.Username, Permission: models.PermissionWrite}
	require.NoError(t, member.Create(s, owner))
	_, err = s.Insert(&models.FeedbackReporterClaim{UserID: testuser1.ID, ProjectID: sub.ID})
	require.NoError(t, err)
	require.NoError(t, s.Commit())
	s.Close()

	resolved, err := models.ProvisionFeedbackAccessRetrying(context.Background(), &testuser1)
	require.NoError(t, err, "the retry must recover once it sees the already-committed claim")
	assert.Equal(t, sub.ID, resolved, "must resolve to the already-planted sub-project, not create a second one")

	// Exactly the two sub-projects planted in this test, none duplicated by the retry.
	db.AssertCount(t, "projects", builder.Eq{"parent_project_id": root.ProjectID}, 2)
}

// provisioningOwner resolves the account newFeedbackEnv pointed
// brazn.feedbackowner at, the same way feedbackOwner does internally, so a
// test can hand-create a sub-project exactly as ensureFeedbackSubProject
// would.
func (env *managedEnv) provisioningOwner(t *testing.T) *user.User {
	t.Helper()

	owner, err := user.GetUserByUsername(dbSessionForTest(t), feedbackOwnerUsername)
	require.NoError(t, err)
	return owner
}
