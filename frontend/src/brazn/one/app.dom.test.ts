import {describe, it, expect, beforeAll, beforeEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {init as initI18n} from '../../../public/one/i18n.js'
import {
	COMMERCIAL_OUTCOME_MESSAGE_KEY,
	DENY,
	applyGates,
	clearRefusal,
	describeCommercialRefusal,
	describeForkError,
	getOrganizationError,
	hydrateI18n,
	isRefused,
	readGateFacts,
	refusalText,
	registerView,
	reloadOrganization,
	renderRefusal,
} from '../../../public/one/app.js'
import type {GateFacts} from '../../../public/one/app.js'

// The SHIPPED shell and the SHIPPED English catalogue, as text. Hand-writing a fixture would test
// a page that does not exist: the applier's whole job is to write into the markup in task.html,
// and `.refusal-text:empty{display:none}` in that same file is what makes an empty sentence node
// invisible rather than a gap in the layout.
import taskHtml from '../../../public/one/task.html?raw'
import enRaw from '../../../public/one/i18n/en.json?raw'

/*
 * The DOM half of frontend/public/one/app.js: the gate applier, the one shared refusal path, the
 * i18n hydration and the organization/roster facts.
 *
 * IMPORT ORDER IS LOAD-BEARING. app.js self-schedules boot() only when `#app` already exists, and
 * ES imports are evaluated before any beforeEach runs - so the shell below is injected into a
 * document that app.js has already declined to boot. Never move the injection to module scope.
 */

const ORIGIN = 'https://dev.tasks.brazn.one'

// The shipped shell, minus its own <script type="module" src="./app.js">. The module is already
// imported above; leaving the tag in would ask the DOM to fetch and evaluate a second copy of it.
// Nothing else is altered - the ids, the template and the gate/refusal contract are the page's.
const SHELL = (/<body>([\s\S]*)<\/body>/.exec(taskHtml) ?? ['', ''])[1]
	.replace(/<script[\s\S]*?<\/script>/g, '')

// The real catalogue, served to i18n.js through its only seam - the global fetch. Loading it once
// for the whole file is what lets these tests assert the SENTENCE a user sees rather than a key
// path, and every describe below depends on it.
beforeAll(async () => {
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
	await initI18n('en', ['en'])
	vi.unstubAllGlobals()
})

// P, with a roster: personal-cloud, not an organization administrator, a member of team 7 without
// the admin bit, and team 9 unreadable.
const PERSONAL: GateFacts = {
	hasEdition: true,
	personalEdition: true,
	orgAdmin: false,
	writeRestricted: false,
	teams: {'7': {readable: true, admin: false}, '9': {readable: false, admin: false}},
}

const TEAMS_ADMIN: GateFacts = {
	hasEdition: true,
	personalEdition: false,
	orgAdmin: true,
	writeRestricted: false,
	teams: {'7': {readable: true, admin: true}, '9': {readable: true, admin: true}},
}

// Each control sits in its own row, so `nextElementSibling` is null until the applier inserts a
// sentence node - which is how the "a hidden node explains nothing" assertion stays honest.
const FIXTURE = `
<div class="setting-row"><button id="inviteBtn" class="btn" data-requires="teams">Invite</button></div>
<div class="setting-row"><input id="teamName" class="input" data-requires="team-admin" data-team="7"></div>
<div class="setting-row"><select id="assignee" class="select" data-requires="teams"></select></div>
<div class="setting-row"><button id="orgTab" class="btn" data-requires="admin">Organization</button></div>
<div class="setting-row"><span id="editionLine" data-requires="edition"></span></div>
<div class="setting-row"><div id="teamCard" data-requires="team" data-team="9"><button id="renameBtn" class="btn">Rename</button><input id="cardInput" class="label-inline-input"><textarea id="cardNote"></textarea><select id="cardSelect"></select><p class="refusal-text"></p></div></div>
`

function app(): HTMLElement {
	const root = document.getElementById('app')
	if (root === null) throw new Error('the shell has no #app')
	return root
}

function byId(id: string): HTMLElement {
	const el = document.getElementById(id)
	if (el === null) throw new Error(`fixture has no #${id}`)
	return el
}

function refusalAfter(id: string): Element | null {
	const next = byId(id).nextElementSibling
	return next !== null && next.classList.contains('refusal-text') ? next : null
}

describe('one/app.js DOM applier', () => {
	beforeEach(() => {
		document.body.innerHTML = SHELL
		app().innerHTML = FIXTURE
	})

	it('injects the shipped shell, with the nodes app.js writes into', () => {
		// If task.html ever stops carrying these, every test below would still pass while testing
		// nothing - so the fixture asserts itself first.
		expect(SHELL.length).toBeGreaterThan(200)
		expect(document.getElementById('app')).not.toBeNull()
		expect(document.getElementById('modalRoot')).not.toBeNull()
		expect(document.getElementById('toastRoot')).not.toBeNull()
		expect(document.getElementById('a11yLive')?.getAttribute('aria-live')).toBe('polite')
		expect(document.getElementById('brandLogo')?.tagName).toBe('TEMPLATE')
	})

	it('writes each decision out: hidden, refused-with-a-reason, or untouched', () => {
		applyGates(app(), PERSONAL)

		// Disabled with a reason, and carrying the server-shaped hook a test (and the CSS) reads.
		expect(byId('inviteBtn').classList.contains('is-refused')).toBe(true)
		expect(byId('inviteBtn').getAttribute('data-deny-reason')).toBe(DENY.PERSONAL)
		// The sentence comes off the shipped catalogue through the messageKey the pure decision
		// returned. This is the only place the whole chain is visible at once.
		// MUTATION: changing DENY_MESSAGE_KEY[DENY.PERSONAL] to a key en.json does not carry makes
		// this red - the node would render the raw key path.
		expect(refusalAfter('inviteBtn')?.textContent).toBe('This is part of ONE Teams.')

		expect(byId('teamName').getAttribute('data-deny-reason')).toBe(DENY.TEAM_NOT_ADMIN)
		expect(byId('teamCard').getAttribute('data-deny-reason')).toBe(DENY.TEAM_UNREADABLE)

		// Hidden: no reason attribute and no sentence, because nothing is being explained.
		expect(byId('orgTab').classList.contains('hidden')).toBe(true)
		expect(byId('orgTab').hasAttribute('data-deny-reason')).toBe(false)
		// MUTATION: making applyDecision render a refusal on the hidden branch makes this red, and
		// it would leak the existence of an organization surface to accounts that have none.
		expect(refusalAfter('orgTab')).toBeNull()

		// Enabled: untouched, and no sentence node invented for it.
		expect(byId('editionLine').classList.contains('is-refused')).toBe(false)
		expect(byId('editionLine').hasAttribute('aria-disabled')).toBe(false)
	})

	it('refuses each control in the shape its element type can announce', () => {
		applyGates(app(), PERSONAL)

		const button = byId('inviteBtn') as HTMLButtonElement
		const input = byId('teamName') as HTMLInputElement
		const select = byId('assignee') as HTMLSelectElement

		// A `disabled` button is not focusable, so a screen-reader user could never reach the
		// reason we just wrote next to it.
		// MUTATION: setting `el.disabled = true` for buttons in refuseControl makes this red.
		expect(button.getAttribute('aria-disabled')).toBe('true')
		expect(button.disabled).toBe(false)

		// readOnly, not disabled: still focusable, still announced, and it cannot lose typing the
		// way an ignored-but-editable field would.
		expect(input.readOnly).toBe(true)
		expect(input.getAttribute('aria-disabled')).toBe('true')
		expect(input.disabled).toBe(false)

		// readOnly does not exist on a select, and a select the user can change but the page
		// ignores is the worst of both.
		expect(select.disabled).toBe(true)
	})

	it('refuses the form controls INSIDE a refused group, not just the wrapper', () => {
		applyGates(app(), PERSONAL)

		const input = byId('cardInput') as HTMLInputElement
		const note = byId('cardNote') as HTMLTextAreaElement
		const select = byId('cardSelect') as HTMLSelectElement
		const button = byId('renameBtn') as HTMLButtonElement

		// THE ACCESSIBILITY FIX. `data-requires` sits on the WRAPPER here, exactly as it does on
		// #labelLine in view-task.js, and the wrapper is the only node the applier used to touch.
		// The stylesheet's pointer-events:none stops a mouse and isRefused() stops both the click
		// and the Enter paths, so nothing was ever written - but a keyboard or screen-reader user
		// reached a field that ANNOUNCED as editable, typed into it, and watched the typing be
		// discarded with no explanation. pointer-events is not an accessibility API.
		// MUTATION: deleting the querySelectorAll loop from refuseControl makes this red. Traced:
		// nothing else in the applier reaches a descendant - applyGates only walks [data-requires],
		// and none of these four carries one.
		expect(input.readOnly).toBe(true)
		expect(input.getAttribute('aria-disabled')).toBe('true')
		expect(note.readOnly).toBe(true)
		expect(select.disabled).toBe(true)
		expect(button.getAttribute('aria-disabled')).toBe('true')

		// Each descendant still gets the shape ITS element type can announce, not one blanket
		// attribute: a disabled button could never be focused to reach the reason beside it, and
		// readOnly does not exist on a select.
		// MUTATION: replacing the per-tag dispatch in refuseOne with a single setAttribute makes
		// this red.
		expect(button.disabled).toBe(false)
		expect(input.disabled).toBe(false)

		// The sentence explaining the refusal is NOT marked unavailable. It is the one thing in a
		// refused group that has to stay ordinary.
		// MUTATION: widening REFUSABLE_DESCENDANTS to '*' makes this red.
		const sentence = byId('teamCard').querySelector('.refusal-text')
		expect(sentence?.hasAttribute('aria-disabled')).toBe(false)
		expect(sentence?.textContent).toBe('We cannot read this team\'s members right now.')
	})

	it('releases the whole group again, descendants included', () => {
		// A control the VIEW refused in its own markup - `rename-org` is the documented one
		// (ruling C8.1), and the contract-only commercial controls take the same shape.
		const owned = document.createElement('button')
		owned.id = 'ownRefusal'
		owned.className = 'btn is-refused'
		owned.setAttribute('data-deny-reason', DENY.NO_ROUTE)
		owned.setAttribute('aria-disabled', 'true')
		byId('teamCard').appendChild(owned)

		applyGates(app(), PERSONAL)
		applyGates(app(), TEAMS_ADMIN)

		// The exact inverse, and it has to recurse for the same reason: a group released without
		// its children leaves every control inside it readOnly after the subscription that
		// unlocked it, with nothing on screen saying why.
		// MUTATION: deleting the querySelectorAll loop from releaseControl makes this red.
		expect((byId('cardInput') as HTMLInputElement).readOnly).toBe(false)
		expect(byId('cardInput').hasAttribute('aria-disabled')).toBe(false)
		expect((byId('cardSelect') as HTMLSelectElement).disabled).toBe(false)
		expect(byId('renameBtn').hasAttribute('aria-disabled')).toBe(false)

		// ...but a descendant that owns its own refusal is NOT released by the group's release.
		// The gate never refused it, so the gate has no standing to undo it - and re-enabling
		// `rename-org` because an unrelated gate passed would make a control live for a route
		// that does not exist.
		// MUTATION: dropping the `is-refused`/`data-deny-reason` skip from releaseControl's loop
		// makes this red.
		expect(byId('ownRefusal').classList.contains('is-refused')).toBe(true)
		expect(byId('ownRefusal').getAttribute('aria-disabled')).toBe('true')
		expect(isRefused(byId('ownRefusal'))).toBe(true)
	})

	it('leaves a markup refusal alone when the gate AROUND it passes', () => {
		// THE SHIPPED SHAPE, AND IT IS NOT THE ONE ABOVE. `refusedGroup()` in the views puts
		// `.is-refused` and `data-deny-reason` on a WRAPPER and leaves the control inside carrying
		// `aria-disabled` and nothing else - this is `organizationIdentityBlock` in view-settings.js
		// verbatim, and all five markup refusals on these pages are that shape. The fixture above
		// puts both markers on the button, which no view does, so it agreed with a release loop that
		// read only the child's own attributes and stripped every real one.
		const section = document.createElement('section')
		section.id = 'orgSection'
		section.setAttribute('data-requires', 'admin')
		// The control the gate IS entitled to release, in the same section: without it a skip that
		// swallowed everything would read as a pass here.
		section.innerHTML = '<button id="orgPlainBtn" class="btn" aria-disabled="true">Add</button>'
			+ '<div class="org-identity-item is-refused" data-deny-reason="' + DENY.NO_ROUTE + '">'
			+ '<button id="renameOrgBtn" class="mini-edit" aria-disabled="true"'
			+ ' aria-label="Edit organization name"></button>'
			+ '<p class="refusal-text" data-refusal-source="server">Renaming is not available yet.</p>'
			+ '</div>'
		app().appendChild(section)

		// An organization administrator, so `admin` passes and the section is released on EVERY
		// render - which is why this is the state a real session settles into rather than an edge.
		applyGates(app(), TEAMS_ADMIN)

		// MUTATION: narrowing releaseControl's skip back to the child's OWN `is-refused` /
		// `data-deny-reason` makes this red. That is what shipped: the pencil beside the
		// organization name kept its refusal styling and its sentence, and announced as an
		// ordinary available button to anyone who could not see either.
		expect(byId('renameOrgBtn').getAttribute('aria-disabled')).toBe('true')
		expect(isRefused(byId('renameOrgBtn'))).toBe(true)
		expect(byId('orgPlainBtn').hasAttribute('aria-disabled')).toBe(false)
	})

	it('keeps a control refused when its OWN gate passes but its group\'s does not', () => {
		// applyGates walks in document order, so the group is decided first and an inner node whose
		// own gate passes is released afterwards - which would strip the announcement the group
		// just made while isRefused() and the stylesheet still treat the control as refused.
		const inner = document.createElement('input')
		inner.id = 'innerOk'
		inner.setAttribute('data-requires', 'write')
		byId('teamCard').appendChild(inner)

		applyGates(app(), PERSONAL)

		// PERSONAL is not write-restricted, so `write` passes for this node while the group's
		// `team` gate fails.
		// MUTATION: deleting the `el.parentElement?.closest('.is-refused')` re-refusal from
		// applyDecision's enabled branch makes this red.
		expect((byId('innerOk') as HTMLInputElement).readOnly).toBe(true)
		expect(isRefused(byId('innerOk'))).toBe(true)
	})

	it('releases a control when the gate passes on a later render', () => {
		applyGates(app(), PERSONAL)
		applyGates(app(), TEAMS_ADMIN)

		const input = byId('teamName') as HTMLInputElement
		const select = byId('assignee') as HTMLSelectElement

		// MUTATION: deleting any line of releaseControl - the aria-disabled removal, the readOnly
		// reset or the select's disabled reset - makes this red, and the control would stay dead
		// after the subscription that unlocked it.
		expect(byId('inviteBtn').classList.contains('is-refused')).toBe(false)
		expect(byId('inviteBtn').hasAttribute('aria-disabled')).toBe(false)
		expect(byId('inviteBtn').hasAttribute('data-deny-reason')).toBe(false)
		expect(input.readOnly).toBe(false)
		expect(select.disabled).toBe(false)
		expect(refusalAfter('inviteBtn')?.textContent).toBe('')
		expect(byId('orgTab').classList.contains('hidden')).toBe(false)
	})

	it('keeps a server refusal through a re-gate, and clears only its own', () => {
		const target = byId('inviteBtn')
		renderRefusal(target, {
			message: 'No free seat is available.',
			reason: DENY.SERVER,
			source: 'server',
		})

		applyGates(app(), TEAMS_ADMIN)

		// A server refusal written by an action a moment ago is the more recent and more specific
		// truth; erasing it because a gate re-ran would drop the only report the write produced.
		// MUTATION: dropping the `{source: 'gate'}` argument from the clearRefusal calls in
		// applyDecision makes this red.
		expect(refusalAfter('inviteBtn')?.textContent).toBe('No free seat is available.')

		clearRefusal(target, {source: 'server'})
		expect(refusalAfter('inviteBtn')?.textContent).toBe('')
	})

	it('renders the server sentence verbatim and never as markup', () => {
		const node = renderRefusal(byId('inviteBtn'), {
			message: '<img src=x onerror="alert(1)">',
			messageKey: 'one.deny.personalEdition',
			source: 'server',
		})

		// The message comes off the wire from two different codebases and one of them is not this
		// repository.
		// MUTATION: assigning through innerHTML in renderRefusal makes this red - querySelector
		// would find a real element.
		expect(node?.querySelector('img')).toBeNull()
		expect(node?.textContent).toBe('<img src=x onerror="alert(1)">')

		// The server's own words beat our translated paraphrase whenever both are present.
		// MUTATION: preferring messageKey over message in refusalText makes this red.
		expect(refusalText({message: 'Seat limit reached.', messageKey: 'one.deny.personalEdition'}))
			.toBe('Seat limit reached.')
		// ...and the key is used only when the server said nothing at all.
		expect(refusalText({message: '', messageKey: 'one.deny.writeRestricted'}))
			.toBe('Your account is read-only right now.')
	})

	it('applies a gate to the root node itself, not only to its descendants', () => {
		const el = document.createElement('button')
		el.setAttribute('data-requires', 'teams')
		app().appendChild(el)

		applyGates(el, PERSONAL)

		// openModal() applies gates to the modal root, whose own first child commonly carries the
		// gate.
		// MUTATION: deleting the `nodes.unshift(scope)` line from applyGates makes this red.
		expect(el.classList.contains('is-refused')).toBe(true)
	})

	it('treats a control inside a refused group as refused', () => {
		applyGates(app(), PERSONAL)

		// aria-disabled does not stop a click the way `disabled` does, and `.is-refused` on a GROUP
		// disables its children in CSS only - so the handler guard has to walk ancestors, or the
		// one honest thing about a refused control (that pressing it does nothing) would be true in
		// the stylesheet and false in the handler.
		// MUTATION: replacing the `closest(...)` call in isRefused with a classList check on the
		// element itself makes this red.
		expect(isRefused(byId('renameBtn'))).toBe(true)
		expect(isRefused(byId('editionLine'))).toBe(false)
		// A caller with nothing to inspect is refused, not permitted.
		expect(isRefused(null)).toBe(true)
	})
})

describe('one/app.js i18n hydration', () => {
	beforeEach(() => {
		document.body.innerHTML = SHELL
	})

	it('replaces text and all four attribute forms', () => {
		app().innerHTML = `
			<h2 id="h" data-i18n="organization.title"></h2>
			<button id="a" data-i18n-aria="misc.closeDialog"></button>
			<input id="p" data-i18n-placeholder="one.org.teamNameExample">
			<span id="t" data-i18n-title="misc.close"></span>
		`

		hydrateI18n(app())

		expect(byId('h').textContent).toBe('Organization')
		expect(byId('a').getAttribute('aria-label')).toBe('Close dialog')
		expect(byId('p').getAttribute('placeholder')).toBe('e.g. Product')
		// MUTATION: dropping any row from I18N_ATTRIBUTES makes this red, and that attribute's
		// copy would ship English to every locale with nothing reporting it.
		expect(byId('t').getAttribute('title')).toBe('Close')
	})

	it('reaches inside the brand-logo template, which a page-wide walk cannot see', () => {
		const template = document.getElementById('brandLogo') as HTMLTemplateElement

		hydrateI18n(document)
		// Template content is a separate DocumentFragment, so document.querySelectorAll never sees
		// these two nodes. The markup's own alt is the English fallback that would otherwise ship
		// to all six languages in silence.
		expect(template.content.querySelector('img')?.getAttribute('alt')).toBe('ONE Tasks')

		hydrateI18n(template.content)
		// MUTATION: making hydrateI18n reject a DocumentFragment root - or hydrateShell dropping
		// its explicit template pass - makes this red.
		expect(template.content.querySelector('img')?.getAttribute('alt')).toBe('ONE')
	})
})

describe('one/app.js organization and roster facts', () => {
	interface Call {
		url: string
		init: RequestInit
	}

	const calls: Call[] = []
	let queue: Response[] = []

	const fetchStub = vi.fn(async (url: string, init: RequestInit = {}) => {
		calls.push({url, init})
		const next = queue.shift()
		if (next === undefined) throw new Error(`unstubbed request: ${url}`)
		return next
	})

	function json(body: unknown, status = 200): Response {
		return new Response(JSON.stringify(body), {status, headers: {'content-type': 'application/json'}})
	}

	beforeEach(() => {
		document.body.innerHTML = SHELL
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})
		// id 42 is the acting user; the roster below is written around it.
		api.setToken(`header.${btoa(JSON.stringify({id: 42, brazn_edition: 'teams-cloud'}))}.signature`)
	})

	it('degrades ONE unreadable roster instead of losing the tab', async () => {
		queue = [
			json({
				teams: [
					{team_id: 7, project_id: 70, primary: false},
					{team_id: 9, project_id: 90, primary: true},
					{team_id: 11, project_id: 110, primary: false},
				],
			}),
			json({id: 7, members: [{id: 42, admin: false}, {id: 5, admin: true}]}),
			json({message: 'You are not a member of this team.'}, 403),
			json({id: 11, members: [{id: 42, admin: true}]}),
		]

		await reloadOrganization()
		const facts = readGateFacts()

		expect(facts.orgAdmin).toBe(true)
		// Team.CanRead requires membership, and the organization administrator is commonly not a
		// member of the commercially provisioned primary team - so a 403 here is the EXPECTED case.
		// MUTATION: replacing Promise.allSettled with Promise.all in loadTeams makes this red -
		// reloadOrganization() rejects and the whole Team-management tab blanks on the roster the
		// administrator was always going to be refused.
		expect(facts.teams).toEqual({
			'7': {readable: true, admin: false},
			'9': {readable: false, admin: false},
			'11': {readable: true, admin: true},
		})

		// The admin bit comes from members[].admin for the acting user and is NEVER inferred from
		// organization administration: Team.CanUpdate is the team-admin bit, so an organization
		// administrator with no admin row on team 7 cannot rename it.
		// MUTATION: setting `admin: facts.orgAdmin` (or true for every readable team) in loadTeams
		// makes this red on team 7.
		expect(facts.teams['7'].admin).toBe(false)
	})

	it('reads a 403 organization as "no organization surface", not as an error', async () => {
		queue = [json({message: 'You are not the organization administrator.'}, 403)]

		await reloadOrganization()
		const facts = readGateFacts()

		// 403 is the ordinary answer for P, M, TA and U, and for every CI session. It produces no
		// toast, no banner and no console line - it just hides the tabs.
		//
		// CORRECTED MUTATION SENTENCE. This test used to claim that "removing the fold in
		// api.getOrganization" would redden it. TRACED, AND FALSE: without the fold,
		// getOrganization() throws a ForkError, loadOrganization catches it and sets
		// state.organization = null anyway, so orgAdmin is still false, teams is still {} and the
		// call count is still 1. All three assertions stay GREEN. The fold is real and is covered
		// in api.requests.test.ts, where the two outcomes actually differ; what THIS test pins is
		// the derivation, so the mutation has to be in the derivation.
		//
		// MUTATION: sourcing `orgAdmin` from anything but the organization read returning 200 - e.g.
		// `orgAdmin: api.hasEditionClaim()` in readGateFacts - makes this red. The token minted in
		// beforeEach carries `brazn_edition: 'teams-cloud'`, so the claim is present and every such
		// mutation renders the Organization tab live for an account the server answered 403 for.
		expect(facts.orgAdmin).toBe(false)
		expect(facts.teams).toEqual({})
		expect(calls).toHaveLength(1)
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v1/brazn/organization')

		// And the OTHER half of F3, which is what the false sentence above was gesturing at: a 403
		// leaves NO error behind. That is the difference between "you are not an administrator" and
		// "something broke", and it is the one distinction this call draws.
		// MUTATION: removing the 403 fold in api.getOrganization makes THIS line red - the throw
		// would land in loadOrganization's catch and organizationError would be set for the
		// majority of users on the instance.
		expect(getOrganizationError()).toBeNull()
	})

	it('keeps a NON-403 organization failure as an error, and says so on screen', async () => {
		// A 500 is not a 403. Until this notice existed the two rendered byte-identically - both
		// tabs gone, in silence - so an administrator who hit a transient failure saw exactly the
		// screen a demoted account sees, with no banner, no toast and no retry. C4 reserves HIDE
		// for "the whole surface is absent for this user", which a 500 is not.
		queue = [json({message: 'boom'}, 500)]
		registerView('settings', {render: () => '<p id="viewBody">settings</p>'})

		await reloadOrganization()

		// MUTATION: widening api.getOrganization's fold to `status >= 400` makes this red -
		// organizationError goes null and the 500 becomes indistinguishable from the 403 again.
		expect(getOrganizationError()).not.toBeNull()

		const notice = app().querySelector('[data-notice="organization-unavailable"]')
		// MUTATION: deleting the organizationNotice() term from pageNotices() makes this red, and
		// getOrganizationError() goes back to being an export nothing reads.
		expect(notice).not.toBeNull()
		expect(notice?.querySelector('strong')?.textContent)
			.toBe('We cannot read your subscription right now.')
		// The retry is the point of the notice: a surface with no way out is only a slower silence.
		expect(notice?.querySelector('button')?.getAttribute('data-action')).toBe('retry')
		// The view still renders underneath. The account tab does not depend on the organization,
		// and losing the whole page over a surface most users never see would be worse.
		expect(app().querySelector('#viewBody')).not.toBeNull()
	})

	it('renders no organization notice for the ordinary 403', async () => {
		queue = [json({message: 'You are not the organization administrator.'}, 403)]
		registerView('settings', {render: () => '<p id="viewBody">settings</p>'})

		await reloadOrganization()

		// The guard on the guard. Without this the notice could fire for everyone and the test
		// above would still pass, which is CLAUDE.md section 4's "a test that cannot fail".
		// MUTATION: keying the notice on `state.organization === null` instead of on
		// `state.organizationError !== null` makes this red - and every non-administrator on the
		// instance would be shown a failure notice for the ordinary answer.
		expect(app().querySelector('[data-notice="organization-unavailable"]')).toBeNull()
	})

	it('turns a fork refusal into the server\'s own sentence, verbatim', async () => {
		queue = [json({
			message: 'You have used the teams your seats allow.',
			seats_purchased: 6,
			teams_used: 2,
			seats_needed: 9,
		}, 409)]

		const err: unknown = await api.createOrganizationTeam('Product').catch((caught: unknown) => caught)
		const refusal = describeForkError(err)

		// The 409 body carries a server-computed seats_needed. A translated paraphrase could state
		// a number the server would refuse, so the page renders what it was sent.
		// MUTATION: mapping the 409 to a t() key instead of to err.serverMessage makes this red.
		expect(refusal.message).toBe('You have used the teams your seats allow.')
		expect(refusal.messageKey).toBeNull()
	})

	it('reports the CI shape of a commercial call as unavailable, not as failure or success', () => {
		// reason 'not-json' is what readCommercialResult answers for the SPA index.html the fork
		// serves at 200 for an unrouted /v1 path - the shape CI produces, every time.
		// MUTATION: mapping 'not-json' onto the generic request-failed key makes this red, and the
		// page would tell the user their action failed when nothing was ever attempted.
		const absent = describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.NOT_JSON})
		expect(absent.messageKey).toBe('one.deny.commercial')
		expect(absent.reason).toBe(DENY.COMMERCIAL)
		expect(refusalText(absent)).toBe('We could not reach the subscription service, so nothing was changed.')

		// A transport failure - fetch itself rejecting - reports as absence for the same reason:
		// nothing answered, so nothing was attempted. It is NOT the generic "that did not work",
		// which would tell the user their action failed against a service it never reached.
		// MUTATION: dropping COMMERCIAL_REFUSAL.NETWORK from the first branch of
		// describeCommercialRefusal makes this red - it falls through to one.error.requestFailed.
		const unreachable = describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.NETWORK, status: 0})
		expect(unreachable.reason).toBe(DENY.COMMERCIAL)
		expect(refusalText(unreachable)).toBe('We could not reach the subscription service, so nothing was changed.')

		// A service that answered in its own words is quoted rather than paraphrased.
		const spoken = describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			message: 'No free seat is available.',
		})
		expect(spoken.message).toBe('No free seat is available.')
		expect(spoken.messageKey).toBeNull()

		// A silent HTTP refusal still has to say something the user can report - AND IT MUST NOT BE
		// THE STATUS. `HTTP 503` was literally what this rendered, and a bare status is the normal
		// shape for the commonest commercial refusals, not an edge case. The full status table has
		// its own case below; this line is here because it is the one the old sentence lived on.
		// MUTATION: putting the status back into the sentence - restoring `messageParams:
		// {status}` and an `HTTP {status}` catalogue value - makes this red.
		const http = describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.HTTP, status: 503})
		expect(refusalText(http)).toBe('The subscription service is unavailable right now, so nothing was changed.')
		expect(http.messageParams).toBeUndefined()

		// A lost session is its own sentence, not a generic failure.
		expect(refusalText(describeForkError(new api.SessionLostError())))
			.toBe('Your session has ended. Sign in to continue.')
		expect(refusalText(describeForkError(new TypeError('boom'))))
			.toBe('That did not work. Nothing was changed.')
	})

	it('names the refusal OUTCOME the service sent, not a generic failure', () => {
		// THE OTHER HALF OF BAR 8. api.js's descriptors read the full outcome vocabulary and
		// classify every value; until this map existed only the affirmative half reached a
		// sentence, and every 200-with-refusal rendered "That did not work. Nothing was changed."
		//
		// The body is the four fields POST /v1/organizations/invitations really projects
		// (client-http-27c95232:2854-2884). There is NO `message` among them, which is precisely
		// why the outcome has to carry the sentence.
		const notInvitable = describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			status: 200,
			message: null,
			body: {outcome: 'not_invitable', invited_user_id: 'usr-7', invitation: null, seat_notice: null},
		})

		// MUTATION: deleting the COMMERCIAL_REFUSAL.OUTCOME branch from describeCommercialRefusal
		// makes this red - the sentence falls back to one.error.requestFailed, which is true of
		// every refusal and therefore tells the administrator nothing about an address that can
		// never be invited.
		expect(refusalText(notInvitable)).toBe(
			'That address cannot be invited. It belongs to a personal account, to another organization, '
			+ 'or to an account that has been deleted. Nothing was sent.',
		)

		// The seat-purchase refusals name which number is in the way (percy-model:1130-1147).
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'below_users', seats: 5, previous_seats: 5},
		}).messageKey).toBe('one.commercial.belowUsers')

		// A removal refused because the person still administers the organization gets the next
		// step, not an error: transfer first, then remove (percy-model:1533-1539).
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'still_administrator'},
		}).messageKey).toBe('one.commercial.stillAdministrator')

		// The SERVER'S OWN sentence still wins over every key in the table (ruling C4).
		// MUTATION: moving the outcome lookup above the `message` branch makes this red.
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			message: 'That address belongs to another organization.',
			body: {outcome: 'not_invitable'},
		}).message).toBe('That address belongs to another organization.')

		// An outcome nobody has classified fails to the generic sentence rather than to a guess -
		// the same direction readCommercialResult already fails in.
		// MUTATION: defaulting the lookup to any real key makes this red.
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'queued'},
		}).messageKey).toBe('one.error.requestFailed')

		// And an inherited Object.prototype member is not a vocabulary: a bare TABLE[value] index
		// answers a function for 'constructor', which would then be handed to t() as a key.
		// MUTATION: replacing outcomeKey's hasOwnProperty guard with a bare index makes this red.
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'constructor'},
		}).messageKey).toBe('one.error.requestFailed')
	})

	it('reads invitation_outcome on a not_admitted decision, which is the half that matters', () => {
		// POST /v1/team-access-requests/decide projects TWO fields, and the handler says why:
		// "an administrator told only 'not admitted' cannot tell 'buy more seats' from 'this
		// address belongs to another organization'" (client-http-27c95232:3251-3264).
		// MUTATION: dropping the `not_admitted` branch from commercialOutcomeMessageKey makes this
		// red - the nested cause is discarded and the outer, causeless sentence is shown instead.
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'not_admitted', invitation_outcome: 'at_seat_ceiling'},
		}).messageKey).toBe('one.commercial.atSeatCeiling')

		// When the nested value is one nothing has classified, the outer sentence still stands -
		// it is less specific but it is true, and it says the request stays open.
		expect(describeCommercialRefusal({
			reason: api.COMMERCIAL_REFUSAL.OUTCOME,
			body: {outcome: 'not_admitted', invitation_outcome: 'something_new'},
		}).messageKey).toBe('one.commercial.notAdmitted')
	})

	it('gives every enumerated bare status a sentence, and NEVER the status itself', () => {
		// The blocker. A bare status is the normal shape for the commonest commercial refusals -
		// api.js cites thirteen `bare(response, …)` sites - and every one of them rendered as
		// `HTTP 403` / `HTTP 402` / `HTTP 404` to the user.
		// MUTATION: emptying COMMERCIAL_STATUS_MESSAGE_KEY makes every line below red.
		const expected: ReadonlyArray<readonly [number, string]> = [
			[401, 'one.commercial.notAuthenticated'],
			[402, 'one.commercial.paymentRequired'],
			[403, 'one.commercial.forbidden'],
			[404, 'one.commercial.notFound'],
			[409, 'one.commercial.conflict'],
			[503, 'one.commercial.unavailable'],
		]
		for (const [status, key] of expected) {
			const refusal = describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.HTTP, status})
			expect(refusal.messageKey, `status ${status}`).toBe(key)
			// The number itself must never reach a rendered string. This is what `HTTP {status}`
			// plus `messageParams` used to do, and it is the assertion that keeps it from coming
			// back by any route - a new key whose value interpolated the status would redden here.
			expect(refusal.messageParams, `status ${status}`).toBeUndefined()
			expect(refusalText(refusal), `status ${status}`).not.toContain(String(status))
		}

		// The 403 sentence is SCOPE-NEUTRAL on purpose, and this is the assertion that records
		// why. describeCommercialRefusal is handed the result alone, with no operation handle, so
		// it cannot tell an organization-scoped 403 (invite, removal, the join queue - where "you
		// are not the organization administrator" is exactly right) from an account-scoped one
		// (erasure, the successor list - where it is a cause the page never saw).
		// MUTATION: pointing 403 at a key that names the organization administrator makes this
		// red, and every account-scoped refusal would state a reason nobody observed.
		expect(refusalText(describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.HTTP, status: 403})))
			.not.toContain('administrator')

		// A status nothing has enumerated still degrades to a sentence, not to a number.
		const unknown = describeCommercialRefusal({reason: api.COMMERCIAL_REFUSAL.HTTP, status: 418})
		expect(unknown.messageKey).toBe('one.error.requestFailed')
		expect(refusalText(unknown)).not.toContain('418')
	})

	it('gives a bodiless FORK refusal a sentence too, and not the same one', async () => {
		// The fork usually sends `message ?? detail ?? title`, so this is the tail case - but the
		// tail rendered `HTTP 404` as well. The two tables are deliberately separate: a bare 403 on
		// /api/v2 is the managed gate (`service-managed` answers 403 for everyone, instance
		// administrator included) and on /v1 it is `not_administrator`. One shared sentence would
		// be wrong on whichever side lost.
		//
		// The errors are produced by driving a real fork call rather than by constructing a
		// ForkError by hand: `api.d.ts` declares no constructor signature, so `new ForkError(403,
		// …)` would not type-check, and going through the shipped path also proves the body really
		// yields a null serverMessage rather than assuming it.
		async function refusalFor(status: number) {
			queue = [json({}, status)]
			const err: unknown = await api.createOrganizationTeam('Product').catch((caught: unknown) => caught)
			return describeForkError(err)
		}

		// MUTATION: pointing describeForkError at COMMERCIAL_STATUS_MESSAGE_KEY makes this red -
		// its 403 says "the subscription service would not allow that", which names the wrong
		// service for a refusal that came from the fork's managed gate.
		const forbidden = await refusalFor(403)
		expect(forbidden.messageKey).toBe('one.deny.forkForbidden')
		expect(refusalText(forbidden)).not.toContain('403')
		expect(forbidden.messageParams).toBeUndefined()

		expect((await refusalFor(404)).messageKey).toBe('one.deny.forkNotFound')
		expect((await refusalFor(500)).messageKey).toBe('one.error.serverUnavailable')

		// A status with no sentence degrades to prose rather than to a number.
		expect(refusalText(await refusalFor(418))).toBe('That did not work. Nothing was changed.')
	})

	it('every outcome sentence in the table is real English, not a key path', () => {
		// The end-to-end half of the sweep in app.gating.test.ts: that file proves each key EXISTS
		// in the shipped catalogue, this one proves t() actually resolves it through the loaded
		// catalogue rather than falling back to the key path.
		// MUTATION: renaming any COMMERCIAL_OUTCOME_MESSAGE_KEY value without adding the key makes
		// this red - t() returns the dotted path, which contains a '.' and starts with 'one.'.
		for (const key of Object.values(COMMERCIAL_OUTCOME_MESSAGE_KEY)) {
			const sentence = refusalText({messageKey: key})
			expect(sentence, key).not.toBe(key)
			expect(sentence.length, key).toBeGreaterThan(20)
		}
	})
})
