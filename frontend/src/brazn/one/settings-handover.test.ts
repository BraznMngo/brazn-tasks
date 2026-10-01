import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'

import {init as initI18n} from '../../../public/one/i18n.js'
import {isRefused} from '../../../public/one/app.js'
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
 * BRA-1636 ITEM 1 — "Administrator handover shows names, not numbers."
 *
 * Both handover pickers on the settings page — Team management's "Transfer the role" and the
 * successor step in front of account deletion — listed each candidate as the bare id the
 * commercial service sends. The acceptance, asserted here as what a person sees:
 *
 *   * every option reads "Full name (email)", the email in a lighter grey, no option shows a number;
 *   * choosing a person and confirming sends THAT person's id (the commercial id, unchanged);
 *   * the picker opens with nobody chosen, and the confirm button is disabled until someone is;
 *   * a candidate the page cannot name is not choosable, and the picker says that person's
 *     details could not be shown — it never falls back to printing the id.
 *
 * Nothing here depends on `<select>`, `<option>` or element ids: a choice is found by its visible
 * text, chosen by clicking it, and the confirm button is found by its visible label. Every expected
 * string is written out by hand from the fixtures below, never computed by the code under test.
 *
 * The organization is fed through app.js's own `getOrganization` export (mocked to return the
 * fixture), which is the same read the page's Administrator card and member list use.
 */

type Handler = (event: Event, el: Element) => void | Promise<void>

const captured: {
	render: ((ctx: unknown) => string) | null
	mount: ((root: Element, ctx: unknown) => void) | null
	actions: Record<string, Handler>
} = {render: null, mount: null, actions: {}}

const fixture: {organization: unknown} = {organization: null}

vi.mock('../../../public/one/app.js', async (importOriginal) => {
	const real = await importOriginal<typeof import('../../../public/one/app.js')>()
	return {
		...real,
		getOrganization: () => fixture.organization,
		// The real one re-reads the fork organization over the network and re-renders the whole
		// shell; neither is under test, and an unstubbed request would only add noise.
		reloadOrganization: vi.fn(async () => {}),
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

// The shipped stylesheet, so "the email is in lighter grey" is asserted on the colour a reader
// would actually get. Read from disk because Vitest turns a `?raw` CSS import into an empty string.
const CSS = readFileSync(resolve(process.cwd(), 'public/one/one.css'), 'utf8')

/*
 * app.js's delegated click listener is installed by `boot()`, which this file does not run. This
 * mirrors its `data-action` branch (app.js, the `document.addEventListener('click', …)` in the
 * listener installer), including the real `isRefused` — so a disabled control does nothing here
 * for the same reason it does nothing in the page.
 */
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

	const style = document.createElement('style')
	style.textContent = CSS
	document.head.appendChild(style)

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
	document.body.innerHTML = SHELL
	const api = await import('../../../public/one/api.js')
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
	api.setToken('access-1')
	fixture.organization = ORGANIZATION
	// mount() installs the view's document-level `change` listener (once). The tab is neither
	// 'account' nor 'team', so mount starts no reads of its own.
	const app = document.getElementById('app')
	if (app === null || captured.mount === null) throw new Error('no #app, or the settings view registered no mount')
	captured.mount(app, {route: {view: 'settings', tab: 'organization'}, facts: {}})
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
const SEBASTIAN = {user_id: 7, name: 'Sebastian Schnur', username: 'sschnur', email: 'sebastian@brazn.one'}
const STEFAN = {user_id: 12, name: 'Stefan Kannemann', username: 'skannemann', email: 'stefan@brazn.one'}
// No name: the Administrator card's rule (`displayName`) falls back to the username.
const MAREN = {user_id: 15, name: '   ', username: 'mkoch', email: 'maren@brazn.one'}

const ORGANIZATION = {
	id: 'org-1',
	organization_name: 'Brazn GmbH',
	administrator: ADMIN,
	members: [ADMIN, SEBASTIAN, STEFAN, MAREN],
	teams: [],
}

const UNNAMEABLE = 'acct_xxxxxxxxxxxxxxxxxxxxxx'

/** The commercial service's answer: string ids, as `GET /v1/account/successor-candidates` sends. */
function candidates(...ids: string[]): Response {
	return json({candidates: ids.map((id) => ({user_id: id}))})
}

/* ------------------------------------------------------------------ *
 * What a person sees.
 * ------------------------------------------------------------------ */

function norm(text: string | null | undefined): string {
	return (text ?? '').replace(/\s+/g, ' ').trim()
}

function modalBody(): Element {
	const body = document.querySelector('#modalRoot .modal-body')
	if (body === null) throw new Error('no modal is open')
	return body
}

function modalText(): string {
	return norm(document.getElementById('modalRoot')?.textContent)
}

/** Every control inside the modal body a person could try to pick. */
function controls(): Element[] {
	return [...modalBody().querySelectorAll(
		'input:not([type="hidden"]), option, [role="option"], [role="radio"], button, [data-action]',
	)]
}

function labelOf(el: Element): string {
	if (el instanceof HTMLInputElement) {
		const label = el.labels?.[0] ?? el.closest('label')
		return norm(label?.textContent)
	}
	return norm(el.textContent)
}

const PLACEHOLDER = 'Select a team member…'

function choosable(el: Element): boolean {
	if (isRefused(el)) return false
	if (el instanceof HTMLOptionElement && (el.disabled || el.value === '')) return false
	return labelOf(el) !== PLACEHOLDER
}

/** The labels of every choice that can actually be chosen, in order. */
function choices(): string[] {
	return controls().filter(choosable).map(labelOf)
}

function isChosen(el: Element): boolean {
	if (el instanceof HTMLInputElement) return el.checked
	if (el instanceof HTMLOptionElement) return el.selected
	return ['aria-checked', 'aria-selected', 'aria-pressed'].some((name) => el.getAttribute(name) === 'true')
}

/** Choose a person by the name a reader sees, by clicking it. */
function choose(name: string): void {
	const el = controls().filter(choosable).find((candidate) => labelOf(candidate).includes(name))
	if (el === undefined) throw new Error(`no choosable entry reads "${name}"; choices: ${JSON.stringify(choices())}`)
	if (el instanceof HTMLOptionElement) {
		const select = el.closest('select')
		if (select === null) throw new Error('an option outside a select')
		select.value = el.value
		select.dispatchEvent(new Event('change', {bubbles: true}))
		return
	}
	;(el as HTMLElement).click()
}

/** The footer button a person reads as `label`. */
function footButton(label: string): HTMLButtonElement {
	const button = [...document.querySelectorAll('#modalRoot .modal-foot button')]
		.find((el) => norm(el.textContent) === label)
	if (!(button instanceof HTMLButtonElement)) throw new Error(`no footer button reads "${label}"`)
	return button
}

function isDisabled(button: HTMLButtonElement): boolean {
	return button.disabled || button.getAttribute('aria-disabled') === 'true'
}

async function press(button: HTMLButtonElement): Promise<void> {
	button.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}))
	await pending
	await settle()
}

/** Relative luminance (WCAG) of a colour string as happy-dom computes it. */
function rgbOf(colour: string): [number, number, number] {
	const value = colour.trim()
	let m = /^#([0-9a-f]{3})$/i.exec(value)
	if (m) return [...m[1]].map((c) => parseInt(c + c, 16)) as [number, number, number]
	m = /^#([0-9a-f]{6})$/i.exec(value)
	if (m) return [0, 2, 4].map((i) => parseInt(m![1].slice(i, i + 2), 16)) as [number, number, number]
	m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(value)
	if (m) return [Number(m[1]), Number(m[2]), Number(m[3])]
	m = /^hsla?\(\s*([\d.]+)(?:deg)?[\s,]+([\d.]+)%[\s,]+([\d.]+)%/i.exec(value)
	if (m) {
		const h = Number(m[1]) / 360
		const s = Number(m[2]) / 100
		const l = Number(m[3]) / 100
		const q = l < 0.5 ? l * (1 + s) : l + s - l * s
		const p = 2 * l - q
		const channel = (t0: number) => {
			const t1 = t0 < 0 ? t0 + 1 : t0 > 1 ? t0 - 1 : t0
			if (t1 < 1 / 6) return p + (q - p) * 6 * t1
			if (t1 < 1 / 2) return q
			if (t1 < 2 / 3) return p + (q - p) * (2 / 3 - t1) * 6
			return p
		}
		return [channel(h + 1 / 3), channel(h), channel(h - 1 / 3)].map((c) => Math.round(c * 255)) as [number, number, number]
	}
	throw new Error(`unparsed colour ${JSON.stringify(colour)}`)
}

function luminance(colour: string): number {
	const [r, g, b] = rgbOf(colour).map((c) => {
		const s = c / 255
		return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
	})
	return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** The innermost element inside `root` whose text contains `text`. */
function innermost(root: Element, text: string): Element {
	let found: Element | null = null
	for (const el of root.querySelectorAll('*')) {
		if ((el.textContent ?? '').includes(text)) found = el
	}
	if (found === null) throw new Error(`"${text}" is not in its own element inside the choice`)
	return found
}

/** The element that draws one choice: the input's label, or the choice itself. */
function choiceBox(name: string): Element {
	const el = controls().filter(choosable).find((candidate) => labelOf(candidate).includes(name))
	if (el === undefined) throw new Error(`no choosable entry reads "${name}"`)
	if (el instanceof HTMLInputElement) return el.labels?.[0] ?? el.closest('label') ?? el
	return el
}

/* ------------------------------------------------------------------ *
 * The two flows share one set of assertions; each is opened its own way.
 * ------------------------------------------------------------------ */

interface Flow {
	name: string
	open: () => Promise<void>
	confirmLabel: string
}

const TRANSFER: Flow = {
	name: 'Team management → Transfer the role',
	open: async () => {
		await captured.actions['transfer-admin']?.(new Event('click'), document.createElement('button'))
		await settle()
	},
	confirmLabel: 'Transfer the role',
}

const ERASURE: Flow = {
	name: 'Delete account → successor step',
	open: async () => {
		await captured.actions['delete-account-second']?.(new Event('click'), document.createElement('button'))
		await settle()
	},
	confirmLabel: 'Delete account',
}

describe.each([TRANSFER, ERASURE])('BRA-1636 item 1 — $name', (flow) => {
	it('lists every candidate as "Full name (email)", never as a number', async () => {
		enqueue(candidates('7', '12'))
		await flow.open()

		expect(requests()[0]?.url).toBe(`${ORIGIN}/v1/account/successor-candidates`)
		// MUTATION: labelling a choice with the id again (`const label = String(candidate.id)` in
		// successorPicker(), view-settings.js) makes this red — the choices read
		// ["7 (sebastian@brazn.one)", "12 (stefan@brazn.one)"]. Run and confirmed red. This is the
		// line the ticket's last acceptance point asks for.
		expect(choices()).toEqual([
			'Sebastian Schnur (sebastian@brazn.one)',
			'Stefan Kannemann (stefan@brazn.one)',
		])
		// Belt and braces, not a separate mutation claim: no control in the picker, choosable or
		// not, carries a digit (the fixtures' names and emails contain none).
		for (const control of controls()) expect(labelOf(control)).not.toMatch(/\d/)
	})

	it('uses the Administrator card\'s name rule: a blank name falls back to the username', async () => {
		enqueue(candidates('15', '7'))
		await flow.open()

		// MUTATION: reading `member.name` raw instead of through displayName() makes this red — the
		// choice would read "(maren@brazn.one)" or "    (maren@brazn.one)" with no name at all.
		expect(choices()).toEqual([
			'mkoch (maren@brazn.one)',
			'Sebastian Schnur (sebastian@brazn.one)',
		])
	})

	it('draws the email in a lighter grey than the name', async () => {
		enqueue(candidates('7', '12'))
		await flow.open()

		for (const [name, email] of [['Sebastian Schnur', 'sebastian@brazn.one'], ['Stefan Kannemann', 'stefan@brazn.one']]) {
			const box = choiceBox(name)
			const nameEl = innermost(box, name)
			const emailEl = innermost(box, email)
			// The email has to be its own element, or no colour rule can tell it from the name.
			expect(emailEl, `the email of ${name} shares an element with the name`).not.toBe(nameEl)
			expect(emailEl.contains(nameEl), `the name of ${name} sits inside the email's element`).toBe(false)

			const nameColour = getComputedStyle(nameEl).color
			const emailColour = getComputedStyle(emailEl).color
			// MUTATION: deleting `.handover-email{color:var(--text-muted)}` from one.css makes this
			// red — the email inherits the name's colour and the luminances are equal.
			expect(luminance(emailColour), `email ${emailColour} vs name ${nameColour}`).toBeGreaterThan(luminance(nameColour))
			// Grey, not a tint: no channel more than 15% of the range away from the others.
			const [r, g, b] = rgbOf(emailColour)
			expect(Math.max(r, g, b) - Math.min(r, g, b), `email colour ${emailColour} is not a grey`).toBeLessThanOrEqual(38)
		}
	})

	it('opens with nobody chosen and the confirm button disabled', async () => {
		enqueue(candidates('7', '12'))
		await flow.open()

		// MUTATION: pre-checking or pre-selecting the first candidate makes this red — the
		// organization would go to whoever happened to be listed first.
		expect(controls().filter(choosable).filter(isChosen).map(labelOf)).toEqual([])
		// MUTATION: dropping `disabled` from the confirm button's markup makes this red.
		expect(isDisabled(footButton(flow.confirmLabel))).toBe(true)

		// Pressing it anyway sends nothing beyond the candidate read.
		await press(footButton(flow.confirmLabel))
		expect(requests()).toHaveLength(1)
	})

	it('enables the confirm button once a person is chosen', async () => {
		enqueue(candidates('7', '12'))
		await flow.open()

		choose('Stefan Kannemann')
		await settle()

		// MUTATION: deleting the listener that releases the button on a choice makes this red —
		// the handover could never be confirmed.
		expect(isDisabled(footButton(flow.confirmLabel))).toBe(false)
		expect(controls().filter(choosable).filter(isChosen).map(labelOf)).toEqual(['Stefan Kannemann (stefan@brazn.one)'])
	})

	it('does not let a candidate it cannot name be chosen, says so, and never prints the id', async () => {
		enqueue(candidates('7', UNNAMEABLE, '12'))
		await flow.open()

		// MUTATION: rendering the unmatched candidate as an ordinary choice (any label) makes this
		// red — there would be three choosable entries.
		expect(choices()).toEqual([
			'Sebastian Schnur (sebastian@brazn.one)',
			'Stefan Kannemann (stefan@brazn.one)',
		])
		// MUTATION: falling back to `String(id)` for an unmatched candidate makes this red.
		expect(modalText()).not.toContain('acct_')
		// MUTATION: dropping the unmatched candidate silently (no sentence) makes this red.
		expect(modalText().match(/details could not be shown/g) ?? []).toHaveLength(1)
		// And nothing choosable carries its id as a value either.
		for (const control of controls().filter(choosable)) {
			expect((control as HTMLInputElement).value ?? '').not.toBe(UNNAMEABLE)
		}
	})

	it('does not print a NUMERIC id it cannot match to a member either', async () => {
		// A numeric id that is nobody in this organization — the shape a "fall back to the
		// number" path would print, and one a branch keyed on the `acct_` prefix would miss.
		enqueue(candidates('99', '12'))
		await flow.open()

		// MUTATION: `member === undefined ? String(candidate.id) : displayName(member)` in
		// successorPicker() makes this red — "99" is printed as a choice.
		expect(choices()).toEqual(['Stefan Kannemann (stefan@brazn.one)'])
		expect(modalText()).not.toContain('99')
		expect(modalText().match(/details could not be shown/g) ?? []).toHaveLength(1)
	})

	it('says nothing about unshown details when every candidate is named', async () => {
		// Guards the guard: the sentence above must be caused by the unmatched candidate.
		enqueue(candidates('7', '12'))
		await flow.open()

		// MUTATION: printing the "could not be shown" sentence unconditionally makes this red.
		expect(modalText()).not.toContain('could not be shown')
	})
})

describe('BRA-1636 item 1 — confirming sends the chosen person\'s commercial id', () => {
	it('Transfer the role: makes the chosen person administrator', async () => {
		enqueue(
			candidates('7', '12'),
			json({organization_id: 'org-1', from_user_id: '1', to_user_id: '12'}),
		)
		await TRANSFER.open()

		// The SECOND person, so "send the first candidate" cannot pass.
		choose('Stefan Kannemann')
		await settle()
		await press(footButton('Transfer the role'))

		expect(requests()).toHaveLength(2)
		expect(requests()[1]?.url).toBe(`${ORIGIN}/v1/organizations/admin-transfer`)
		// MUTATION: sending the member's fork row as a number (12), the first candidate ("7"), or
		// anything other than the commercial id string as received makes this red.
		expect(requests()[1]?.body).toEqual({organization_id: 'org-1', to_user_id: '12', idempotency_key: 'idem-1'})
	})

	it('Delete account: hands over to the chosen person before erasing', async () => {
		enqueue(
			candidates('7', '12'),
			json({organization_id: 'org-1', from_user_id: '1', to_user_id: '12'}),
			new Response(null, {status: 204}),
		)
		await ERASURE.open()

		choose('Stefan Kannemann')
		await settle()
		await press(footButton('Delete account'))

		expect(requests().map((call) => call.url)).toEqual([
			`${ORIGIN}/v1/account/successor-candidates`,
			`${ORIGIN}/v1/organizations/admin-transfer`,
			`${ORIGIN}/v1/account/erasure`,
		])
		// MUTATION: sending anything but the chosen person's commercial id string makes this red.
		expect(requests()[1]?.body).toEqual({organization_id: 'org-1', to_user_id: '12', idempotency_key: 'idem-1'})
	})
})
