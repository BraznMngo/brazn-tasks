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
	"encoding/json"

	"code.vikunja.io/api/pkg/notifications"

	"xorm.io/xorm"
)

// Brazn fork (BRA-1117). Deleting the notification rows that carry a copy of an
// erased person, wherever those rows live.
//
// Upstream file: this file is fork-only, but the call site in user_delete.go is
// not - re-apply that on merge.

// notificationErasureBatch bounds both halves of the pass below: how many rows
// are held in memory while scanning, and how many ids go into one DELETE.
//
// The DELETE is the binding one. SQLite's SQLITE_MAX_VARIABLE_NUMBER is 999 on
// the older builds and 32766 on newer ones, so one IN list holding every match
// could fail on the backend most installations actually run. 500 is inside
// every limit in the test-api matrix, and this runs once per erased user, so
// the extra round trips cost nothing anybody can measure.
const notificationErasureBatch = 500

// deleteNotificationsNamingUser removes every row of the notifications table
// whose stored payload carries a copy of this user, REGARDLESS OF WHOSE ROW IT
// IS. DeleteUser's other notification delete is keyed on notifiable_id and so
// only ever reaches the person's own inbox; this reaches everybody else's.
//
// WHY THE ROWS GO RATHER THAN BEING SCRUBBED (Sebastian's ruling, BRA-1117,
// after BRA-1112 escalated it). Eight notification types return themselves from
// ToDB(), so notifyDB json.Marshals the whole struct - including every
// *user.User it reaches - onto the RECIPIENT's row, and a user.User serialises
// id, name, username AND email. That payload is the only place those copies
// live, so retaining them is not lawful. Scrubbing them in place is not
// provable: the person can appear at several places inside one payload and
// BRA-1112's investigation could not trace every path. Deleting the row is
// provable, and an erasure we cannot prove is complete is not an erasure. The
// cost - other people lose old notification history about somebody who no
// longer exists - is accepted.
//
// WHY THE PAYLOAD IS DECODED IN GO AND NOT QUERIED IN SQL. A LIKE over the
// serialised JSON fails on all three counts that matter here. It is not
// portable: this fork's test-api matrix covers sqlite, sqlite-in-memory,
// postgres, mysql, mariadb and paradedb, and MySQL normalises a JSON value on
// storage while SQLite keeps the bytes it was given, so no single pattern
// matches both. It is not precise: `"id":6` also matches a task, a project, a
// comment or a team with that id. And it is not even fast, because a
// leading-wildcard LIKE scans the table anyway. Decoding sidesteps all three -
// JSON semantics survive every backend's normalisation, so a semantic
// comparison is dialect-independent by construction. Correctness over
// cleverness: this is one user's erasure on an hourly cron, not a hot path.
//
// WHY IT WALKS THE TREE INSTEAD OF READING NAMED FIELDS. notificationUsers in
// notifications_refresh.go already enumerates "the user fields a stored
// notification renders", and reusing it would be the obvious saving. It is the
// wrong instrument, and it is ALREADY INCOMPLETE: it returns only n.Doer for a
// TaskCommentNotification, while the payload also holds the same person at
// .comment.author, because TaskComment.Author is set on create and the whole
// comment goes into the notification. The nesting is worse than that one case.
// Task carries CreatedBy and Assignees, Project carries Owner, and Team carries
// CreatedBy plus Members - where TeamUser embeds user.User, so every existing
// member of a team is written by name, username and email into the notification
// row of whoever is added next. A field list has to be kept in step with all of
// that by hand, in a package nobody edits when they add a field somewhere else.
// A walk does not.
//
// It also survives an unregistered name. notifications.Lookup returns nothing
// for a name no init() registered - the test fixtures' own test.notification is
// one - so a Lookup-based scan would skip those rows silently rather than
// examine them.
func deleteNotificationsNamingUser(s *xorm.Session, userID int64) error {
	matched, err := findNotificationsNamingUser(s, userID)
	if err != nil {
		return err
	}

	for len(matched) > 0 {
		chunk := matched
		if len(chunk) > notificationErasureBatch {
			chunk = chunk[:notificationErasureBatch]
		}

		_, err = s.In("id", chunk).Delete(&notifications.DatabaseNotification{})
		if err != nil {
			return err
		}

		matched = matched[len(chunk):]
	}

	return nil
}

// findNotificationsNamingUser collects the ids to delete in a pass of its own,
// before anything is deleted. Paging by "id > last" is only stable while the
// rows it pages over are not moving, so scanning and deleting in one loop would
// step over rows as the offsets shifted under it.
func findNotificationsNamingUser(s *xorm.Session, userID int64) ([]int64, error) {
	var matched []int64
	var lastID int64

	for {
		batch := []*notifications.DatabaseNotification{}
		findErr := s.Where("id > ?", lastID).
			OrderBy("id ASC").
			Limit(notificationErasureBatch).
			Find(&batch)
		if findErr != nil {
			return nil, findErr
		}

		if len(batch) == 0 {
			return matched, nil
		}

		for _, row := range batch {
			lastID = row.ID

			payload, decodeErr := decodeNotificationPayload(row.Notification)
			if decodeErr != nil {
				// A payload that cannot be read is not evidence that it holds
				// nobody. Failing the erasure makes that visible; treating it as
				// a miss would make an incomplete erasure report success.
				return nil, decodeErr
			}

			if payloadNamesUser(payload, userID) {
				matched = append(matched, row.ID)
			}
		}
	}
}

// decodeNotificationPayload returns the stored payload as a generic JSON tree.
//
// The marshal-then-unmarshal is not a detour: it is the same round trip
// refreshNotificationUsers already makes in production on every backend this
// fork ships on, which is what makes it safe to rely on here without
// introducing any new xorm behaviour of our own. It also fixes the number
// representation - a plain json.Unmarshal into interface{} always yields
// float64 - so the comparison below does not have to guess what the driver
// produced.
func decodeNotificationPayload(stored interface{}) (interface{}, error) {
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}

	var payload interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	// One unwrap, for the case where the column arrives as the raw JSON text
	// rather than as an already-decoded tree. Without it that row would decode
	// to a bare Go string, match nothing, and be reported clean - a silent miss
	// rather than an error, and the one failure mode here that could differ
	// between backends.
	if text, isText := payload.(string); isText {
		// Asked as "is this text JSON" rather than by unmarshalling and
		// treating the failure as a no: a payload that is genuinely just a
		// string holds no object and therefore no user, which is an answer and
		// not an error, and the two must not be written so that they look the
		// same to the next reader.
		if !json.Valid([]byte(text)) {
			return nil, nil
		}

		var inner interface{}
		if err := json.Unmarshal([]byte(text), &inner); err != nil {
			return nil, err
		}
		return inner, nil
	}

	return payload, nil
}

// payloadNamesUser reports whether a serialised user.User with this id appears
// anywhere in the tree, at any depth.
//
// No cycle guard is needed: json.Unmarshal cannot produce one.
func payloadNamesUser(node interface{}, userID int64) bool {
	switch n := node.(type) {
	case map[string]interface{}:
		if objectIsUser(n, userID) {
			return true
		}
		for _, child := range n {
			if payloadNamesUser(child, userID) {
				return true
			}
		}
	case []interface{}:
		for _, child := range n {
			if payloadNamesUser(child, userID) {
				return true
			}
		}
	}

	return false
}

// objectIsUser reports whether this one object is a serialised user.User with
// the given id.
//
// The username key is the discriminator. user.User declares it json:"username"
// with NO omitempty, so a serialised user always carries it - including the
// oldest and thinnest form still in these tables, which refreshNotificationUsers
// records as having stored "only id+username". Nothing else reachable from a
// notification payload carries both a username and an id: ProjectUser and
// TeamMember do, but neither is nested in any of these structs, and Team's
// member list is []*TeamUser, which embeds user.User and therefore serialises
// the real user id inline.
//
// If some future type did carry both, the error would be one extra row deleted,
// which is the safe direction for an erasure.
func objectIsUser(obj map[string]interface{}, userID int64) bool {
	if _, hasUsername := obj["username"]; !hasUsername {
		return false
	}

	id, isNumber := obj["id"].(float64)
	return isNumber && id == float64(userID)
}
