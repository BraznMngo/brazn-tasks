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
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBraznProvisioningPersonalInboxAlsoProvisionsFeedback closes the gap
// ensurePersonalInbox left: an account adopted through the commercial
// create_personal_inbox operation (one Google sign-in created on the
// development instance before managed mode existed) got an Inbox but never a
// Percy Feedback sub-project, because that operation never called
// ProvisionFeedbackAccess at all - only CreateNewProjectForUser's ordinary
// registration path did.
//
// THE CHEAP CHECK: remove the ProvisionFeedbackAccessRetrying call this
// ticket adds to ProvisionPersonalInbox and this goes red - the subject gets
// an Inbox as before, but storedEntities(ProtectedKindFeedback) stays empty
// and the assertion on the sub-project's existence fails.
func TestBraznProvisioningPersonalInboxAlsoProvisionsFeedback(t *testing.T) {
	env := newManagedEnv(t)
	setConfigForTest(t, config.BraznFeedbackOwner, feedbackOwnerUsername)

	rec := env.provision(personalInboxPayload(managedTopologySubject))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	root := storedEntities(t, models.ProtectedKindFeedback)
	require.Len(t, root, 1, "the shared Percy Feedback root must exist once this subject is provisioned")

	s := dbSessionForTest(t)
	sub := &models.Project{}
	has, err := s.
		Join("INNER", "users_projects", "users_projects.project_id = projects.id").
		Where("projects.parent_project_id = ? AND users_projects.user_id = ?", root[0].ProjectID, 1).
		Get(sub)
	require.NoError(t, err)
	assert.True(t, has, "the subject provisioned via create_personal_inbox must have their own Percy Feedback sub-project")
}

// TestBraznProvisioningPersonalInboxToleratesNoFeedbackOwner is the fail-safe
// direction: an instance with no brazn.feedbackowner configured must still
// provision the Inbox - the whole point ProvisionPersonalInbox's own doc
// comment gives for adopting rather than failing an account it cannot fully
// place.
func TestBraznProvisioningPersonalInboxToleratesNoFeedbackOwner(t *testing.T) {
	env := newManagedEnv(t)

	rec := env.provision(personalInboxPayload(managedTopologySubject))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Len(t, storedEntities(t, models.ProtectedKindInbox), 1)
	assert.Empty(t, storedEntities(t, models.ProtectedKindFeedback),
		"no feedback owner configured means no root to create, not a failed Inbox provisioning")
}
