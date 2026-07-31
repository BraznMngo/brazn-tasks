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

package routes_test

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/modules/keyvalue"
	"code.vikunja.io/api/pkg/routes"
)

const classificationFileName = "route-classification.json"

type classifiedRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Class  string `json:"class"`
}

type classificationFile struct {
	Routes []classifiedRoute `json:"routes"`
}

// isReadOnlyMethod reports whether a method cannot change state by contract.
// Everything else - including echo's catch-all pseudo-method used by the
// CalDAV routes - counts as mutating and must be classified. Unknown or new
// methods therefore fail closed.
func isReadOnlyMethod(method string) bool {
	return method == "GET" || method == "HEAD" || method == "OPTIONS" ||
		method == "PROPFIND" || method == "REPORT"
}

func isValidClass(class string) bool {
	return class == "protected-topology" || class == "access-expanding" || class == "ordinary"
}

// TestMutatingRoutesAreClassified derives the full route table from
// RegisterRoutes - the same call the server makes, never a hand-maintained
// duplicate - and fails when a mutating route (anything that is not a plain
// read) has no entry in route-classification.json, or when that file lists a
// route that no longer exists. This is the BRA-925 guardrail: an upstream
// upgrade cannot add a mutating endpoint without someone classifying it.
func TestMutatingRoutesAreClassified(t *testing.T) {
	config.InitDefaultConfig()

	// Route registration is gated by feature flags. Force-enable everything
	// that registers routes so the classification covers the full surface an
	// operator could expose, regardless of shipped defaults. Plugin routes
	// are the one deliberate exception: they come from external plugin files
	// which do not exist in this repository, so there is nothing to classify.
	for _, flag := range []config.Key{
		config.AuthLdapEnabled,
		config.AuthLocalEnabled,
		config.AuthOpenIDEnabled,
		config.BackgroundsEnabled,
		config.BackgroundsUnsplashEnabled,
		config.BackgroundsUploadEnabled,
		config.MigrationMicrosoftTodoEnable,
		config.MigrationTodoistEnable,
		config.MigrationTrelloEnable,
		config.ServiceEnableCaldav,
		config.ServiceEnableLinkSharing,
		config.ServiceEnableRegistration,
		config.ServiceEnableTaskAttachments,
		config.ServiceEnableTaskComments,
		config.ServiceEnableTotp,
		config.ServiceEnableUserDeletion,
		config.WebhooksEnabled,
	} {
		flag.Set(true)
	}
	config.ServiceTestingtoken.Set("route-classification-harness")

	log.InitLogger()
	keyvalue.InitStorage()

	e := routes.NewEcho()
	routes.RegisterRoutes(e)

	registered := make(map[string]bool)
	for _, route := range e.Router().Routes() {
		if route.Method == "echo_route_not_found" {
			continue
		}
		method := route.Method
		if method == "echo_route_any" {
			method = "ANY"
		}
		if isReadOnlyMethod(method) {
			continue
		}
		registered[method+" "+route.Path] = true
	}

	raw, err := os.ReadFile(classificationFileName)
	if err != nil {
		t.Fatalf("could not read %s: %v", classificationFileName, err)
	}
	var file classificationFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("could not parse %s: %v", classificationFileName, err)
	}

	classified := make(map[string]bool, len(file.Routes))
	for i, entry := range file.Routes {
		key := entry.Method + " " + entry.Path
		if !isValidClass(entry.Class) {
			t.Errorf("%s: entry %q has invalid class %q, must be protected-topology, access-expanding or ordinary",
				classificationFileName, key, entry.Class)
		}
		if classified[key] {
			t.Errorf("%s: duplicate entry %q", classificationFileName, key)
		}
		classified[key] = true

		if i > 0 {
			prev := file.Routes[i-1]
			if entry.Path < prev.Path || (entry.Path == prev.Path && entry.Method < prev.Method) {
				t.Errorf("%s: entry %q is out of order, keep the file sorted by path then method so parallel additions merge cleanly",
					classificationFileName, key)
			}
		}
	}

	var missing []string
	for key := range registered {
		if !classified[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)

	var stale []string
	for key := range classified {
		if !registered[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("%d mutating route(s) are registered in the source but have no entry in pkg/routes/%s:\n\n  %s\n\n"+
			"Add each one to that file, classified as exactly one of:\n\n"+
			"  protected-topology - creates, moves, renames or deletes a project or a team\n"+
			"  access-expanding   - shares a project, invites or adds a member, grants a right,\n"+
			"                       creates a link share, makes something public, or can move a\n"+
			"                       task out of a private Inbox into a shared project\n"+
			"  ordinary           - normal task work under stock Vikunja permissions\n\n"+
			"If in doubt, pick the safer class (protected-topology or access-expanding), not ordinary.\n"+
			"A protected-topology or access-expanding route must ALSO be wired into the managed-mode\n"+
			"enforcement (BRA-914) before it ships; classifying it here only records the decision.",
			len(missing), classificationFileName, strings.Join(missing, "\n  "))
	}

	if len(stale) > 0 {
		t.Errorf("%d stale entries in pkg/routes/%s do not match any registered mutating route\n"+
			"(renamed or removed upstream?):\n\n  %s\n\n"+
			"Remove or update them so the file stays an exact inventory of the mutating surface.",
			len(stale), classificationFileName, strings.Join(stale, "\n  "))
	}
}
