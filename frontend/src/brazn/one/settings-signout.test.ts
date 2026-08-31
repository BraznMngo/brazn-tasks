import {describe, it, expect, beforeEach, afterEach, beforeAll, vi} from 'vitest'

import {init as initI18n} from '../../../public/one/i18n.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import {
	ORIGIN,
	captureDocumentListeners,
	enqueue,
	fetchStub,
	json,
	navigations,
	releaseDocumentListeners,
	requests,
	resetHarness,
	restoreLocation,
	settle,
	standAt,
} from './auth-page-harness'

/*
 * THE SIGN-OUT CONTROL — ACCEPTANCE CRITERION 16: "The settings page has a sign-out control."
 *
 * Four things have to be true for that sentence to mean anything to a person, and only the first
 * is about the control existing:
 *
 *   1. it is drawn on the settings page;
 *   2. NO GATE CAN REFUSE IT — every other control on that page can be refused by a gate, and a
 *      person whose plan lapsed, whose writes are restricted or whose edition is wrong must still
 *      be able to leave;
 *   3. pressing it ends the session and lands them on a page for signed-out people;
 *   4. pressing it when the session is ALREADY gone still lands them somewhere sensible, because
 *      that is the state a person is most likely to be in when they reach for it.
 *
 * The fourth was raised as an open question against this change and is answered here.
 *
 * The view's `render` and the registry's handlers are captured through app.js's own two
 * registration functions, so what is asserted is the shipped view and the shipped handler rather
 * than a copy written here.
 */

type Handler = (event: Event, el: Element) => void | Promise<void>

const captured: {render: ((ctx: unknown) => string) | null, actions: Record<string, Handler>} = {
	render: null,
	actions: {},
}

vi.mock('../../../public/one/app.js', async (importOriginal) => {
	const real = await importOriginal<typeof import('../../../public/one/app.js')>()
	return {
		...real,
		registerView: (name: string, view: {render: (ctx: unknown) => string}) => {
			if (name === 'settings') captured.render = view.render
		},
		registerActions: (map: Record<string, Handler>) => {
			Object.assign(captured.actions, map)
		},
	}
})

beforeAll(async () => {
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
	await initI18n('en', ['en'])
	// Importing the shipped view module is what registers both, through the two functions above.
	// @ts-expect-error view-settings.js ships no .d.ts, unlike its siblings in the same directory.
	// The import is for its side effect - it calls registerView and registerActions at module
	// scope - so there is nothing to type here, and adding a declaration file would be a change to
	// production for a test's convenience.
	await import('../../../public/one/view-settings.js')
})

beforeEach(() => {
	resetHarness()
	captureDocumentListeners()
	standAt('/one/settings.html')
})

afterEach(() => {
	releaseDocumentListeners()
	document.body.innerHTML = ''
	restoreLocation()
})

/** The settings page as somebody with the least possible standing sees it. */
function settingsMarkup(overrides: Record<string, unknown> = {}): string {
	if (captured.render === null) throw new Error('view-settings.js registered no settings view')
	return captured.render({
		route: {view: 'settings', tab: 'account'},
		facts: {
			hasEdition: false,
			personalEdition: false,
			orgAdmin: false,
			writeRestricted: true,
			teams: {},
			...overrides,
		},
	})
}

function signOutControl(markup: string): Element {
	const host = document.createElement('div')
	host.innerHTML = markup
	const el = host.querySelector('[data-action="sign-out"]')
	if (el === null) throw new Error('the settings page draws no sign-out control')
	return el
}

/* ================================================================== *
 * CRITERION 16
 * ================================================================== */

describe('criterion 16 — the settings page has a sign-out control', () => {
	it('draws one, with a name a person would recognise', () => {
		const el = signOutControl(settingsMarkup())
		// MUTATION: deleting the sign-out row from profileCard() makes this red, and the only way
		// out of a session is to clear the browser's cookies.
		expect(el.textContent?.trim()).toBe('Sign out')
	})

	it('declares NO GATE, so nothing on this page can refuse it', () => {
		// This is the property that matters and the one a reader would never check. Every other
		// control here carries `data-requires`, and `applyGates` refuses or hides those. A person
		// reaches for this control precisely when something is wrong with their account, which is
		// exactly the state in which a gate would take it away from them.
		const el = signOutControl(settingsMarkup())
		expect(el.getAttribute('data-requires')).toBeNull()
		// It is not inside a gated group either, which would refuse it just as effectively.
		const host = document.createElement('div')
		host.innerHTML = settingsMarkup()
		const control = host.querySelector('[data-action="sign-out"]')
		expect(control?.closest('[data-requires]')).toBeNull()
	})

	it('SURVIVES THE REAL GATE APPLIER at its most hostile, rather than only reading as ungated', async () => {
		// Reading the markup proves the attribute is absent. It does not prove the control is still
		// there and still pressable after `applyGates` has walked the page — which is the thing a
		// person actually depends on, and which a gated ANCESTOR or a `GATES_THAT_HIDE` removal
		// would break without any attribute appearing on the button itself.
		const {applyGates} = await import('../../../public/one/app.js')
		const facts = {
			hasEdition: false,
			personalEdition: false,
			orgAdmin: false,
			writeRestricted: true,
			teams: {},
		}
		const host = document.createElement('div')
		host.innerHTML = settingsMarkup(facts)
		document.body.appendChild(host)

		applyGates(host, facts as never)

		const control = host.querySelector('[data-action="sign-out"]')
		// MUTATION: putting any `data-requires` on the sign-out row or its container makes this red.
		expect(control, 'the gate applier removed the sign-out control').not.toBeNull()
		expect(control?.getAttribute('aria-disabled')).not.toBe('true')
		expect(control?.closest('.is-refused')).toBeNull()
		expect((control as HTMLButtonElement).disabled).toBe(false)
		host.remove()
	})

	it('is drawn for somebody with no plan at all, not only for a paying customer', () => {
		// The worst case for criterion 16: an account with no edition claim and restricted writes.
		expect(() => signOutControl(settingsMarkup({hasEdition: false, writeRestricted: true}))).not.toThrow()
		expect(() => signOutControl(settingsMarkup({hasEdition: true, personalEdition: true}))).not.toThrow()
		expect(() => signOutControl(settingsMarkup({hasEdition: true, orgAdmin: true}))).not.toThrow()
	})

	it('ends the session and lands the person on OUR sign-in page', async () => {
		const api = await import('../../../public/one/api.js')
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		api.setToken('access-1')
		enqueue(json({message: 'Logged out.'}))

		await captured.actions['sign-out']?.(new Event('click'), document.createElement('button'))
		await settle()

		// The server was told, so the refresh cookie is cleared.
		expect(requests()[0]?.url).toBe(`${ORIGIN}/api/v1/user/logout`)
		// MUTATION: dropping the `accessToken = null` in signOut's finally makes this red.
		expect(api.hasSession()).toBe(false)
		// AND NOT `/login`, which is the old application's route. Serving that one form serves the
		// whole application, which is what criterion 17 exists to stop.
		expect(navigations()).toEqual([`${ORIGIN}/one/signin.html`])
		api.configure({fetch: null, origin: null, randomUUID: null})
	})

	it('STILL LANDS SOMEWHERE SENSIBLE when the session is already gone', async () => {
		// THE OPEN QUESTION, ANSWERED. The control is ungated, so it is pressable in a state where
		// the session has already expired — which is the likeliest state of all, because an expired
		// session is why somebody goes looking for it. The server then refuses the logout. A page
		// that let that refusal escape would leave the person pressing a button and watching
		// nothing happen.
		const api = await import('../../../public/one/api.js')
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		enqueue(json({message: 'missing, malformed or expired token'}, 401))
		enqueue(json({message: 'missing refresh token'}, 401))

		await captured.actions['sign-out']?.(new Event('click'), document.createElement('button'))
		await settle()

		// MUTATION: removing the try/catch around api.signOut in the 'sign-out' handler makes this
		// red — the rejection escapes and the person is left on the settings page.
		expect(api.hasSession()).toBe(false)
		expect(navigations()).toEqual([`${ORIGIN}/one/signin.html`])
		api.configure({fetch: null, origin: null, randomUUID: null})
	})

	it('sends the browser to the identity provider when the session came from one', async () => {
		// A session opened through Google has to end there too, or the next visit signs straight
		// back in without asking and the person believes they signed out.
		const api = await import('../../../public/one/api.js')
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		api.setToken('access-1')
		enqueue(json({message: 'Logged out.', oidc_logout_url: 'https://accounts.google.example/logout'}))

		await captured.actions['sign-out']?.(new Event('click'), document.createElement('button'))
		await settle()

		expect(navigations()).toEqual(['https://accounts.google.example/logout'])
		api.configure({fetch: null, origin: null, randomUUID: null})
	})
})
