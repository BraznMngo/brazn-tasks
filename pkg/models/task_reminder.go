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
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	// What this reminder does when it falls due, as ONE chose it when setting the reminder
	// (BRA-1631). Null for a reminder set before reminders carried actions, which does what
	// every reminder did then: see defaultReminderActions.
	Actions []ReminderAction `xorm:"json null" json:"actions,omitempty" doc:"What the reminder does when it falls due. Each action is an object whose kind says what, carrying whatever else that kind needs: notification adds a notification to the bell, toast shows the person a toast, and any other kind is stored as given for the client that performs it. A reminder carries each kind at most once. Absent means none were given, and the reminder adds a notification and shows a toast, which is what every reminder did before reminders carried actions. Saving a task without this field keeps what its reminders already do."`
	// The kinds among the reminder's actions that are done since it fell due. The server
	// records the one kind it performs itself, and whoever performs any other kind records
	// that one: see CompleteReminderAction.
	DoneActions []string `xorm:"json null" json:"-"`
	// When the last of the reminder's actions was done. Until then the reminder is waiting
	// for whoever performs the rest, and GetDueReminders lists it.
	SettledAt time.Time `xorm:"datetime null" json:"-"`
}

// ReminderAction is one thing a reminder does when it falls due (BRA-1631): an object
// whose "kind" says what, and whatever else that kind needs, such as the buttons a toast
// shows and what each of them opens.
//
// The set of kinds is open. The server performs the one kind it owns and stores every other
// kind exactly as it was given, for whoever performs it, so a kind added later changes
// neither how a reminder is stored nor how it falls due.
type ReminderAction map[string]any

const (
	// ReminderActionNotification adds a notification to the bell on the task pages. It is the
	// one kind the server performs, at the moment the reminder falls due.
	ReminderActionNotification = "notification"
	// ReminderActionToast shows the person a toast, which a client does. The server knows the
	// kind for one reason: a toast and a notification in the bell tell the person the same
	// thing, so dealing with either one deals with both.
	ReminderActionToast = "toast"
)

// maxReminderActions bounds how many actions one reminder carries.
const maxReminderActions = 8

// maxReminderActionsBytes bounds the stored configuration, so that a reminder cannot be
// used to store arbitrary data.
const maxReminderActionsBytes = 4096

// reminderActionKind is what a kind looks like: a short lowercase name.
var reminderActionKind = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)

// defaultReminderActions is what a reminder set before reminders carried actions does:
// what every reminder did then, a notification in the bell and a toast carrying its words
// (Sebastian, 24 September 2026). Nothing changes for anyone who set one before.
func defaultReminderActions() []ReminderAction {
	return []ReminderAction{
		{"kind": ReminderActionNotification},
		{"kind": ReminderActionToast},
	}
}

// reminderActions is what a reminder does when it falls due.
func reminderActions(r *TaskReminder) []ReminderAction {
	if r.Actions == nil {
		return defaultReminderActions()
	}
	return r.Actions
}

// reminderPerforms reports whether a reminder does the given kind when it falls due.
func reminderPerforms(r *TaskReminder, kind string) bool {
	for _, action := range reminderActions(r) {
		if action["kind"] == kind {
			return true
		}
	}
	return false
}

// reminderPending lists the kinds among a reminder's actions that are not done yet, in the
// order the reminder carries them. The reminder is settled once there are none.
func reminderPending(r *TaskReminder) []string {
	pending := []string{}
	for _, action := range reminderActions(r) {
		kind, _ := action["kind"].(string)
		if !slices.Contains(r.DoneActions, kind) {
			pending = append(pending, kind)
		}
	}
	return pending
}

// recordReminderDone notes that some of a reminder's actions are done, and settles the
// reminder once none is left. A kind the reminder does not carry, or one already done, is
// passed over. It changes only the value it is given, and reports whether it changed it.
func recordReminderDone(r *TaskReminder, now time.Time, kinds ...string) (changed bool) {
	for _, kind := range kinds {
		if !reminderPerforms(r, kind) || slices.Contains(r.DoneActions, kind) {
			continue
		}
		r.DoneActions = append(r.DoneActions, kind)
		changed = true
	}
	if r.SettledAt.IsZero() && len(reminderPending(r)) == 0 {
		r.SettledAt = now
		changed = true
	}
	return changed
}

// recordReminderUndone notes that one of a reminder's actions is no longer done, which leaves
// the reminder waiting again. It changes only the value it is given, and reports whether it
// changed it.
func recordReminderUndone(r *TaskReminder, kind string) (changed bool) {
	at := slices.Index(r.DoneActions, kind)
	if at < 0 {
		return false
	}
	r.DoneActions = slices.Delete(r.DoneActions, at, at+1)
	r.SettledAt = time.Time{}
	return true
}

// validateReminderActions checks a configuration somebody is setting. Nil means none was
// given, which is always valid. Only the shape is checked and never the kind: the set of
// kinds is open, and a kind the server does not perform is somebody else's to read.
func validateReminderActions(actions []ReminderAction) error {
	if actions == nil {
		return nil
	}
	if len(actions) == 0 {
		return ErrReminderActionsInvalid{Reason: "a reminder has to do at least one thing"}
	}
	if len(actions) > maxReminderActions {
		return ErrReminderActionsInvalid{Reason: fmt.Sprintf("a reminder can do at most %d things", maxReminderActions)}
	}
	kinds := make(map[string]bool, len(actions))
	for _, action := range actions {
		kind, isName := action["kind"].(string)
		if !isName || !reminderActionKind.MatchString(kind) || !slices.Contains([]string{"notification", "toast", "run-one"}, kind) {
			return ErrReminderActionsInvalid{Reason: "every action needs a kind, a short lowercase name"}
		}
		// Whether an action is done is recorded against its kind, so a reminder carries each
		// kind once.
		if kinds[kind] {
			return ErrReminderActionsInvalid{Reason: "a reminder does each kind of thing at most once"}
		}
		kinds[kind] = true
	}
	encoded, err := json.Marshal(actions)
	if err != nil || len(encoded) > maxReminderActionsBytes {
		return ErrReminderActionsInvalid{Reason: fmt.Sprintf("the actions must fit in %d bytes", maxReminderActionsBytes)}
	}
	return nil
}

// TableName returns a pretty table name
func (TaskReminder) TableName() string {
	return "task_reminders"
}

// reminderIdentity names a reminder within its task, so that a task's reminders can be
// rewritten without losing what was already known about each one.
//
// A reminder tied to one of the task's dates is named by that tie, because its moment moves
// whenever that date does and naming it by the moment would lose it on every date change.
// One that stands at a fixed moment is named by that moment, which is all it has.
func reminderIdentity(r *TaskReminder) string {
	if r.RelativeTo != "" {
		return "relative:" + string(r.RelativeTo) + ":" + strconv.FormatInt(r.RelativePeriod, 10)
	}
	return "at:" + strconv.FormatInt(r.Reminder.UTC().Unix(), 10)
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
//
// actions is what it does when it falls due (BRA-1631). Giving none leaves it doing what
// every reminder did before reminders carried actions.
func CreateStandaloneReminder(s *xorm.Session, a web.Auth, reminder time.Time, text string, actions ...ReminderAction) (tr *TaskReminder, err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return nil, err
	}

	if reminder.IsZero() {
		return nil, ErrReminderMomentMissing{}
	}

	if err = validateReminderActions(actions); err != nil {
		return nil, err
	}

	tr = &TaskReminder{
		Reminder:    reminder.UTC(),
		Text:        text,
		SubjectKind: ReminderSubjectNone,
		CreatedByID: u.ID,
		Actions:     actions,
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

// DueReminder is a reminder that has fallen due and is not settled yet, as the client that
// performs its actions reads it (BRA-1631). It reaches that client over the standing
// connection the moment it falls due, and through GetDueReminders when the connection was
// down at that moment.
type DueReminder struct {
	ID       int64            `json:"id" doc:"The reminder's id, which recording one of its actions as done names."`
	Reminder time.Time        `json:"reminder" format:"date-time" doc:"The moment the reminder was set for, in UTC."`
	Text     string           `json:"text" doc:"The reminder's own words: its text, or for a reminder about a task the task's title."`
	TaskID   int64            `json:"task_id,omitempty" doc:"The task the reminder is about. Absent for a reminder about nothing."`
	Actions  []ReminderAction `json:"actions" doc:"What the reminder does when it falls due, each action with everything it was set with. A reminder set before reminders carried actions reads as adding a notification and showing a toast."`
	Pending  []string         `json:"pending" doc:"The kinds among its actions that are not done yet. A client performs the ones it knows and records each as done, and the reminder is settled once none is left."`
	FiredAt  time.Time        `json:"fired_at" format:"date-time" doc:"When the reminder fell due on the server."`
}

// dueReminderOf describes one reminder for the client, with the words it fires with.
func dueReminderOf(r *TaskReminder, words string) *DueReminder {
	return &DueReminder{
		ID:       r.ID,
		Reminder: r.Reminder.UTC(),
		Text:     words,
		TaskID:   r.TaskID,
		Actions:  reminderActions(r),
		Pending:  reminderPending(r),
		FiredAt:  r.FiredAt.UTC(),
	}
}

// unsettledRemindersOf names the reminders one person is still owed something for: fallen
// due, and not every action they carry done.
func unsettledRemindersOf(userID int64) builder.Cond {
	return builder.And(
		builder.Eq{"created_by_id": userID},
		builder.NotNull{"fired_at"},
	)
}

// GetDueReminders returns this person's reminders that have fallen due and are not settled
// yet, the earliest first (BRA-1631). It is how a client catches up on what fell due while
// its standing connection was down, whatever those reminders do: one that adds nothing to
// the bell is here all the same.
func GetDueReminders(s *xorm.Session, a web.Auth, limit, start int) (due []*DueReminder, total int64, err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return nil, 0, err
	}

	reminders := []*TaskReminder{}
	err = s.
		Where(unsettledRemindersOf(u.ID)).
		OrderBy("fired_at ASC, id ASC").
		Limit(limit, start).
		Find(&reminders)
	if err != nil {
		return nil, 0, err
	}

	total, err = s.Where(unsettledRemindersOf(u.ID)).Count(&TaskReminder{})
	if err != nil {
		return nil, 0, err
	}

	taskIDs := []int64{}
	for _, r := range reminders {
		if r.SubjectKind != ReminderSubjectNone {
			taskIDs = append(taskIDs, r.TaskID)
		}
	}
	tasks := make(map[int64]*Task)
	if len(taskIDs) > 0 {
		err = s.In("id", taskIDs).Find(&tasks)
		if err != nil {
			return nil, 0, err
		}
	}

	due = make([]*DueReminder, 0, len(reminders))
	for _, r := range reminders {
		words := r.Text
		if task, has := tasks[r.TaskID]; has && r.SubjectKind != ReminderSubjectNone {
			words = task.Title
		}
		due = append(due, dueReminderOf(r, words))
	}
	return due, total, nil
}

// CompleteReminderAction records that one of the actions of a reminder which fell due is
// done (BRA-1631): the person dealt with its toast, or ONE acted on a reminder whose action
// was to run it. The reminder is settled once every action it carries is done.
//
// Dealing with a reminder's toast also reads its notification in the bell, so the bell goes
// down by one; reminderToastsFollowTheBell is the same rule from the bell's side.
//
// Recording an action that is already done changes nothing. A reminder that has not fallen
// due, or is somebody else's, does not exist as far as this person is concerned, and a kind
// the reminder does not carry is refused rather than recorded.
func CompleteReminderAction(s *xorm.Session, a web.Auth, id int64, kind string) (err error) {
	u, err := user.GetFromAuth(a)
	if err != nil {
		return err
	}

	r := &TaskReminder{}
	has, err := s.
		Where(builder.And(builder.Eq{"id": id}, builder.Eq{"created_by_id": u.ID}, builder.NotNull{"fired_at"})).
		Get(r)
	if err != nil {
		return err
	}
	if !has {
		return ErrReminderDoesNotExist{ID: id}
	}
	if !reminderPerforms(r, kind) {
		return ErrReminderActionNotCarried{ID: id, Kind: kind}
	}

	if recordReminderDone(r, time.Now().UTC(), kind) {
		_, err = s.ID(r.ID).Cols("done_actions", "settled_at").Update(r)
		if err != nil {
			return err
		}
	}

	if kind != ReminderActionToast {
		return nil
	}
	bell := []*notifications.DatabaseNotification{}
	err = s.
		Where(builder.And(
			builder.Eq{"notifiable_id": u.ID},
			builder.Eq{"name": (&ReminderDueNotification{}).Name()},
			builder.Eq{"subject_id": r.ID},
			builder.IsNull{"read_at"},
		)).
		Find(&bell)
	if err != nil {
		return err
	}
	for _, n := range bell {
		if err = notifications.MarkNotificationAsRead(s, n, true); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	notifications.OnReadChanged(reminderToastsFollowTheBell)
}

// reminderToastsFollowTheBell keeps each of one person's reminders' toasts in step with the
// reminder's notification in the bell, however that notification was marked (BRA-1631).
//
// A toast and a notification in the bell tell the person the same thing. So reading the
// notification deals with the toast, and the reminder is settled if nothing else is left,
// which keeps a reminder somebody read on the task pages from coming back as a toast. And
// marking it unread puts the toast back, so the two never disagree about whether the person
// has dealt with the reminder: BRA-1571 held that to one place, and it still is one.
//
// It reads only the reminders where the two could disagree: those not settled, and those
// whose notification is unread.
func reminderToastsFollowTheBell(s *xorm.Session, userID int64) error {
	bellName := (&ReminderDueNotification{}).Name()
	unread := builder.Select("subject_id").From("notifications").Where(builder.And(
		builder.Eq{"notifiable_id": userID},
		builder.Eq{"name": bellName},
		builder.IsNull{"read_at"},
	))
	reminders := []*TaskReminder{}
	err := s.
		Where(builder.And(
			builder.Eq{"created_by_id": userID},
			builder.NotNull{"fired_at"},
			builder.Or(builder.IsNull{"settled_at"}, builder.In("id", unread)),
		)).
		Find(&reminders)
	if err != nil || len(reminders) == 0 {
		return err
	}

	ids := make([]int64, 0, len(reminders))
	for _, r := range reminders {
		ids = append(ids, r.ID)
	}
	bells := []*notifications.DatabaseNotification{}
	err = s.
		Where(builder.And(builder.Eq{"notifiable_id": userID}, builder.Eq{"name": bellName})).
		In("subject_id", ids).
		Find(&bells)
	if err != nil {
		return err
	}
	hasBell := make(map[int64]bool, len(bells))
	unreadBell := make(map[int64]bool, len(bells))
	for _, n := range bells {
		hasBell[n.SubjectID] = true
		if n.ReadAt.IsZero() {
			unreadBell[n.SubjectID] = true
		}
	}

	now := time.Now().UTC()
	for _, r := range reminders {
		if !hasBell[r.ID] || !reminderPerforms(r, ReminderActionToast) {
			continue
		}
		var changed bool
		if unreadBell[r.ID] {
			changed = recordReminderUndone(r, ReminderActionToast)
		} else {
			changed = recordReminderDone(r, now, ReminderActionToast)
		}
		if !changed {
			continue
		}
		if _, err = s.ID(r.ID).Cols("done_actions", "settled_at").Update(r); err != nil {
			return err
		}
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

// getTasksWithRemindersDueAndTheirUsers returns the one person to tell about each reminder
// that is due and has not fired yet.
//
// "Due" means the moment has passed, not that it falls inside the coming minute, so a
// reminder whose moment went by while nothing was running still fires on the next pass.
// The reminders it returns carry no fired timestamp; stamping them is the caller's job,
// and it belongs after the notification is written.
//
// Every reminder reaches the person it belongs to and nobody else, whether it is about a
// task or about nothing at all. A reminder on a task somebody shares therefore no longer
// tells everybody who can see that task, and it no longer asks whether its person can still
// see the task before telling them. That is why this reads neither the task's people nor
// their permissions, where it once read both.
//
// It takes no recipient filter, and used to. The filter narrowed the people it read to those
// whose email preference was switched on, and nothing narrows them now: firing a reminder
// writes a notification record whatever any mail setting says. The overdue digest still
// filters its own recipients and still passes one to getTaskUsersForTasks, which is why that
// function keeps the argument.
func getTasksWithRemindersDueAndTheirUsers(s *xorm.Session, now time.Time) (reminderNotifications []*ReminderDueNotification, err error) {
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
		ownerIDs = append(ownerIDs, r.CreatedByID)
		if r.SubjectKind != ReminderSubjectNone {
			taskIDs = append(taskIDs, r.TaskID)
		}
	}

	if len(due) == 0 {
		return
	}

	// The tasks are read plainly, by id, with nobody's permission consulted. A reminder
	// belongs to the person who set it and follows them rather than the task: moving the
	// task somewhere they can no longer see must not take their own reminder away.
	tasks := make(map[int64]*Task)
	projects := make(map[int64]*Project)
	if len(taskIDs) > 0 {
		err = s.In("id", taskIDs).Find(&tasks)
		if err != nil {
			return
		}

		projects, err = GetProjectsMapSimpleByTaskIDs(s, taskIDs)
		if err != nil {
			return
		}
	}

	owners, err := user.GetUsersByCond(s, builder.In("id", ownerIDs))
	if err != nil {
		return
	}

	for _, r := range due {
		u, has := owners[r.CreatedByID]
		if !has {
			log.Errorf("[Task Reminder Cron] Reminder %d has nobody to remind, skipping", r.ID)
			continue
		}

		if r.SubjectKind == ReminderSubjectNone {
			reminderNotifications = append(reminderNotifications, &ReminderDueNotification{
				User:         u,
				TaskReminder: r,
				Text:         r.Text,
			})
			continue
		}

		task, hasTask := tasks[r.TaskID]
		if !hasTask {
			log.Errorf("[Task Reminder Cron] Reminder %d is about task %d, which is gone, skipping", r.ID, r.TaskID)
			continue
		}

		reminderNotifications = append(reminderNotifications, &ReminderDueNotification{
			User:         u,
			Task:         task,
			Project:      projects[task.ProjectID],
			TaskReminder: r,
		})
	}

	return
}

// RegisterReminderCron registers a cron function which runs every minute and fires every
// reminder that has come due and has not fired yet.
//
// It is registered whatever the mail configuration says, because a reminder sends no email:
// it performs the actions it was set with, adds a notification to the bell only when one of
// them says so, and is announced to its person's connected clients (BRA-1631).
func RegisterReminderCron() {
	webhookEnabled := config.WebhooksEnabled.GetBool()

	tz := config.GetTimeZone()
	log.Debugf("[Task Reminder Cron] Timezone is %s", tz)

	err := cron.Schedule("* * * * *", func() {
		s := db.NewSession()
		defer s.Close()

		if err := fireDueReminders(s, time.Now(), webhookEnabled); err != nil {
			events.CleanupPending(s)
			log.Errorf("[Task Reminder Cron] Pass failed: %s", err)
			return
		}

		if err := s.Commit(); err != nil {
			events.CleanupPending(s)
			log.Errorf("[Task Reminder Cron] Could not commit: %s", err)
			return
		}

		// What fell due is announced only once it is committed, so a client that hears it and
		// settles it at once finds it fallen due (BRA-1631).
		events.DispatchPending(context.Background(), s)
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
	reminders, err := getTasksWithRemindersDueAndTheirUsers(s, now)
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
		// Only a reminder that adds a notification to the bell writes one (BRA-1631). One whose
		// only action is to run ONE still falls due, and nobody's bell shows it.
		if reminderPerforms(n.TaskReminder, ReminderActionNotification) || n.TaskReminder != nil {
			err = notifications.Notify(n.User, n, s)
			if err != nil {
				log.Errorf("[Task Reminder Cron] Could not notify user %d: %s", n.User.ID, err)
				failed[n.TaskReminder.ID] = true
				continue
			}
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

	// Stamped only now, after every notification for these reminders has been written. A
	// reminder whose notification could not be written keeps no stamp and is picked up
	// again on the next pass. The other order would lose a reminder whenever writing its
	// notification failed.
	//
	// The stamp carries what is already done (BRA-1631): the notification, when the reminder
	// adds one, which settles a reminder that does nothing else. Every reminder left waiting
	// for a client is announced to the person it belongs to, carrying what it does, once this
	// pass is committed, so a client hears it over its standing connection at that moment
	// rather than at its next check.
	firedAt := time.Now().UTC()
	for _, n := range reminders {
		r := n.TaskReminder
		if !notified[r.ID] || failed[r.ID] {
			continue
		}

		r.FiredAt = firedAt
		recordReminderDone(r, firedAt, ReminderActionNotification)
		_, err = s.ID(r.ID).Cols("fired_at", "done_actions", "settled_at").Update(r)
		if err != nil {
			return err
		}

		if !r.SettledAt.IsZero() {
			continue
		}
		words := n.Text
		if n.Task != nil {
			words = n.Task.Title
		}
		events.DispatchOnCommit(s, &ReminderDueEvent{
			UserID:   n.User.ID,
			Reminder: dueReminderOf(r, words),
		})
	}

	return nil
}
