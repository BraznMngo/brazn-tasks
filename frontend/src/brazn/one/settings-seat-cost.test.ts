import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'

import {init as initI18n, t as translate} from '../../../public/one/i18n.js'
import {applyGates, isRefused, setViewState} from '../../../public/one/app.js'
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
 * BRA-1636 ITEM 5, THE PAGE HALF — the total cost and "Next invoice" on the Seats card.
 *
 * The acceptance, asserted as what the administrator sees and what the page sends:
 *
 *   * a monthly organization shows "per month" and a yearly one "per year", and each total is the
 *     catalogue's per-seat price for that period × max(3, seats) — written out by hand below;
 *   * the total moves while − and + are pressed, before anything is confirmed;
 *   * a trial while buying is closed says no invoice is scheduled and shows the first invoice's
 *     amount; a paying organization shows the date and the amount;
 *   * a member who is not the administrator sees neither figure, and the page does not ask;
 *   * with the service unable to answer, the card says the figures cannot be read, never 0.
 *
 * The price arrives from `GET /v1/organizations/seats/cost` (one-apps #1126). Amounts are the
 * English `Intl` currency form of the hand-written minor units. Every sentence is looked up from a
 * literally named catalogue key, never copied (Testing-Rules).
 */

const k = (...parts: string[]) => parts.join('.')

function t(key: string, params?: Parameters<typeof translate>[1]): string {
	const sentence = translate(key, params)
	if (sentence === key) throw new Error(`missing key: ${key}`)
	return sentence
}

type Handler = (event: Event, el: Element) => void | Promise<void>

const captured: {
	render: ((ctx: unknown) => string) | null
	mount: ((root: Element, ctx: unknown) => void) | null
	actions: Record<string, Handler>
} = {render: null, mount: null, actions: {}}

type Facts = Parameters<typeof applyGates>[1]

const fixture: {organization: unknown, facts: Facts} = {organization: null, facts: {} as Facts}

const reloadOrganization = vi.fn(async () => {})

vi.mock('../../../public/one/app.js', async (importOriginal) => {
	const real = await importOriginal<typeof import('../../../public/one/app.js')>()
	return {
		...real,
		getOrganization: () => fixture.organization,
		reloadOrganization: () => reloadOrganization(),
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
	// @ts-expect-error view-settings.js ships no .d.ts, unlike its siblings in the same directory.
	await import('../../../public/one/view-settings.js')
	vi.unstubAllGlobals()

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
	reloadOrganization.mockClear()
	document.body.innerHTML = SHELL
	document.querySelector('.stage')?.classList.remove('hidden')
	const api = await import('../../../public/one/api.js')
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
	api.setToken('access-1')
	// The cost read IS under test, so its status starts unset; the invitations read is not.
	setViewState('settings', {
		pendingInvitesStatus: 'ready', pendingInvites: [], seatDraft: null, seatPurchase: null,
		seatCostStatus: undefined, seatCost: null,
	})
	fixture.facts = ADMIN_FACTS
})

afterEach(async () => {
	document.body.innerHTML = ''
	const api = await import('../../../public/one/api.js')
	api.configure({fetch: null, origin: null, randomUUID: null})
})

/* ------------------------------------------------------------------ *
 * Fixtures — hand-written.
 * ------------------------------------------------------------------ */

const ADMIN = {user_id: 1, name: 'Ada Admin', username: 'ada', email: 'ada@brazn.one'}

function organization(seats: {purchased: number, occupied: number, teams: number}) {
	return {
		id: 'org-1',
		organization_name: 'Brazn GmbH',
		administrator: ADMIN,
		members: [ADMIN],
		teams: [],
		seats_purchased: seats.purchased,
		seats_occupied: seats.occupied,
		teams_used: seats.teams,
		teams_allowed: Math.floor(seats.purchased / 3),
		seats_per_team: 3,
		can_create_team: true,
	}
}

/** A `SeatCost` body as one-apps #1126 answers it. */
function cost(overrides: Record<string, unknown> = {}) {
	return {
		organization_id: 'org-1',
		cadence: 'monthly',
		seats: 5,
		billed_seats: 5,
		per_seat_minor: 500,
		currency: 'eur',
		tax_inclusive: true,
		total_minor: 2500,
		next_invoice: {status: 'not_scheduled_beta'},
		...overrides,
	}
}

const COST_URL = `${ORIGIN}/v1/organizations/seats/cost?organization_id=org-1`

/* ------------------------------------------------------------------ *
 * What a person sees.
 * ------------------------------------------------------------------ */

function norm(text: string | null | undefined): string {
	return (text ?? '').replace(/\s+/g, ' ').trim()
}

function button(label: string): HTMLButtonElement {
	const el = [...document.querySelectorAll('#app button')]
		.find((candidate) => candidate.getAttribute('aria-label') === label || norm(candidate.textContent) === label)
	if (!(el instanceof HTMLButtonElement)) throw new Error(`no button reads "${label}"`)
	return el
}

const FEWER = () => t(k('organization', 'seats', 'fewer'))
const MORE = () => t(k('organization', 'seats', 'more'))
const ADAPT = () => t(k('organization', 'seats', 'adapt'))

/** The text of the Seats card. */
function seatsCard(): string {
	return norm(button(FEWER()).closest('.card')?.textContent)
}

function total(): string {
	return norm(document.getElementById('seatTotal')?.textContent)
}

function nextInvoice(): string {
	return norm(document.getElementById('seatNextInvoice')?.textContent)
}

async function press(el: HTMLButtonElement): Promise<void> {
	el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}))
	await pending
	await settle()
}

/** Open the Team management tab and let the cost read answer. */
async function open(): Promise<void> {
	draw()
	await settle()
}

const monthly = (amount: string) => t(k('one', 'org', 'seats', 'costMonthly'), {amount})
const annual = (amount: string) => t(k('one', 'org', 'seats', 'costAnnual'), {amount})
const UNREADABLE = () => t(k('one', 'org', 'seats', 'costUnreadable'))

/* ------------------------------------------------------------------ *
 * The total.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 5 — the total', () => {
	it('a monthly organization: €5 a seat × 5 seats reads "€25.00 per month", from one read of the cost route', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost()))
		await open()

		expect(requests().map((call) => call.url)).toEqual([COST_URL])
		expect(requests()[0]?.init.method).toBe('GET')
		// MUTATION: dropping `seatCostBlock` from `seatsCard` makes this red.
		expect(total()).toBe(monthly('€25.00'))
	})

	it('a yearly organization: €50 a seat × 3 seats reads "€150.00 per year", the yearly price as the catalogue gives it', async () => {
		fixture.organization = organization({purchased: 3, occupied: 3, teams: 1})
		// €50 is the yearly catalogue price, not 12 × a monthly one (Commercial-Rules §2.2).
		enqueue(json(cost({cadence: 'annual', seats: 3, billed_seats: 3, per_seat_minor: 5000, total_minor: 15000})))
		await open()

		// MUTATION: always using `costMonthly` makes this red.
		expect(total()).toBe(annual('€150.00'))
		expect(seatsCard()).not.toContain(monthly('€150.00'))
	})

	it('moves with − and + before anything is confirmed, and sends nothing for a press', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost()))
		await open()
		expect(total()).toBe(monthly('€25.00'))

		await press(button(MORE()))
		// MUTATION: drawing the total from `total_minor` (today's seats) instead of the number on
		// screen makes this red.
		expect(total()).toBe(monthly('€30.00'))

		await press(button(FEWER()))
		await press(button(FEWER()))
		await press(button(FEWER()))
		expect(total()).toBe(monthly('€15.00'))
		// Only the cost read reached the server: no press quotes, buys or re-reads.
		expect(requests()).toHaveLength(1)
	})

	it('an organization holding fewer than 3 seats is billed for 3', async () => {
		fixture.organization = organization({purchased: 2, occupied: 1, teams: 0})
		enqueue(json(cost({seats: 2, billed_seats: 3, total_minor: 1500})))
		await open()

		// MUTATION: dropping `Math.max(SEAT_FLOOR, …)` makes this red (it would read €10.00).
		expect(total()).toBe(monthly('€15.00'))
	})

	it('says tax is added when the price does not include it, and only then', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost({currency: 'usd', tax_inclusive: false})))
		await open()

		expect(total()).toBe(monthly('$25.00'))
		// MUTATION: dropping the `taxInclusive` branch makes this red.
		expect(seatsCard()).toContain(t(k('one', 'org', 'seats', 'taxAdded')))
	})

	it('a price that includes tax says nothing about tax', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost()))
		await open()

		expect(seatsCard()).not.toContain(t(k('one', 'org', 'seats', 'taxAdded')))
	})
})

/* ------------------------------------------------------------------ *
 * Next invoice.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 5 — next invoice', () => {
	it('a trial while buying is closed: no invoice during the beta, and the first invoice\'s amount', async () => {
		fixture.organization = organization({purchased: 3, occupied: 3, teams: 1})
		enqueue(json(cost({seats: 3, billed_seats: 3, total_minor: 1500, next_invoice: {status: 'not_scheduled_beta'}})))
		await open()

		expect(seatsCard()).toContain(t(k('one', 'org', 'seats', 'nextInvoice')))
		expect(nextInvoice()).toBe(t(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€15.00'}))
	})

	it('a trial while buying is open: no invoice until the administrator pays, and its amount', async () => {
		fixture.organization = organization({purchased: 3, occupied: 3, teams: 1})
		enqueue(json(cost({seats: 3, billed_seats: 3, total_minor: 1500, next_invoice: {status: 'awaiting_purchase'}})))
		await open()

		expect(nextInvoice()).toBe(t(k('one', 'org', 'seats', 'nextInvoiceAwaiting'), {amount: '€15.00'}))
	})

	it('a paying organization: the date and the amount, for the seats held today and not the number on screen', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		// Midday UTC, so the calendar day is the same in every time zone the runner may use.
		enqueue(json(cost({next_invoice: {status: 'scheduled', date: '2026-11-15T12:00:00Z'}})))
		await open()

		const line = nextInvoice()
		expect(line).toContain('€25.00')
		expect(line).toMatch(/2026/)
		expect(line).toMatch(/15/)

		await press(button(MORE()))
		// The next invoice bills what is held when it is raised, and the extra seat is not held yet.
		expect(nextInvoice()).toBe(line)
	})

	it('an organization that is not charged says so and shows no amount', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost({next_invoice: {status: 'not_charged'}})))
		await open()

		expect(nextInvoice()).toBe(t(k('one', 'org', 'seats', 'nextInvoiceNotCharged')))
	})

	it('reads the cost again after a confirmed change', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost()))
		await open()

		await press(button(FEWER()))
		await press(button(ADAPT()))
		enqueue(json({organization_id: 'org-1', outcome: 'changed', seats: 4, previous_seats: 5, users: 3, active_teams: 1}))
		const confirm = [...document.querySelectorAll('#modalRoot .modal-foot button')]
			.find((candidate) => norm(candidate.textContent) === ADAPT())
		if (!(confirm instanceof HTMLButtonElement)) throw new Error('no confirm button')
		await press(confirm)
		expect(reloadOrganization).toHaveBeenCalledTimes(1)

		// app.js `reloadOrganization` ends with a render; this file draws it.
		fixture.organization = organization({purchased: 4, occupied: 3, teams: 1})
		enqueue(json(cost({seats: 4, billed_seats: 4, total_minor: 2000})))
		await open()

		// MUTATION: not clearing `seatCostStatus` in `confirm-seats` makes this red — the card
		// would keep the figures read before the change.
		expect(requests().filter((call) => call.url === COST_URL)).toHaveLength(2)
		expect(nextInvoice()).toBe(t(k('one', 'org', 'seats', 'nextInvoiceBeta'), {amount: '€20.00'}))
	})
})

/* ------------------------------------------------------------------ *
 * Who sees it, and what a failure shows.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 5 — never a zero or a guess', () => {
	it('a member who is not the administrator sees neither figure, and the page does not ask', async () => {
		fixture.facts = MEMBER_FACTS
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		await open()

		// MUTATION: dropping the `orgAdmin` check from mount makes this red — the request goes out
		// with nothing queued for it.
		expect(requests()).toHaveLength(0)
		expect(norm(document.getElementById('app')?.textContent)).not.toContain(t(k('one', 'org', 'seats', 'nextInvoice')))
	})

	it('a 403 from the service shows nothing, not the unreadable sentence', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(new Response(null, {status: 403}))
		await open()

		expect(document.getElementById('seatTotal')).toBeNull()
		expect(seatsCard()).not.toContain(UNREADABLE())
	})

	it('a price the service cannot read (503) says the figures cannot be read, and shows no amount', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json({error: 'price_unreadable'}, 503))
		await open()

		expect(seatsCard()).toContain(UNREADABLE())
		expect(document.getElementById('seatTotal')).toBeNull()
		expect(seatsCard()).not.toMatch(/€/)
	})

	it('an unreachable service says the same', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		// Nothing queued: the stub throws, as a failed network request does.
		await open()

		expect(seatsCard()).toContain(UNREADABLE())
		expect(seatsCard()).not.toMatch(/€/)
	})

	it.each([
		['no per-seat price', {per_seat_minor: undefined}],
		['a per-seat price that is not a whole number of cents', {per_seat_minor: 4.5}],
		['an unknown billing period', {cadence: 'weekly'}],
		['no currency', {currency: ''}],
		['no answer about tax', {tax_inclusive: undefined}],
		['an unknown invoice state', {next_invoice: {status: 'soon'}}],
		['a scheduled invoice with no date', {next_invoice: {status: 'scheduled'}}],
	])('a body with %s says the figures cannot be read, and shows no amount', async (_label, overrides) => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost(overrides)))
		await open()

		// MUTATION: dropping the matching check from `readSeatCost` makes this row red — the card
		// would draw a total from a part of the answer.
		expect(seatsCard()).toContain(UNREADABLE())
		expect(document.getElementById('seatTotal')).toBeNull()
	})

	it('shows nothing while the read is under way', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		enqueue(json(cost()))
		draw()

		// Before the answer: neither a figure nor the unreadable sentence.
		expect(document.getElementById('seatTotal')).toBeNull()
		expect(seatsCard()).not.toContain(UNREADABLE())
		await settle()
		expect(total()).toBe(monthly('€25.00'))
	})
})
