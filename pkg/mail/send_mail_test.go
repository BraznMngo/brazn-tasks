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

package mail

import (
	"testing"

	"code.vikunja.io/api/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGetMessageSetsMessageID(t *testing.T) {
	config.ServicePublicURL.Set("https://tasks.example.com/")
	config.MailerFromEmail.Set("test@example.com")

	opts := &Opts{
		To:          "recipient@example.com",
		Subject:     "Test",
		Message:     "Hello",
		ContentType: ContentTypePlain,
	}

	m := getMessage(opts)

	msgID := m.GetMessageID()
	assert.NotEmpty(t, msgID)
	assert.Contains(t, msgID, "@tasks.example.com>")
}

// BRA-1374: the sender name was a literal "Brazn Tasks" in getMessage,
// which meant correcting it needed a rebuild. It is config now
// (MailerFromName), so a deployment can change it without one -- this test
// pins both halves of that: the default, and that setting it actually
// changes the assembled From header.
func TestGetMessageFromNameIsConfigured(t *testing.T) {
	config.MailerFromEmail.Set("test@example.com")

	t.Run("defaults to ONE", func(t *testing.T) {
		config.MailerFromName.Set("ONE")
		opts := &Opts{To: "recipient@example.com", Subject: "Test", ContentType: ContentTypePlain}
		getMessage(opts)
		assert.Equal(t, "ONE <test@example.com>", opts.From)
	})

	t.Run("a configured name replaces it without a rebuild", func(t *testing.T) {
		config.MailerFromName.Set("Custom Sender")
		opts := &Opts{To: "recipient@example.com", Subject: "Test", ContentType: ContentTypePlain}
		getMessage(opts)
		assert.Equal(t, "Custom Sender <test@example.com>", opts.From)
		assert.NotContains(t, opts.From, "Brazn Tasks")
		config.MailerFromName.Set("ONE")
	})

	t.Run("an explicit From on the opts is never overridden", func(t *testing.T) {
		opts := &Opts{From: "Someone Else <someone@example.com>", To: "recipient@example.com", Subject: "Test", ContentType: ContentTypePlain}
		getMessage(opts)
		assert.Equal(t, "Someone Else <someone@example.com>", opts.From)
	})
}
