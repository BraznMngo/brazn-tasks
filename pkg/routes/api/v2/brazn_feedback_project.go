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

package apiv2

import (
	"context"
	"net/http"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/danielgtaylor/huma/v2"
)

// feedbackProjectBody is what a client needs in order to file into or list
// from the caller's own Feedback sub-project (BRA-1414).
type feedbackProjectBody struct {
	Body struct {
		// ProjectID is null when Feedback is not provisioned on this
		// instance - brazn.managedmode off, brazn.feedbackowner unset, or
		// naming an account that does not exist here. A caller should read
		// that as "the feature does not exist on this instance", not retry
		// it.
		ProjectID *int64 `json:"project_id" doc:"The caller's Feedback sub-project id, or null if the feature is unavailable on this instance."`
	}
}

func init() { AddRouteRegistrar(RegisterFeedbackProjectRoutes) }

// RegisterFeedbackProjectRoutes wires the Feedback lookup.
func RegisterFeedbackProjectRoutes(api huma.API) {
	Register(api, huma.Operation{
		OperationID: "feedback-project",
		Summary:     "Resolve the caller's Feedback sub-project",
		Description: "Returns the id of the project a \"file feedback\" or \"list my feedback\" client should read and write, provisioning it first if this account has none yet. Null when Feedback is not configured on this instance.",
		Method:      http.MethodGet,
		Path:        "/brazn/feedback/project",
		Tags:        []string{"brazn"},
	}, feedbackProject)
}

// feedbackProject resolves the caller's own Feedback sub-project,
// provisioning it on first use so an account created before
// brazn.feedbackowner was configured is not left without one.
//
// UNTIL THIS ROUTE, THE ONLY WAY ANYTHING OUTSIDE pkg/models HAS EVER FOUND
// THIS PROJECT IS ITS TITLE (see ensureFeedbackSubProject's own comment) -
// which happens to be unique per reporter today, but is not a contract: a
// customer project sharing that title would be indistinguishable from it by
// name alone. This route is the first real lookup, and callers should move
// off title-matching onto it.
//
// GATED ON BraznManagedMode LIKE THE ACCOUNT-CREATION PATH, deliberately:
// CreateNewProjectForUser only calls ProvisionFeedbackAccess inside that same
// gate (pkg/models/project.go), so a self-hosted instance that has not turned
// managed mode on stays untouched by this route too, not just by
// registration.
//
// A WRITE BEHIND A GET, ON PURPOSE, NOT AN OVERSIGHT: the first call for a
// reporter provisions their sub-project exactly as CreateNewProjectForUser
// already does unconditionally at signup, for every account regardless of
// any write restriction - this route only makes that same one-time
// provisioning reachable again later (an account whose feedback owner was
// configured after they registered), not a new capability. It is therefore
// exempt from BRA-1087's write-restriction gate for the same reason
// provisioning at signup already was.
//
// USES ProvisionFeedbackAccessRetrying, NOT ProvisionFeedbackAccess DIRECTLY,
// because this route - unlike the registration-time call - is expected to be
// asked for the same account's feedback project more than once, including
// concurrently (two devices, a client retry): see that function's own
// comment for why a plain check-then-insert is not safe for a caller with
// that shape.
func feedbackProject(ctx context.Context, _ *struct{}) (*feedbackProjectBody, error) {
	if !config.BraznManagedMode.GetBool() {
		out := &feedbackProjectBody{}
		return out, nil
	}

	a, err := authFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	u, isUser := a.(*user.User)
	if !isUser {
		return nil, huma.Error403Forbidden("Feedback is available to accounts, not to link shares.")
	}

	projectID, err := models.ProvisionFeedbackAccessRetrying(ctx, u)
	if err != nil {
		return nil, translateDomainError(err)
	}

	out := &feedbackProjectBody{}
	if projectID != 0 {
		out.Body.ProjectID = &projectID
	}
	return out, nil
}
