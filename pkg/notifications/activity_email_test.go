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

package notifications

import (
	"testing"

	"code.vikunja.io/api/pkg/config"
)

func TestIsActivityEmail(t *testing.T) {
	cases := map[string]bool{
		"task.comment":               true,
		"task.assigned":              true,
		"task.deleted":               true,
		"task.mentioned":             true,
		"project.created":            true,
		"team.member.added":          true,
		"task.reminder":              false,
		"task.undone.overdue":        false,
		"user.email.confirm":         false,
		"disabled.mail.notification": false,
	}
	for name, want := range cases {
		if got := isActivityEmail(name); got != want {
			t.Errorf("isActivityEmail(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNotifyMailSkipsActivityWhenDisabled(t *testing.T) {
	config.InitDefaultConfig()
	config.ServiceEnableActivityEmails.Set(false)
	config.MailerEnabled.Set(true)

	n := &activityMailNotification{}
	err := notifyMail(&disabledMailNotifiable{}, n)
	if err != nil {
		t.Fatalf("notifyMail returned error: %v", err)
	}
	if n.called {
		t.Fatal("ToMail must not be called when activity emails are off")
	}
}

func TestNotifyMailSendsActivityWhenEnabled(t *testing.T) {
	config.InitDefaultConfig()
	config.ServiceEnableActivityEmails.Set(true)
	config.MailerEnabled.Set(false) // mailer off: SendMail no-ops without blocking

	n := &activityMailNotification{}
	err := notifyMail(&disabledMailNotifiable{}, n)
	if err != nil {
		t.Fatalf("notifyMail returned error: %v", err)
	}
	if !n.called {
		t.Fatal("ToMail must be called when activity emails are on")
	}
}

type activityMailNotification struct {
	called bool
}

func (n *activityMailNotification) ToMail(string) *Mail {
	n.called = true
	return NewMail().Subject("Comment").Line("body")
}
func (n *activityMailNotification) ToDB() any    { return nil }
func (n *activityMailNotification) Name() string { return "task.comment" }
