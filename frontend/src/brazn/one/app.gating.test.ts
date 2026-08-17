import {describe, it, expect, beforeEach} from 'vitest'

import * as api from '../../../public/one/api.js'
import {
	ATTRIBUTE_HOOKS,
	COMMERCIAL_OUTCOME_MESSAGE_KEY,
	DENY,
	DENY_MESSAGE_KEY,
	GATES,
	GATES_THAT_HIDE,
	SETTINGS_TABS,
	VIEWS,
	actionNames,
	decideGate,
	editionMessageKey,
	parseRoute,
	readGateFacts,
	registerActions,
	resolveRoute,
	routeToSearch,
	shouldHandOffToLogin,
} from '../../../public/one/app.js'
import type {GateFacts, GateRequest} from '../../../public/one/app.js'

import enRaw from '../../../public/one/i18n/en.json?raw'

/*
 * THE ROLE MATRIX - roughly forty controls across five roles plus the write-restricted overlay,
 * and the largest body of new logic on this page (ruling C6).
 *
 * It is driven entirely through `decideGate`, which is pure: no DOM, no module state, no t(), no
 * clock, no network. That is what lets the matrix be a table here instead of forty mounted
 * fixtures. The thin DOM applier that writes these decisions out is covered in app.dom.test.ts.
 *
 * Importing app.js is side-effect free: boot() self-schedules only when `#app` exists, and this
 * file never mounts a shell.
 */

type Role = 'OA' | 'TA' | 'M' | 'P' | 'U'
type State = 'enabled' | 'disabled' | 'hidden'

/*
 * The five roles, as the facts each one actually presents:
 *   OA  organization administrator - the org read returned 200. Not a member of team 9, which is
 *       the EXPECTED case for the commercially provisioned primary team (ruling C11).
 *   TA  team administrator - members[].admin on team 7, but no organization surface.
 *   M   ordinary team member.
 *   P   personal-cloud account.
 *   U   no claims at all. This is every CI session, and the row most likely to be got wrong.
 */
const FACTS: Record<Role, GateFacts> = {
	OA: {
		hasEdition: true,
		personalEdition: false,
		orgAdmin: true,
		writeRestricted: false,
		teams: {'7': {readable: true, admin: true}, '9': {readable: false, admin: false}},
	},
	TA: {
		hasEdition: true,
		personalEdition: false,
		orgAdmin: false,
		writeRestricted: false,
		teams: {'7': {readable: true, admin: true}},
	},
	M: {
		hasEdition: true,
		personalEdition: false,
		orgAdmin: false,
		writeRestricted: false,
		teams: {'7': {readable: true, admin: false}},
	},
	P: {
		hasEdition: true,
		personalEdition: true,
		orgAdmin: false,
		writeRestricted: false,
		teams: {},
	},
	U: {
		hasEdition: false,
		personalEdition: false,
		orgAdmin: false,
		writeRestricted: false,
		teams: {},
	},
}

const ROLES: readonly Role[] = ['OA', 'TA', 'M', 'P', 'U']

interface Row {
	label: string
	request: GateRequest
	expected: Record<Role, State>
}

const MATRIX: readonly Row[] = [
	{
		// The Organization and Team tabs. HIDE, not disable: 403 is the ORDINARY answer for
		// everyone but OA, and a disabled tab strip would advertise an organization concept to
		// accounts that have none.
		label: 'organization tab',
		request: {requires: 'admin'},
		expected: {OA: 'enabled', TA: 'hidden', M: 'hidden', P: 'hidden', U: 'hidden'},
	},
	{
		// The edition line. HIDE for U: there is no true edition to print, and a disabled line
		// saying nothing is worse than no line.
		label: 'edition line',
		request: {requires: 'edition'},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'enabled', U: 'hidden'},
	},
	{
		// Labels and the assignee select. U is ENABLED: absence of the claim is the permissive
		// case, because the failure that costs a customer access is worse than the one that costs
		// a wasted click - and the server refuses for real either way.
		label: 'label chips / assignee select',
		request: {requires: 'teams'},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'disabled', U: 'enabled'},
	},
	{
		label: 'any write control, no restriction in force',
		request: {requires: 'write'},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'enabled', U: 'enabled'},
	},
	{
		label: 'team 7 roster',
		request: {requires: 'team', team: '7'},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'disabled', U: 'disabled'},
	},
	{
		label: 'team 7 rename / member admin toggle',
		request: {requires: 'team-admin', team: '7'},
		expected: {OA: 'enabled', TA: 'enabled', M: 'disabled', P: 'disabled', U: 'disabled'},
	},
	{
		// The primary team OA cannot read. One 403 degrades ONE row; it must not blank the tab.
		label: 'team 9 rename (roster unreadable)',
		request: {requires: 'team-admin', team: '9'},
		expected: {OA: 'disabled', TA: 'disabled', M: 'disabled', P: 'disabled', U: 'disabled'},
	},
	{
		// A team-scoped gate with no data-team resolves to unreadable, never to "fine": the page
		// genuinely cannot read a team it cannot name.
		label: 'team-scoped control with no data-team',
		request: {requires: 'team-admin'},
		expected: {OA: 'disabled', TA: 'disabled', M: 'disabled', P: 'disabled', U: 'disabled'},
	},
	{
		// Hide beats disable: the whole surface is absent, so there is nothing to explain.
		label: 'admin-only write control',
		request: {requires: 'admin write'},
		expected: {OA: 'enabled', TA: 'hidden', M: 'hidden', P: 'hidden', U: 'hidden'},
	},
	{
		label: 'ungated control',
		request: {requires: ''},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'enabled', U: 'enabled'},
	},
	{
		label: 'node with no data-requires at all',
		request: {},
		expected: {OA: 'enabled', TA: 'enabled', M: 'enabled', P: 'enabled', U: 'enabled'},
	},
	{
		// A typo in a gate name must REFUSE the control and be visible, never silently enable it.
		label: 'misspelled gate token',
		request: {requires: 'teamadmin'},
		expected: {OA: 'disabled', TA: 'disabled', M: 'disabled', P: 'disabled', U: 'disabled'},
	},
]

function lookup(catalogue: unknown, key: string): unknown {
	let node: unknown = catalogue
	for (const part of key.split('.')) {
		if (node === null || typeof node !== 'object') return undefined
		node = (node as Record<string, unknown>)[part]
	}
	return node
}

describe('one/app.js role matrix', () => {
	it('resolves every gated control to the same state for every role', () => {
		let disabledSeen = 0
		let hiddenSeen = 0

		for (const row of MATRIX) {
			for (const role of ROLES) {
				const decision = decideGate(row.request, FACTS[role])
				expect(decision.state, `${row.label} for ${role}`).toBe(row.expected[role])

				if (decision.state === 'disabled') {
					disabledSeen += 1
					// Ruling C4's whole point: a control the user can see but not use has to say
					// why. A disabled control with no reason is the state this page exists to
					// eliminate.
					// MUTATION: returning `disabled(null)` anywhere, or dropping an entry from
					// DENY_MESSAGE_KEY, makes this red.
					expect(decision.reason, `${row.label} for ${role}`).not.toBeNull()
					expect(decision.messageKey, `${row.label} for ${role}`).not.toBeNull()
				}
				if (decision.state === 'hidden') {
					hiddenSeen += 1
					// Nothing is rendered to explain a hidden node, so it carries no message key.
					expect(decision.reason, `${row.label} for ${role}`).not.toBeNull()
					expect(decision.messageKey, `${row.label} for ${role}`).toBeNull()
				}
				if (decision.state === 'enabled') {
					expect(decision.reason, `${row.label} for ${role}`).toBeNull()
					expect(decision.messageKey, `${row.label} for ${role}`).toBeNull()
				}
			}
		}

		// The two counts keep the branch assertions above from being vacuous: if the matrix ever
		// collapsed to all-enabled, the reason checks would never run and the suite would still be
		// green.
		// 21 disabled and 9 hidden across 12 controls x 5 roles; the remaining 30 are enabled.
		expect(disabledSeen).toBe(21)
		expect(hiddenSeen).toBe(9)
	})

	it('hides only what GATES_THAT_HIDE names, and disables everything else', () => {
		// MUTATION: removing 'admin' from GATES_THAT_HIDE makes the `admin` row of the matrix red -
		// and the resulting state is 'enabled', not 'disabled', because no rule in DISABLE_ORDER
		// covers `admin` and the unknown-token sweep accepts it as a known gate. That is an
		// organization tab rendered live for an account with no organization.
		expect([...GATES_THAT_HIDE]).toEqual(['admin', 'edition'])
		// Every hide gate must also be a known gate, or it would fail closed before it could hide.
		for (const gate of GATES_THAT_HIDE) expect(GATES).toContain(gate)
	})

	it('names the right refusal when two gates fail at once', () => {
		const restrictedPersonal: GateFacts = {...FACTS.P, writeRestricted: true}

		// Fixed order, so the sentence a control carries is deterministic: a Team-Edition boundary
		// survives paying the invoice and the write restriction does not, so it is the sentence
		// still true tomorrow.
		// MUTATION: moving 'write' before 'teams' in DISABLE_ORDER makes this red.
		expect(decideGate({requires: 'teams write'}, restrictedPersonal).reason).toBe(DENY.PERSONAL)

		const restrictedTeams: GateFacts = {...FACTS.M, writeRestricted: true}
		expect(decideGate({requires: 'teams write'}, restrictedTeams).reason).toBe(DENY.WRITE_RESTRICTED)
	})

	it('prefers "roster unreadable" over "not an administrator" for a team it could not read', () => {
		// We do not know the admin bit of a team we could not read, so claiming the user is not an
		// administrator of it would state a fact we never saw.
		// MUTATION: swapping the two guards in decideGate's `team-admin` case makes this red.
		expect(decideGate({requires: 'team-admin', team: '9'}, FACTS.OA).reason).toBe(DENY.TEAM_UNREADABLE)
		expect(decideGate({requires: 'team-admin', team: '7'}, FACTS.M).reason).toBe(DENY.TEAM_NOT_ADMIN)
	})

	it('says "no teams yet" for an organization with none, not "we cannot read this team"', () => {
		// view-settings.js emits data-team="" when selectedTeam() is null, and for an organization
		// that has not created its first team yet that is the ORDINARY state, not a failure. It used
		// to resolve to TEAM_UNREADABLE - "We cannot read this team's members right now." - which
		// names a team that does not exist and tells an administrator to wait for something that is
		// never coming.
		const emptyOrg: GateFacts = {...FACTS.OA, teams: {}}
		expect(decideGate({requires: 'team', team: ''}, emptyOrg).reason).toBe(DENY.NO_TEAM)
		expect(decideGate({requires: 'team-admin', team: ''}, emptyOrg).reason).toBe(DENY.NO_TEAM)

		// The distinction is EMPTINESS OF THE ROSTER, not the empty string on its own. An
		// organization that has teams, with a control scoped to none of them, is still a control the
		// page cannot resolve - that stays "unreadable", which is what it honestly is.
		// MUTATION: making teamFact return NO_TEAM for every empty data-team - i.e. dropping the
		// hasAnyTeam() check - makes THIS line red, and an administrator with three teams would be
		// told the organization has none.
		expect(decideGate({requires: 'team', team: ''}, FACTS.OA).reason).toBe(DENY.TEAM_UNREADABLE)

		// And a NAMED team the roster does not list is unreadable too, never absent: we were told
		// the team exists, we simply could not read it.
		// MUTATION: falling back to NO_TEAM in teamFact's second return makes this red.
		expect(decideGate({requires: 'team', team: '404'}, FACTS.OA).reason).toBe(DENY.TEAM_UNREADABLE)
	})

	it('fails closed on an unrecognised gate token', () => {
		const decision = decideGate({requires: 'sudo'}, FACTS.OA)

		// MUTATION: deleting the trailing `for (const token of tokens)` sweep in decideGate makes
		// this red - an unknown token would resolve to "no requirement" and the control would go
		// live.
		expect(decision.state).toBe('disabled')
		expect(decision.reason).toBe(DENY.UNKNOWN_GATE)

		// A known token mixed with an unknown one still refuses.
		expect(decideGate({requires: 'write sudo'}, FACTS.OA).state).toBe('disabled')
	})

	it('carries a shipped English sentence for every reason it can render', () => {
		const catalogue: unknown = JSON.parse(enRaw)

		// TWO SWEEPS, because one of them cannot see half the table.
		//
		// First: every key the MATRIX can actually produce. This is the end-to-end half - a
		// decision comes out of decideGate and its key is looked up in the shipped catalogue.
		const reachable = new Set<string>()
		for (const row of MATRIX) {
			for (const role of ROLES) {
				const {messageKey} = decideGate(row.request, FACTS[role])
				if (messageKey !== null) reachable.add(messageKey)
			}
		}
		reachable.add(decideGate({requires: 'write'}, {...FACTS.M, writeRestricted: true}).messageKey ?? '')
		expect(reachable.size).toBeGreaterThanOrEqual(4)
		for (const key of reachable) {
			expect(typeof lookup(catalogue, key), `${key} in en.json`).toBe('string')
		}

		// Second: the WHOLE table, traced rather than assumed. decideGate can never emit
		// DENY.COMMERCIAL or DENY.SERVER - describeCommercialRefusal and describeForkError write
		// those - so a sweep driven from the matrix alone leaves two of the ten entries
		// unchecked, and renaming either would ship a raw key path with only a console warning
		// behind it. This is the assertion the sentence below is actually about.
		// MUTATION: renaming ANY DENY_MESSAGE_KEY value without adding the key to
		// public/one/i18n/en.json makes this red.
		const declared = Object.values(DENY_MESSAGE_KEY).filter((key): key is string => key !== null)
		expect(declared.length).toBe(9)
		for (const key of declared) {
			expect(typeof lookup(catalogue, key), `${key} in en.json`).toBe('string')
		}

		// THE THIRD TABLE, and the one that had no sweep at all until this round: the commercial
		// service's refusal `outcome` vocabulary. It is reachable from neither of the two above -
		// decideGate cannot emit it, and DENY_MESSAGE_KEY does not list it, because the value comes
		// off the wire rather than out of a gate. Every entry is a sentence a user reads on a
		// 200-with-refusal, so a rename here ships a raw dotted path onto a refusal surface.
		// MUTATION: renaming any COMMERCIAL_OUTCOME_MESSAGE_KEY value without adding the key to
		// public/one/i18n/en.json makes this red.
		const outcomes = Object.values(COMMERCIAL_OUTCOME_MESSAGE_KEY)
		expect(outcomes.length).toBe(10)
		for (const key of outcomes) {
			expect(typeof lookup(catalogue, key), `${key} in en.json`).toBe('string')
		}

		// And the hidden reasons carry no key at all: nothing is rendered to explain them, so a
		// key appearing there would be a sentence with nowhere to go.
		expect(DENY_MESSAGE_KEY[DENY.NOT_ADMIN]).toBeNull()
		expect(DENY_MESSAGE_KEY[DENY.NO_EDITION]).toBeNull()
	})
})

describe('one/app.js facts and edition (ruling C1)', () => {
	beforeEach(() => {
		api.resetSession()
	})

	function tokenWith(claims: Record<string, unknown>): string {
		const payload = {id: 1, exp: Math.round(Date.now() / 1000) + 600, ...claims}
		return `header.${btoa(JSON.stringify(payload))}.signature`
	}

	it('takes the edition from the JWT claim, not from the organization read', () => {
		api.setToken(tokenWith({brazn_edition: 'personal-cloud'}))

		const facts = readGateFacts()

		// The organization read 403s for P, M, TA and U - that is every user the `teams` gate
		// covers - so an edition derived from it would leave the most common user's label line and
		// assignee select decided by whichever way an implementer resolved `undefined`.
		// MUTATION: sourcing personalEdition from the organization payload (SPEC-UI section 5.4)
		// makes this red: with no organization loaded there is no edition value at all, so the fact
		// would be false here.
		expect(facts.personalEdition).toBe(true)
		expect(facts.hasEdition).toBe(true)
		// Never a claim, never a config flag, and never brazn_managed_mode - which is stuck on the
		// unmerged PR #50 and which this page is designed not to need.
		expect(facts.orgAdmin).toBe(false)
		expect(facts.teams).toEqual({})
	})

	it('reads absence of the edition claim as "no edition to name", not as personal', () => {
		api.setToken(tokenWith({}))

		const facts = readGateFacts()

		// MUTATION: defaulting a missing claim to personal-cloud makes this red - and it would
		// restrict every CI session and every token minted before the claim existed.
		expect(facts.personalEdition).toBe(false)
		expect(facts.hasEdition).toBe(false)
		expect(editionMessageKey(facts)).toBeNull()
	})

	it('names any non-personal edition as Teams, including one it has never seen', () => {
		api.setToken(tokenWith({brazn_edition: 'personal-cloud'}))
		expect(editionMessageKey(readGateFacts())).toBe('one.edition.personal')

		api.setToken(tokenWith({brazn_edition: 'teams-cloud'}))
		expect(editionMessageKey(readGateFacts())).toBe('one.edition.teams')

		api.setToken(tokenWith({brazn_edition: 'enterprise-cloud'}))
		// personal-cloud is the only defined constant; every other non-null value is Teams.
		// MUTATION: switching editionMessageKey to a `=== 'teams-cloud'` test makes this red.
		expect(editionMessageKey(readGateFacts())).toBe('one.edition.teams')
	})

	it('surfaces the write restriction from the claim', () => {
		api.setToken(tokenWith({brazn_write_restricted: true}))
		expect(readGateFacts().writeRestricted).toBe(true)

		api.setToken(tokenWith({}))
		// Absence is the permitting case: the claim is stamped only when true.
		expect(readGateFacts().writeRestricted).toBe(false)
	})
})

describe('one/app.js routing (ruling C9)', () => {
	it('accepts only a positive integer task id', () => {
		// Number() alone accepts '1e3', ' 12 ', '0x0c' and '' - every one of which builds a URL the
		// API answers 404 for.
		// MUTATION: replacing the /^[1-9][0-9]*$/ test in parseTaskId with Number.isFinite makes
		// this red.
		expect(parseRoute('?task=12').taskId).toBe(12)
		expect(parseRoute('?task=0').taskId).toBeNull()
		expect(parseRoute('?task=1e3').taskId).toBeNull()
		expect(parseRoute('?task=0x0c').taskId).toBeNull()
		expect(parseRoute('?task=%2012%20').taskId).toBeNull()
		expect(parseRoute('?task=').taskId).toBeNull()
		expect(parseRoute('').taskId).toBeNull()
	})

	it('falls back to settings rather than to an error surface', () => {
		// Percy is the only thing that builds these links; a malformed one is a bug on that side,
		// and the user gets a working page rather than a stack trace about someone else's mistake.
		// MUTATION: deleting clampView() makes this red - `?view=task` with no id would select a
		// view that has nothing to render.
		expect(parseRoute('?view=task').view).toBe('settings')
		expect(parseRoute('?task=12').view).toBe('task')
		expect(parseRoute('?task=12&view=settings').view).toBe('settings')
		expect(parseRoute('?view=nonsense&task=12').view).toBe('task')
		expect(parseRoute('?tab=nonsense').tab).toBe('account')
		expect(VIEWS).toContain('settings')
		expect(SETTINGS_TABS[0]).toBe('account')
	})

	it('clamps the organization and team tabs for an account that is not an administrator', () => {
		const route = {taskId: null, view: 'settings' as const, tab: 'organization' as const}

		// A link to ?tab=organization from an account that lost administration must land on
		// `account` rather than on an empty tab - silently, because 403 is the ordinary answer.
		// MUTATION: deleting the tab clamp from resolveRoute makes this red.
		expect(resolveRoute(route, FACTS.M).tab).toBe('account')
		expect(resolveRoute(route, FACTS.OA).tab).toBe('organization')
	})

	it('round-trips a route through the query string', () => {
		expect(routeToSearch({taskId: 12, view: 'task', tab: 'account'})).toBe('?task=12&view=task')
		expect(routeToSearch({taskId: null, view: 'settings', tab: 'team'})).toBe('?view=settings&tab=team')
		expect(parseRoute(routeToSearch({taskId: 12, view: 'task', tab: 'account'})))
			.toEqual({taskId: 12, view: 'task', tab: 'account'})
	})
})

describe('one/app.js login hand-off (the restricted-UI redirect loop)', () => {
	/*
	 * `/login` is a vue-router path: not a fork route, and not a file in dist/. On an instance
	 * with brazn.restricteduionly ON it therefore reaches the static handler's not-found path,
	 * where braznServeAppShell redirects every SPA path to /one/task.html
	 * (pkg/routes/static_brazn.go:82-86). An unconditional location.assign('/login') from a
	 * no-session boot is then an infinite redirect chain, in exactly - and only - the
	 * configuration this feature exists to create.
	 *
	 * The server-side repair is not this page's to make. What IS this page's is that the loop is
	 * bounded, and that decision is pure so it can be a table here rather than a navigation.
	 */

	it('hands off once, and refuses to hand off again after coming straight back', () => {
		// First boot of the tab: nothing has been tried, so the user goes to sign in.
		expect(shouldHandOffToLogin({marker: false, redirects: 0})).toBe(true)

		// We are back here with no session, and the marker says why. Stopping is what turns
		// ERR_TOO_MANY_REDIRECTS into a visible surface with a button on it.
		// MUTATION: deleting the `if (marker === true) return false` line makes this red, and the
		// page returns to looping until the browser gives up.
		expect(shouldHandOffToLogin({marker: true, redirects: 1})).toBe(false)
		expect(shouldHandOffToLogin({marker: true, redirects: 0})).toBe(false)
	})

	it('still hands off when a person presses Sign in', () => {
		// The refusal above is about an AUTOMATIC hop. A button that declines to do the one thing
		// it says would be a worse dead end than the bounce, and a user-initiated hop cannot
		// chain: it costs one click each time.
		// MUTATION: dropping the `force` short-circuit makes this red - the terminal surface's
		// only control would do nothing at all.
		expect(shouldHandOffToLogin({marker: true, redirects: 3, force: true})).toBe(true)
	})

	it('falls back to redirectCount only when storage cannot answer', () => {
		// marker === null is "sessionStorage threw" - private modes, storage disabled by policy.
		// A document that arrived here through a redirect with no session is the loop's own
		// signature, so it stops; one that was opened directly gets its hand-off.
		// MUTATION: returning `true` unconditionally for a null marker makes the first of these
		// red, and every storage-less browser loops.
		expect(shouldHandOffToLogin({marker: null, redirects: 1})).toBe(false)
		expect(shouldHandOffToLogin({marker: null, redirects: 0})).toBe(true)

		// Traced, and stated because it is the weakness of the fallback rather than a property to
		// be proud of: under the lockout a /tasks/123 deep link is ALSO redirected here, so a
		// storage-less first visit through one is refused its automatic hand-off and gets the
		// surface with the button instead. That is why redirectCount is consulted second and
		// never first.
		expect(shouldHandOffToLogin({redirects: 2})).toBe(false)
	})

	it('defaults every input rather than throwing on a missing one', () => {
		// handOffToLogin() reads all three from the browser, and two of the three reads can throw
		// on a locked-down browser. The defaults are what keep a storage failure from becoming a
		// boot failure.
		expect(shouldHandOffToLogin()).toBe(true)
		expect(shouldHandOffToLogin({})).toBe(true)
	})
})

describe('one/app.js action registry', () => {
	it('ships the chrome actions app.js owns, and no hook without an emitter', () => {
		// The shell emits no data-action of its own; every hook is delegated from document. These
		// five are the ones app.js itself claims, and the two view modules add theirs on import.
		//
		// TWO WERE DROPPED and their absence is asserted, not merely un-asserted. `return-signin`
		// mirrored a prototype control that lived in the deleted demo arm, and `data-nav` was a
		// prototype attribute hook this page has no navigation control to emit - both were
		// registered handlers for names nothing writes, which reads to the next person as a
		// load-bearing affordance.
		// MUTATION: re-registering either name makes this red. So does adding a sixth chrome action
		// without listing it, which is the case this file has always covered.
		expect(actionNames()).toEqual([
			'data-settings-tab',
			'modal-close',
			'reload',
			'retry',
			'signin',
		])

		// The attribute-keyed hooks are a separate list and it drifted from the registry once
		// already: `data-nav` sat in ATTRIBUTE_HOOKS with a handler and no emitter anywhere.
		// `data-resource` is registered by view-task.js on import, not here, which is why it is in
		// the list but not in actionNames() above.
		expect([...ATTRIBUTE_HOOKS]).toEqual(['data-settings-tab', 'data-resource'])
	})

	it('refuses a duplicate action name', () => {
		// Two views quietly claiming `confirm-remove` is a real bug, and the last one to load would
		// win in silence.
		// MUTATION: deleting the `actions.has(name)` check from registerActions makes this red.
		expect(() => registerActions({retry: () => {}})).toThrow(/already registered/)
		expect(() => registerActions({'not-a-function': undefined as never})).toThrow(/not a function/)
	})
})
