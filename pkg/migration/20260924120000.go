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

package migration

import (
	"encoding/json"
	"time"

	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

// A reminder performs the actions ONE chose for it, and is settled once every one of them is
// done (BRA-1631).
//
// The three columns are additions. A reminder that carries no actions keeps doing what every
// reminder did before, adding a notification to the bell and showing a toast, so nothing
// changes for anybody who set one.
//
// A reminder that has already fired needs one thing more: which of those two are done. Its
// notification was added when it fired. Whether its toast is done - whether the person has
// dealt with the reminder - was held on that notification, and the notifications written
// before this name no reminder. Each such reminder is therefore matched to the notification
// it wrote - its owner's, about the same task or carrying the same words, written within
// minutes of the firing - and that notification is given the reminder as its subject. If the
// person has read it, the reminder is settled at the moment they read it; if not, its toast is
// still to be shown. A fired reminder whose notification cannot be found is settled at the
// moment it fired: nothing records it as waiting, and counting it as waiting would put every
// reminder anybody ever acknowledged back in front of them.
type taskReminders20260924120000 struct {
	ID             int64            `xorm:"bigint autoincr not null unique pk"`
	TaskID         int64            `xorm:"bigint not null INDEX"`
	Reminder       time.Time        `xorm:"DATETIME not null INDEX 'reminder'"`
	Created        time.Time        `xorm:"created not null"`
	RelativePeriod int64            `xorm:"bigint null"`
	RelativeTo     string           `xorm:"varchar(50) null"`
	SubjectKind    string           `xorm:"varchar(20) null"`
	Text           string           `xorm:"'reminder_text' longtext null"`
	CreatedByID    int64            `xorm:"bigint null"`
	FiredAt        time.Time        `xorm:"datetime null"`
	Actions        []map[string]any `xorm:"json null"`
	DoneActions    []string         `xorm:"json null"`
	SettledAt      time.Time        `xorm:"datetime null"`
}

func (taskReminders20260924120000) TableName() string {
	return "task_reminders"
}

// reminderNotifications20260924120000 is the part of a notification this migration reads.
type reminderNotifications20260924120000 struct {
	ID           int64     `xorm:"bigint autoincr not null unique pk"`
	NotifiableID int64     `xorm:"bigint not null"`
	Notification string    `xorm:"'notification'"`
	Name         string    `xorm:"varchar(250) index not null"`
	SubjectID    int64     `xorm:"bigint null"`
	ReadAt       time.Time `xorm:"datetime null"`
	Created      time.Time `xorm:"created not null"`
}

func (reminderNotifications20260924120000) TableName() string {
	return "notifications"
}

// firingWindow is how far apart a reminder's firing and the notification it wrote can be. The
// sweep writes the notification and stamps the firing in the same pass, seconds apart.
const firingWindow = time.Hour

// notificationIsAbout20260924120000 reports whether a notification a reminder sweep wrote is
// about this reminder. A plain function, because the notification's other method takes its
// value and recvcheck holds a type to one kind of receiver.
func notificationIsAbout20260924120000(n *reminderNotifications20260924120000, r *taskReminders20260924120000) bool {
	var written struct {
		Task *struct {
			ID int64 `json:"id"`
		} `json:"task"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(n.Notification), &written); err != nil {
		return false
	}
	if r.TaskID != 0 {
		return written.Task != nil && written.Task.ID == r.TaskID
	}
	return written.Task == nil && written.Text == r.Text
}

func init() {
	migrations = append(migrations, &xormigrate.Migration{
		ID:          "20260924120000",
		Description: "let a reminder carry the actions it performs, and be settled once they are done",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Sync(taskReminders20260924120000{}); err != nil {
				return err
			}

			fired := []*taskReminders20260924120000{}
			if err := tx.Where("fired_at IS NOT NULL AND settled_at IS NULL").Find(&fired); err != nil {
				return err
			}
			if len(fired) == 0 {
				return nil
			}

			written := []*reminderNotifications20260924120000{}
			err := tx.
				Where("name = ? AND (subject_id IS NULL OR subject_id = 0)", "task.reminder").
				Find(&written)
			if err != nil {
				return err
			}
			byOwner := make(map[int64][]*reminderNotifications20260924120000)
			for _, n := range written {
				byOwner[n.NotifiableID] = append(byOwner[n.NotifiableID], n)
			}

			claimed := make(map[int64]bool)
			for _, r := range fired {
				var match *reminderNotifications20260924120000
				var matchGap time.Duration
				for _, n := range byOwner[r.CreatedByID] {
					if claimed[n.ID] || !notificationIsAbout20260924120000(n, r) {
						continue
					}
					gap := n.Created.Sub(r.FiredAt)
					if gap < 0 {
						gap = -gap
					}
					if gap > firingWindow || (match != nil && gap >= matchGap) {
						continue
					}
					match, matchGap = n, gap
				}

				// Every reminder here carries no actions, so it does what every reminder did:
				// a notification, which it added when it fired, and a toast.
				settled := &taskReminders20260924120000{DoneActions: []string{"notification", "toast"}}
				switch {
				case match == nil:
					settled.SettledAt = r.FiredAt
				case match.ReadAt.IsZero():
					settled.DoneActions = []string{"notification"}
				default:
					settled.SettledAt = match.ReadAt
				}

				if match != nil {
					claimed[match.ID] = true
					_, err = tx.ID(match.ID).
						Cols("subject_id").
						Update(&reminderNotifications20260924120000{SubjectID: r.ID})
					if err != nil {
						return err
					}
				}

				_, err = tx.ID(r.ID).
					Cols("done_actions", "settled_at").
					Update(settled)
				if err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *xorm.Engine) error {
			return nil
		},
	})
}
