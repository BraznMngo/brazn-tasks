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

package e2etests

// BRA-1631 independent QA: story 11 over a real socket.
//
// Written by the QA agent from the ticket: "A reminder falls due while ONE is running. Its toast
// appears within seconds, because the desktop hears it over its standing connection rather than
// at its next check." The tests in pkg/models prove that the reminder cron announces a reminder
// once its pass is committed, and the tests in pkg/websocket prove that an announcement is pushed
// to the right connections. Neither proves that the two meet on a running task server. This test
// serves the task server's own routes from a real HTTP server, upgrades real WebSocket
// connections, authenticates them with JWTs built from the fixture users, so that no credentials
// are involved, and lets the reminder cron fire the reminders as the task server schedules it.
// The event name and the fields of each frame are written as literals, because they are the
// contract with the desktop client that subscribes to them.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/cron"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/user"
	ws "code.vikunja.io/api/pkg/websocket"

	cws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sebastiansExample is the reminder for ONE the ticket asks to be used wherever this is covered, in
// his words.
const sebastiansExample = "remind yourself to run the manual ritual x when an invoice comes in"

// qaFrameWithin bounds the wait for anything a standing connection is owed. The reminder cron's
// next pass is at most a minute away, and everything else arrives in milliseconds.
const qaFrameWithin = 90 * time.Second

// qaQuietFor is how long a standing connection must then stay quiet: many times what the listener
// needs to handle anything still waiting its turn.
const qaQuietFor = 3 * time.Second

// qaUser2 is the fixture user who owns none of the reminders testuser1 sets.
var qaUser2 = user.User{
	ID:       2,
	Username: "user2",
	Email:    "user2@example.com",
	Issuer:   "local",
}

// The task server registers the websocket listeners at startup, next to the model's (see
// pkg/initialize). setupE2ETestEnv registers only the model's, so this registers the websocket
// ones with the function the task server calls: once, and before setupE2ETestEnv starts the
// router that reads them.
var registerSocketListenersOnce sync.Once

// qaFrame is one message the task server writes to a standing connection, read the way a client
// reads it.
type qaFrame struct {
	Event   string          `json:"event"`
	Error   string          `json:"error"`
	Action  string          `json:"action"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// qaReminderOnTheWire is the reminder a reminder.due frame carries.
type qaReminderOnTheWire struct {
	ID       int64           `json:"id"`
	Reminder time.Time       `json:"reminder"`
	Text     string          `json:"text"`
	TaskID   int64           `json:"task_id"`
	Actions  json.RawMessage `json:"actions"`
	Pending  []string        `json:"pending"`
	FiredAt  time.Time       `json:"fired_at"`
}

// qaNextFrame is the next thing the task server wrote to a standing connection.
func qaNextFrame(t *testing.T, frames <-chan qaFrame) qaFrame {
	t.Helper()
	select {
	case frame, open := <-frames:
		require.True(t, open, "the standing connection closed")
		return frame
	case <-time.After(qaFrameWithin):
		require.FailNow(t, "nothing arrived on the standing connection within "+qaFrameWithin.String())
		return qaFrame{}
	}
}

// qaReminderIn is the reminder a reminder.due frame carries.
func qaReminderIn(t *testing.T, frame qaFrame) qaReminderOnTheWire {
	t.Helper()
	require.Equal(t, "reminder.due", frame.Event,
		"a frame that is not reminder.due: error %q, action %q, data %s", frame.Error, frame.Action, frame.Data)
	var reminder qaReminderOnTheWire
	require.NoError(t, json.Unmarshal(frame.Data, &reminder))
	return reminder
}

// qaStandingConnection is one client's standing connection: opened, authenticated as the person
// with a JWT built from the fixtures, and subscribed to reminder.due. Everything the task server
// writes to it arrives on the returned channel. It is read continuously, as a client reads it,
// which is also what answers the server's keepalive pings while the test waits for the cron.
func qaStandingConnection(t *testing.T, address string, person *user.User) <-chan qaFrame {
	t.Helper()
	ctx := t.Context()

	conn, resp, err := cws.Dial(ctx, address, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err, "the socket at %s did not upgrade", address)
	t.Cleanup(func() { _ = conn.Close(cws.StatusNormalClosure, "") })

	frames := make(chan qaFrame, 16)
	go func() {
		defer close(frames)
		for {
			var frame qaFrame
			if wsjson.Read(ctx, conn, &frame) != nil {
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	token, err := auth.NewUserJWTAuthtoken(person, "test-session-id")
	require.NoError(t, err)
	require.NoError(t, wsjson.Write(ctx, conn, map[string]string{"action": "auth", "token": token}))
	require.Equal(t, qaFrame{Action: "auth.success", Success: true}, qaNextFrame(t, frames),
		"the task server did not accept %s's token", person.Username)

	// A subscription the server accepts is not answered, so one it refuses is the barrier: once
	// that refusal arrives, the server has handled the subscription to reminder.due sent before
	// it, and had it refused that one, its refusal would have arrived first.
	require.NoError(t, wsjson.Write(ctx, conn, map[string]string{"action": "subscribe", "event": "reminder.due"}))
	require.NoError(t, wsjson.Write(ctx, conn, map[string]string{"action": "subscribe", "event": "qa.not-an-event"}))
	require.Equal(t, qaFrame{Error: "invalid_event", Event: "qa.not-an-event"}, qaNextFrame(t, frames),
		"the task server refused %s's subscription to reminder.due", person.Username)

	return frames
}

// qaTheOnlyRemindersThatFallDue sets a reminder for testuser1 and one for user 2, both a minute in
// the past, and removes every other reminder: the fixtures' reminders are all in the past and none
// of them has fired, so the cron would otherwise fire every one of them in the same pass.
func qaTheOnlyRemindersThatFallDue(t *testing.T) (forONE, theirs *models.TaskReminder) {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	_, err := s.Where("id > 0").Delete(&models.TaskReminder{})
	require.NoError(t, err)

	moment := time.Now().Add(-time.Minute)
	forONE, err = models.CreateStandaloneReminder(s, &testuser1, moment, sebastiansExample,
		models.ReminderAction{"kind": "toast"},
		models.ReminderAction{"kind": "run-one", "instruction": sebastiansExample})
	require.NoError(t, err)
	theirs, err = models.CreateStandaloneReminder(s, &qaUser2, moment, "Somebody else's reminder.")
	require.NoError(t, err)

	require.NoError(t, s.Commit())
	return forONE, theirs
}

// Story 11: a reminder that falls due while its person's client is connected arrives over that
// client's standing connection from the pass that fires it, carrying what the client needs to
// perform it, and reaches nobody else's.
//
// Mutation claims: removing the reminder.due listener from ws.RegisterListeners, or the
// DispatchPending call from the cron's closure, leaves the person's connection with nothing to
// read, and fails at the first wait. Removing reminder.due from the events a client may subscribe
// to fails at the subscription. Pushing a reminder to every connected person fails at the first
// frame or at the last check. Holding a pass's announcements back to the next pass fails at the
// first wait or at the check on when the reminder arrived.
func TestBRA1631Story11AReminderThatFallsDueArrivesOverItsPersonsStandingConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registerSocketListenersOnce.Do(ws.RegisterListeners)
	e, err := setupE2ETestEnv(ctx)
	require.NoError(t, err)
	ws.InitHub()

	server := httptest.NewServer(e)
	defer server.Close()

	// One connection on each version of the API, which both upgrade to the same socket.
	personsConnection := qaStandingConnection(t, server.URL+"/api/v2/ws", &testuser1)
	othersConnection := qaStandingConnection(t, server.URL+"/api/v1/ws", &qaUser2)

	forONE, theirs := qaTheOnlyRemindersThatFallDue(t)

	// The reminder cron, scheduled by the function the task server calls at startup. Its first
	// pass is at the start of the next minute.
	cron.Init()
	defer cron.Stop()
	models.RegisterReminderCron()

	arrived := qaReminderIn(t, qaNextFrame(t, personsConnection))
	arrivedAt := time.Now()
	assert.Equal(t, forONE.ID, arrived.ID, "the reminder that arrived is not the person's")
	assert.Equal(t, sebastiansExample, arrived.Text)
	assert.Zero(t, arrived.TaskID, "a reminder about nothing is about no task")
	assert.WithinDuration(t, forONE.Reminder, arrived.Reminder, time.Second)
	assert.JSONEq(t, `[{"kind":"toast"},{"kind":"run-one","instruction":"`+sebastiansExample+`"}]`,
		string(arrived.Actions), "each action arrives with everything it was set with")
	assert.Equal(t, []string{"toast", "run-one"}, arrived.Pending,
		"the server performs neither action, so both are still to do")
	assert.WithinDuration(t, arrived.FiredAt, arrivedAt, 10*time.Second,
		"it arrived from the pass that fired it rather than from a later one")

	theirsArrived := qaReminderIn(t, qaNextFrame(t, othersConnection))
	assert.Equal(t, theirs.ID, theirsArrived.ID, "the reminder that arrived is not user 2's")
	// Set without actions, it does what every reminder did before reminders carried them: the pass
	// has added its notification to the bell, and its toast is still to show.
	assert.JSONEq(t, `[{"kind":"notification"},{"kind":"toast"}]`, string(theirsArrived.Actions))
	assert.Equal(t, []string{"toast"}, theirsArrived.Pending)

	qaNothingReachedTheWrongPerson(t,
		qaConnected{userID: testuser1.ID, frames: personsConnection},
		qaConnected{userID: qaUser2.ID, frames: othersConnection})
}

// qaConnected is one person's standing connection.
type qaConnected struct {
	userID int64
	frames <-chan qaFrame
}

// qaNothingReachedTheWrongPerson checks, once a pass has delivered each person's own reminder,
// that it wrote nothing to anybody's connection that was not theirs.
//
// The listener handles one announcement at a time, so one made now is handled after the pass's,
// and had the pass pushed a reminder to another person's connection as well, it would have been
// written there before this one. That order holds for the announcements the listener had already
// handled. One the pass made separately for the wrong person could still be waiting its turn and
// be written after it, so every connection must then stay quiet.
func qaNothingReachedTheWrongPerson(t *testing.T, connections ...qaConnected) {
	t.Helper()
	for _, connected := range connections {
		require.NoError(t, events.Dispatch(&models.ReminderDueEvent{
			UserID:   connected.userID,
			Reminder: &models.DueReminder{Text: "Sent after the pass."},
		}))
		next := qaReminderIn(t, qaNextFrame(t, connected.frames))
		assert.Equal(t, "Sent after the pass.", next.Text,
			"user %d's connection was sent a reminder that is not theirs", connected.userID)
	}
	time.Sleep(qaQuietFor)
	for _, connected := range connections {
		select {
		case frame, open := <-connected.frames:
			assert.Fail(t, "a connection was written to, or closed, after the pass was over",
				"user %d: open %t, event %q, data %s", connected.userID, open, frame.Event, frame.Data)
		default:
		}
	}
}
