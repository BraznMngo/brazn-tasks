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

	"code.vikunja.io/api/pkg/i18n"
	"code.vikunja.io/api/pkg/mail"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// teamInviteNotification has the shape the rule is about: it is triggered by
// one person and delivered to another, and every visible string is resolved
// through the language it is handed. It sets an Action so the second
// language-dependent path -- the copy-the-URL line RenderMail adds to the HTML
// part -- is exercised alongside the body.
type teamInviteNotification struct {
	doerName string
	teamName string
}

func (n *teamInviteNotification) ToMail(lang string) *Mail {
	return NewMail().
		Subject(i18n.T(lang, "notifications.team.member_added.subject", n.doerName, n.teamName)).
		Greeting(i18n.T(lang, "notifications.greeting", "Recipient")).
		Line(i18n.T(lang, "notifications.team.member_added.message", n.doerName, n.teamName)).
		Action(i18n.T(lang, "notifications.common.actions.open_team"), "https://example.com/teams/1")
}

func (n *teamInviteNotification) ToDB() interface{} { return nil }
func (n *teamInviteNotification) Name() string      { return "test.team.invite" }

// The catalogue's own wording, written out by hand rather than read back
// through i18n, so these assert what a recipient actually sees instead of
// agreeing with whatever the lookup returns. Neither greeting is a substring of
// the other, so Contains and NotContains cannot both be satisfied by one
// language.
const (
	englishGreeting = "Hi Recipient,"
	germanGreeting  = "Hallo Recipient,"

	// The leading run of notifications.common.copy_url in each language, cut
	// before the first character html/template would escape, so the assertion
	// cannot break on HTML escaping rather than on language.
	englishCopyURL = "The button doesn’t work? Copy this link"
	germanCopyURL  = "Der Button funktioniert nicht? Kopiere diesen Link"

	// The person who triggered the invitation. Identical in both directions:
	// the doer is what stays fixed while the recipient changes.
	doerName = "Antje"
)

// TestNotifyUsesTheRecipientLanguage locks BRA-918's rule: a message is written
// in the language of the person receiving it, not the language of whoever
// triggered it.
//
// Both directions are asserted on purpose. A fixture with only one language in
// play proves nothing, because a mail that came out German is equally
// consistent with "the recipient's language was used" and with "the sender's
// language was used" when both happen to be German. Here the notification, the
// doer and the content are one shared fixture and the recipient is the only
// thing that differs between the two subtests, so the language can only be
// tracking the recipient.
//
// Mutation check: the guard is notification.ToMail(notifiable.Lang()) in
// notifyMail, together with SendMail(mail, notifiable.Lang()) on the line after
// it. Replacing notifiable.Lang() with any fixed language fails one of the two
// subtests -- "en" fails the German one, "de-DE" fails the English one -- and a
// server-wide default fails whichever subtest it is not. Neither subtest can be
// satisfied by the other's expectation, because each asserts that the other
// language is absent.
func TestNotifyUsesTheRecipientLanguage(t *testing.T) {
	invite := &teamInviteNotification{doerName: doerName, teamName: "Marketing"}

	t.Run("an English recipient gets English", func(t *testing.T) {
		mail.ResetSent()

		recipient := &testNotifiable{ShouldSendNotification: true, Language: "en"}
		require.NoError(t, Notify(recipient, invite))

		sent := mail.LastSent()
		require.NotNil(t, sent)
		assert.Contains(t, sent.Message, englishGreeting)
		assert.NotContains(t, sent.Message, germanGreeting)
		assert.Contains(t, sent.HTMLMessage, englishCopyURL)
		assert.Contains(t, sent.Message, "Antje")
	})

	t.Run("a German recipient gets German", func(t *testing.T) {
		mail.ResetSent()

		recipient := &testNotifiable{ShouldSendNotification: true, Language: "de-DE"}
		require.NoError(t, Notify(recipient, invite))

		sent := mail.LastSent()
		require.NotNil(t, sent)
		assert.Contains(t, sent.Message, germanGreeting)
		assert.NotContains(t, sent.Message, englishGreeting)
		assert.Contains(t, sent.HTMLMessage, germanCopyURL)
		assert.Contains(t, sent.Message, "Antje")
	})
}

// TestNotifyFallsBackToEnglish covers the other half of the rule: a recipient
// whose language is unset or unrecognised is sent English, and never a raw
// translation key.
//
// Mutation check: the guard is the fallback branch in i18n.T. Remove it and T
// returns the key it was handed, so the body reads "notifications.greeting" --
// which fails the Contains assertion for the English greeting and the
// NotContains assertion for a leaked key independently of each other.
func TestNotifyFallsBackToEnglish(t *testing.T) {
	invite := &teamInviteNotification{doerName: doerName, teamName: "Marketing"}

	cases := []struct {
		name string
		lang string
	}{
		{"a recipient who never chose a language", ""},
		{"a recipient whose language the catalogue does not have", "kl-KL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mail.ResetSent()

			recipient := &testNotifiable{ShouldSendNotification: true, Language: tc.lang}
			require.NoError(t, Notify(recipient, invite))

			sent := mail.LastSent()
			require.NotNil(t, sent)
			assert.Contains(t, sent.Message, englishGreeting)
			assert.NotContains(t, sent.Message, "notifications.")
		})
	}
}
