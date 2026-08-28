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
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureWarnings sends log output into a buffer for the length of one test and
// hands back a function that reads what has been written so far.
//
// Warning level and above only, so an unrelated info line cannot make a "did it
// warn" assertion pass.
func captureWarnings(t *testing.T) func() string {
	t.Helper()

	captured := &bytes.Buffer{}
	restore := log.SwapLoggerForTest(slog.New(
		slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelWarn}),
	))
	t.Cleanup(restore)

	return captured.String
}

// setFeedbackOwnerForTest points brazn.feedbackowner somewhere for one test and
// re-arms the warn-once, so the order tests happen to run in cannot decide
// whether this one sees a warning.
func setFeedbackOwnerForTest(t *testing.T, username string) {
	t.Helper()

	previous := config.BraznFeedbackOwner.Get()
	t.Cleanup(func() {
		config.BraznFeedbackOwner.Set(previous)
		rearmFeedbackOwnerWarning()
	})
	config.BraznFeedbackOwner.Set(username)
	rearmFeedbackOwnerWarning()
}

// TestProvisioningSaysSoWhenNoFeedbackOwnerIsConfigured is BRA-1414's sixth
// acceptance criterion: an instance that has been told about no owner must say
// so at warning level, once, naming the setting.
//
// THE SILENCE IS THE DEFECT AND NOT A MISSING NICETY. The feedback capability
// shipped before launch, ran for no customer at all, and reported nothing about
// it, because this exact branch returned an empty owner and said nothing - so
// the six weeks it went unnoticed were six weeks in which no observation could
// have found it.
//
// THE CHEAP CHECK, both directions: delete the warning and the first assertion
// fails on an empty buffer; drop the sync.Once and warn on every call and the
// second fails on a count of two. Neither can be made to pass by a change that
// leaves an operator with nothing to read.
func TestProvisioningSaysSoWhenNoFeedbackOwnerIsConfigured(t *testing.T) {
	db.LoadAndAssertFixtures(t)
	setFeedbackOwnerForTest(t, "")
	warnings := captureWarnings(t)

	s := db.NewSession()
	defer s.Close()

	first, err := ProvisionFeedbackAccess(s, &user.User{ID: 1, Username: "user1"})
	require.NoError(t, err, "an unconfigured instance must still be able to create accounts")
	second, err := ProvisionFeedbackAccess(s, &user.User{ID: 2, Username: "user2"})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	assert.Zero(t, first, "no owner means no project to hand back")
	assert.Zero(t, second)
	db.AssertMissing(t, "projects", map[string]interface{}{"title": FeedbackProjectTitle})

	out := warnings()
	assert.Contains(t, out, "brazn.feedbackowner",
		"the warning has to name the setting, or it tells an operator nothing they can act on")
	assert.Equal(t, 1, strings.Count(out, "brazn.feedbackowner"),
		"two accounts provisioned, one line: a warning per call is noise, which is the silence again")
}

// TestProvisioningSaysSoWhenTheFeedbackOwnerIsNotAnAccountHere is the other way
// the owner can be absent, and it was already the only one that spoke.
//
// It is here so that the pair is read together: the case that had never once
// occurred warned, and the case that was happening on every environment did
// not.
func TestProvisioningSaysSoWhenTheFeedbackOwnerIsNotAnAccountHere(t *testing.T) {
	db.LoadAndAssertFixtures(t)
	setFeedbackOwnerForTest(t, "an-account-this-instance-does-not-have")
	warnings := captureWarnings(t)

	s := db.NewSession()
	defer s.Close()

	projectID, err := ProvisionFeedbackAccess(s, &user.User{ID: 1, Username: "user1"})
	require.NoError(t, err, "an owner nobody has created must not fail the registration it runs inside")
	require.NoError(t, s.Commit())

	assert.Zero(t, projectID)
	db.AssertMissing(t, "projects", map[string]interface{}{"title": FeedbackProjectTitle})
	assert.Contains(t, warnings(), "an-account-this-instance-does-not-have",
		"the warning has to name the account it looked for")
}
