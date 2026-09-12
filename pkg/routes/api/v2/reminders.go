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
	"time"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"

	"github.com/danielgtaylor/huma/v2"
)

// Reminder is a reminder which is about nothing in particular: it belongs to one person,
// carries its own words, and creates no task. A reminder about a task is set on the task
// itself, through its reminders field, and is not part of this resource.
type Reminder struct {
	ID       int64     `json:"id" readOnly:"true" doc:"The unique, numeric id of this reminder."`
	Reminder time.Time `json:"reminder" format:"date-time" doc:"The moment the reminder fires, in UTC."`
	Text     string    `json:"text" minLength:"1" maxLength:"1000" doc:"The words the reminder fires with."`
	FiredAt  time.Time `json:"fired_at" readOnly:"true" doc:"When the reminder fired; zero value while it still has to fire."`
	Created  time.Time `json:"created" readOnly:"true" doc:"A timestamp when this reminder was created. You cannot change this value."`
}

func newReminder(tr *models.TaskReminder) *Reminder {
	return &Reminder{
		ID:       tr.ID,
		Reminder: tr.Reminder.UTC(),
		Text:     tr.Text,
		FiredAt:  tr.FiredAt,
		Created:  tr.Created,
	}
}

type reminderListBody struct {
	Body Paginated[*Reminder]
}

func init() { AddRouteRegistrar(RegisterReminderRoutes) }

// RegisterReminderRoutes wires the standalone-reminder resource onto the Huma API.
func RegisterReminderRoutes(api huma.API) {
	tags := []string{"reminders"}

	Register(api, huma.Operation{
		OperationID: "reminders-list",
		Summary:     "List reminders",
		Description: "Returns the reminders the authenticated user set which are about nothing in particular, soonest first. Reminders set on a task are returned with that task, not here. A reminder that has already fired keeps its place in the list until it is deleted.",
		Method:      http.MethodGet,
		Path:        "/reminders",
		Tags:        tags,
	}, remindersList)

	Register(api, huma.Operation{
		OperationID: "reminders-create",
		Summary:     "Create a reminder",
		Description: "Creates a reminder which is about nothing in particular, belonging to the authenticated user. The moment is stored in UTC, no task is created, and nothing is emailed: when the moment passes the server writes a notification for that person. To be reminded about a task, or a period before its due date, set a reminder on the task instead.",
		Method:      http.MethodPost,
		Path:        "/reminders",
		Tags:        tags,
	}, remindersCreate)

	Register(api, huma.Operation{
		OperationID: "reminders-delete",
		Summary:     "Delete a reminder",
		Description: "Deletes one of the authenticated user's own reminders, whether or not it has fired. Only the person the reminder belongs to can delete it, and a reminder set on a task is not reachable here.",
		Method:      http.MethodDelete,
		Path:        "/reminders/{id}",
		Tags:        tags,
	}, remindersDelete)
}

func remindersList(ctx context.Context, in *struct {
	ListParams
}) (*reminderListBody, error) {
	a, err := authFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	s := db.NewSession()
	defer s.Close()

	reminders, total, err := models.GetStandaloneReminders(s, a, in.PerPage, in.PerPage*(in.Page-1))
	if err != nil {
		return nil, translateDomainError(err)
	}

	items := make([]*Reminder, 0, len(reminders))
	for _, r := range reminders {
		items = append(items, newReminder(r))
	}

	return &reminderListBody{Body: NewPaginated(items, total, in.Page, in.PerPage)}, nil
}

func remindersCreate(ctx context.Context, in *struct {
	Body Reminder
}) (*singleBody[Reminder], error) {
	a, err := authFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	s := db.NewSession()
	defer s.Close()

	tr, err := models.CreateStandaloneReminder(s, a, in.Body.Reminder, in.Body.Text)
	if err != nil {
		_ = s.Rollback()
		return nil, translateDomainError(err)
	}

	if err := s.Commit(); err != nil {
		return nil, translateDomainError(err)
	}

	return &singleBody[Reminder]{Body: newReminder(tr)}, nil
}

func remindersDelete(ctx context.Context, in *struct {
	ID int64 `path:"id" doc:"The numeric id of the reminder to delete."`
}) (*emptyBody, error) {
	a, err := authFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	s := db.NewSession()
	defer s.Close()

	if err := models.DeleteStandaloneReminder(s, a, in.ID); err != nil {
		_ = s.Rollback()
		return nil, translateDomainError(err)
	}

	if err := s.Commit(); err != nil {
		return nil, translateDomainError(err)
	}

	return &emptyBody{}, nil
}
