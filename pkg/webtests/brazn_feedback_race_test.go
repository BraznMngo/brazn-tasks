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

// The two claim tables (BRA-1414 follow-up) that make Percy Feedback
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
func TestFeedbackRootClaimCannotBeHeldTwice(t *testing.T) {
	newManagedEnv(t)

	s := db.NewSession()
	defer s.Close()

	_, err := s.Insert(&models.FeedbackRootClaim{ID: 1, ProjectID: 10})
	require.NoError(t, err)

	_, err = s.Insert(&models.FeedbackRootClaim{ID: 1, ProjectID: 20})
	require.Error(t, err, "the root claim's id is fixed, and a second claim on it must not be stored")
	assert.True(t, db.IsUniqueConstraintError(err, "brazn_feedback_root"),
		"the refusal must come from the primary key, not from something else: %v", err)
}

// TestFeedbackReporterClaimCannotBeHeldTwice is the same property for one
// reporter's own sub-project.
func TestFeedbackReporterClaimCannotBeHeldTwice(t *testing.T) {
	newManagedEnv(t)

	s := db.NewSession()
	defer s.Close()

	_, err := s.Insert(&models.FeedbackReporterClaim{UserID: 1, ProjectID: 10})
	require.NoError(t, err)

	_, err = s.Insert(&models.FeedbackReporterClaim{UserID: 1, ProjectID: 20})
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
// THE CHEAP CHECK: revert ensureFeedbackSubProject to check-then-insert (no
// claim row) and this test still passes on its own - the constructed state
// already has a sub-project ensureFeedbackSubProject's own read would find -
// but TestFeedbackReporterClaimCannotBeHeldTwice above would fail to compile
// once FeedbackReporterClaim is removed, which is what actually pins the
// mechanism this test exercises the retry loop around.
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
