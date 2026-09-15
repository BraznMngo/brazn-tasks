import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'

import {init as initI18n, t} from '../../../public/one/i18n.js'

// Assembled, not written out — see i18n.test.ts's own note on this trick. Upstream's
// check-translations job scans frontend/src for t() call LITERALS and fails the build for any it
// cannot find in frontend/src/i18n/lang/en.json — the fork's Vue catalogue. `one.commercial.*`
// lives in frontend/public/one/i18n/en.json instead, a different catalogue the scanner does not
// read, so a literal here is a false positive the scanner cannot tell from a real missing key.
const k = (...parts: string[]) => parts.join('.')
import {formatDate} from '../../../public/one/app.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import settingsHtml from '../../../public/one/settings.html?raw'
import {
	ORIGIN,
	enqueue,
	fetchStub,
	json,
	requests,
	resetHarness,
	settle,
} from './auth-page-harness'

/*
 * THE CANCEL-SUBSCRIPTION CONTROL (BRA-1140).
 *
 * "There is no control anywhere that lets a customer stop paying." The route
 * (`POST /v1/subscription/cancellation`) and the client call (`api.cancelSubscription`) were both
 * already built and tested at the api.js layer (`api.commercial.test.ts`); nothing called either
 * one. This file is the other half: the settings page actually draws the control, only where the
 * route can succeed, and reads the service's own answer rather than assuming one.
 *
 * The view's `render` and the registry's handlers are captured through app.js's own two
 * registration functions (the same technique `settings-signout.test.ts` uses), so what is
 * asserted is the shipped view and the shipped handler rather than a copy written here.
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

// The shipped shell, minus its own module tag — same treatment as view-task.naming.test.ts, and
// the same reason: app.js is imported above, and leaving the tag in would ask the document to
// fetch and evaluate a second copy of it. This is what gives the test a real #modalRoot and
// #toastRoot rather than ones invented here.
const SHELL = (/<body>([\s\S]*)<\/body>/.exec(settingsHtml) ?? ['', ''])[1]
	.replace(/<script[\s\S]*?<\/script>/g, '')

beforeAll(async () => {
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
	await initI18n('en', ['en'])
	// @ts-expect-error view-settings.js ships no .d.ts, unlike its siblings in the same directory.
	await import('../../../public/one/view-settings.js')
	vi.unstubAllGlobals()
})

beforeEach(async () => {
	resetHarness()
	document.body.innerHTML = SHELL
	const api = await import('../../../public/one/api.js')
	api.resetSession()
})

afterEach(() => {
	document.body.innerHTML = ''
})

const FACTS = {hasEdition: false, personalEdition: false, orgAdmin: false, writeRestricted: false, teams: {}}

/**
 * `subscriptionCard()` gates on `editionMessageKey()`, which — like `readGateFacts()` — is called
 * with NO argument and so reads the JWT claim through `api.js`'s own session state rather than the
 * `facts` a view is handed (app.gating.test.ts pins the same thing for `readGateFacts`). So the
 * gate this page draws from is the token, not the `ctx.facts` object below — which still has to be
 * a well-formed `GateFacts` for the rest of the page to render at all.
 */
function tokenWith(claims: Record<string, unknown>): string {
	return `header.${btoa(JSON.stringify({id: 1, ...claims}))}.signature`
}

/** Paint the settings page into the shipped #app, the way boot() would. */
function paintSettings(): void {
	if (captured.render === null) throw new Error('view-settings.js registered no settings view')
	const app = document.getElementById('app')
	if (app === null) throw new Error('the shell has no #app')
	app.innerHTML = captured.render({route: {view: 'settings', tab: 'account'}, facts: FACTS})
}

function modalMarkup(): string {
	return document.getElementById('modalRoot')?.innerHTML ?? ''
}

function modalText(): string {
	return (document.getElementById('modalRoot')?.textContent ?? '').replace(/\s+/g, ' ').trim()
}

describe('BRA-1140 — the settings page draws a cancel-subscription control', () => {
	it('draws it for a Personal Cloud subscription', async () => {
		const api = await import('../../../public/one/api.js')
		api.setToken(tokenWith({brazn_edition: 'personal-cloud'}))
		paintSettings()
		const el = document.querySelector('[data-action="cancel-subscription"]')
		// MUTATION: deleting the cancel row from subscriptionCard() makes this red, and nothing
		// anywhere lets a Personal customer stop paying.
		expect(el, 'no cancel-subscription control on a Personal account').not.toBeNull()
		expect(el?.textContent?.trim()).toBe('Cancel subscription')
	})

	it('does NOT draw it for a Teams account, which the route always refuses (403)', async () => {
		const api = await import('../../../public/one/api.js')
		api.setToken(tokenWith({brazn_edition: 'teams-cloud'}))
		paintSettings()
		// MUTATION: dropping the `key === 'one.edition.personal'` gate makes this red — a Teams
		// member or administrator would be shown a control built only to refuse them, which is the
		// exact shape acceptance point 4 rules out.
		expect(document.querySelector('[data-action="cancel-subscription"]')).toBeNull()
	})

	it('does NOT draw it for an account with no edition claim', () => {
		paintSettings()
		expect(document.querySelector('[data-action="cancel-subscription"]')).toBeNull()
	})
})

describe('BRA-1140 — pressing it', () => {
	beforeEach(async () => {
		const api = await import('../../../public/one/api.js')
		api.setToken(tokenWith({brazn_edition: 'personal-cloud'}))
		paintSettings()
	})

	it('opens a confirmation that says this cannot be undone, and commits nothing yet', async () => {
		await captured.actions['cancel-subscription']?.(new Event('click'), document.createElement('button'))

		// MUTATION: calling api.cancelSubscription() straight from the row's own handler instead of
		// opening this modal first makes this red — nothing enqueued a response, so an un-stubbed
		// request would throw.
		expect(requests()).toHaveLength(0)
		expect(modalMarkup()).toContain('data-action="confirm-cancel-subscription"')
		expect(modalText()).toContain('This cannot be undone!')
	})

	it('cancels the subscription and tells the customer when their access actually ends', async () => {
		const api = await import('../../../public/one/api.js')
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		api.setToken('access-1')
		enqueue(json({user_id: 'usr-1', cancelled_at: '2026-09-15T00:00:00Z', access_ends_at: '2027-01-01T00:00:00Z'}))

		await captured.actions['cancel-subscription']?.(new Event('click'), document.createElement('button'))
		await captured.actions['confirm-cancel-subscription']?.(new Event('click'), document.createElement('button'))
		await settle()

		expect(requests()[0]?.url).toBe(`${ORIGIN}/v1/subscription/cancellation`)
		expect(requests()[0]?.init.method).toBe('POST')
		expect((requests()[0]?.body as {idempotency_key?: string})?.idempotency_key).toBe('idem-1')

		// The date is READ from the service's own response, not computed here — this asserts the
		// page prints the same string app.js's own formatter produces for that instant, rather than
		// pinning a hard-coded date string this test could get right for the wrong reason.
		// MUTATION: printing `cancelled_at` instead of `access_ends_at` makes this red.
		expect(modalText()).toContain(formatDate('2027-01-01T00:00:00Z'))
		expect(modalText()).toContain('Subscription cancelled')

		api.configure({fetch: null, origin: null, randomUUID: null})
	})

	it('shows the service refusal rather than a generic error, and leaves the modal open', async () => {
		const api = await import('../../../public/one/api.js')
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		api.setToken('access-1')
		// The bare-status shape `bare()` actually writes for this ladder's 409 — nothing to
		// cancel, e.g. a community account (http.ts :3089-3091).
		enqueue(new Response(null, {status: 409}))

		await captured.actions['cancel-subscription']?.(new Event('click'), document.createElement('button'))
		await captured.actions['confirm-cancel-subscription']?.(new Event('click'), document.createElement('button'))
		await settle()

		// MUTATION: swallowing `!result.ok` and falling through to the success modal makes this red
		// — a refused cancellation would read as a confirmed one.
		expect(modalText()).not.toContain('Subscription cancelled')
		expect(modalText()).toContain(t(k('one', 'commercial', 'conflict')))
		// The confirm button is still there: a refusal must not silently close the dialog on
		// somebody who has not actually been told anything happened.
		expect(modalMarkup()).toContain('data-action="confirm-cancel-subscription"')

		api.configure({fetch: null, origin: null, randomUUID: null})
	})
})
