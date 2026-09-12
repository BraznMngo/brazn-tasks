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

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/cron"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/api/pkg/utils"
	"code.vikunja.io/api/pkg/web"

	"xorm.io/builder"
	"xorm.io/xorm"
)

// ReminderRelation represents the date attribute of the task which a period based reminder relates to
type ReminderRelation string

// All valid ReminderRelations
const (
	ReminderRelationDueDate   ReminderRelation = `due_date`
	ReminderRelationStartDate ReminderRelation = `start_date`
	ReminderRelationEndDate   ReminderRelation = `end_date`
)

// ReminderSubject says what a reminder is about.
type ReminderSubject string

// All valid ReminderSubjects
const (
	// ReminderSubjectNone is a reminder about nothing in particular. It carries its own
	// words, belongs to one person, and nothing can complete or delete it out from under it.
	ReminderSubjectNone ReminderSubject = `none`
	// ReminderSubjectTask is a reminder about a task. Its words are the task's title and
	// it reaches everybody who can see that task.
	ReminderSubjectTask ReminderSubject = `task`
)

// TaskReminder holds a reminder on a task.
// If RelativeTo and the assciated date field are defined, then the attribute Reminder will be computed.
// If RelativeTo is missing, than Reminder must be given.
type TaskReminder struct {
	ID int64 `xorm:"bigint autoincr not null unique pk" json:"-"`
	// 0 when SubjectKind is ReminderSubjectNone. The column stays not-null so that
	// making a reminder's subject optional needed no table rebuild on live data.
	TaskID int64 `xorm:"bigint not null INDEX" json:"-"`
	// The absolute time when the user wants to be reminded of the task.
	Reminder time.Time `xorm:"DATETIME not null INDEX 'reminder'" json:"reminder"`
	Created  time.Time `xorm:"created not null" json:"-"`
	// A period in seconds relative to another date argument. Negative values mean the reminder triggers before the date. Default: 0, tiggers when RelativeTo is due.
	RelativePeriod int64 `xorm:"bigint null" json:"relative_period"`
	// The name of the date field to which the relative period refers to.
	RelativeTo ReminderRelation `xorm:"varchar(50) null" json:"relative_to"`
	// What this reminder is about.
	SubjectKind ReminderSubject `xorm:"varchar(20) null" json:"-"`
	// The words this reminder fires with. Only a reminder with no subject carries them;
	// a task reminder takes its words from the task.
	Text string `xorm:"'reminder_text' longtext null" json:"-"`
	// Who is reminded. Only set for a reminder with no subject.
	CreatedByID int64 `xorm:"bigint null" json:"-"`
	// When this reminder fired. Stamped after the notification is written, which is what
	// makes the sweep idempotent: a restart, or a minute nothing was running, cannot fire
	// it twice and cannot lose it.
	FiredAt time.Time `xorm:"datetime null" json:"-"`
}

// TableName returns a pretty table name
func (TaskReminder) TableName() string {
	return "task_reminders"
}

// standaloneRemindersOf names the reminders about nothing which belong to one person.
func standaloneRemindersOf(userID int64) builder.Cond {
	return builder.And(
		builder.Eq{"created_by_id": userID},
		builder.Eq{"subject_kind": string(ReminderSubjectNone)},
	)
}

// CreateStandaloneReminder stores a reminder which is about nothing at all, belonging to
// the person who asked for it. Its moment is stored in UTC and no task is created or
// touched. A link share cannot set one, because there would be nobody to remind.
func CreateStandaloneReminder(s *xorm.Session, a web.Auth, reminder time.Time, text string) (tr *TaskReminder, err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return nil, err
	}

	if reminder.IsZero() {
		return nil, ErrReminderMomentMissing{}
	}

	tr = &TaskReminder{
		Reminder:    reminder.UTC(),
		Text:        text,
		SubjectKind: ReminderSubjectNone,
		CreatedByID: u.ID,
	}

	_, err = s.Insert(tr)
	return tr, err
}

// GetStandaloneReminders returns the reminders about nothing which belong to this person,
// soonest first.
func GetStandaloneReminders(s *xorm.Session, a web.Auth, limit, start int) (reminders []*TaskReminder, total int64, err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return nil, 0, err
	}

	reminders = []*TaskReminder{}
	err = s.
		Where(standaloneRemindersOf(u.ID)).
		OrderBy("reminder ASC").
		Limit(limit, start).
		Find(&reminders)
	if err != nil {
		return nil, 0, err
	}

	total, err = s.Where(standaloneRemindersOf(u.ID)).Count(&TaskReminder{})
	return reminders, total, err
}

// DeleteStandaloneReminder removes a reminder about nothing, fired or not. Only the person
// it belongs to can, and a reminder about a task is not reachable this way: it is removed
// with the task's own reminders.
func DeleteStandaloneReminder(s *xorm.Session, a web.Auth, id int64) (err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return err
	}

	deleted, err := s.
		Where(builder.And(builder.Eq{"id": id}, standaloneRemindersOf(u.ID))).
		Delete(&TaskReminder{})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrReminderDoesNotExist{ID: id}
	}

	return nil
}

type taskUser struct {
	Task *Task      `xorm:"extends"`
	User *user.User `xorm:"extends"`
}

const dbTimeFormat = `2006-01-02 15:04:05`

//nolint:gocyclo
func getTaskUsersForTasks(s *xorm.Session, taskIDs []int64, cond builder.Cond) (taskUsers []*taskUser, err error) {
	if len(taskIDs) == 0 {
		return
	}

	taskUsers = []*taskUser{}
	taskMap := make(map[int64]*Task, len(taskIDs))
	err = s.In("id", taskIDs).Find(&taskMap)
	if err != nil {
		return
	}

	projectIDs := []int64{}
	for _, task := range taskMap {
		projectIDs = append(projectIDs, task.ProjectID)
	}
	projects := make(map[int64]*Project)
	err = s.In("id", projectIDs).Find(&projects)
	if err != nil {
		return
	}

	// user_id -> project_id -> has read access
	userPermissionOnProject := make(map[int64]map[int64]bool)

	seen := make(map[int64]map[int64]struct{})
	appendUser := func(taskID int64, u *user.User) (err error) {
		if u == nil {
			return
		}
		task, hasTask := taskMap[taskID]
		if !hasTask {
			return
		}
		if seen[taskID] == nil {
			seen[taskID] = make(map[int64]struct{})
		}
		if _, exists := seen[taskID][u.ID]; exists {
			return
		}
		seen[taskID][u.ID] = struct{}{}

		userProjects, has := userPermissionOnProject[u.ID]
		if !has {
			userPermissionOnProject[u.ID] = make(map[int64]bool)
			userProjects = userPermissionOnProject[u.ID]
		}
		_, projectExists := userProjects[task.ProjectID]
		if !projectExists {
			p, exists := projects[task.ProjectID]
			if !exists {
				return
			}

			userProjects[task.ProjectID] = p.isOwner(u)

			if !p.isOwner(u) {
				userProjects[task.ProjectID], _, err = p.checkPermission(s, u, PermissionRead, PermissionWrite, PermissionAdmin)
				if err != nil {
					return err
				}
			}
		}

		if !userProjects[task.ProjectID] {
			return
		}

		taskUsers = append(taskUsers, &taskUser{Task: task, User: u})

		return
	}

	type userWithTask struct {
		TaskID    int64
		user.User `xorm:"extends"`
	}

	conditions := []builder.Cond{
		builder.In("tasks.id", taskIDs),
		builder.Eq{"users.status": user.StatusActive},
		taskNotDeletedCond("tasks"),
	}
	if cond != nil {
		conditions = append(conditions, cond)
	}

	creators := []*userWithTask{}
	err = s.Table("tasks").
		Select("DISTINCT tasks.id AS task_id, users.id, users.name, users.username, users.email, users.email_reminders_enabled, users.overdue_tasks_reminders_enabled, users.overdue_tasks_reminders_time, users.language, users.timezone, users.created, users.updated").
		Join("INNER", "users", "tasks.created_by_id = users.id").
		Where(builder.And(conditions...)).
		Find(&creators)
	if err != nil {
		return
	}

	for _, creator := range creators {
		err = appendUser(creator.TaskID, &creator.User)
		if err != nil {
			return
		}
	}

	assigneeConds := []builder.Cond{
		builder.In("task_assignees.task_id", taskIDs),
	}
	if cond != nil {
		assigneeConds = append(assigneeConds, cond)
	}

	assignees := []*TaskAssigneeWithUser{}
	err = s.Table("task_assignees").
		Select("DISTINCT task_assignees.task_id, users.id, users.name, users.username, users.email, users.email_reminders_enabled, users.overdue_tasks_reminders_enabled, users.overdue_tasks_reminders_time, users.language, users.timezone, users.created, users.updated").
		Join("INNER", "users", "task_assignees.user_id = users.id").
		Where(builder.And(assigneeConds...)).
		Find(&assignees)
	if err != nil {
		return
	}

	for i := range assignees {
		err = appendUser(assignees[i].TaskID, &assignees[i].User)
		if err != nil {
			return
		}
	}

	subscriptions, err := GetSubscriptionsForEntities(s, SubscriptionEntityTask, taskIDs)
	if err != nil {
		return nil, err
	}

	subscriberIDs := []int64{}
	for _, subs := range subscriptions {
		for _, sub := range subs {
			subscriberIDs = append(subscriberIDs, sub.UserID)
		}
	}

	if len(subscriberIDs) == 0 {
		return
	}

	subscriberCond := []builder.Cond{
		builder.In("id", subscriberIDs),
	}
	if cond != nil {
		subscriberCond = append(subscriberCond, cond)
	}

	subscribers, err := user.GetUsersByCond(s, builder.And(subscriberCond...))
	if err != nil {
		return nil, err
	}

	for taskID, subs := range subscriptions {
		for _, sub := range subs {
			u, has := subscribers[sub.UserID]
			if !has {
				continue
			}
			err = appendUser(taskID, u)
			if err != nil {
				return
			}
		}
	}

	return
}

// getTasksWithRemindersDueAndTheirUsers returns everybody who has to be told about every
// reminder that is due and has not fired yet.
//
// "Due" means the moment has passed, not that it falls inside the coming minute, so a
// reminder whose moment went by while nothing was running still fires on the next pass.
// The reminders it returns carry no fired timestamp; stamping them is the caller's job,
// and it belongs after the notification is written.
func getTasksWithRemindersDueAndTheirUsers(s *xorm.Session, now time.Time, cond builder.Cond) (reminderNotifications []*ReminderDueNotification, err error) {
	now = utils.GetTimeWithoutNanoSeconds(now)
	reminderNotifications = []*ReminderDueNotification{}

	log.Debugf("[Task Reminder Cron] Looking for reminders due at or before %s to send...", now)

	// The bound is formatted text compared against a stored DATETIME, and the ORM stores
	// every datetime column in UTC, so the bound is formatted in UTC too. It keeps the same
	// 14h of slack the previous query carried, because the filter only has to be generous:
	// the exact comparison below is on absolute instants. There is no lower bound, which is
	// what lets a reminder that came due while nothing was running still fire.
	//
	// A reminder about nothing is not subject to the done-or-deleted filter, because it has
	// nothing that could be done or deleted.
	reminders := []*TaskReminder{}
	err = s.
		// The columns have to be named. Left to itself xorm selects * once a join is
		// present, and tasks carries id, created and created_by_id too, so the task's
		// values would land on the reminder and the wrong rows would be stamped as fired.
		Select("task_reminders.*").
		Join("LEFT", "tasks", "tasks.id = task_reminders.task_id").
		Where("task_reminders.fired_at IS NULL").
		And("task_reminders.reminder < ?", now.UTC().Add(time.Hour*14).Format(dbTimeFormat)).
		And(builder.Or(
			builder.Eq{"task_reminders.subject_kind": string(ReminderSubjectNone)},
			builder.And(
				builder.Eq{"tasks.done": false},
				builder.IsNull{"tasks.deleted_at"},
			),
		)).
		Find(&reminders)
	if err != nil {
		return
	}

	log.Debugf("[Task Reminder Cron] Found %d reminders", len(reminders))

	if len(reminders) == 0 {
		return
	}

	due := make([]*TaskReminder, 0, len(reminders))
	var taskIDs []int64
	var ownerIDs []int64
	for _, r := range reminders {
		if r.Reminder.After(now) {
			continue
		}
		due = append(due, r)
		if r.SubjectKind == ReminderSubjectNone {
			ownerIDs = append(ownerIDs, r.CreatedByID)
			continue
		}
		taskIDs = append(taskIDs, r.TaskID)
	}

	if len(due) == 0 {
		return
	}

	usersPerTask := make(map[int64][]*taskUser)
	projects := make(map[int64]*Project)
	if len(taskIDs) > 0 {
		usersWithReminders, err := getTaskUsersForTasks(s, taskIDs, cond)
		if err != nil {
			return nil, err
		}

		for _, ur := range usersWithReminders {
			usersPerTask[ur.Task.ID] = append(usersPerTask[ur.Task.ID], ur)
		}

		projects, err = GetProjectsMapSimpleByTaskIDs(s, taskIDs)
		if err != nil {
			return nil, err
		}
	}

	owners := make(map[int64]*user.User)
	if len(ownerIDs) > 0 {
		owners, err = user.GetUsersByCond(s, builder.In("id", ownerIDs))
		if err != nil {
			return
		}
	}

	seen := make(map[int64]map[int64]bool)
	for _, r := range due {
		if r.SubjectKind == ReminderSubjectNone {
			u, has := owners[r.CreatedByID]
			if !has {
				log.Errorf("[Task Reminder Cron] Reminder %d is about nothing and has no owner, skipping", r.ID)
				continue
			}
			reminderNotifications = append(reminderNotifications, &ReminderDueNotification{
				User:         u,
				TaskReminder: r,
				Text:         r.Text,
			})
			continue
		}

		for _, u := range usersPerTask[r.TaskID] {
			// This ensures we send each reminder only once to each user
			if seen[r.ID] == nil {
				seen[r.ID] = make(map[int64]bool)
			}
			if _, exists := seen[r.ID][u.User.ID]; exists {
				continue
			}
			seen[r.ID][u.User.ID] = true

			reminderNotifications = append(reminderNotifications, &ReminderDueNotification{
				User:         u.User,
				Task:         u.Task,
				Project:      projects[u.Task.ProjectID],
				TaskReminder: r,
			})
		}
	}

	return
}

// RegisterReminderCron registers a cron function which runs every minute and fires every
// reminder that has come due and has not fired yet.
//
// It is registered whatever the mail configuration says, because a reminder now writes a
// notification record rather than an email, and that record is what the desktop app and the
// notification bell both read.
func RegisterReminderCron() {
	webhookEnabled := config.WebhooksEnabled.GetBool()

	tz := config.GetTimeZone()
	log.Debugf("[Task Reminder Cron] Timezone is %s", tz)

	err := cron.Schedule("* * * * *", func() {
		s := db.NewSession()
		defer s.Close()

		if err := fireDueReminders(s, time.Now(), webhookEnabled); err != nil {
			log.Errorf("[Task Reminder Cron] Pass failed: %s", err)
			return
		}

		if err := s.Commit(); err != nil {
			log.Errorf("[Task Reminder Cron] Could not commit: %s", err)
		}
	})
	if err != nil {
		log.Fatalf("Could not register reminder cron: %s", err)
	}
}

// fireDueReminders is one pass of the sweep: everything the cron does except opening the
// session and committing it.
//
// It is a named function rather than the body of the cron's closure so that a test can run
// a pass, run a second one, and count what the person actually received. Acceptance items
// 4, 5 and 10 of BRA-1571 are all about what happens across two passes, and nothing could
// observe that while this was an anonymous closure inside a scheduler registration.
func fireDueReminders(s *xorm.Session, now time.Time, webhookEnabled bool) error {
	reminders, err := getTasksWithRemindersDueAndTheirUsers(s, now, nil)
	if err != nil {
		return err
	}

	if len(reminders) == 0 {
		return nil
	}

	log.Debugf("[Task Reminder Cron] Sending %d reminders", len(reminders))

	failed := make(map[int64]bool)
	notified := make(map[int64]bool)
	for _, n := range reminders {
		err = notifications.Notify(n.User, n, s)
		if err != nil {
			log.Errorf("[Task Reminder Cron] Could not notify user %d: %s", n.User.ID, err)
			failed[n.TaskReminder.ID] = true
			continue
		}

		if webhookEnabled && n.Task != nil {
			err = events.Dispatch(&TaskReminderFiredEvent{
				Task:     n.Task,
				User:     n.User,
				Project:  n.Project,
				Reminder: n.TaskReminder,
			})
			if err != nil {
				log.Errorf("[Task Reminder Cron] Could not dispatch reminder event for task %d: %s", n.Task.ID, err)
			}
		}

		notified[n.TaskReminder.ID] = true
		log.Debugf("[Task Reminder Cron] Sent reminder %d to user %d", n.TaskReminder.ID, n.User.ID)
	}

	firedIDs := []int64{}
	for id := range notified {
		if failed[id] {
			continue
		}
		firedIDs = append(firedIDs, id)
	}

	// Stamped only now, after every notification for these reminders has been
	// written. A reminder whose notification could not be written keeps no stamp and
	// is picked up again on the next pass. The other order would lose a reminder
	// whenever writing its notification failed.
	if len(firedIDs) > 0 {
		_, err = s.In("id", firedIDs).Cols("fired_at").Update(&TaskReminder{FiredAt: time.Now().UTC()})
		if err != nil {
			return err
		}
	}

	return nil
}
