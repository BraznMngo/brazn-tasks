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

// Package locale resolves a canonical Brazn account locale onto the language
// code Brazn Tasks stores on a user.
//
// Percy owns the account locale and expresses it as a bare language subtag
// ("de", "en"). Brazn Tasks inherits Vikunja's catalogue, which is keyed by the
// full code ("de-DE", "en"). Nothing bridged the two: "de" is refused by the
// `language` validation tag at the API boundary, and had it reached the
// database anyway, i18n.T would have quietly fallen back to English -- so a
// German account would have received English mail with nothing reporting it.
//
// A language code is a machine-readable identifier, not prose. It is
// byte-identical in every language and is never translated.
package locale

import (
	"strings"

	"code.vikunja.io/api/pkg/i18n"
)

// Default is the language used when a locale cannot be resolved to anything
// the catalogue ships. It matches the fallback i18n.T already applies, so the
// interface and the mailer cannot disagree about what "unknown" means.
const Default = "en"

// baseLanguages maps a bare language subtag onto the catalogue Brazn Tasks
// ships for it. Only the two languages the product guarantees are listed. Every
// other language inherited from upstream stays reachable, by its exact code.
var baseLanguages = map[string]string{
	"de": "de-DE",
	"en": "en",
}

// baseSubtag returns the lowercased primary subtag of a locale, so that "de-AT",
// "de_DE" and "DE" all reduce to "de".
func baseSubtag(loc string) string {
	if i := strings.IndexAny(loc, "-_"); i >= 0 {
		return strings.ToLower(loc[:i])
	}
	return strings.ToLower(loc)
}

// Normalize maps a locale onto a language code Brazn Tasks can render.
//
// An empty locale is returned unchanged. "Not set" is a real state: it lets the
// frontend fall back to the browser language, and mail already degrades to
// English inside i18n.T. Rewriting it to Default here would silently take that
// detection away from every user who never picked a language.
//
// A non-empty locale that cannot be resolved becomes Default, so whatever is
// stored is always a code the catalogue actually has.
func Normalize(loc string) string {
	if loc == "" {
		return ""
	}

	if i18n.HasLanguage(loc) {
		return loc
	}

	if lang, ok := baseLanguages[baseSubtag(loc)]; ok {
		return lang
	}

	return Default
}

// Accepts reports whether a locale can be resolved to a language Brazn Tasks
// ships. It backs the `language` validation tag, so it is what decides whether
// the API takes a canonical account locale at all.
//
// An unrecognised locale is refused rather than quietly normalised: at the API
// boundary a typo is worth reporting, whereas Normalize's Default exists to
// cope with values already sitting in the database.
func Accepts(loc string) bool {
	if loc == "" {
		return true
	}

	if i18n.HasLanguage(loc) {
		return true
	}

	_, ok := baseLanguages[baseSubtag(loc)]
	return ok
}
