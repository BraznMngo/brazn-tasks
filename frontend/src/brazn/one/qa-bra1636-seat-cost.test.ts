import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'

import {init as initI18n, t as lookupSentence} from '../../../public/one/i18n.js'
import {applyGates, isRefused, setViewState} from '../../../public/one/app.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import deRaw from '../../../public/one/i18n/de-DE.json?raw'
import esRaw from '../../../public/one/i18n/es-ES.json?raw'
import frRaw from '../../../public/one/i18n/fr-FR.json?raw'
import jaRaw from '../../../public/one/i18n/ja-JP.json?raw'
import zhRaw from '../../../public/one/i18n/zh-CN.json?raw'
import settingsHtml from '../../../public/one/settings.html?raw'
import {ORIGIN, enqueue, fetchStub, json, requests, resetHarness, settle} from './auth-page-harness'

/*
 * BRA-1636 ITEM 5 (THE PAGE HALF) — INDEPENDENT QA ACCEPTANCE TESTS.
 *
 * Written from the ticket text (Linear BRA-1636 item 5, and Sebastian's decision of 25 September
 * 2026 that the cost line shows the total for the organisation's own billing period, never a
 * yearly price turned into a monthly one). One block per acceptance point, in the ticket's order:
 *
 *   A1  a monthly organisation shows "per month", a yearly one "per year", and each total equals
 *       the catalogue's per-seat price for that period × max(3, seats);
 *   A2  the total changes while − and + are pressed, before anything is confirmed;
 *   A3  a trial while buying is closed shows that no invoice is scheduled, and the first invoice's
 *       amount;
 *   A4  a member who is not the administrator sees neither figure;
 *   A5  with the commercial service unreachable, the card says the figures cannot be read, and
 *       does not show 0.
 *
 * And the ticket's rules: the price is the service's (none written into the page, the yearly one
 * not worked out from a monthly one), billed seats are max(3, seats), "Next invoice" is the date
 * and the amount, the amount follows what the organisation is charged after a confirmed change,
 * and no invoice amount is stored.
 *
 * Prices and amounts are written out by hand, in minor units for the service and in the English
 * `Intl` currency form for what the reader sees. The test catalogue's prices are €5 a seat a
 * month and €50 a seat a year. Sentences a person reads are compared through the catalogue lookup
 * of a literally named key, never as copied words (Testing-Rules). Where the ticket demands that a
 * sentence SAYS something ("per month", "no invoice is scheduled"), that is checked on the English
 * catalogue value by a loose pattern, so a writer can still reword it.
 *
 * Each guard names the production edit that turns it red. Every one of those edits was made, run
 * and reverted during QA (the ledger entry for this review lists them).
 */

const k = (...parts: string[]) => parts.join('.')

/** A catalogue lookup that refuses a missing key, so a `not.toContain` can never pass vacuously. */
function say(key: string, params?: Parameters<typeof lookupSentence>[1]): string {
	const sentence = lookupSentence(key, params)
	if (sentence === key) throw new Error(`missing catalogue key: ${key}`)
	return sentence
}

type Handler = (event: Event, el: Element) => void | Promise<void>
type Facts = Parameters<typeof applyGates>[1]

const captured: {
	render: ((ctx: unknown) => string) | null
	mount: ((root: Element, ctx: unknown) => void) | null
	actions: Record<string, Handler>
} = {render: null, mount: null, actions: {}}

/** `organization` is the fork read now. `afterReload` is what the fork read returns after a reload. */
const fixture: {organization: unknown, afterReload: unknown, facts: Facts} = {
	organization: null,
	afterReload: null,
	facts: {} as Facts,
}

const reloads = {count: 0}

vi.mock('../../../public/one/app.js', async (importOriginal) => {
	const real = await importOriginal<typeof import('../../../public/one/app.js')>()
	return {
		...real,
		getOrganization: () => fixture.organization,
		// As app.js `reloadOrganization` does: read the fork organization again, then render.
		reloadOrganization: async () => {
			reloads.count += 1
			if (fixture.afterReload !== null) fixture.organization = fixture.afterReload
			draw()
		},
		requestRender: () => draw(),
		registerView: (name: string, view: {render: (ctx: unknown) => string, mount: (root: Element, ctx: unknown) => void}) => {
			if (name === 'settings') {
				captured.render = view.render
				captured.mount = view.mount
			}
		},
		registerActions: (map: Record<string, Handler>) => {
			Object.assign(captured.actions, map)
		},
	}
})

const SHELL = (/<body>([\s\S]*)<\/body>/.exec(settingsHtml) ?? ['', ''])[1]
	.replace(/<script[\s\S]*?<\/script>/g, '')

const ADMIN_FACTS: Facts = {orgAdmin: true, hasEdition: true, personalEdition: false, writeRestricted: false, teams: {}}
const MEMBER_FACTS: Facts = {orgAdmin: false, hasEdition: true, personalEdition: false, writeRestricted: false, teams: {}}

/** The Team management tab, drawn as the app draws it: render, mount, then the gates. */
function draw(): void {
	const app = document.getElementById('app')
	if (app === null || captured.render === null || captured.mount === null) throw new Error('no #app or no settings view')
	const ctx = {route: {view: 'settings', tab: 'team'}, facts: fixture.facts}
	app.innerHTML = captured.render(ctx)
	captured.mount(app, ctx)
	applyGates(app, fixture.facts)
}

let pending: Promise<unknown> = Promise.resolve()

beforeAll(async () => {
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
	await initI18n('en', ['en'])
	// @ts-expect-error view-settings.js ships no .d.ts.
	await import('../../../public/one/view-settings.js')
	vi.unstubAllGlobals()

	// A person's click goes through the same refusal check app.js's delegated listener uses.
	document.addEventListener('click', (event) => {
		const target = event.target as Element | null
		const el = typeof target?.closest === 'function' ? target.closest('[data-action]') : null
		if (el === null || isRefused(el)) return
		const handler = captured.actions[el.getAttribute('data-action') ?? '']
		if (handler !== undefined) pending = Promise.resolve(handler(event, el))
	})
})

beforeEach(async () => {
	resetHarness()
	reloads.count = 0
	fixture.afterReload = null
	fixture.facts = ADMIN_FACTS
	document.body.innerHTML = SHELL
	document.querySelector('.stage')?.classList.remove('hidden')
	localStorage.clear()
	sessionStorage.clear()
	const api = await import('../../../public/one/api.js')
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'qa-key'})
	api.setToken('access-qa')
	// A fresh visit: nothing about the cost has been read yet. The invitations read is not under
	// test, so it is marked done and sends nothing.
	setViewState('settings', {
		pendingInvitesStatus: 'ready', pendingInvites: [], seatDraft: null, seatPurchase: null,
		seatCostStatus: undefined, seatCost: undefined,
	})
})

afterEach(async () => {
	document.body.innerHTML = ''
	localStorage.clear()
	sessionStorage.clear()
	const api = await import('../../../public/one/api.js')
	api.configure({fetch: null, origin: null, randomUUID: null})
})

/* -------------------------------- fixtures (hand-written) -------------------------------- */

const ADMIN = {user_id: 7, name: 'Grace Admin', username: 'grace', email: 'grace@example.org'}

/** The fork organization read, which is where the seat number on screen comes from. */
function org(purchased: number, occupied: number, teams: number) {
	return {
		id: 'org-qa',
		organization_name: 'QA Org',
		administrator: ADMIN,
		members: [ADMIN],
		teams: [],
		seats_purchased: purchased,
		seats_occupied: occupied,
		teams_used: teams,
		teams_allowed: Math.floor(purchased / 3),
		seats_per_team: 3,
		can_create_team: true,
	}
}

/**
 * A `GET /v1/organizations/seats/cost` answer, every field given by the caller. The shape is the
 * one-apps `SeatCost` (cloud/service/src/service.ts, #1126); nothing here is computed.
 */
function seatCost(fields: {
	cadence: 'monthly' | 'annual'
	seats: number
	billed_seats: number
	per_seat_minor: number
	total_minor: number
	next_invoice: Record<string, unknown>
	currency?: string
	tax_inclusive?: boolean
}) {
	return {organization_id: 'org-qa', currency: 'eur', tax_inclusive: true, ...fields}
}

const COST_PATH = `${ORIGIN}/v1/organizations/seats/cost`
const costReads = () => requests().filter((call) => call.url.startsWith(COST_PATH))

/* -------------------------------- what a person sees -------------------------------- */

const norm = (s: string | null | undefined) => (s ?? '').replace(/\s+/g, ' ').trim()

/** A button found by its accessible name or its text, inside the page (not the modal). */
function pageButton(name: string): HTMLButtonElement {
	const el = [...document.querySelectorAll('#app button')]
		.find((b) => b.getAttribute('aria-label') === name || norm(b.textContent) === name)
	if (!(el instanceof HTMLButtonElement)) throw new Error(`no button named "${name}"`)
	return el
}

function confirmButton(name: string): HTMLButtonElement {
	const el = [...document.querySelectorAll('#modalRoot button')].find((b) => norm(b.textContent) === name)
	if (!(el instanceof HTMLButtonElement)) throw new Error(`no confirm button named "${name}"`)
	return el
}

const MINUS = () => pageButton(say(k('organization', 'seats', 'fewer')))
const PLUS = () => pageButton(say(k('organization', 'seats', 'more')))
const ADAPT = () => pageButton(say(k('organization', 'seats', 'adapt')))

/** The Seats card, found from the − button that sits on it. */
const seatsCardText = () => norm(MINUS().closest('.card')?.textContent)

/** Everything on the tab a person can see: the text of #app without what the gates hid. */
function visibleText(): string {
	const app = document.getElementById('app')
	if (app === null) return ''
	const copy = app.cloneNode(true) as HTMLElement
	copy.querySelectorAll('.hidden').forEach((el) => el.remove())
	return norm(copy.textContent)
}

async function click(el: HTMLButtonElement): Promise<void> {
	el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}))
	await pending
	await settle()
}

async function clickTimes(get: () => HTMLButtonElement, n: number): Promise<void> {
	for (let i = 0; i < n; i++) await click(get())
}

/** Open the Team management tab and let the cost read answer. */
async function openTeamTab(): Promise<void> {
	draw()
	await settle()
}

const perMonth = (amount: string) => say(k('one', 'org', 'seats', 'costMonthly'), {amount})
const perYear = (amount: string) => say(k('one', 'org', 'seats', 'costAnnual'), {amount})
const NEXT_INVOICE = () => say(k('one', 'org', 'seats', 'nextInvoice'))
const UNREADABLE = () => say(k('one', 'org', 'seats', 'costUnreadable'))

/** Any money a card could show: a currency sign, or a zero amount written either way. */
const ANY_MONEY = /[€$£¥]|\b0[.,]00\b/

/* ================================ A1: per month, per year, and the total ================================ */

describe('QA BRA-1636 item 5 — A1: the total for the organisation\'s own billing period', () => {
	it('a monthly Teams organisation with 5 seats at €5 a seat shows €25.00 per month', async () => {
		fixture.organization = org(5, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()

		// One read of the seat cost route, for this organisation, and nothing else.
		expect(requests()).toHaveLength(1)
		const read = new URL(requests()[0]?.url ?? '')
		expect(read.origin + read.pathname).toBe(COST_PATH)
		expect(read.searchParams.get('organization_id')).toBe('org-qa')
		expect(requests()[0]?.init.method ?? 'GET').toBe('GET')

		// MUTATION: dropping `seatCostBlock(meter)` from `seatsCard` makes this red.
		expect(seatsCardText()).toContain(perMonth('€25.00'))
		// MUTATION: always choosing `costAnnual` makes this red.
		expect(seatsCardText()).not.toContain(perYear('€25.00'))
	})

	it('a yearly Teams organisation with 4 seats at €50 a seat shows €200.00 per year, never a monthly figure', async () => {
		fixture.organization = org(4, 3, 1)
		enqueue(json(seatCost({
			cadence: 'annual', seats: 4, billed_seats: 4, per_seat_minor: 5000, total_minor: 20000,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()

		// MUTATION: always choosing `costMonthly` makes this red.
		expect(seatsCardText()).toContain(perYear('€200.00'))
		// MUTATION: showing a yearly price as a monthly one (€200.00 / 12 = €16.67 "per month") makes
		// this red, through the line above as well as these two.
		expect(seatsCardText()).not.toContain(perMonth('€200.00'))
		expect(seatsCardText()).not.toContain('€16.67')
	})

	it('the price is the service\'s, not one written into the page: €4.37 a month × 6 seats is €26.22, €49.99 a year × 3 is €149.97', async () => {
		fixture.organization = org(6, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 6, billed_seats: 6, per_seat_minor: 437, total_minor: 2622,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()
		// MUTATION: writing the €5 / €50 catalogue prices into the page instead of `per_seat_minor`
		// makes this red (it would read €30.00).
		expect(seatsCardText()).toContain(perMonth('€26.22'))

		resetHarness()
		setViewState('settings', {seatDraft: null, seatCostStatus: undefined, seatCost: undefined})
		fixture.organization = org(3, 3, 1)
		enqueue(json(seatCost({
			cadence: 'annual', seats: 3, billed_seats: 3, per_seat_minor: 4999, total_minor: 14997,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()
		// Not 12 × a monthly price: the yearly one is its own catalogue price.
		expect(seatsCardText()).toContain(perYear('€149.97'))
	})

	it('billed seats are max(3, seats): an organisation holding 1 seat at €5 a month shows €15.00 per month', async () => {
		fixture.organization = org(1, 1, 0)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 1, billed_seats: 3, per_seat_minor: 500, total_minor: 1500,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()

		// MUTATION: dropping `Math.max(SEAT_FLOOR, …)` from the total makes this red (it reads €5.00).
		expect(seatsCardText()).toContain(perMonth('€15.00'))
		expect(seatsCardText()).not.toContain(perMonth('€5.00'))
	})

	it('billed seats are max(3, seats) on a yearly plan too: 2 seats at €50 a year shows €150.00 per year', async () => {
		fixture.organization = org(2, 2, 0)
		enqueue(json(seatCost({
			cadence: 'annual', seats: 2, billed_seats: 3, per_seat_minor: 5000, total_minor: 15000,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()

		expect(seatsCardText()).toContain(perYear('€150.00'))
	})

	it('the English sentences say "per month" and "per year" around the amount', () => {
		// The ticket's wording of the two forms, as properties of the catalogue, not frozen copy.
		const month = say(k('one', 'org', 'seats', 'costMonthly'), {amount: '€15.00'})
		const year = say(k('one', 'org', 'seats', 'costAnnual'), {amount: '€150.00'})
		expect(month).toMatch(/€15\.00 per month/)
		expect(year).toMatch(/€150\.00 per year/)
	})
})

/* ================================ A2: the total moves with − and + ================================ */

describe('QA BRA-1636 item 5 — A2: the total moves while − and + are pressed, before anything is confirmed', () => {
	it('monthly, 5 seats at €5: + twice reads €35.00, − four times reads €15.00, and nothing is sent', async () => {
		fixture.organization = org(5, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()
		expect(seatsCardText()).toContain(perMonth('€25.00'))

		await click(PLUS())
		// MUTATION: drawing the total from `total_minor` (the seats held today) instead of the
		// number on screen makes this red.
		expect(seatsCardText()).toContain(perMonth('€30.00'))
		expect(seatsCardText()).not.toContain(perMonth('€25.00'))
		await click(PLUS())
		expect(seatsCardText()).toContain(perMonth('€35.00'))

		await clickTimes(MINUS, 4)
		expect(seatsCardText()).toContain(perMonth('€15.00'))

		// Before anything is confirmed: no confirm step is open, and the only request is the read.
		// MUTATION: re-reading or quoting the cost on each press makes this red.
		expect(norm(document.getElementById('modalRoot')?.textContent)).toBe('')
		expect(requests()).toHaveLength(1)
		expect(reloads.count).toBe(0)
	})

	it('yearly, 3 seats at €50: + once reads €200.00 per year', async () => {
		fixture.organization = org(3, 3, 1)
		enqueue(json(seatCost({
			cadence: 'annual', seats: 3, billed_seats: 3, per_seat_minor: 5000, total_minor: 15000,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()
		expect(seatsCardText()).toContain(perYear('€150.00'))

		await click(PLUS())
		expect(seatsCardText()).toContain(perYear('€200.00'))
		expect(requests()).toHaveLength(1)
	})
})

/* ================================ A3 and "Next invoice": the date and the amount ================================ */

describe('QA BRA-1636 item 5 — A3: next invoice', () => {
	it('a trial while buying is closed: no invoice is scheduled during the beta, and the first invoice is €15.00', async () => {
		fixture.organization = org(3, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 3, billed_seats: 3, per_seat_minor: 500, total_minor: 1500,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()

		const card = seatsCardText()
		expect(card).toContain(NEXT_INVOICE())
		// MUTATION: showing `nextInvoiceAwaiting`, or the beta sentence without its amount, makes this red.
		expect(card).toContain(say(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€15.00'}))
	})

	it('the English beta sentence says no invoice is scheduled during the beta, and carries the amount', () => {
		const sentence = say(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€15.00'})
		expect(sentence).toMatch(/no invoice/i)
		expect(sentence).toMatch(/beta/i)
		expect(sentence).toContain('€15.00')
	})

	it('a paying organisation: the date and the amount (15 November 2026, €25.00)', async () => {
		fixture.organization = org(5, 3, 1)
		// Midday UTC, so the calendar day is the 15th in every time zone the runner may use.
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'scheduled', date: '2026-11-15T12:00:00Z'},
		})))
		await openTeamTab()

		const card = seatsCardText()
		expect(card).toContain(NEXT_INVOICE())
		// The line after the "Next invoice" heading names the day, the year and the amount.
		const line = card.slice(card.indexOf(NEXT_INVOICE()) + NEXT_INVOICE().length)
		// MUTATION: leaving the date out of the scheduled line makes this red.
		expect(line).toMatch(/\b15\b/)
		expect(line).toMatch(/2026/)
		expect(line).toMatch(/\b11\b|Nov/)
		expect(line).toContain('€25.00')
		// A paying organisation is not told it is in the beta.
		expect(card).not.toContain(say(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€25.00'}))
	})

	it('after a confirmed change the next invoice is what the organisation is now charged (5 → 4 seats: €20.00)', async () => {
		fixture.organization = org(5, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'not_scheduled_beta'},
		})))
		await openTeamTab()
		expect(seatsCardText()).toContain(say(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€25.00'}))

		await click(MINUS())
		await click(ADAPT())
		enqueue(
			json({organization_id: 'org-qa', outcome: 'changed', seats: 4, previous_seats: 5, users: 3, active_teams: 1}),
			json(seatCost({
				cadence: 'monthly', seats: 4, billed_seats: 4, per_seat_minor: 500, total_minor: 2000,
				next_invoice: {status: 'not_scheduled_beta'},
			})),
		)
		fixture.afterReload = org(4, 3, 1)
		await click(confirmButton(say(k('organization', 'seats', 'adapt'))))

		expect(reloads.count).toBe(1)
		// MUTATION: not clearing `seatCostStatus` in `confirm-seats` makes this red: the card keeps
		// the €25.00 read before the change and no second read is sent.
		expect(costReads()).toHaveLength(2)
		expect(seatsCardText()).toContain(say(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€20.00'}))
		expect(seatsCardText()).not.toContain('€25.00')
		expect(seatsCardText()).toContain(perMonth('€20.00'))
	})
})

/* ================================ A4: only the administrator sees either figure ================================ */

describe('QA BRA-1636 item 5 — A4: a member who is not the administrator sees neither figure', () => {
	it('a Teams member: the page does not ask for the cost and shows no figure and no invoice', async () => {
		fixture.facts = MEMBER_FACTS
		fixture.organization = org(5, 3, 1)
		// An answer is queued anyway, so a page that asked would have figures to draw.
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'scheduled', date: '2026-11-15T12:00:00Z'},
		})))
		await openTeamTab()

		// MUTATION: dropping the `orgAdmin` check before `ensureSeatCost()` in mount makes this red.
		expect(costReads()).toHaveLength(0)
		const seen = visibleText()
		expect(seen).not.toMatch(ANY_MONEY)
		expect(seen).not.toContain(NEXT_INVOICE())
		expect(seen).not.toContain(perMonth('€25.00'))
	})

	it('a person the service does not take for the administrator (403): neither figure, no invoice', async () => {
		// The fork says administrator, the commercial service answers 403: the service decides.
		fixture.organization = org(5, 3, 1)
		enqueue(new Response(null, {status: 403}))
		await openTeamTab()

		expect(costReads()).toHaveLength(1)
		// MUTATION: falling back to a written-in price when the read does not answer makes this red.
		const card = seatsCardText()
		expect(card).not.toMatch(ANY_MONEY)
		expect(card).not.toContain(NEXT_INVOICE())
	})
})

/* ================================ A5: never a zero or a guess ================================ */

describe('QA BRA-1636 item 5 — A5: when the service cannot answer, the card says so and shows no 0', () => {
	const UNANSWERED: Array<[string, (() => Response) | null]> = [
		['the service is unreachable (the request fails)', null],
		['the price cannot be read (503 price_unreadable)', () => json({error: 'price_unreadable'}, 503)],
		['the service is unavailable (bare 503)', () => new Response(null, {status: 503})],
		['the service fails (500)', () => new Response(null, {status: 500})],
		['the route is not deployed (404)', () => new Response('not found', {status: 404})],
		['the address answers the app shell (200 text/html)', () => new Response('<!doctype html><html></html>', {status: 200, headers: {'content-type': 'text/html'}})],
	]

	it.each(UNANSWERED)('%s: the card says the figures cannot be read, and shows no amount', async (_label, answer) => {
		fixture.organization = org(5, 3, 1)
		// Nothing queued means the request rejects, as a network failure does.
		if (answer !== null) enqueue(answer())
		await openTeamTab()

		expect(costReads()).toHaveLength(1)
		const card = seatsCardText()
		// MUTATION: rendering nothing for a failed read (`return ''` for status `failed`) makes this red.
		expect(card).toContain(UNREADABLE())
		// MUTATION: drawing the figures from zeros, or from a written-in price, when the read fails
		// makes this red.
		expect(card).not.toMatch(ANY_MONEY)
		expect(card).not.toContain(perMonth('€0.00'))
	})

	it.each([
		['no per-seat price', {cadence: 'monthly', seats: 5, billed_seats: 5, total_minor: 2500, next_invoice: {status: 'not_scheduled_beta'}}],
		['no total', {cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, next_invoice: {status: 'not_scheduled_beta'}}],
		['a per-seat price as text', {cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: '5.00', total_minor: 2500, next_invoice: {status: 'not_scheduled_beta'}}],
		['no billing period', {seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500, next_invoice: {status: 'not_scheduled_beta'}}],
		['no next invoice', {cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500}],
	])('an answer with %s is not shown in part: the card says the figures cannot be read', async (_label, body) => {
		fixture.organization = org(5, 3, 1)
		enqueue(json({organization_id: 'org-qa', currency: 'eur', tax_inclusive: true, ...body}))
		await openTeamTab()

		// MUTATION: reading a missing per-seat price or total as 0 in `readSeatCost` makes the first
		// three rows red; dropping its cadence check makes "no billing period" red. NOT A GUARD for
		// the `next_invoice` check: with that check gone, the "no next invoice" row stays green,
		// because the throw that follows lands in `ensureSeatCost`'s catch, which also says the
		// figures cannot be read. The row holds the behaviour, not that check.
		expect(seatsCardText()).toContain(UNREADABLE())
		expect(seatsCardText()).not.toMatch(ANY_MONEY)
	})

	it('the English sentence says the figures cannot be read', () => {
		expect(UNREADABLE()).toMatch(/cannot be read|can't be read|cannot read|unable to read/i)
	})
})

/* ================================ the rule: no invoice amount is stored ================================ */

describe('QA BRA-1636 item 5 — no invoice amount is stored', () => {
	it('after the cost is read and − / + pressed, nothing the browser keeps holds an amount', async () => {
		fixture.organization = org(5, 3, 1)
		enqueue(json(seatCost({
			cadence: 'monthly', seats: 5, billed_seats: 5, per_seat_minor: 500, total_minor: 2500,
			next_invoice: {status: 'scheduled', date: '2026-11-15T12:00:00Z'},
		})))
		await openTeamTab()
		await click(PLUS())
		expect(seatsCardText()).toContain(perMonth('€30.00'))

		const kept = [localStorage, sessionStorage]
			.flatMap((store) => Object.keys(store).map((key) => `${key}=${store.getItem(key) ?? ''}`))
			.concat(document.cookie)
			.join('\n')
		// MUTATION: writing the read into localStorage (`localStorage.setItem('seatCost', …)`) makes this red.
		expect(kept).not.toMatch(/2500|25\.00|3000|30\.00|total_minor|totalMinor|per_seat/)
	})
})

/* ================================ the sentences exist in all six languages ================================ */

type Catalogue = Record<string, unknown>
const CATALOGUES: Array<[string, Catalogue]> = [
	['en', JSON.parse(enRaw)],
	['de-DE', JSON.parse(deRaw)],
	['es-ES', JSON.parse(esRaw)],
	['fr-FR', JSON.parse(frRaw)],
	['ja-JP', JSON.parse(jaRaw)],
	['zh-CN', JSON.parse(zhRaw)],
]

function lookup(cat: Catalogue, path: string): unknown {
	return path.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], cat)
}

/** Each sentence the card shows, and the placeholders the figure depends on. */
const CARD_SENTENCES: Array<[string, string[]]> = [
	['one.org.seats.costMonthly', ['{amount}']],
	['one.org.seats.costAnnual', ['{amount}']],
	['one.org.seats.nextInvoice', []],
	['one.org.seats.nextInvoiceOn', ['{date}', '{amount}']],
	['one.org.seats.nextInvoiceBeta', ['{amount}']],
	['one.org.seats.costUnreadable', []],
]

describe('QA BRA-1636 item 5 — every sentence of the card exists in all six languages', () => {
	it.each(CATALOGUES)('%s carries each sentence, with the placeholder its figure needs', (lang, cat) => {
		for (const [key, markers] of CARD_SENTENCES) {
			const value = lookup(cat, key)
			// MUTATION: deleting `costAnnual` from ja-JP.json, or `{amount}` from its value, makes this red.
			expect(typeof value === 'string' && value.trim() !== '', `${lang} ${key}`).toBe(true)
			for (const marker of markers) expect(value as string, `${lang} ${key}`).toContain(marker)
		}
	})

	it('the English sentences keep the prose standard: no em dash, en dash or semicolon, no sentence over 20 words', () => {
		const en = JSON.parse(enRaw) as Catalogue
		for (const [key] of CARD_SENTENCES) {
			const value = String(lookup(en, key))
			expect(value, key).not.toMatch(/[—–;]/)
			for (const sentence of value.split(/(?<=[.!?])\s+/)) {
				expect(sentence.split(/\s+/).length, `${key}: ${sentence}`).toBeLessThanOrEqual(20)
			}
		}
	})
})
