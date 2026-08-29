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

	"code.vikunja.io/api/pkg/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupByExactEmail(t *testing.T) {
	db.LoadAndAssertFixtures(t)
	s := db.NewSession()
	defer s.Close()

	t.Run("empty", func(t *testing.T) {
		u, err := LookupByExactEmail(s, "")
		require.NoError(t, err)
		assert.Nil(t, u)
	})

	t.Run("incomplete address", func(t *testing.T) {
		u, err := LookupByExactEmail(s, "stefan@braznmngo")
		require.NoError(t, err)
		assert.Nil(t, u)
	})

	t.Run("unknown full address", func(t *testing.T) {
		u, err := LookupByExactEmail(s, "nobody-exists-here@example.com")
		require.NoError(t, err)
		assert.Nil(t, u)
	})
}
