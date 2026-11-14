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

package oauth2server

import (
	"testing"

	"code.vikunja.io/api/pkg/config"

	"github.com/stretchr/testify/assert"
)

func TestValidateRedirectURI(t *testing.T) {
	t.Run("accepts vikunja-flutter scheme", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("vikunja-flutter://callback"))
	})
	t.Run("accepts vikunja-desktop scheme", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("vikunja-desktop://auth"))
	})
	t.Run("accepts the one scheme", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("one://oauth/callback"))
	})
	t.Run("accepts the one scheme case-insensitively", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("ONE://oauth/callback"))
	})
	t.Run("accepts the legacy percy scheme", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("percy://oauth/callback"))
	})
	t.Run("accepts the percy scheme case-insensitively", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("PERCY://oauth/callback"))
	})
	// The fork allowances are exact scheme matches, unlike the vikunja- prefix
	// rule. These guards that distinction: loosening either to a prefix later
	// would silently widen what the authorization endpoint accepts.
	t.Run("rejects one- prefixed schemes", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("one-evil://callback"))
	})
	t.Run("rejects schemes merely starting with one", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("oneevil://callback"))
	})
	t.Run("rejects percy- prefixed schemes", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("percy-evil://callback"))
	})
	t.Run("rejects schemes merely starting with percy", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("percyevil://callback"))
	})
	t.Run("accepts http localhost", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://localhost/callback"))
	})
	t.Run("accepts http localhost with port", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://localhost:8080/callback"))
	})
	t.Run("accepts http 127.0.0.1", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://127.0.0.1:8080/callback"))
	})
	t.Run("accepts other 127.0.0.0/8 loopback addresses", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://127.0.0.2:1234/callback"))
	})
	t.Run("accepts http ipv6 loopback", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://[::1]:8080/callback"))
	})
	t.Run("accepts expanded ipv6 loopback", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://[0:0:0:0:0:0:0:1]:1234/callback"))
	})
	t.Run("accepts localhost case-insensitively", func(t *testing.T) {
		assert.True(t, ValidateRedirectURI("http://LocalHost:8080/callback"))
	})
	t.Run("rejects 0.0.0.0", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("http://0.0.0.0:8080/callback"))
	})
	t.Run("rejects https scheme", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("https://evil.com/callback"))
	})
	t.Run("rejects https localhost", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("https://localhost/callback"))
	})
	t.Run("rejects non-loopback http", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("http://evil.com/callback"))
	})
	t.Run("rejects http with localhost in path", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("http://evil.com/localhost"))
	})
	t.Run("rejects http with localhost subdomain trick", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("http://localhost.evil.com/callback"))
	})
	t.Run("rejects javascript scheme", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("javascript:alert(1)"))
	})
	t.Run("rejects data scheme", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("data:text/html,<script>alert(1)</script>"))
	})
	t.Run("rejects non-vikunja custom scheme", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI("myapp://callback"))
	})
	t.Run("rejects empty URI", func(t *testing.T) {
		assert.False(t, ValidateRedirectURI(""))
	})
}

// The https allowance is an exact string match against a configured list. The
// expectations below are written out as literals, not derived from the listed
// value. Deleting the exact-equality loop in ValidateRedirectURI makes "accepts
// a listed https address" fail; replacing it with strings.HasPrefix makes the
// "another path" and "a query" cases fail.
func TestValidateRedirectURIHTTPSAllowList(t *testing.T) {
	const listed = "https://dev.brazn.one/runtime/v1/web/"
	set := func(t *testing.T, uris []string) {
		config.ServiceOAuthHTTPSRedirectURIs.Set(uris)
		t.Cleanup(func() { config.ServiceOAuthHTTPSRedirectURIs.Set([]string{}) })
	}

	t.Run("accepts a listed https address", func(t *testing.T) {
		set(t, []string{listed})
		assert.True(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web/"))
	})
	t.Run("accepts any one of several listed addresses", func(t *testing.T) {
		set(t, []string{"https://other.example/cb", listed})
		assert.True(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web/"))
	})
	t.Run("rejects another path", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web/evil"))
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/other/"))
	})
	t.Run("rejects the listed address without its trailing slash", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web"))
	})
	t.Run("rejects a query", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web/?next=https://evil.com"))
	})
	t.Run("rejects another host", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("https://evil.com/runtime/v1/web/"))
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one.evil.com/runtime/v1/web/"))
	})
	t.Run("rejects http for a listed https address", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("http://dev.brazn.one/runtime/v1/web/"))
	})
	t.Run("an empty list refuses every https address", func(t *testing.T) {
		set(t, []string{})
		assert.False(t, ValidateRedirectURI("https://dev.brazn.one/runtime/v1/web/"))
	})
	t.Run("a listed https address does not open other https addresses", func(t *testing.T) {
		set(t, []string{listed})
		assert.False(t, ValidateRedirectURI("https://localhost/callback"))
	})
}
