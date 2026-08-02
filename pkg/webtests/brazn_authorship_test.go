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

package webtests

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type braznCommentAuthor struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type braznCommentItem struct {
	ID     int64              `json:"id"`
	Author braznCommentAuthor `json:"author"`
}

type braznCommentList struct {
	Items []braznCommentItem `json:"items"`
}

// TestBraznCommentAuthorshipIsServerSet pins the property BRA-945's authorship
// marker rests on: who wrote a comment is decided by the authenticated session,
// never by the request body.
//
// This is the difference between the two halves of that ticket. The marker
// Percy applies through the connector is a client-side claim — anything holding
// a token can leave it off, or put someone else's name on it. The fork's own
// attribution cannot be forged, and the two body-borne routes to it fail in
// different ways, so they are pinned separately.
//
// author_id is not a property of the create schema at all: TaskComment.AuthorID
// carries `json:"-"`, and Huma generates struct schemas with
// additionalProperties:false, so a body carrying it is rejected with 422 before
// the handler runs. That strictness is not incidental — it is the same rule that
// forces every v2 update route to accept the read shape rather than the model
// (see the "Body matches the read shape" comment repeated across
// pkg/routes/api/v2), because otherwise a client echoing max_permission back
// would be rejected.
//
// author, by contrast, *is* a property — readOnly, for the response — so a body
// may carry it and encoding/json will populate TaskComment.Author from it. Only
// models.TaskComment.CreateWithTimestamps overwriting both Author and AuthorID
// from the authenticated session stops that from sticking.
//
// The first two cases are differential on purpose: their bodies differ by
// exactly one property, so the 422 can only be author_id and not some unrelated
// validation failure that would make the assertion pass for the wrong reason.
//
// The forged-author case reads the comment back rather than trusting the create
// response, and the distinction is not cosmetic. Create returns an in-memory
// Author that is populated even when the persisted author_id is wrong, so
// asserting on the echo would still pass with the server-side assignment
// deleted. The list resolves Author from the stored author_id (see
// getAllCommentsForTasksWithoutPermissionCheck), which is where the difference
// decides the outcome.
//
// Deleting `tc.AuthorID = tc.Author.ID` from CreateWithTimestamps makes that
// case fail: the comment persists with author_id 0, the list join resolves no
// user, and the author comes back empty.
func TestBraznCommentAuthorshipIsServerSet(t *testing.T) {
	onTask1 := webHandlerTestV2{
		user:     &testuser1,
		basePath: "/api/v2/tasks/1/comments",
		idParam:  "commentid",
		t:        t,
	}
	require.NoError(t, onTask1.ensureEnv())

	// The one property that varies between the first two cases is author_id.
	const probe = `"comment":"BRA-945 authorship probe"`

	t.Run("a body with no authorship field is accepted", func(t *testing.T) {
		rec, err := onTask1.testCreateWithUser(nil, nil, `{`+probe+`}`)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("author_id has no route into the body", func(t *testing.T) {
		// Identical to the accepted body above plus author_id, so a 422 here
		// isolates that property. user6 is a real fixture user with no access to
		// task 1: had the field been honoured it would forge and escalate at once.
		rec, _ := onTask1.testCreateWithUser(nil, nil, `{`+probe+`,"author_id":6}`)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"author_id must not be a writable property; body: %s", rec.Body.String())
	})

	t.Run("a forged author object does not become the author", func(t *testing.T) {
		// A complete enough user object to satisfy the nested schema whether or
		// not Huma validates readOnly properties on a write.
		forged := `{` + probe + `,"author":{"id":6,"name":"user6","username":"user6"}}`

		rec, err := onTask1.testCreateWithUser(nil, nil, forged)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		var created struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
		require.NotZero(t, created.ID, "create must return the id of the new comment")

		// High per_page so the probe cannot drop off the first page as fixtures grow.
		readRec, err := onTask1.testReadAllWithUser(url.Values{"per_page": {"200"}}, nil)
		require.NoError(t, err)

		var list braznCommentList
		require.NoError(t, json.Unmarshal(readRec.Body.Bytes(), &list),
			"comments ReadAll must be a paginated envelope: %s", readRec.Body.String())

		found := false
		for _, item := range list.Items {
			if item.ID != created.ID {
				continue
			}
			found = true
			assert.Equal(t, testuser1.ID, item.Author.ID,
				"the persisted author must be the authenticated user, not the forged one")
			assert.Equal(t, "user1", item.Author.Username)
		}
		require.True(t, found, "the created comment must come back from the list")
	})
}
