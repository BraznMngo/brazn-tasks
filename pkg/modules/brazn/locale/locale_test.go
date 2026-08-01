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

package locale

import (
	"os"
	"testing"

	"code.vikunja.io/api/pkg/i18n"
	"code.vikunja.io/api/pkg/log"

	"github.com/stretchr/testify/assert"
)

// TestMain loads the real catalogue. Accepts and Normalize both consult
// i18n.HasLanguage, so without it every exact-code case would take the
// base-subtag branch instead and the two paths would never be told apart.
func TestMain(m *testing.M) {
	log.InitLogger()
	i18n.Init()
	os.Exit(m.Run())
}

// TestNormalize pins the mapping against codes written out by hand. The
// expected values are the catalogue filenames in pkg/i18n/lang, not values read
// back out of baseLanguages, so what is asserted is that the mapping is correct
// rather than that the table agrees with itself.
func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"the canonical account locale for German resolves to the German catalogue", "de", "de-DE"},
		{"a catalogue code passes through untouched", "de-DE", "de-DE"},
		{"a regional variant folds onto the German catalogue", "de-AT", "de-DE"},
		{"an underscore separator is understood", "de_DE", "de-DE"},
		{"case is irrelevant", "DE", "de-DE"},
		{"English is the same either way", "en", "en"},
		{"regional English folds onto English", "en-US", "en"},
		{"unset stays unset", "", ""},
		{"an unrecognised locale falls back to English", "kl-KL", "en"},
		{"a language inherited from upstream keeps its exact code", "fr-FR", "fr-FR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Normalize(tc.in))
		})
	}
}

// TestNormalizeSelectsTheGermanCatalogue closes the gap the table above cannot.
// "de-DE" could be the wrong string and every case there would still pass,
// because both the table and its expectations would be wrong together. This
// resolves a real key through i18n with the normalized code, so the code is
// checked against the catalogue that actually ships.
//
// The expected strings are the catalogue's own wording; a copy change to the
// greeting is meant to fail here, because that is the file this maps onto.
func TestNormalizeSelectsTheGermanCatalogue(t *testing.T) {
	const key = "notifications.greeting"

	german := i18n.T(Normalize("de"), key, "Sam")
	english := i18n.T(Normalize("en"), key, "Sam")

	assert.Equal(t, "Hallo Sam,", german)
	assert.Equal(t, "Hi Sam,", english)
}

// TestAccepts covers both directions. Refusal needs its own case: an Accepts
// that simply returned true would satisfy every acceptance case above it and
// hand the database an unrenderable language code.
func TestAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"the canonical account locale for German", "de", true},
		{"a catalogue code", "de-DE", true},
		{"a regional variant", "de-AT", true},
		{"English", "en", true},
		{"unset, which means the browser decides", "", true},
		{"a language inherited from upstream, by exact code", "fr-FR", true},
		{"an unrecognised locale is refused, not normalised", "kl-KL", false},
		{"a bare subtag the product does not guarantee is refused", "fr", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Accepts(tc.in))
		})
	}
}
