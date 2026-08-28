import {describe, it, expect, beforeEach, afterEach, vi} from 'vitest'

import {t, init, negotiateLanguage, currentLocale, supportedLocales, projectTitle} from '../../../public/one/i18n.js'

// The six shipped catalogues, as TEXT. `?raw` keeps them out of the module graph as data rather
// than as code, so the audit at the bottom reads exactly the bytes the browser will fetch. The
// import is relative for the same reason the module imports are (ruling C5): a root-absolute
// specifier into public/ is the form Vite rejects.
import enRaw from '../../../public/one/i18n/en.json?raw'
import esRaw from '../../../public/one/i18n/es-ES.json?raw'
import deRaw from '../../../public/one/i18n/de-DE.json?raw'
import frRaw from '../../../public/one/i18n/fr-FR.json?raw'
import zhRaw from '../../../public/one/i18n/zh-CN.json?raw'
import jaRaw from '../../../public/one/i18n/ja-JP.json?raw'

/*
 * frontend/public/one/i18n.js: exact-tag negotiation, the negotiated -> en -> key-path fallback
 * chain, and the audit that keeps the product from calling itself Vikunja (CLAUDE.md section 7).
 *
 * i18n.js fetches its catalogues through the bare global `fetch` - it has no injection seam,
 * unlike api.js - so these tests stub the global and unstub it afterwards.
 */

// Fixture keys, assembled rather than written as literals. Upstream's check-translations job
// treats every t() call literal it finds under frontend/src as an app key and fails on any that
// frontend/src/i18n/lang/en.json does not define. These keys are this file's own fixtures and
// deliberately exist nowhere else - the page's real catalogue is public/one/i18n/.
const k = (...parts: string[]) => parts.join('.')

type Catalogue = Record<string, unknown>

function serve(catalogues: Record<string, Catalogue>) {
	const stub = vi.fn(async (input: string) => {
		const locale = String(input).replace(/^.*\/([^/]+)\.json(?:\?.*)?$/, '$1')
		const body = catalogues[locale]
		if (body === undefined) return new Response('not found', {status: 404})
		return new Response(JSON.stringify(body), {headers: {'content-type': 'application/json'}})
	})
	vi.stubGlobal('fetch', stub)
	return stub
}

describe('one/i18n.js language negotiation', () => {
	it('matches EXACT tags and never widens a region', () => {
		expect(negotiateLanguage('de-DE', ['en'])).toBe('de-DE')
		expect(negotiateLanguage(null, ['fr-FR', 'en'])).toBe('fr-FR')

		// The Vue app's SUPPORTED_LOCALES are exact tags. Widening is what produces a page that is
		// half-translated in a locale nobody shipped.
		// MUTATION: adding a primary-subtag match (`tag.split('-')[0]`) to negotiateLanguage makes
		// this red - 'es' would resolve to 'es-ES'.
		expect(negotiateLanguage('es', ['es'])).toBe('en')
		expect(negotiateLanguage('pt-BR', ['ar-SA'])).toBe('en')
		expect(negotiateLanguage(null, [])).toBe('en')
	})

	it('lets the stored preference beat the browser', () => {
		// Both tags below are supported, so the assertion can only be satisfied by the ORDER in
		// which the candidates are collected - not by one of them being unsupported.
		// MUTATION: pushing navigatorLanguages before `preferred` in negotiateLanguage makes this
		// red.
		expect(negotiateLanguage('ja-JP', ['de-DE', 'en'])).toBe('ja-JP')
	})

	it('ships a catalogue for every locale it claims to support', () => {
		const shipped = {
			'en': enRaw,
			'es-ES': esRaw,
			'de-DE': deRaw,
			'fr-FR': frRaw,
			'zh-CN': zhRaw,
			'ja-JP': jaRaw,
		}
		// A locale in SUPPORTED_LOCALES with no file behind it negotiates successfully and then
		// 404s on the catalogue, which degrades the whole page to English silently.
		// MUTATION: adding a tag to SUPPORTED_LOCALES without shipping public/one/i18n/<tag>.json
		// makes this red.
		expect([...supportedLocales()].sort()).toEqual(Object.keys(shipped).sort())
	})
})

describe('one/i18n.js fallback chain', () => {
	beforeEach(() => {
		vi.spyOn(console, 'warn').mockImplementation(() => {})
	})

	afterEach(() => {
		vi.unstubAllGlobals()
		vi.restoreAllMocks()
	})

	it('resolves negotiated -> en -> the key path', async () => {
		serve({
			'en': {
				misc: {save: 'Save'},
				one: {deny: {noRoute: 'This is not available yet.'}},
			},
			'de-DE': {misc: {save: 'Speichern'}},
		})

		expect(await init('de-DE', ['en'])).toBe('de-DE')
		expect(currentLocale()).toBe('de-DE')
		expect(t('misc.save')).toBe('Speichern')

		// de-DE has no `one` node AT ALL, so this has to fail at depth 1 and fall through rather
		// than throw on a missing intermediate.
		// MUTATION: deleting the `typeof node !== 'object'` guard from lookup() makes this red -
		// it throws a TypeError instead of falling back to en.
		expect(t(k('one', 'deny', 'noRoute'))).toBe('This is not available yet.')

		// The key path is the documented last resort: a blank label would be worse than an English
		// one, and worse than the key.
		//
		// ASSEMBLED RATHER THAN WRITTEN OUT, and that is not style. This key must NOT exist in
		// any catalogue or the assertion is vacuous - but upstream's check-translations job
		// scans the source for t() call literals and fails the build for every one it cannot
		// find in en.json. A literal here is therefore unresolvable: adding the key to en.json
		// to satisfy the scanner is exactly what would make this test pass for the wrong reason.
		const missingKey = ['one', 'deny', 'chainMissingKey'].join('.')
		expect(t(missingKey)).toBe(missingKey)
		// The lang attribute is what a screen reader switches voice on.
		expect(document.documentElement.lang).toBe('de-DE')
	})

	it('treats a present-but-empty value as missing', async () => {
		serve({
			'en': {misc: {save: 'Save'}},
			'fr-FR': {misc: {save: ''}},
		})
		await init('fr-FR', [])

		// A blanked value is a bug in the trim, not a translation. Rendering it would produce an
		// unlabelled button.
		// MUTATION: dropping the `node !== ''` condition from lookup() makes this red - t() returns
		// the empty string.
		expect(t('misc.save')).toBe('Save')
	})

	it('survives a missing regional catalogue by falling back to en', async () => {
		serve({'en': {misc: {save: 'Save'}}})

		// MUTATION: removing the try/catch around the overlay fetch in init() makes this red -
		// init() rejects and app.js renders its fatal surface over a page that would have worked
		// perfectly well in English.
		expect(await init('zh-CN', [])).toBe('en')
		expect(currentLocale()).toBe('en')
		expect(t('misc.save')).toBe('Save')
		expect(console.warn).toHaveBeenCalled()
	})

	it('treats en as a hard dependency and rejects when it cannot be loaded', async () => {
		serve({})

		// Without en the page has no string layer at all, so this must reject and let app.js
		// render its error state rather than a page full of key paths.
		// MUTATION: wrapping the en fetch in the same try/catch as the overlay makes this red.
		await expect(init('en', [])).rejects.toThrow(/404/)
	})

	it('warns ONCE per missing key', async () => {
		serve({'en': {misc: {save: 'Save'}}})
		await init('en', [])

		// Assembled, not written out — see the note on `missingKey` above: a bare t() literal
		// naming a key that does not exist fails upstream's check-translations job.
		const warnOnceKey = ['one', 'task', 'warnOnceKey'].join('.')

		expect(t(warnOnceKey)).toBe(warnOnceKey)
		expect(t(warnOnceKey)).toBe(warnOnceKey)

		const warnings = vi.mocked(console.warn).mock.calls
			.filter(call => String(call[0]).includes(warnOnceKey))
		// A key missing inside a render loop floods the console and buries the first occurrence,
		// which is the one that says where it came from.
		// MUTATION: deleting the `warned` Set from t() makes this red - the count becomes 2.
		expect(warnings).toHaveLength(1)
	})

	it('interpolates params, selects a plural branch, and unescapes the vue-i18n literal form', async () => {
		serve({
			'en': {
				one: {
					common: {atUsername: "{'@'}{username}"},
					org: {seats: {members: '{count} member | {count} members'}},
				},
				organization: {members: {inUse: '{used} / {limit} seats in use'}},
			},
		})
		await init('en', [])

		// "{'@'}" is vue-i18n's escape for the linked-message marker. These values live in
		// frontend/src/i18n/lang/en.json too, where vue-i18n compiles them - so this page has to
		// read the same spelling rather than a cleaned-up copy.
		// MUTATION: deleting the literal branch from interpolate()'s replacer makes this red -
		// the output becomes "{'@'}ada".
		expect(t(k('one', 'common', 'atUsername'), {username: 'ada'})).toBe('@ada')
		expect(t('organization.members.inUse', {used: 3, limit: 9})).toBe('3 / 9 seats in use')
		expect(t(k('one', 'org', 'seats', 'members'), {count: 1})).toBe('1 member')
		expect(t(k('one', 'org', 'seats', 'members'), {count: 2})).toBe('2 members')
		// ja-JP writes single-branch values for the relation kinds, so a value with no '|' must
		// pass through untouched even when a count is supplied.
		expect(t('organization.members.inUse', {used: 1, limit: 1, count: 1})).toBe('1 / 1 seats in use')
	})
})

describe('one/i18n.js project titles', () => {
	afterEach(() => {
		vi.unstubAllGlobals()
	})

	// The word "Inbox" is a value in a database column, not customer-facing copy. The server
	// writes it into every account's own project on registration and the managed rules identify
	// that project by its immutable id, so the column cannot be renamed - which is exactly why
	// the page has to translate it on the way out.
	//
	// MUTATION: print `project.title` at either picker instead of calling this, and a customer
	// reading the move dialog or the add-task dialog is offered a project called "Inbox" while
	// the settings page two clicks away calls the same project "Your Tasks".
	it('shows the stored Inbox as the name the customer knows it by', async () => {
		serve({en: {one: {org: {tile: {privateName: 'Your Tasks'}}}}})
		await init('en', ['en'])

		expect(projectTitle({title: 'Inbox'})).toBe('Your Tasks')
	})

	// The name is TRANSLATED rather than substituted for a second English literal, which is the
	// whole reason it resolves through the catalogue instead of through a constant.
	it('translates it, rather than swapping one English word for another', async () => {
		serve({
			en: {one: {org: {tile: {privateName: 'Your Tasks'}}}},
			'de-DE': {one: {org: {tile: {privateName: 'Deine Aufgaben'}}}},
		})
		await init('de-DE', ['de-DE'])

		expect(projectTitle({title: 'Inbox'})).toBe('Deine Aufgaben')
	})

	// Only that one stored value is replaced. A customer who names a project "inbox", or
	// "Inbox archive", owns an ordinary project and must see the name they chose - the swap keys
	// on the exact literal the server writes and on nothing looser.
	it('leaves every other title exactly as the customer wrote it', async () => {
		serve({en: {one: {org: {tile: {privateName: 'Your Tasks'}}}}})
		await init('en', ['en'])

		expect(projectTitle({title: 'Feedback'})).toBe('Feedback')
		expect(projectTitle({title: 'inbox'})).toBe('inbox')
		expect(projectTitle({title: 'Inbox archive'})).toBe('Inbox archive')
		expect(projectTitle({title: 'My Inbox'})).toBe('My Inbox')
	})

	// A project read that failed leaves a row with no title, and a picker built from it must show
	// an empty option rather than the string "undefined" or a thrown TypeError.
	it('renders nothing rather than debug text for a project with no title', async () => {
		serve({en: {one: {org: {tile: {privateName: 'Your Tasks'}}}}})
		await init('en', ['en'])

		expect(projectTitle(undefined)).toBe('')
		expect(projectTitle(null)).toBe('')
		expect(projectTitle({})).toBe('')
	})
})

describe('one/i18n.js shipped catalogues', () => {
	const CATALOGUES: ReadonlyArray<readonly [string, string]> = [
		['en', enRaw],
		['es-ES', esRaw],
		['de-DE', deRaw],
		['fr-FR', frRaw],
		['zh-CN', zhRaw],
		['ja-JP', jaRaw],
	]

	function values(node: unknown, sink: string[]): string[] {
		if (typeof node === 'string') sink.push(node)
		else if (node !== null && typeof node === 'object') {
			for (const child of Object.values(node as Record<string, unknown>)) values(child, sink)
		}
		return sink
	}

	it('parses, and every file carries real copy', () => {
		let total = 0
		for (const [locale, raw] of CATALOGUES) {
			const parsed: unknown = JSON.parse(raw)
			const strings = values(parsed, [])
			// THESE FLOORS ARE AN ANTI-VACUITY GUARD, NOT A COVERAGE ASSERTION, and the distinction
			// matters because the number looks like coverage and is not. They exist so the audits
			// below cannot pass by walking nothing - a recursion that silently visits zero values is
			// the exact shape of a test that cannot fail (CLAUDE.md section 4).
			//
			// What they deliberately do NOT assert is how much of the page each locale translates.
			// The real figures are en 348, de-DE 151, ja-JP 127, fr-FR 120, zh-CN 120, es-ES 107, so
			// 54-69% of what a user reads falls back to English outside en - and that is CORRECT
			// rather than a regression: every missing key is missing from
			// frontend/src/i18n/lang/<locale>.json too, because the `one.*` namespace is new and the
			// fork's own `organization.*` tree exists only in en and de-DE. Raising these floors
			// towards the real counts would turn every future upstream translation drop into a red
			// build for a fact this page does not control. The coverage figure belongs in
			// docs/one-tasks-restricted-views.md, where it is stated with its method; a test that
			// pinned it would be a test of somebody else's translation process.
			expect(strings.length, `${locale} value count`).toBeGreaterThan(locale === 'en' ? 250 : 80)
			total += strings.length
		}
		expect(total).toBeGreaterThan(800)
	})

	it('never names the product or the subscription by a dead name', () => {
		for (const [locale, raw] of CATALOGUES) {
			for (const value of values(JSON.parse(raw), [])) {
				// Stricter than .github/workflows/fork-guards.yml, which allowlists "Vikunja export"
				// for the upstream catalogues: these six files are OURS and carry no such key, so
				// any occurrence at all is one too many.
				// MUTATION: restoring an upstream string that names the product - e.g. writing
				// "Export your Vikunja data" back into user.export.title in en.json - makes this
				// red, and it is the same failure fork-guards catches on the upstream catalogues.
				expect(value.toLowerCase(), `${locale}: ${value}`).not.toContain('vikunja')

				// BRA-1444: the product has been called three things - Vikunja upstream, then
				// Brazn Tasks, and now ONE - and what a customer buys has been called two,
				// Percy Cloud and then Brazn Cloud, before it became "your ONE subscription"
				// [Ruled, Sebastian 2026-08-27]. A branch cut before a rename, a revert or an
				// upstream merge can put any of them back, and every one reads as correct to
				// anyone who has not been told otherwise.
				// "brazn tasks" and "brazn cloud" and not "brazn": brazn.one is the product's
				// own address and may legitimately appear in a value. The dead names have a
				// space where the address and the repository path have a dot and a hyphen.
				// MUTATION: writing "Save to Brazn Tasks" into misc.save in en.json makes this
				// red; so do "Brazn Cloud", "Percy Cloud" and "Percy" anywhere.
				expect(value.toLowerCase(), `${locale}: ${value}`).not.toContain('brazn tasks')
				expect(value.toLowerCase(), `${locale}: ${value}`).not.toContain('brazn cloud')

				// The subscription's older name is asserted on its own line even though the
				// bare "percy" line below already covers it, and that is deliberate. Percy is
				// the assistant's real name and stays correct for the assistant, so the line
				// below is the one that will one day be relaxed for attribution copy. This
				// line is what stops that relaxation from letting the old subscription name
				// back into a value a customer reads.
				expect(value.toLowerCase(), `${locale}: ${value}`).not.toContain('percy cloud')
				expect(value.toLowerCase(), `${locale}: ${value}`).not.toContain('percy')
			}
		}
	})

	it('keeps every key path a plain dotted path', () => {
		// t() splits on '.', so a key containing a dot in its own name is unreachable: the lookup
		// would descend into an object that does not exist and fall through to the key path,
		// which renders as visible debug text.
		function walk(node: unknown, path: string) {
			if (node === null || typeof node !== 'object') return
			for (const [key, child] of Object.entries(node as Record<string, unknown>)) {
				expect(key, `${path}${key}`).not.toContain('.')
				walk(child, `${path}${key}.`)
			}
		}
		for (const [locale, raw] of CATALOGUES) walk(JSON.parse(raw), `${locale}:`)
	})
})
