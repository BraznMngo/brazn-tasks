/**
 * BRA-1444 — signed-out /one/ auth shell: title ONE, language selector, mark.
 *
 * Live beta serves /login and /register as signin.html (restricted UI), not the
 * Vue Login that PR #84 already fixed. These tests pin the surface customers hit.
 */

import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

import {
	brandBlock,
	getStoredLanguage,
	languageBlock,
	loadStrings,
	renderAuth,
	saveLanguage,
} from '../../../public/one/auth-shell.js'
import {currentLocale} from '../../../public/one/i18n.js'

describe('auth-shell BRA-1444 title / language / mark', () => {
	beforeEach(() => {
		localStorage.clear()
		document.body.innerHTML = '<div class="auth-stage hidden"><main class="auth-card" id="auth"></main></div>'
		document.title = 'ONE Tasks'
		document.documentElement.lang = 'en'
	})

	afterEach(() => {
		localStorage.clear()
		vi.unstubAllGlobals()
	})

	it('brandBlock uses the real ONE logos with ONE as the fallback alt', () => {
		const html = brandBlock()
		expect(html).toContain('logo-light.v1.png')
		expect(html).toContain('logo-dark.v1.png')
		expect(html).toContain('alt="ONE"')
		expect(html).not.toContain('ONE Tasks')
		expect(html).not.toContain('Brazn Tasks')
		expect(html).not.toContain('Percy')
	})

	it('loadStrings prefers a stored language and sets the document title to ONE', async () => {
		saveLanguage('de-DE')
		expect(getStoredLanguage()).toBe('de-DE')

		const fetchStub = vi.fn(async (url: string) => {
			const locale = String(url).includes('de-DE') ? 'de-DE' : 'en'
			const body = locale === 'de-DE'
				? {one: {page: {title: 'ONE'}, brand: {logoAlt: 'ONE'}, auth: {language: 'Sprache'}}}
				: {one: {page: {title: 'ONE'}, brand: {logoAlt: 'ONE'}, auth: {language: 'Language'}}}
			return new Response(JSON.stringify(body), {
				status: 200,
				headers: {'content-type': 'application/json'},
			})
		})
		vi.stubGlobal('fetch', fetchStub)

		await loadStrings()
		expect(currentLocale()).toBe('de-DE')
		expect(document.title).toBe('ONE')
		expect(document.title).not.toBe('ONE Tasks')
	})

	it('renderAuth appends a language selector that lists the six launch locales', async () => {
		const fetchStub = vi.fn(async () => new Response(JSON.stringify({
			one: {page: {title: 'ONE'}, brand: {logoAlt: 'ONE'}, auth: {language: 'Language'}},
		}), {status: 200, headers: {'content-type': 'application/json'}}))
		vi.stubGlobal('fetch', fetchStub)
		await loadStrings()

		renderAuth('<h1 class="auth-title">Sign in</h1>')
		const select = document.getElementById('auth-language') as HTMLSelectElement | null
		expect(select).not.toBeNull()
		expect(select?.tagName).toBe('SELECT')
		const values = [...(select?.options ?? [])].map(o => o.value)
		expect(values).toEqual(['en', 'es-ES', 'de-DE', 'fr-FR', 'zh-CN', 'ja-JP'])
		expect(document.querySelector('.auth-stage')?.classList.contains('hidden')).toBe(false)
	})

	it('languageBlock shares the Vue localStorage key name', () => {
		saveLanguage('fr-FR')
		expect(localStorage.getItem('language')).toBe('fr-FR')
		expect(languageBlock()).toContain('auth-language')
	})
})
