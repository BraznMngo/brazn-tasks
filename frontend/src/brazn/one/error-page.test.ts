import {describe, it, expect, beforeEach, afterEach, beforeAll, vi} from 'vitest'

import {errorSurface, pairForReason, reasonFromSearch} from '../../../public/one/error.js'
import {init as initI18n, t} from '../../../public/one/i18n.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import deRaw from '../../../public/one/i18n/de-DE.json?raw'
import esRaw from '../../../public/one/i18n/es-ES.json?raw'
import frRaw from '../../../public/one/i18n/fr-FR.json?raw'
import jaRaw from '../../../public/one/i18n/ja-JP.json?raw'
import zhRaw from '../../../public/one/i18n/zh-CN.json?raw'
import {
	ORIGIN,
	captureDocumentListeners,
	cardText,
	mountAuthCard,
	releaseDocumentListeners,
	resetHarness,
	restoreLocation,
	settle,
	standAt,
} from './auth-page-harness'

/*
 * THE GENERAL ERROR PAGE — ACCEPTANCE CRITERION 9.
 *
 * "Every way this can fail — expired token, withdrawn invitation, full seat ceiling, an address
 * that already has an account — ends on the general error page saying what happened and what to do
 * next, IN THE READER'S LANGUAGE."
 *
 * Two halves, and only one of them is this page's. This file decides that the page says something
 * true, actionable and translated for each of those failures, and that a stranger cannot make it
 * display their own text. Whether each failure ACTUALLY arrives here is the invitation page's half,
 * and the route contract behind it was still being settled during this review, so nothing below
 * pins a route name or an outcome word.
 */

const CATALOGUES: Record<string, string> = {
	en: enRaw,
	'de-DE': deRaw,
	'es-ES': esRaw,
	'fr-FR': frRaw,
	'ja-JP': jaRaw,
	'zh-CN': zhRaw,
}

beforeAll(() => {
	vi.stubGlobal('fetch', async (input: string) => {
		const match = /\/i18n\/([^/]+)\.json/.exec(String(input))
		const raw = match === null ? undefined : CATALOGUES[match[1]]
		return raw === undefined
			? new Response('not found', {status: 404})
			: new Response(raw, {headers: {'content-type': 'application/json'}})
	})
})

beforeEach(() => {
	resetHarness()
	captureDocumentListeners()
	sessionStorage.clear()
	standAt('/one/error.html')
})

afterEach(() => {
	releaseDocumentListeners()
	document.body.innerHTML = ''
	restoreLocation()
})

let pageCounter = 0

async function freshPage() {
	document.body.innerHTML = ''
	pageCounter += 1
	const page = await import(/* @vite-ignore */ `../../../public/one/error.js?fresh=${pageCounter}`)
	mountAuthCard()
	return page
}

/** Every failure the ticket names by hand, plus the two the service can add. */
const FAILURES = [
	'invitation-expired',
	'invitation-revoked',
	'invitation-unknown',
	// BRA-1475's later addition: the invitation is fine and only the link ran out, which is the one
	// case where the general sentence is actively wrong — it would tell somebody to ask for a new
	// invitation when the invitation they hold is live.
	'link-expired',
	'seats-full',
	'account-exists',
	'not-invitable',
] as const

/* ================================================================== *
 * CRITERION 9 — what happened, what to do next, in the reader's language
 * ================================================================== */

describe('criterion 9 — every failure is worded, and worded in the reader\'s language', () => {
	it('gives each named failure its own heading and its own body, ACTUALLY TRANSLATED, in all six languages', async () => {
		// SIX LANGUAGES, NOT ONE. The criterion says "in the reader's language" in as many words,
		// and a page whose sentences exist only in English meets every other requirement and still
		// fails this one for five sixths of the product's readers.
		//
		// THE ASSERTION IS "DIFFERENT FROM ENGLISH", AND THE OBVIOUS ONE WOULD HAVE BEEN USELESS.
		// A key missing from a language file does not render as a key path here — i18n.js treats a
		// missing overlay key as "fall through to en" on purpose, so an untranslated page renders
		// perfectly good English. A test asserting the string is non-empty, or that it carries no
		// key path, therefore passes for every language whether or not one word was translated.
		// That is `docs/Testing-Rules.md`'s first shape, and it is the reason this test compares
		// against the English string instead.
		await initI18n('en', ['en'])
		const english = new Map<string, string>()
		for (const reason of FAILURES) {
			const pair = pairForReason(reason)
			english.set(pair.title, t(pair.title))
			english.set(pair.body, t(pair.body))
		}

		for (const locale of Object.keys(CATALOGUES)) {
			await initI18n(locale, [locale])
			for (const reason of FAILURES) {
				const pair = pairForReason(reason)
				for (const key of [pair.title, pair.body]) {
					const rendered = t(key)
					expect(rendered, `${locale} / ${key}`).not.toContain('one.error.')
					expect(rendered.trim(), `${locale} / ${key}`).not.toBe('')
					if (locale !== 'en') {
						// MUTATION: deleting this key from that language's catalogue makes this red.
						expect(rendered, `${locale} / ${key} is still the English sentence`)
							.not.toBe(english.get(key))
					}
				}
			}
		}
		await initI18n('en', ['en'])
	})

	it('carries the one sentence the ticket defines, exactly', async () => {
		await initI18n('en', ['en'])
		// The ticket quotes this and defines no other text on this page.
		expect(t(pairForReason('account-exists').body))
			.toBe('This account already exists. Please ask your administrator to add you as an existing user instead.')
	})

	it('gives each failure a DIFFERENT heading, so the page is not one sentence wearing six hats', async () => {
		await initI18n('en', ['en'])
		const headings = FAILURES.map(reason => t(pairForReason(reason).title))
		// MUTATION: collapsing REASONS so every reason maps to the general pair makes this red, and
		// somebody at a full seat ceiling is told only that something went wrong.
		expect(new Set(headings).size).toBe(headings.length)
	})
})

/* ================================================================== *
 * The page takes its heading and body from whoever sent somebody here
 * ================================================================== */

describe('the page is given its words by the page that sent the person', () => {
	it('reads the reason from the address and the service\'s sentence from per-tab storage', async () => {
		await initI18n('en', ['en'])
		sessionStorage.setItem('brazn.one.error-sentence', 'Your organisation has 3 of 3 seats in use.')
		standAt('/one/error.html', '?reason=seats-full')

		const {boot} = await freshPage()
		await boot()
		await settle()

		const text = cardText()
		expect(text).toContain('This organisation has no free seats')
		expect(text).toContain('Ask your administrator to add a seat')
		// The service's own sentence is an EXTRA line, never a replacement for the body — the body
		// is the half a refusal never carries, because it says what to do next.
		expect(text).toContain('Your organisation has 3 of 3 seats in use.')
	})

	it('consumes the sentence, so a later unrelated visit does not inherit it', async () => {
		await initI18n('en', ['en'])
		sessionStorage.setItem('brazn.one.error-sentence', 'A refusal from an hour ago.')
		standAt('/one/error.html', '?reason=seats-full')

		const {boot} = await freshPage()
		await boot()
		await settle()

		// MUTATION: deleting the removeItem in takeErrorSentence makes this red, and a stale
		// refusal reappears under an unrelated heading.
		expect(sessionStorage.getItem('brazn.one.error-sentence')).toBeNull()
	})

	it('CANNOT BE MADE TO DISPLAY A STRANGER\'S TEXT through the address', async () => {
		// `reason` arrives in the query, so anybody can put anything there. A closed set is what
		// stops this page becoming somewhere to host a sentence of somebody else's choosing.
		await initI18n('en', ['en'])
		standAt('/one/error.html', '?reason=Your%20account%20is%20suspended%2C%20call%20555-0100')

		const {boot} = await freshPage()
		await boot()
		await settle()

		const text = cardText()
		// MUTATION: rendering the raw reason, or building a catalogue key from it, makes this red.
		expect(text).not.toContain('555-0100')
		expect(text).toContain('Something went wrong')
	})

	it('renders a sentence carrying markup as characters, not as elements', () => {
		const surface = errorSurface({reason: 'seats-full', sentence: '<img src=x onerror=alert(1)>'})
		// MUTATION: dropping esc() around state.sentence makes this red. These pages are the only
		// place in this product an unauthenticated stranger can put a string into.
		expect(surface).not.toContain('<img src=x')
		expect(surface).toContain('&lt;img src=x')
	})

	it('falls to the general pair for a reason nobody added here yet', () => {
		// FAIL CLOSED: a vocabulary that grows later says something true and vague rather than
		// something meaningless and specific.
		expect(pairForReason('a-word-from-2027')).toEqual(pairForReason(undefined))
		expect(pairForReason('toString')).toEqual(pairForReason(undefined))
		expect(reasonFromSearch('?reason=seats-full')).toBe('seats-full')
		expect(reasonFromSearch('?reason=')).toBeNull()
		expect(reasonFromSearch('')).toBeNull()
	})

	it('always offers a way onward, so the page is never a dead end', async () => {
		await initI18n('en', ['en'])
		standAt('/one/error.html', '?reason=invitation-expired')

		const {boot} = await freshPage()
		await boot()
		await settle()

		const onward = document.querySelector('[data-action="error-sign-in"]')
		expect(onward).not.toBeNull()
		expect(onward?.textContent?.trim()).toBe('Go to sign in')
		expect(ORIGIN).toBe('https://dev.tasks.brazn.one')
	})
})
