import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'

import {init as initI18n, t} from '../../../public/one/i18n.js'
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
 * BRA-1636 ITEMS 4 AND 6 — INDEPENDENT QA ACCEPTANCE TESTS.
 *
 * Written from the ticket text (Sebastian, 25 September 2026), one block per acceptance point.
 * Numbers and amounts are written out by hand. Sentences a person reads are compared through the
 * catalogue lookup of a literally named key, never as copied words (Testing-Rules: "Never assert
 * a sentence a customer reads"). Where the ticket demands that a sentence SAYS something (nothing
 * refunded, next invoice), the property is checked on the English catalogue value by a loose
 * pattern, so a writer can still reword it.
 *
 * Each guard names the production edit that turns it red. Every one of those edits was made,
 * run, and reverted during QA.
 */

const k = (...parts: string[]) => parts.join('.')

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
		// The fork organization endpoint is the source of the seat number. A reload here reads the
		// next fork answer and redraws, as app.js `reloadOrganization` does.
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

	// A person's click goes through the same refusal check app.js's delegated listener uses, so a
	// disabled button does nothing here for the same reason it does nothing in the page.
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
	const api = await import('../../../public/one/api.js')
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'qa-key'})
	api.setToken('access-qa')
	// The seat cost read (BRA-1636 item 5) has its own tests; marked started so mount sends nothing.
	setViewState('settings', {pendingInvitesStatus: 'ready', pendingInvites: [], seatDraft: null, seatPurchase: null, seatCostStatus: 'loading'})
})

afterEach(async () => {
	document.body.innerHTML = ''
	const api = await import('../../../public/one/api.js')
	api.configure({fetch: null, origin: null, randomUUID: null})
})

/* -------------------------------- fixtures (hand-written) -------------------------------- */

const ADMIN = {user_id: 7, name: 'Grace Admin', username: 'grace', email: 'grace@example.org'}

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

/* -------------------------------- what a person sees -------------------------------- */

const norm = (s: string | null | undefined) => (s ?? '').replace(/\s+/g, ' ').trim()

/** A visible button found by its accessible name or its text, inside the page (not the modal). */
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

const MINUS = () => pageButton(t(k('organization', 'seats', 'fewer')))
const PLUS = () => pageButton(t(k('organization', 'seats', 'more')))
const ADAPT = () => pageButton(t(k('organization', 'seats', 'adapt')))

/**
 * Every request but the seat cost read. BRA-1636 item 5 reads the cost again in the render after
 * a confirmed change; it is a read, has its own tests, and is not what these counts are about.
 */
function seatRequests() {
	return requests().filter((call) => !call.url.startsWith(`${ORIGIN}/v1/organizations/seats/cost`))
}

/** The number between − and +, as read on screen. */
function shownNumber(): string {
	const group = MINUS().parentElement
	if (group === null) throw new Error('− has no parent')
	const texts = [...group.children].map((c) => norm(c.textContent))
	const i = texts.indexOf(norm(MINUS().textContent))
	return texts[i + 1] ?? ''
}

const cardText = () => norm(MINUS().closest('.card')?.textContent)
const modalText = () => norm(document.getElementById('modalRoot')?.textContent)
const toastText = () => norm(document.getElementById('toastRoot')?.textContent)

async function click(el: HTMLButtonElement): Promise<void> {
	el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}))
	await pending
	await settle()
}

async function clickTimes(get: () => HTMLButtonElement, n: number): Promise<void> {
	for (let i = 0; i < n; i++) await click(get())
}

/* ================================ ITEM 4 ================================ */

describe('QA BRA-1636 item 4 — one Adapt seats control replaces Add seats', () => {
	it('shows −, the number, + in that order, and no Add seats button', () => {
		fixture.organization = org(5, 3, 1)
		draw()
		const group = MINUS().parentElement as HTMLElement
		const order = [...group.querySelectorAll('button, strong, span, output')].map((c) => norm(c.textContent))
		// MUTATION: putting the old `add-seat` button back makes the second assertion red.
		expect(order.slice(0, 3)).toEqual(['−', '5', '+'])
		expect(document.querySelector('[data-action="add-seat"]')).toBeNull()
		expect([...document.querySelectorAll('#app button')].map((b) => norm(b.textContent))).not.toContain('Add seats')
	})
})

describe('QA BRA-1636 item 4 — acceptance: decrease 5 → 3 with 3 members', () => {
	it('− twice and confirm: one request with seats 3, the confirm says nothing is refunded and it applies from the next invoice, seats read 3', async () => {
		fixture.organization = org(5, 3, 1)
		draw()

		await clickTimes(MINUS, 2)
		expect(shownNumber()).toBe('3')
		// MUTATION: calling api.purchaseSeats or api.quoteSeats from `seat-step` makes this red.
		expect(requests()).toHaveLength(0)

		await click(ADAPT())
		// MUTATION: quoting a decrease (removing the `seats < meter.purchased` early return) makes
		// this red: an unstubbed quote request is recorded.
		expect(requests()).toHaveLength(0)
		// MUTATION: showing `one.org.seats.nothingNow` instead of `one.org.seats.decrease` on a
		// decrease makes this red.
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'decrease')))
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'inSubscription'), {count: 3}))

		enqueue(json({organization_id: 'org-qa', outcome: 'changed', seats: 3, previous_seats: 5, users: 3, active_teams: 1}))
		fixture.afterReload = org(3, 3, 1)
		await click(confirmButton(t(k('organization', 'seats', 'adapt'))))

		// Confirmed once: exactly one write, absolute.
		// MUTATION: sending `meter.purchased - 1` (a delta-style step) in confirm-seats makes this red.
		expect(seatRequests()).toHaveLength(1)
		expect(requests()[0]?.url).toBe(`${ORIGIN}/v1/organizations/seats`)
		expect(requests()[0]?.init.method).toBe('POST')
		expect((requests()[0]?.body as {seats?: number}).seats).toBe(3)
		expect(modalText()).toBe('')
		expect(reloads.count).toBe(1)
		expect(shownNumber()).toBe('3')
		expect(cardText()).toContain(t(k('one', 'org', 'seats', 'inSubscription'), {count: 3}))
	})

	it('the decrease sentence, in English, says nothing is refunded, it applies from the next invoice, and nobody loses access', () => {
		// The ticket requires these three facts in plain words. Loose patterns, not frozen copy.
		const sentence = t(k('one', 'org', 'seats', 'decrease'))
		expect(sentence).toMatch(/refund/i)
		expect(sentence).toMatch(/next invoice/i)
		expect(sentence).toMatch(/access/i)
	})
})

describe('QA BRA-1636 item 4 — limits', () => {
	it('with 3 members, − is disabled at 3 and the card says why', async () => {
		fixture.organization = org(4, 3, 1)
		draw()
		expect(isRefused(MINUS())).toBe(false)
		const reasons = [
			t(k('organization', 'seats', 'lowestFloor'), {count: 3}),
			t(k('organization', 'seats', 'lowestMembers'), {count: 3}),
		]
		expect(reasons.some((r) => cardText().includes(r))).toBe(false)

		await click(MINUS())
		expect(shownNumber()).toBe('3')
		// MUTATION: dropping ` disabled` from − at the lowest number makes this red.
		expect(isRefused(MINUS())).toBe(true)
		// MUTATION: dropping the limit sentences from seatAdapter makes this red.
		expect(reasons.some((r) => cardText().includes(r))).toBe(true)
		await click(MINUS())
		expect(shownNumber()).toBe('3')
		expect(requests()).toHaveLength(0)
	})

	it('− stops at the people already seated (7 of 9 seats, 1 team) and says why', async () => {
		fixture.organization = org(9, 7, 1)
		draw()
		await clickTimes(MINUS, 2)
		// MUTATION: leaving `seats_occupied` out of seatLimits makes this red (the number reaches 6).
		expect(shownNumber()).toBe('7')
		expect(isRefused(MINUS())).toBe(true)
		expect(cardText()).toContain(t(k('organization', 'seats', 'lowestMembers'), {count: 7}))
	})

	it('− stops at what 3 teams need (3 × 3 = 9) and says why', async () => {
		fixture.organization = org(10, 4, 3)
		draw()
		await click(MINUS())
		// MUTATION: leaving the teams rule out of seatLimits makes this red (− stays enabled at 9).
		expect(shownNumber()).toBe('9')
		expect(isRefused(MINUS())).toBe(true)
		expect(cardText()).toContain(t(k('organization', 'seats', 'lowestTeams'), {count: 9}))
	})

	it('− stops at the floor of 3 with only 1 member and no team', async () => {
		fixture.organization = org(4, 1, 0)
		draw()
		await clickTimes(MINUS, 3)
		// MUTATION: changing SEAT_FLOOR to 2 makes this red.
		expect(shownNumber()).toBe('3')
		expect(isRefused(MINUS())).toBe(true)
		expect(cardText()).toContain(t(k('organization', 'seats', 'lowestFloor'), {count: 3}))
	})

	it('at 100 seats + is disabled and the card says why; 98 + twice stops at 100', async () => {
		fixture.organization = org(98, 3, 1)
		draw()
		expect(cardText()).not.toContain(t(k('organization', 'seats', 'highest'), {count: 100}))
		await clickTimes(PLUS, 3)
		// MUTATION: changing SEAT_CEILING to 101 makes this red.
		expect(shownNumber()).toBe('100')
		expect(isRefused(PLUS())).toBe(true)
		expect(cardText()).toContain(t(k('organization', 'seats', 'highest'), {count: 100}))
		expect(requests()).toHaveLength(0)
	})

	it('an organization already at 100 shows + disabled from the start', () => {
		fixture.organization = org(100, 3, 1)
		draw()
		expect(shownNumber()).toBe('100')
		expect(isRefused(PLUS())).toBe(true)
		expect(isRefused(MINUS())).toBe(false)
	})
})

describe('QA BRA-1636 item 4 — acceptance: increase by three', () => {
	it('+ three times and confirm: the quote asks for 8, the confirm shows €45.67 from the server, seats rise to 8', async () => {
		fixture.organization = org(5, 3, 1)
		draw()
		await clickTimes(PLUS, 3)
		expect(shownNumber()).toBe('8')
		expect(requests()).toHaveLength(0)

		enqueue(json({
			organization_id: 'org-qa',
			seats: 5,
			seats_after: 8,
			proration: {base_minor: 4567, currency: 'EUR', days_remaining: 10, days_in_period: 31},
		}))
		await click(ADAPT())

		expect(requests()).toHaveLength(1)
		const quote = new URL(requests()[0]?.url ?? '')
		expect(quote.origin + quote.pathname).toBe(`${ORIGIN}/v1/organizations/seats/quote`)
		expect(quote.searchParams.get('seats')).toBe('8')
		// The amount is the server's: 4567 minor units of EUR, written out by hand.
		// MUTATION: dropping the prorationDue branch (always showing nothingNow) makes this red.
		expect(modalText()).toContain('€45.67')
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'prorationDue'), {amount: '€45.67'}))
		expect(modalText()).not.toContain(t(k('one', 'org', 'seats', 'decrease')))

		enqueue(json({organization_id: 'org-qa', outcome: 'changed', seats: 8, previous_seats: 5, users: 3, active_teams: 1}))
		fixture.afterReload = org(8, 3, 1)
		await click(confirmButton(t(k('organization', 'seats', 'adapt'))))

		expect(seatRequests()).toHaveLength(2)
		expect(requests()[1]?.url).toBe(`${ORIGIN}/v1/organizations/seats`)
		expect((requests()[1]?.body as {seats?: number}).seats).toBe(8)
		expect(modalText()).toBe('')
		expect(shownNumber()).toBe('8')
		expect(cardText()).toContain(t(k('one', 'org', 'seats', 'inSubscription'), {count: 8}))
	})

	it.each([
		['no proration (trial)', null],
		['a zero amount', {base_minor: 0, currency: 'EUR', days_remaining: 10, days_in_period: 31}],
	])('in a trial the confirm says you pay nothing now: %s', async (_label, proration) => {
		fixture.organization = org(5, 3, 1)
		draw()
		await clickTimes(PLUS, 3)
		enqueue(json({organization_id: 'org-qa', seats: 5, seats_after: 8, proration}))
		await click(ADAPT())

		// MUTATION: showing prorationDue with an empty amount instead of nothingNow makes this red.
		expect(modalText()).toContain(t(k('one', 'org', 'seats', 'nothingNow')))
		expect(modalText()).not.toContain('€')
	})
})

describe('QA BRA-1636 item 4 — a reduction the server refuses', () => {
	it.each([
		['below_users', k('one', 'commercial', 'belowUsers')],
		['below_active_teams', k('one', 'commercial', 'belowActiveTeams')],
	])('%s shows the existing refusal sentence and nothing changes', async (outcome, key) => {
		fixture.organization = org(6, 3, 1)
		draw()
		await click(MINUS())
		await click(ADAPT())
		enqueue(json({organization_id: 'org-qa', outcome, seats: 6, previous_seats: 6, users: 3, active_teams: 1}))
		await click(confirmButton(t(k('organization', 'seats', 'adapt'))))

		expect(requests()).toHaveLength(1)
		// MUTATION: replacing refuseModal(...) in confirm-seats with closeModal() makes this red.
		expect(modalText()).toContain(t(key))
		// Nothing changed: no success message, no reload, the subscription still reads 6.
		expect(toastText()).not.toContain(t(k('one', 'toast', 'seatsChanged')))
		expect(reloads.count).toBe(0)
		expect(cardText()).toContain(t(k('one', 'org', 'seats', 'inSubscription'), {count: 6}))
	})
})

describe('QA BRA-1636 item 4 — who sees the control', () => {
	it('a member who is not the administrator sees no control', () => {
		fixture.organization = org(5, 3, 1)
		fixture.facts = MEMBER_FACTS
		draw()
		// MUTATION: removing data-requires="admin write" from the seatAdapter wrapper makes this red.
		for (const get of [MINUS, PLUS, ADAPT]) {
			expect(get().closest('.hidden')).not.toBeNull()
		}
	})

	it('the administrator sees it', () => {
		fixture.organization = org(5, 3, 1)
		draw()
		for (const get of [MINUS, PLUS, ADAPT]) {
			expect(get().closest('.hidden')).toBeNull()
		}
	})
})

/* ================================ ITEM 6 ================================ */

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

/*
 * The false sentences the ticket orders removed, copied from the catalogues at d3ab4e61f. Holding
 * the OLD words is a removal check, not frozen copy: any honest rewording passes.
 */
const OLD_EXPLANATION: Record<string, string> = {
	'en': 'A seat frees the moment someone is removed, and an invitation needs a free one before it can be sent.',
	'de-DE': 'Ein Sitzplatz wird frei, sobald jemand entfernt wird, und eine Einladung braucht einen freien Platz, bevor sie verschickt werden kann.',
	'es-ES': 'Un puesto queda libre en cuanto quitas a alguien, y una invitación necesita uno libre para poder enviarse.',
	'fr-FR': 'Une licence se libère dès qu’une personne est retirée, et une invitation ne peut être envoyée que s’il reste une licence disponible.',
	'ja-JP': 'メンバーを削除するとライセンスはすぐに空きます。招待を送信するには、空きライセンスが1件必要です。',
	'zh-CN': '移除成员后，其席位会立即释放；发送邀请前需要有一个空闲席位。',
}

/* The false clause alone, per language, so a sentence that keeps it inside new words is caught. */
const FALSE_CLAUSE: Record<string, RegExp> = {
	'en': /needs? a free (one|seat)|before it can be sent/i,
	'de-DE': /braucht einen freien/i,
	'es-ES': /necesita uno libre/i,
	'fr-FR': /ne peut être envoyée que/i,
	'ja-JP': /送信するには.*必要/,
	'zh-CN': /发送邀请前需要/,
}

describe('QA BRA-1636 item 6 — an invitation needs no free seat', () => {
	it.each(CATALOGUES)('%s: organization.seats.explanation no longer says an invitation needs a free seat', (lang, cat) => {
		const value = lookup(cat, k('organization', 'seats', 'explanation'))
		expect(typeof value, `${lang} lost the key`).toBe('string')
		// MUTATION: restoring the old value in that language's catalogue makes this red.
		expect(value).not.toBe(OLD_EXPLANATION[lang])
		expect(value as string).not.toMatch(FALSE_CLAUSE[lang] as RegExp)
	})

	it('the English sentence says sending an invitation does not use a seat', () => {
		expect(t(k('organization', 'seats', 'explanation'))).toMatch(/invitation[^.]*(does not|doesn't) use a seat/i)
	})

	it('the Add member dialog shows the corrected sentence', async () => {
		fixture.organization = org(5, 5, 1)
		draw()
		await captured.actions.invite?.(new Event('click'), document.createElement('button'))
		expect(modalText()).toContain(t(k('organization', 'seats', 'explanation')))
		expect(modalText()).not.toContain(OLD_EXPLANATION.en)
	})
})

/* ================================ catalogue completeness and prose ================================ */

const NEW_OR_CHANGED_KEYS = [
	'organization.seats.adapt',
	'organization.seats.fewer',
	'organization.seats.more',
	'organization.seats.lowestFloor',
	'organization.seats.lowestTeams',
	'organization.seats.lowestMembers',
	'organization.seats.highest',
	'organization.seats.explanation',
	'one.org.seats.nothingNow',
	'one.org.seats.decrease',
	'one.toast.seatsChanged',
]

describe('QA BRA-1636 — every new sentence exists in all six languages', () => {
	it.each(CATALOGUES)('%s carries every key the control uses', (lang, cat) => {
		// MUTATION: deleting `nothingNow` from fr-FR.json makes this red.
		for (const key of NEW_OR_CHANGED_KEYS) {
			const value = lookup(cat, key)
			expect(typeof value === 'string' && value.trim() !== '', `${lang} ${key}`).toBe(true)
		}
	})
})

describe('QA BRA-1636 — English prose standard', () => {
	it('no em dash, en dash or semicolon in any new or changed English sentence', () => {
		const en = JSON.parse(enRaw) as Catalogue
		// MUTATION: putting an em dash into one.org.seats.decrease makes this red.
		for (const key of NEW_OR_CHANGED_KEYS) {
			const value = String(lookup(en, key))
			expect(value, key).not.toMatch(/[—–;]/)
		}
	})

	it('short sentences: no new English sentence runs past 20 words', () => {
		const en = JSON.parse(enRaw) as Catalogue
		for (const key of NEW_OR_CHANGED_KEYS) {
			for (const sentence of String(lookup(en, key)).split(/(?<=[.!?])\s+/)) {
				expect(sentence.split(/\s+/).length, `${key}: ${sentence}`).toBeLessThanOrEqual(20)
			}
		}
	})
})
