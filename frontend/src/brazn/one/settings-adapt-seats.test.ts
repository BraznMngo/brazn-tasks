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
 * BRA-1636 ITEM 4 — one "Adapt seats" control with − and +.
 *
 * The acceptance, asserted as what a person sees and what the page sends:
 *
 *   * 5 seats, 3 members: press − twice and confirm. Seats become 3, and the confirm said nothing
 *     is refunded and the change applies from the next invoice;
 *   * − is disabled at the lowest number the server accepts and the page says why; at 100, + is
 *     disabled and the page says why;
 *   * press + three times and confirm: seats rise by three, and the confirm showed the pro-rata
 *     amount from the server, or "nothing now" in a trial;
 *   * a reduction the server refuses shows the existing refusal sentence, and nothing changes;
 *   * no press buys or removes a seat: only the confirm does;
 *   * a member who is not the administrator sees no control.
 *
 * Every expected number is written out by hand from the fixtures, never computed by the code
 * under test. Every sentence a person reads is compared through the catalogue lookup of a
 * literally named key, never as copied words (Testing-Rules: "Never assert a sentence a customer
 * reads"). Where the ticket demands that a sentence SAYS something (nothing refunded, next
 * invoice, nobody loses access), the property is checked on the English catalogue value by a
 * loose pattern, so a writer can still reword it.
 */

const k = (...parts: string[]) => parts.join('.')

/**
 * The catalogue sentence for a key. The real t() answers a missing key with the key itself, which
 * the page would then show too, so a lookup that fails here fails loudly instead.
 */
function t(key: string, params?: Parameters<typeof translate>[1]): string {
	const sentence = translate(key, params)
	if (sentence === key) throw new Error(`missing key: ${key}`)
	return sentence
}

const EN: Record<string, unknown> = JSON.parse(enRaw)

/**
 * A key's English sentence with any value in its placeholders, so a check that a sentence is
 * absent catches it whatever number or amount it was drawn with.
 */
function anyValue(key: string): RegExp {
	const raw = key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], EN)
	if (typeof raw !== 'string') throw new Error(`no English sentence for ${key}`)
	return new RegExp(raw.split(/\{\w+\}/).map((piece) => piece.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.+?'))
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
		// The real one redraws the whole shell from app.js's own state, which this file does not
		// boot. This redraws the settings view the same way app.js `render()` does: render, mount,
		// then gates.
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

/** The Team management tab, drawn the way app.js `render()` draws it. */
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

	// app.js's delegated click listener, mirrored with the real `isRefused`, so a disabled control
	// does nothing here for the same reason it does nothing in the page.
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
	// app.js shows the shell after its first render. This file draws the view itself, so it shows
	// the shell itself, or every control would read as hidden.
	document.querySelector('.stage')?.classList.remove('hidden')
	const api = await import('../../../public/one/api.js')
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
	api.setToken('access-1')
	// The pending-invitations read is not under test: mark it done so mount starts no request.
	setViewState('settings', {pendingInvitesStatus: 'ready', pendingInvites: [], seatDraft: null, seatPurchase: null})
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

// Functions, because the catalogue loads in beforeAll, after this module runs.
const FEWER = () => t(k('organization', 'seats', 'fewer'))
const MORE = () => t(k('organization', 'seats', 'more'))
const ADAPT = () => t(k('organization', 'seats', 'adapt'))

/** The control as a person reads it: −, the number, +, and the button. */
function control(): string {
	const group = [...document.querySelectorAll('#app [role="group"]')]
		.find((candidate) => candidate.getAttribute('aria-label') === ADAPT()) ?? null
	if (group === null) throw new Error('no Adapt seats control')
	return norm(group.textContent)
}

/** The text of the Seats card. */
function seatsCardText(): string {
	const card = button(FEWER()).closest('.card')
	return norm(card?.textContent)
}

function isDisabled(el: HTMLButtonElement): boolean {
	return isRefused(el)
}

function modalText(): string {
	return norm(document.getElementById('modalRoot')?.textContent)
}

function footButton(label: string): HTMLButtonElement {
	const el = [...document.querySelectorAll('#modalRoot .modal-foot button')]
		.find((candidate) => norm(candidate.textContent) === label)
	if (!(el instanceof HTMLButtonElement)) throw new Error(`no footer button reads "${label}"`)
	return el
}

async function press(el: HTMLButtonElement): Promise<void> {
	el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}))
	await pending
	await settle()
}

async function pressTimes(label: string, times: number): Promise<void> {
	for (let i = 0; i < times; i++) await press(button(label))
}

/** The control as it reads with `n` seats chosen: −, the number, +, and the button. */
const shows = (n: number) => `− ${n} + ${ADAPT()}`

const DECREASE = () => t(k('one', 'org', 'seats', 'decrease'))
const inSubscription = (count: number) => t(k('one', 'org', 'seats', 'inSubscription'), {count})

/* ------------------------------------------------------------------ *
 * Decrease.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 4 — reducing seats', () => {
	it('5 seats, 3 members: − twice and confirm makes 3 seats; the confirm says nothing is refunded and it starts with the next invoice', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()

		expect(control()).toBe(shows(5))
		// Nothing to confirm while the number is what the organization already holds.
		expect(isDisabled(button(ADAPT()))).toBe(true)

		await pressTimes(FEWER(), 2)

		expect(control()).toBe(shows(3))
		// MUTATION: buying on each press (calling purchaseSeats or quoteSeats from `seat-step`)
		// makes this red. No press may reach the server.
		expect(requests()).toHaveLength(0)

		await press(button(ADAPT()))
		// A decrease moves no money, so there is no quote to ask for.
		expect(requests()).toHaveLength(0)
		expect(modalText()).toContain(inSubscription(3))
		// MUTATION: dropping `one.org.seats.decrease` from the decrease confirm makes this red.
		expect(modalText()).toContain(DECREASE())
		// The ticket requires these facts in plain words. Loose patterns, not frozen copy.
		expect(DECREASE()).toMatch(/refund/i)
		expect(DECREASE()).toMatch(/next invoice/i)
		expect(DECREASE()).toMatch(/access/i)

		enqueue(json({organization_id: 'org-1', outcome: 'changed', seats: 3, previous_seats: 5, users: 3, active_teams: 1}))
		await press(footButton(ADAPT()))

		expect(requests()).toHaveLength(1)
		expect(requests()[0]?.url).toBe(`${ORIGIN}/v1/organizations/seats`)
		// Absolute, never a delta.
		expect(requests()[0]?.body).toEqual({organization_id: 'org-1', seats: 3, idempotency_key: 'idem-1'})
		expect(modalText()).toBe('')
		expect(norm(document.getElementById('toastRoot')?.textContent)).toBe(t(k('one', 'toast', 'seatsChanged')))
		expect(reloadOrganization).toHaveBeenCalledTimes(1)
	})

	it('with 3 members, − is disabled at 3 and the page says why', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()
		// Not at the limit yet: no sentence.
		expect(seatsCardText()).not.toMatch(anyValue(k('organization', 'seats', 'lowestFloor')))

		await pressTimes(FEWER(), 2)

		// MUTATION: dropping `disabled` from − at the lowest number makes this red.
		expect(isDisabled(button(FEWER()))).toBe(true)
		// 3 is the floor and 3 is the people: the floor names the reason, because removing members
		// would not let the number go lower.
		expect(seatsCardText()).toContain(t(k('organization', 'seats', 'lowestFloor'), {count: 3}))

		// Pressing it anyway changes nothing.
		await press(button(FEWER()))
		expect(control()).toBe(shows(3))
	})

	it('− stops at the people already seated and says to remove members first', async () => {
		fixture.organization = organization({purchased: 6, occupied: 4, teams: 1})
		draw()

		await press(button(FEWER()))
		await press(button(FEWER()))

		// MUTATION: leaving `seats_occupied` out of the lowest number makes this red — the
		// number would go on down to 3 and the server would refuse with `below_users`.
		expect(control()).toBe(shows(4))
		expect(isDisabled(button(FEWER()))).toBe(true)
		expect(seatsCardText()).toContain(t(k('organization', 'seats', 'lowestMembers'), {count: 4}))
	})

	it('− stops at what the existing teams need and says so', async () => {
		fixture.organization = organization({purchased: 8, occupied: 2, teams: 2})
		draw()

		await pressTimes(FEWER(), 3)

		// 2 teams × 3 seats per team = 6, written out by hand.
		// MUTATION: leaving the teams rule out of the lowest number makes this red — the number
		// would go on down to 5 and the server would refuse with `below_active_teams`.
		expect(control()).toBe(shows(6))
		expect(isDisabled(button(FEWER()))).toBe(true)
		expect(seatsCardText()).toContain(t(k('organization', 'seats', 'lowestTeams'), {count: 6}))
	})

	it('a stale − at the limit cannot step past it', async () => {
		fixture.organization = organization({purchased: 3, occupied: 3, teams: 1})
		draw()

		// A press that reaches the handler although the button is disabled, as a button drawn
		// before the organization changed would.
		const stale = document.createElement('button')
		stale.setAttribute('data-step', '-1')
		await captured.actions['seat-step']?.(new Event('click'), stale)

		// MUTATION: deleting the limit check in `seat-step` makes this red — the number reads 2.
		expect(control()).toBe(shows(3))
	})

	it.each([
		['below_users', k('one', 'commercial', 'belowUsers')],
		['below_active_teams', k('one', 'commercial', 'belowActiveTeams')],
	])('a reduction the server refuses (%s) shows its sentence and changes nothing', async (outcome, key) => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()
		await press(button(FEWER()))
		await press(button(ADAPT()))

		enqueue(json({organization_id: 'org-1', outcome, seats: 5, previous_seats: 5, users: 4, active_teams: 1}))
		await press(footButton(ADAPT()))

		expect(requests()).toHaveLength(1)
		// The confirm stays open with the refusal in it.
		expect(modalText()).toContain(t(key))
		expect(norm(document.getElementById('toastRoot')?.textContent)).toBe('')
		expect(reloadOrganization).not.toHaveBeenCalled()
	})
})

/* ------------------------------------------------------------------ *
 * Increase.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 4 — adding seats', () => {
	it('+ three times and confirm raises seats by three, and the confirm shows the pro-rata amount from the server', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()

		await pressTimes(MORE(), 3)

		expect(control()).toBe(shows(8))
		// MUTATION: quoting or buying on each press makes this red.
		expect(requests()).toHaveLength(0)

		enqueue(json({
			organization_id: 'org-1',
			seats: 5,
			seats_after: 8,
			proration: {base_minor: 1234, currency: 'EUR', days_remaining: 12, days_in_period: 30},
		}))
		await press(button(ADAPT()))

		expect(requests()).toHaveLength(1)
		expect(requests()[0]?.url).toBe(`${ORIGIN}/v1/organizations/seats/quote?organization_id=org-1&seats=8`)
		expect(modalText()).toContain(inSubscription(8))
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'prorationDue'), {amount: '€12.34'}))
		expect(modalText()).not.toContain(t(k('one', 'org', 'seats', 'nothingNow')))
		expect(modalText()).not.toContain(DECREASE())

		enqueue(json({organization_id: 'org-1', outcome: 'changed', seats: 8, previous_seats: 5, users: 3, active_teams: 1}))
		await press(footButton(ADAPT()))

		expect(requests()).toHaveLength(2)
		expect(requests()[1]?.body).toEqual({organization_id: 'org-1', seats: 8, idempotency_key: 'idem-1'})
		expect(modalText()).toBe('')
		expect(reloadOrganization).toHaveBeenCalledTimes(1)
	})

	it('in a trial the quote has no amount, and the confirm says nothing is paid now', async () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()
		await press(button(MORE()))

		enqueue(json({organization_id: 'org-1', seats: 5, seats_after: 6, proration: null}))
		await press(button(ADAPT()))

		expect(modalText()).toContain(inSubscription(6))
		// MUTATION: dropping `one.org.seats.nothingNow` from the increase confirm makes this red.
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'nothingNow')))
		expect(modalText()).not.toMatch(anyValue(k('one', 'org', 'seats', 'prorationDue')))
	})

	it('+ is disabled at 100 and the page says why', async () => {
		fixture.organization = organization({purchased: 99, occupied: 3, teams: 1})
		draw()
		expect(seatsCardText()).not.toMatch(anyValue(k('organization', 'seats', 'highest')))

		await press(button(MORE()))

		expect(control()).toBe(shows(100))
		// MUTATION: raising or dropping the ceiling of 100 makes this red.
		expect(isDisabled(button(MORE()))).toBe(true)
		expect(seatsCardText()).toContain(t(k('organization', 'seats', 'highest'), {count: 100}))

		await press(button(MORE()))
		expect(control()).toBe(shows(100))
	})

	it('a stale + at 100 cannot step past it', async () => {
		fixture.organization = organization({purchased: 100, occupied: 3, teams: 1})
		draw()

		const stale = document.createElement('button')
		stale.setAttribute('data-step', '1')
		await captured.actions['seat-step']?.(new Event('click'), stale)

		// MUTATION: deleting the ceiling check in `seat-step` makes this red — the number reads 101.
		expect(control()).toBe(shows(100))
	})
})

/* ------------------------------------------------------------------ *
 * Who sees it.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 4 — only the administrator sees the control', () => {
	it('a member who is not the administrator sees no control', () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		fixture.facts = MEMBER_FACTS
		draw()

		const fewer = button(FEWER())
		// The control's own gate, inside the Seats card, hides it — not only the tab around it.
		// MUTATION: dropping `data-requires="admin write"` from the control makes this red.
		const gate = fewer.closest('[data-requires]')
		expect(gate?.closest('.card')).not.toBeNull()
		expect(gate?.classList.contains('hidden')).toBe(true)
		for (const label of [FEWER(), MORE(), ADAPT()]) {
			expect(button(label).closest('.hidden'), label).not.toBeNull()
		}
	})

	it('the administrator sees it', () => {
		fixture.organization = organization({purchased: 5, occupied: 3, teams: 1})
		draw()

		for (const label of [FEWER(), MORE(), ADAPT()]) {
			expect(button(label).closest('.hidden'), label).toBeNull()
		}
	})
})

/* ------------------------------------------------------------------ *
 * BRA-1636 item 6 — the Add member dialog's seat sentence.
 * ------------------------------------------------------------------ */

describe('BRA-1636 item 6 — the Add member dialog says an invitation uses no seat', () => {
	it('no longer says an invitation needs a free seat before it can be sent', async () => {
		fixture.organization = organization({purchased: 5, occupied: 5, teams: 1})
		draw()
		await captured.actions.invite?.(new Event('click'), document.createElement('button'))

		// Commercial-Rules §3.5: an invitation reserves nothing, and accepting it adds a seat when
		// none is free.
		// MUTATION: restoring the old `organization.seats.explanation` makes these lines red.
		// The old words are a removal check, not frozen copy: any honest rewording passes.
		expect(modalText()).not.toContain('needs a free one')
		const explanation = t(k('organization', 'seats', 'explanation'))
		expect(modalText()).toContain(explanation)
		expect(explanation).toMatch(/invitation[^.]*(does not|doesn't) use a seat/i)
	})
})
