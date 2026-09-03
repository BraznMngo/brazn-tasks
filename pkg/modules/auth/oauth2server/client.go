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
	"net"
	"net/url"
	"strings"
)

// forkAllowedSchemes are the native-app redirect schemes this fork accepts in
// addition to upstream's "vikunja-" prefixed schemes. The ONE desktop app
// registers one://oauth/callback with the OS (and still accepts a legacy
// percy://oauth/callback deep link); upstream v2.4.0 accepts only "vikunja-"
// prefixed schemes, and registering a user-visible "vikunja-" scheme from a
// de-branded product would assert a mark we have no licence to use. Relaxing
// validation is therefore the honest fix rather than a workaround.
//
// These are deliberately exact schemes, not "one-" / "percy-" prefixes. The
// rule above is a prefix rule and so admits any vikunja-* scheme; ONE needs
// exactly these two schemes, so this adds exactly those and widens nothing else.
//
// FORK PATCH — RE-VERIFY ON EVERY UPSTREAM UPGRADE. Redirect-URI validation is
// exactly the code an upstream security fix touches. A merge that drops,
// reorders or loosens this changes which redirect targets the authorization
// endpoint will mint a code for. Nothing else about the OAuth flow is modified
// here: PKCE remains required, and the token endpoint still requires the
// presented redirect_uri to equal the one bound to the code.
var forkAllowedSchemes = []string{"one", "percy"}

// ValidateRedirectURI checks that the redirect_uri is a native app scheme —
// either a "vikunja-" prefixed scheme (e.g. vikunja-flutter://callback) or one
// of the fork-allowed schemes (see forkAllowedSchemes above) — or a loopback
// http URL as recommended by RFC 8252 for native apps that cannot register a
// custom scheme. Any address in 127.0.0.0/8, the IPv6 loopback (::1, in any
// notation), and the literal hostname "localhost" are accepted; dangerous
// schemes like javascript:, data:, https://, or non-loopback http:// targets
// are rejected.
func ValidateRedirectURI(redirectURI string) bool {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Scheme == "" {
		return false
	}

	if strings.HasPrefix(u.Scheme, "vikunja-") {
		return true
	}

	for _, allowed := range forkAllowedSchemes {
		if strings.EqualFold(u.Scheme, allowed) {
			return true
		}
	}

	if u.Scheme == "http" {
		host := u.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return true
		}
	}

	return false
}
