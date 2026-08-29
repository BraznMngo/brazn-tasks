package notifications

import (
	"testing"

	"code.vikunja.io/api/pkg/config"
)

func TestIsActivityEmail(t *testing.T) {
	cases := map[string]bool{
		"task.comment":              true,
		"task.assigned":             true,
		"task.deleted":              true,
		"task.mentioned":            true,
		"project.created":           true,
		"team.member.added":         true,
		"task.reminder":             false,
		"task.undone.overdue":       false,
		"user.email.confirm":        false,
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
