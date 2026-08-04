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

package signup

import (
	"os"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/log"
)

// TestMain initialises the logger before anything else. Every refusal path in
// this package writes a log line, and log.Errorf dereferences a package-level
// logger that is nil until InitLogger runs - so without this the tests that
// matter most here would panic rather than assert.
func TestMain(m *testing.M) {
	log.InitLogger()
	config.InitDefaultConfig()
	os.Exit(m.Run())
}
