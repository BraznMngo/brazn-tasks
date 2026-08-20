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

package user

import (
	"testing"

	"code.vikunja.io/api/pkg/notifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1374: the registration and password-reset emails were still the
// templates the fork was created from -- old brand names, and English for
// every language but the two that happened to carry a translation. These
// tests drive the real notification -> Mail -> RenderMail pipeline (no
// shortcuts through i18n.T directly) for all six languages the site sells
// in, and are built to fail the way the ticket's own "how to know it is
// right" checklist describes: deleting one of the new wording keys makes
// i18n.T return the bare key, which is not a substring of any of the
// per-language text asserted below, so the test fails rather than passing
// on a coincidence.
var shippedLanguages = []string{"en", "de-DE", "es-ES", "fr-FR", "ja-JP", "zh-CN"}

// wantedConfirmWords/wantedResetWords key a fragment of that language's own
// wording for each new field this ticket added, keyed by ISO language
// code. Missing a language here would make TestEmailConfirmAcrossLanguages/
// TestResetPasswordAcrossLanguages skip it rather than silently pass, which
// is why both tests assert len(cases) == len(shippedLanguages) up front.
var wantedConfirmWords = map[string][2]string{
	// [0] eyebrow, [1] footer (the "if you didn't create an account" line)
	"en":    {"Confirm your email", "you can safely ignore this email"},
	"de-DE": {"E-Mail bestätigen", "einfach ignorieren"},
	"es-ES": {"Confirma tu correo", "con tranquilidad"},
	"fr-FR": {"Confirmez votre e-mail", "toute sécurité"},
	"ja-JP": {"メールアドレスの確認", "無視してください"}, //nolint:gosmopolitan // asserting the actual rendered Japanese wording
	"zh-CN": {"确认您的邮箱", "可以放心忽略"},       //nolint:gosmopolitan // asserting the actual rendered Chinese wording
}

var wantedResetWords = map[string][2]string{
	// [0] eyebrow, [1] valid_duration (now carries the 24h + ignore note)
	"en":    {"Password reset", "24 hours"},
	"de-DE": {"Passwort-Reset", "24 Stunden"},
	"es-ES": {"Restablecimiento de contraseña", "24 horas"},
	"fr-FR": {"Réinitialisation du mot de passe", "24 heures"},
	"ja-JP": {"パスワード再設定", "24時間"}, //nolint:gosmopolitan // asserting the actual rendered Japanese wording
	"zh-CN": {"密码重置", "24小时"},     //nolint:gosmopolitan // asserting the actual rendered Chinese wording
}

func newTestUser() *User {
	return &User{Username: "sebastian"}
}

func TestEmailConfirmAcrossLanguages(t *testing.T) {
	require.Len(t, wantedConfirmWords, len(shippedLanguages), "wantedConfirmWords is missing a shipped language")

	for _, lang := range shippedLanguages {
		t.Run(lang, func(t *testing.T) {
			n := &EmailConfirmNotification{User: newTestUser(), IsNew: true, ConfirmToken: "test-token"}
			mailOpts, err := notifications.RenderMail(n.ToMail(lang), lang)
			require.NoError(t, err)

			for _, brand := range []string{"Brazn Tasks", "Percy"} {
				assert.NotContains(t, mailOpts.HTMLMessage, brand, "lang=%s HTML", lang)
				assert.NotContains(t, mailOpts.Message, brand, "lang=%s plain text", lang)
				assert.NotContains(t, mailOpts.Subject, brand, "lang=%s subject", lang)
			}

			want := wantedConfirmWords[lang]
			assert.Contains(t, mailOpts.HTMLMessage, want[0], "lang=%s eyebrow", lang)
			assert.Contains(t, mailOpts.HTMLMessage, want[1], "lang=%s footer", lang)

			// The heading is the subject rendered as HTML -- confirming this
			// isn't the bare i18n key (which happens whenever a translation
			// is missing and falls back silently) also confirms the
			// heading placeholder itself is wired up.
			assert.NotContains(t, mailOpts.Subject, "notifications.email_confirm.")
			assert.Contains(t, mailOpts.HTMLMessage, mailOpts.Subject, "lang=%s heading should equal the subject", lang)
		})
	}

	// The one language whose whole notifications block used to be empty:
	// its subject must be the Spanish wording, not the English fallback
	// every other key in this block silently returned before BRA-1374.
	t.Run("es-ES does not fall back to English", func(t *testing.T) {
		n := &EmailConfirmNotification{User: newTestUser(), IsNew: true, ConfirmToken: "test-token"}
		mailOpts, err := notifications.RenderMail(n.ToMail("es-ES"), "es-ES")
		require.NoError(t, err)
		assert.Equal(t, "Bienvenido a ONE", mailOpts.Subject)
		assert.NotContains(t, mailOpts.HTMLMessage, "Welcome to ONE")
	})
}

func TestResetPasswordAcrossLanguages(t *testing.T) {
	require.Len(t, wantedResetWords, len(shippedLanguages), "wantedResetWords is missing a shipped language")

	for _, lang := range shippedLanguages {
		t.Run(lang, func(t *testing.T) {
			n := &ResetPasswordNotification{User: newTestUser(), Token: &Token{ClearTextToken: "test-token"}}
			mailOpts, err := notifications.RenderMail(n.ToMail(lang), lang)
			require.NoError(t, err)

			for _, brand := range []string{"Brazn Tasks", "Percy"} {
				assert.NotContains(t, mailOpts.HTMLMessage, brand, "lang=%s HTML", lang)
				assert.NotContains(t, mailOpts.Message, brand, "lang=%s plain text", lang)
				assert.NotContains(t, mailOpts.Subject, brand, "lang=%s subject", lang)
			}

			want := wantedResetWords[lang]
			assert.Contains(t, mailOpts.HTMLMessage, want[0], "lang=%s eyebrow", lang)
			assert.Contains(t, mailOpts.HTMLMessage, want[1], "lang=%s valid_duration", lang)

			assert.NotContains(t, mailOpts.Subject, "notifications.password.reset.")
			assert.Contains(t, mailOpts.HTMLMessage, mailOpts.Subject, "lang=%s heading should equal the subject", lang)

			// "have_nice_day" no longer follows this message (BRA-1374) --
			// still used elsewhere, just not appended here any more.
			assert.NotContains(t, mailOpts.Message, "Have a nice day")
		})
	}

	t.Run("es-ES does not fall back to English", func(t *testing.T) {
		n := &ResetPasswordNotification{User: newTestUser(), Token: &Token{ClearTextToken: "test-token"}}
		mailOpts, err := notifications.RenderMail(n.ToMail("es-ES"), "es-ES")
		require.NoError(t, err)
		assert.Equal(t, "Restablece tu contraseña", mailOpts.Subject)
		assert.NotContains(t, mailOpts.HTMLMessage, "Reset your password")
	})
}

// Both messages' plain-text form is generated from the exact same Line()/
// Action() content the HTML form is -- this is the "plain-text twin" the
// ticket asks for, produced by the existing generic plain template rather
// than a second one. Asserting the plain text says the same thing as the
// HTML (modulo markup) is what would catch the two drifting apart, which a
// test that only ever looks at HTMLMessage cannot.
func TestConfirmAndResetPlainTextMatchesHTML(t *testing.T) {
	n := &EmailConfirmNotification{User: newTestUser(), IsNew: true, ConfirmToken: "test-token"}
	mailOpts, err := notifications.RenderMail(n.ToMail("en"), "en")
	require.NoError(t, err)
	for _, want := range []string{"Good to have you here", "Confirm your email address now", "you can safely ignore this email"} {
		assert.Contains(t, mailOpts.Message, want)
	}

	r := &ResetPasswordNotification{User: newTestUser(), Token: &Token{ClearTextToken: "test-token"}}
	resetOpts, err := notifications.RenderMail(r.ToMail("en"), "en")
	require.NoError(t, err)
	for _, want := range []string{"we received a request to reset your ONE password", "This link is valid for 24 hours"} {
		assert.Contains(t, resetOpts.Message, want)
	}
}
