import {describe, it, expect, beforeAll, beforeEach, vi} from 'vitest'

import {init as initI18n, t} from '../../../public/one/i18n.js'
import {
	COMMERCIAL_NOTICE,
	COMMERCIAL_NOTICE_LINK,
	COMMERCIAL_NOTICE_MESSAGE_KEY,
	applyGates,
	decideCommercialNotice,
	isRefused,
	renderCommercialNotice,
} from '../../../public/one/app.js'
import type {GateFacts, PublishedAddresses} from '../../../public/one/app.js'

import enRaw from '../../../public/one/i18n/en.json?raw'
import deRaw from '../../../public/one/i18n/de-DE.json?raw'

/*
 * BRA-1546 - a read-only account is told so ONCE PER SCREEN, in words that say what to do.
 *
 * A read-only account opening a task read "Your account is read-only right now." ELEVEN TIMES on
 * one screen: beside the project picker, the complete button, the title, the labels, the
 * assignee, the priority, the progress, the due date, the reminders, the description and the
 * attachments. Every disabled control carried its own copy, it named no cause, and it offered
 * nothing to do.
 *
 * Two things are under test here and they fail independently:
 *
 *   1. THE CHOOSER - `decideCommercialNotice`, which is pure and takes its clock as an argument,
 *      so all six situations are a table rather than six mounted sessions.
 *   2. THE SUPPRESSION - that no write-refused control carries a sentence any more, while every
 *      one of them is still visibly and announcedly refused.
 *
 * ASSERTIONS ARE ON THE CHOSEN CASE, not on the customer's whole sentence, wherever the case is
 * what the test is actually about. The sentences are pinned once, on their own, by their
 * distinguishing half - a full-sentence assertion in every row would turn one comma into eleven
 * red rows and would test the catalogue rather than the logic.
 */

const DAY = 86400000
const NOW = Date.parse('2026-09-07T12:00:00Z')

const ADDRESSES: PublishedAddresses = {
	checkout: 'https://brazn.one/en/checkout?convert=1',
	account: 'https://brazn.one/en/account',
}

const NO_ADDRESSES: PublishedAddresses = {checkout: null, account: null}

/** An ordinary Teams member in good standing - every field the notice reads, at its quiet value. */
const MEMBER: GateFacts = {
	hasEdition: true,
	personalEdition: false,
	orgAdmin: false,
	writeRestricted: false,
	writeReason: null,
	graceUntil: null,
	teams: {'7': {readable: true, admin: false}},
}

const ADMINISTRATOR: GateFacts = {...MEMBER, orgAdmin: true}

/** A personal-cloud subscriber. The organization read 403s for them, so `orgAdmin` is false. */
const PERSONAL: GateFacts = {...MEMBER, personalEdition: true}

/** The shipped catalogues, served to i18n.js through its only seam - the global fetch. */
async function loadCatalogue(locale: string): Promise<void> {
	vi.stubGlobal('fetch', async (input: string) => {
		if (String(input).includes('/en.json')) {
			return new Response(enRaw, {headers: {'content-type': 'application/json'}})
		}
		if (String(input).includes('/de-DE.json')) {
			return new Response(deRaw, {headers: {'content-type': 'application/json'}})
		}
		return new Response('not found', {status: 404})
	})
	try {
		await initI18n(locale, [locale])
	} finally {
		vi.unstubAllGlobals()
	}
}

beforeAll(async () => {
	await loadCatalogue('en')
})

describe('one/app.js — which situation the account is in', () => {
	it('says nothing at all for an account in good standing', () => {
		// The answer for almost every session. A banner drawn here would be a red block on a
		// screen where nothing is wrong.
		expect(decideCommercialNotice(MEMBER, NOW)).toBeNull()
		expect(decideCommercialNotice(ADMINISTRATOR, NOW)).toBeNull()
		expect(renderCommercialNotice(MEMBER, ADDRESSES, NOW)).toBe('')
	})

	// Situation 1.
	it('sends an ended trial to get a subscription', () => {
		const facts: GateFacts = {...PERSONAL, writeRestricted: true, writeReason: 'trial_ended'}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.TRIAL_ENDED)
		// There is no invoice to settle, so the link is the checkout and never the billing page.
		expect(notice?.link).toBe('checkout')
		expect(notice?.days).toBeNull()
	})

	// Situation 2.
	it('counts the days down for an administrator inside the grace period', () => {
		const facts: GateFacts = {...ADMINISTRATOR, graceUntil: NOW + 3 * DAY}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.GRACE_ADMINISTRATOR)
		expect(notice?.days).toBe(3)
		expect(notice?.link).toBe('account')
	})

	// Situation 3.
	it('counts the same days down for a member, and sends them to nobody', () => {
		const facts: GateFacts = {...MEMBER, graceUntil: NOW + 3 * DAY}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.GRACE_MEMBER)
		expect(notice?.days).toBe(3)
		expect(notice?.link).toBeNull()
	})

	// Situation 4.
	it('asks the administrator to settle the invoice once the grace period has run out', () => {
		const facts: GateFacts = {
			...ADMINISTRATOR,
			writeRestricted: true,
			writeReason: 'invoice_unpaid',
			graceUntil: NOW - DAY,
		}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.LOCKED_ADMINISTRATOR)
		expect(notice?.link).toBe('account')
		// A deadline that has passed stops being a countdown rather than becoming a negative one.
		expect(notice?.days).toBeNull()
	})

	// Situation 5.
	it('sends the member to their administrator once the grace period has run out', () => {
		const facts: GateFacts = {
			...MEMBER,
			writeRestricted: true,
			writeReason: 'invoice_unpaid',
			graceUntil: NOW - DAY,
		}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.LOCKED_MEMBER)
		expect(notice?.link).toBeNull()
	})

	// The sixth case, and the only one reachable from a token minted before the reason claim
	// existed - which is every token in flight on the day this ships.
	it('names no cause when the token did not say why', () => {
		const facts: GateFacts = {...ADMINISTRATOR, writeRestricted: true, writeReason: null}
		const notice = decideCommercialNotice(facts, NOW)

		expect(notice?.case).toBe(COMMERCIAL_NOTICE.LOCKED_UNEXPLAINED)
		expect(notice?.link).toBeNull()
		// Guessing "invoice_unpaid" here is exactly what BRA-1539 was raised for: it tells a
		// lapsed-trial customer to settle an invoice that was never raised.
		expect(notice?.messageKey).not.toBe(
			COMMERCIAL_NOTICE_MESSAGE_KEY[COMMERCIAL_NOTICE.LOCKED_ADMINISTRATOR],
		)
	})

	it('reads a reason it does not recognise as no reason at all', () => {
		const facts = {
			...ADMINISTRATOR,
			writeRestricted: true,
			writeReason: 'seat_withdrawn_by_a_later_producer',
		} as GateFacts
		// A newer producer's value must not fall through to a sentence that asserts a cause.
		expect(decideCommercialNotice(facts, NOW)?.case).toBe(COMMERCIAL_NOTICE.LOCKED_UNEXPLAINED)
	})
})

describe('one/app.js — the rules the five situations answer to', () => {
	it('never offers a member a payment link, in any situation', () => {
		// The product rule, asserted against the whole table rather than against the rows the
		// cases above happen to reach.
		// MUTATION: giving LOCKED_MEMBER or GRACE_MEMBER a link makes this red.
		expect(COMMERCIAL_NOTICE_LINK[COMMERCIAL_NOTICE.GRACE_MEMBER]).toBeNull()
		expect(COMMERCIAL_NOTICE_LINK[COMMERCIAL_NOTICE.LOCKED_MEMBER]).toBeNull()
		expect(COMMERCIAL_NOTICE_LINK[COMMERCIAL_NOTICE.LOCKED_UNEXPLAINED]).toBeNull()

		// And the case a member can reach out of an ENDED TRIAL, which is the one Sebastian's
		// five situations do not enumerate: a Teams trial that ends with members still on it.
		// They get the member's locked sentence, not the trial one, because the trial sentence
		// carries a payment link they must never be shown.
		const facts: GateFacts = {...MEMBER, writeRestricted: true, writeReason: 'trial_ended'}
		const notice = decideCommercialNotice(facts, NOW)
		expect(notice?.case).toBe(COMMERCIAL_NOTICE.LOCKED_MEMBER)
		expect(notice?.link).toBeNull()
	})

	it('treats a personal subscriber as the person who pays, not as somebody else’s member', () => {
		// `orgAdmin` is FALSE for a personal-cloud account - the organization read 403s for them.
		// An administrator test of `orgAdmin` alone would tell a personal subscriber to contact
		// an administrator they have never had.
		// MUTATION: dropping `|| personalEdition` from decideCommercialNotice makes this red.
		const facts: GateFacts = {...PERSONAL, graceUntil: NOW + 2 * DAY}
		expect(facts.orgAdmin).toBe(false)
		expect(decideCommercialNotice(facts, NOW)?.case).toBe(COMMERCIAL_NOTICE.GRACE_ADMINISTRATOR)
	})

	it('warns while writing still works, which is the only moment the warning can help', () => {
		// The grace cases are reachable with `writeRestricted` FALSE, and that is the whole point:
		// the commercial service leaves write access full until the grace period runs out, so a
		// countdown read only inside a write-restriction branch would render to nobody.
		const facts: GateFacts = {...ADMINISTRATOR, writeRestricted: false, graceUntil: NOW + DAY}
		expect(decideCommercialNotice(facts, NOW)?.case).toBe(COMMERCIAL_NOTICE.GRACE_ADMINISTRATOR)
	})

	it('rounds part of a day up, and never counts down to zero', () => {
		// Part of a day left is still a day the customer has, and "0 remaining" beside an account
		// that still writes contradicts the screen it sits on.
		expect(decideCommercialNotice({...ADMINISTRATOR, graceUntil: NOW + DAY / 4}, NOW)?.days).toBe(1)
		expect(decideCommercialNotice({...ADMINISTRATOR, graceUntil: NOW + DAY}, NOW)?.days).toBe(1)
		expect(decideCommercialNotice({...ADMINISTRATOR, graceUntil: NOW + DAY + 1}, NOW)?.days).toBe(2)
	})

	it('stops counting the instant the deadline passes', () => {
		const spent: GateFacts = {...ADMINISTRATOR, graceUntil: NOW, writeRestricted: false}
		// The deadline has arrived and writes are not yet restricted: there is nothing true to
		// say, so nothing is said. The next token carries the restriction.
		expect(decideCommercialNotice(spent, NOW)).toBeNull()
	})
})

describe('one/app.js — the banner on the page', () => {
	it('draws exactly one notice, carrying the case it chose', () => {
		const facts: GateFacts = {...ADMINISTRATOR, graceUntil: NOW + 5 * DAY}
		const html = renderCommercialNotice(facts, ADDRESSES, NOW)

		expect(html.match(/data-notice="commercial-standing"/g)).toHaveLength(1)
		expect(html).toContain(`data-notice-case="${COMMERCIAL_NOTICE.GRACE_ADMINISTRATOR}"`)
		// The stylesheet's existing red notice, not a second treatment invented for this.
		expect(html).toContain('class="notice refusal-text"')
		expect(html).toContain('role="status"')
	})

	it('resolves the link through the published address and never through a literal', () => {
		const facts: GateFacts = {...ADMINISTRATOR, writeRestricted: true, writeReason: 'invoice_unpaid'}
		const html = renderCommercialNotice(facts, ADDRESSES, NOW)

		expect(html).toContain(`href="${ADDRESSES.account}"`)
		expect(html).toContain('data-notice-link="account"')
		// MUTATION: hardcoding any address in app.js makes this red - the fixture's addresses are
		// not real ones, so a literal would show up here instead of them.
		expect(html).not.toContain('brazn.one/en/checkout')
	})

	it('keeps the sentence and drops the link when the instance published no address', () => {
		// A self-hosted instance publishes neither address. "Please pay your pending invoice here."
		// still says what has to happen; an anchor with no destination would not.
		const facts: GateFacts = {...ADMINISTRATOR, writeRestricted: true, writeReason: 'invoice_unpaid'}
		const html = renderCommercialNotice(facts, NO_ADDRESSES, NOW)

		expect(html).not.toContain('<a ')
		expect(html).toContain('pay your pending invoice here')
	})

	it('puts the day count into the sentence a person reads', () => {
		const facts: GateFacts = {...MEMBER, graceUntil: NOW + 4 * DAY}
		expect(renderCommercialNotice(facts, ADDRESSES, NOW)).toContain('There are 4 remaining')
	})
})

describe('one/app.js — the sentences, in both shipped languages', () => {
	// Pinned by their DISTINGUISHING HALF, once, here. The five wordings are Sebastian's and ship
	// as written (7 September 2026); this is what makes "as written" checkable.
	const DISTINCTIVE: ReadonlyArray<readonly [string, string, string]> = [
		[COMMERCIAL_NOTICE.TRIAL_ENDED, 'get a subscription', 'Abonnement'],
		[COMMERCIAL_NOTICE.GRACE_ADMINISTRATOR, 'pay your pending invoice', 'offene Rechnung'],
		[COMMERCIAL_NOTICE.GRACE_MEMBER, 'contact your admin', 'wende dich an deinen Administrator'],
		[COMMERCIAL_NOTICE.LOCKED_ADMINISTRATOR, 'pay your pending invoice', 'offene Rechnung'],
		[COMMERCIAL_NOTICE.LOCKED_MEMBER, 'contact your admin', 'wende dich an deinen Administrator'],
	]

	function lookup(raw: string, key: string): string | undefined {
		let node: unknown = JSON.parse(raw)
		for (const part of key.split('.')) {
			if (node === null || typeof node !== 'object') return undefined
			node = (node as Record<string, unknown>)[part]
		}
		return typeof node === 'string' ? node : undefined
	}

	it('carries a real sentence for every case, in English and in German', () => {
		// Driven from the WHOLE table rather than from the cases a fixture happens to reach, so a
		// renamed key cannot ship as a raw dotted path onto the one banner a blocked customer
		// reads. Same protection DENY_MESSAGE_KEY carries, same reason.
		const keys = Object.values(COMMERCIAL_NOTICE_MESSAGE_KEY)
		expect(keys.length).toBe(6)
		for (const key of [...keys, 'one.commercial.notice.linkWord']) {
			expect(typeof lookup(enRaw, key), `${key} in en.json`).toBe('string')
			expect(typeof lookup(deRaw, key), `${key} in de-DE.json`).toBe('string')
		}
	})

	it('says the distinguishing thing in each language', () => {
		for (const [name, english, german] of DISTINCTIVE) {
			const key = COMMERCIAL_NOTICE_MESSAGE_KEY[name]
			expect(lookup(enRaw, key), `${name} en`).toContain(english)
			expect(lookup(deRaw, key), `${name} de`).toContain(german)
		}
	})

	it('puts the link on the word "here", and only where there is a link', () => {
		// The link slot is what lets one catalogue value per language carry the whole sentence
		// with the anchor's position inside it, rather than splitting every sentence in two.
		for (const [name] of DISTINCTIVE) {
			const key = COMMERCIAL_NOTICE_MESSAGE_KEY[name]
			const hasSlot = (lookup(enRaw, key) ?? '').includes('{link}')
			expect(hasSlot, `${name} en {link}`).toBe(COMMERCIAL_NOTICE_LINK[name] !== null)
			expect((lookup(deRaw, key) ?? '').includes('{link}'), `${name} de {link}`).toBe(hasSlot)
		}
		expect(lookup(enRaw, 'one.commercial.notice.linkWord')).toBe('here')
	})

	it('counts the days in German too', async () => {
		await loadCatalogue('de-DE')
		try {
			expect(t(COMMERCIAL_NOTICE_MESSAGE_KEY[COMMERCIAL_NOTICE.GRACE_MEMBER], {days: 6}))
				.toContain('6')
			// MUTATION: dropping {days} from the German value makes this red, which is the failure
			// that would otherwise ship a countdown with no number in it to every German reader.
			expect(t(COMMERCIAL_NOTICE_MESSAGE_KEY[COMMERCIAL_NOTICE.GRACE_MEMBER], {days: 6}))
				.not.toContain('{days}')
		} finally {
			await loadCatalogue('en')
		}
	})
})

describe('one/app.js — no control carries its own copy any more', () => {
	// The eleven controls of the task screen, in the shapes the view actually emits: a button, a
	// text input, a select, a textarea and a wrapper group.
	const ELEVEN = `
<div class="setting-row"><button id="c1" class="btn" data-requires="write">Complete</button></div>
<div class="setting-row"><input id="c2" class="input" data-requires="write"></div>
<div class="setting-row"><select id="c3" class="select" data-requires="write"></select></div>
<div class="setting-row"><textarea id="c4" class="editor" data-requires="write"></textarea></div>
<div class="setting-row"><button id="c5" class="btn" data-requires="write">Labels</button></div>
<div class="setting-row"><button id="c6" class="btn" data-requires="write">Assignee</button></div>
<div class="setting-row"><select id="c7" class="select" data-requires="write"></select></div>
<div class="setting-row"><input id="c8" class="input" type="date" data-requires="write"></div>
<div class="setting-row"><button id="c9" class="btn" data-requires="write">Reminders</button></div>
<div class="setting-row"><button id="c10" class="btn" data-requires="write">Attachments</button></div>
<div class="setting-row"><div id="c11" data-requires="write"><button>Upload</button></div></div>
`
	const IDS = ['c1', 'c2', 'c3', 'c4', 'c5', 'c6', 'c7', 'c8', 'c9', 'c10', 'c11']

	const RESTRICTED: GateFacts = {...MEMBER, writeRestricted: true, writeReason: 'invoice_unpaid'}

	beforeEach(() => {
		document.body.innerHTML = `<main id="app">${ELEVEN}</main>`
	})

	function root(): HTMLElement {
		const el = document.getElementById('app')
		if (el === null) throw new Error('no #app')
		return el
	}

	it('leaves no sentence beside any of the eleven', () => {
		applyGates(root(), RESTRICTED)

		// MUTATION: restoring `one.deny.writeRestricted` to DENY_MESSAGE_KEY makes this red with
		// eleven sentences, which is the state this ticket was raised about.
		const sentences = [...root().querySelectorAll('.refusal-text')]
			.filter(node => (node.textContent ?? '').trim() !== '')
		expect(sentences).toHaveLength(0)
	})

	it('still shows every one of them as refused', () => {
		applyGates(root(), RESTRICTED)

		// Removing the ten repeated notices must not remove the fact that a control is disabled.
		// MUTATION: dropping refuseControl from applyDecision's disabled branch makes this red.
		for (const id of IDS) {
			const el = document.getElementById(id) as HTMLElement
			expect(isRefused(el), `${id} refused`).toBe(true)
			expect(el.getAttribute('data-deny-reason'), `${id} reason`).toBe('write-restricted')
		}
		// The element-appropriate "disabled", unchanged: readOnly on inputs and textareas so they
		// stay focusable and announced, `disabled` on selects, aria-disabled on buttons.
		expect((document.getElementById('c2') as HTMLInputElement).readOnly).toBe(true)
		expect((document.getElementById('c4') as HTMLTextAreaElement).readOnly).toBe(true)
		expect((document.getElementById('c3') as HTMLSelectElement).disabled).toBe(true)
		expect(document.getElementById('c1')?.getAttribute('aria-disabled')).toBe('true')
	})

	it('still explains every OTHER refusal beside the control it is about', () => {
		// The suppression is scoped to the write restriction, which is the only refusal true of
		// the whole screen. A Teams boundary is true of one control, so it keeps its sentence.
		document.body.innerHTML =
			'<main id="app"><div class="setting-row">' +
			'<button id="teamsOnly" class="btn" data-requires="teams">Move</button></div></main>'
		applyGates(root(), {...PERSONAL, writeRestricted: false})

		const node = document.getElementById('teamsOnly')?.nextElementSibling
		expect(node?.classList.contains('refusal-text')).toBe(true)
		expect((node?.textContent ?? '').trim()).not.toBe('')
	})
})
