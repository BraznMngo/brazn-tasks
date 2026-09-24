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

package websocket

// BRA-1631 independent QA: the standing connection, the server's half of story 11.
//
// Written by the QA agent from the ticket: "A reminder falls due while ONE is running. Its toast
// appears within seconds, because the desktop hears it over its standing connection rather than
// at its next check." What the server owes is that a reminder that fell due reaches the connections
// of the person it belongs to, and nobody else's, carrying what the client needs to perform it.
// The event name and the kind are written as literals, because they are the contract with the
// desktop client that subscribes to them.

import (
	"context"
	"testing"

	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A connection of one person's, subscribed to reminder.due or not.
func qaReminderConnection(userID int64, subscribed bool) *Connection {
	subscriptions := map[string]bool{}
	if subscribed {
		subscriptions["reminder.due"] = true
	}
	return &Connection{userID: userID, subscriptions: subscriptions, send: make(chan OutgoingMessage, 4)}
}

// Mutation claim: publishing to event.Reminder's owner by any other identifier than the event's
// UserID, or under another event name, fails the first two assertions.
func TestBRA1631Story11AReminderThatFellDueReachesOnlyItsPersonsSubscribedConnections(t *testing.T) {
	InitHub()
	mine := qaReminderConnection(1, true)
	mineNotListening := qaReminderConnection(1, false)
	theirs := qaReminderConnection(2, true)
	for _, conn := range []*Connection{mine, mineNotListening, theirs} {
		GetHub().Register(conn)
	}

	fellDue := &models.ReminderDueEvent{UserID: 1, Reminder: &models.DueReminder{
		ID:      7,
		Text:    "remind yourself to run the manual ritual x when an invoice comes in",
		Actions: []models.ReminderAction{{"kind": "run-one"}},
		Pending: []string{"run-one"},
	}}
	events.TestListener(t, fellDue, &ReminderDueListener{})

	require.Len(t, mine.send, 1, "the person's subscribed connection hears it")
	message := <-mine.send
	assert.Equal(t, "reminder.due", message.Event)
	reminder, ok := message.Data.(*models.DueReminder)
	require.True(t, ok, "it carries the reminder itself, so the client needs nothing else to act")
	assert.Equal(t, int64(7), reminder.ID)
	assert.Equal(t, []string{"run-one"}, reminder.Pending)
	assert.Empty(t, mineNotListening.send, "a connection that did not subscribe hears nothing")
	assert.Empty(t, theirs.send, "a reminder reaches only the person who set it")
}

// A client can subscribe to reminder.due at all. Without this, every reminder that fell due would
// be refused at the subscription and the client would learn of it only at its next check.
//
// Mutation claim: removing "reminder.due" from validEvents fails the assertion.
func TestBRA1631Story11AClientCanSubscribeToReminders(t *testing.T) {
	hub := NewHub()
	conn := &Connection{
		hub:           hub,
		userID:        1,
		authenticated: true,
		subscriptions: make(map[string]bool),
		send:          make(chan OutgoingMessage, 16),
	}
	hub.Register(conn)

	conn.handleMessage(context.Background(), IncomingMessage{Action: ActionSubscribe, Event: "reminder.due"})

	assert.True(t, conn.IsSubscribed("reminder.due"))
}
