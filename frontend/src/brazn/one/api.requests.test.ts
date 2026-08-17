import {describe, it, expect, beforeEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'

/*
 * Request shaping in frontend/public/one/api.js: the X-Vikunja-Format header discipline, the
 * single task read, the destructive-PUT merge, the JWT claim reads and the organization 403.
 *
 * The header rule is the one worth stating in full, because both directions corrupt data
 * SILENTLY. AutoPatch is GET -> merge -> PUT inside one request and it strips the query string,
 * so the header is the only channel that survives - and it applies to every rich-text field in
 * the merged resource, not only the one being edited. A non-description PATCH carrying it
 * round-trips the untouched stored description through a conversion the API's own description
 * calls lossy. A description PATCH missing it stores the user's Markdown as literal text.
 */

const ORIGIN = 'https://dev.tasks.brazn.one'
const MARKDOWN_HEADER = 'x-vikunja-format'

interface Call {
	url: string
	init: RequestInit
}

const calls: Call[] = []
let fallback: Response | null = null
let queue: Response[] = []

const fetchStub = vi.fn(async (url: string, init: RequestInit = {}) => {
	calls.push({url, init})
	const next = queue.shift() ?? fallback
	if (next === null || next === undefined) throw new Error(`unstubbed request: ${url}`)
	// A Response body can be read once; the battery below reuses one shape many times.
	return next.clone()
})

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {status, headers: {'content-type': 'application/json'}})
}

function headersOf(init: RequestInit | undefined): Record<string, string> {
	const headers = (init?.headers ?? {}) as Record<string, string>
	const lowered: Record<string, string> = {}
	for (const [name, value] of Object.entries(headers)) lowered[name.toLowerCase()] = value
	return lowered
}

// The house shape for minting a claim-carrying token, as in
// frontend/src/composables/useManagedCapabilities.test.ts: the claims are only ever read out of a
// real-shaped JWT, never injected through a setter, because there is no setter to inject through.
function makeJwt(claims: Record<string, unknown>): string {
	const payload = {id: 1, exp: Math.round(Date.now() / 1000) + 600, ...claims}
	return `header.${btoa(JSON.stringify(payload))}.signature`
}

// The rejection, typed. `rejects.toMatchObject` compares Errors by message in some matchers, so
// the error is caught and its fields asserted directly - the fields ARE the contract here.
async function createTeamError(): Promise<InstanceType<typeof api.ForkError>> {
	try {
		await api.createOrganizationTeam('Product')
	} catch (err) {
		return err as InstanceType<typeof api.ForkError>
	}
	throw new Error('createOrganizationTeam resolved but the stub answered a refusal')
}

describe('one/api.js request shaping', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fallback = json({})
		fetchStub.mockClear()
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})
		api.setToken('access-1')
	})

	it('puts X-Vikunja-Format: markdown on the description PATCH', async () => {
		await api.updateTaskDescription(7, '# Heading\n\n- item')

		expect(calls).toHaveLength(1)
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v2/tasks/7')
		expect(calls[0].init.method).toBe('PATCH')
		// MUTATION: deleting the third argument to patchTaskInternal in updateTaskDescription makes
		// this red - and the server would then store the user's Markdown as literal text.
		expect(headersOf(calls[0].init)[MARKDOWN_HEADER]).toBe('markdown')
		expect(JSON.parse(String(calls[0].init.body))).toEqual({description: '# Heading\n\n- item'})
	})

	it('puts it on NO other write, PATCHes included', async () => {
		await api.patchTask(7, {done: true})
		await api.renameTeam(3, 'Product')
		await api.renameTeamRootProject(9, 'Product')
		await api.updateComment(7, 11, 'edited')
		await api.createComment(7, 'new')
		await api.saveGeneralSettings({name: 'Ada'}, {color_schema: 'dark'})
		await api.addTaskLabel(7, 2)
		await api.subscribe('task', 7)
		await api.changeEmail('ada@example.com', 'pw')
		await api.deleteTask(7)

		const patches = calls.filter(call => call.init.method === 'PATCH')
		// Without this the assertion below could pass on a battery that happens to contain no
		// PATCH at all, which is the shape of a test that cannot fail.
		expect(patches).toHaveLength(3)

		const carriers = calls.filter(call => headersOf(call.init)[MARKDOWN_HEADER] !== undefined)
		// MUTATION: moving the header from updateTaskDescription's argument into
		// patchTaskInternal's own headers object makes this red - patchTask, renameTeam and
		// renameTeamRootProject would all start carrying it.
		expect(carriers.map(call => call.url)).toEqual([])
	})

	it('updates a comment as a PUT with ?format=markdown and no header at all', async () => {
		await api.updateComment(7, 11, 'edited')

		// The only registered update operation on this resource is a PUT, so going straight to it
		// skips AutoPatch's re-dispatch entirely - which is what lets the query survive and makes
		// the header unnecessary here (ruling C6).
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v2/tasks/7/comments/11?format=markdown')
		expect(calls[0].init.method).toBe('PUT')
		// MUTATION: replacing this PUT with the PATCH-plus-header that SPEC-BACKEND row 13 proposed
		// makes this red on both assertions.
		expect(headersOf(calls[0].init)[MARKDOWN_HEADER]).toBeUndefined()
	})

	it('refuses a description key on the ordinary PATCH, and issues nothing', () => {
		// This assertion is what makes "the header is on exactly one PATCH" true by construction
		// rather than by convention.
		// MUTATION: deleting the `'description' in patch` assertion from patchTask makes this red -
		// the call falls through and PATCHes a description with no header on it.
		expect(() => api.patchTask(7, {done: true, description: '<p>hi</p>'}))
			.toThrow(/description/)
		expect(fetchStub).not.toHaveBeenCalled()
	})

	it('reads the task ONCE, as markdown, with expand repeated per value', async () => {
		fallback = json({id: 12, description: '# hi'})

		await api.getTask(12, {expand: ['comments', 'subtasks']})

		expect(calls).toHaveLength(1)
		// MUTATION: changing `format: 'markdown'` to SPEC-ROLES J2's `format=html` makes this red;
		// so does issuing a second read for the description, which ruling C13 forbids because two
		// full reads can disagree with each other.
		expect(calls[0].url)
			.toBe('https://dev.tasks.brazn.one/api/v2/tasks/12?format=markdown&expand=comments&expand=subtasks')
	})

	it('merges general settings over the stored ones instead of replacing them', async () => {
		queue = [json({
			id: 1,
			settings: {
				name: 'Ada Lovelace',
				language: 'de-DE',
				timezone: 'Europe/Berlin',
				frontend_settings: {color_schema: 'dark', time_format: '12h'},
				extra_settings_links: [{title: 'x', url: 'https://example.com'}],
			},
		})]

		await api.saveGeneralSettings({name: 'Ada'}, {color_schema: 'light'})

		expect(calls.map(call => call.init.method)).toEqual(['GET', 'PUT'])
		// UpdateUserGeneralSettings assigns every field unconditionally with forceOverride = true,
		// so a bare patch blanks the display name and nulls every preference it did not mention.
		// MUTATION: sending `patch` instead of the merged object makes this red - `language` and
		// `timezone` disappear and the server writes them away.
		expect(JSON.parse(String(calls[1].init.body))).toEqual({
			name: 'Ada',
			language: 'de-DE',
			timezone: 'Europe/Berlin',
			// snake_case, and nested: objectToSnakeCase recurses, so the wire path really is
			// frontend_settings.color_schema. Reading colorSchema returns undefined and falls back
			// to light / 24-hour in silence.
			frontend_settings: {color_schema: 'light', time_format: '12h'},
		})
		// readOnly server-side; sending it back is noise at best.
		expect(JSON.parse(String(calls[1].init.body))).not.toHaveProperty('extra_settings_links')
	})

	it('folds the organization 403 to null and keeps every other failure an error', async () => {
		queue = [json({message: 'You are not the organization administrator.'}, 403)]
		// 403 is the ORDINARY answer for a non-administrator - which is P, M, TA, U and every CI
		// session. It must never reach an error surface.
		// MUTATION: removing the 403 fold from getOrganization makes this red - it would throw.
		await expect(api.getOrganization()).resolves.toBeNull()

		queue = [json({message: 'internal server error'}, 500)]
		// MUTATION: widening the fold to `err.status >= 400` makes this red - a genuinely broken
		// read would be rendered as "you are not an administrator", which is a lie the user cannot
		// act on.
		await expect(api.getOrganization()).rejects.toBeInstanceOf(api.ForkError)
	})

	it('carries the server sentence off all three fork error envelopes', async () => {
		// Managed-gate refusals are Echo middleware errors even on a v2 route; v2 handler errors
		// are RFC 9457 problem+json; the team-capacity 409 is a bare struct with its own message.
		// Which envelope arrives is not predictable from the status alone.
		queue = [json({message: 'This action is managed by the service.'}, 403)]
		const managed = await createTeamError()
		expect(managed.status).toBe(403)
		expect(managed.serverMessage).toBe('This action is managed by the service.')

		queue = [json({detail: 'The team could not be created.', title: 'Conflict'}, 409)]
		const problem = await createTeamError()
		expect(problem.status).toBe(409)
		// MUTATION: reading only `message` in readServerMessage makes this red - the sentence
		// becomes null and the page renders its own paraphrase in place of the server's words,
		// which ruling C4 forbids.
		expect(problem.serverMessage).toBe('The team could not be created.')
	})

	it('creates a team on the only route that works, and nowhere else', async () => {
		await api.createOrganizationTeam('Product')

		// PUT /api/v1/teams and POST /api/v2/teams are both service-managed and 403 for everyone,
		// instance admins included.
		// MUTATION: pointing createOrganizationTeam at forkV2Url('teams') makes this red.
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v1/brazn/organization/teams')
		expect(calls[0].init.method).toBe('PUT')
		expect(JSON.parse(String(calls[0].init.body))).toEqual({name: 'Product'})
	})

	it('renames a team with BOTH writes, in order', async () => {
		await api.renameTeamEverywhere(3, 9, 'Product')

		// Creation sets the team name and its root project's title from the same string and links
		// them nowhere, so one write alone drifts the two apart permanently.
		// MUTATION: deleting either call from renameTeamEverywhere makes this red.
		expect(calls.map(call => `${String(call.init.method)} ${call.url}`)).toEqual([
			'PATCH https://dev.tasks.brazn.one/api/v2/teams/3',
			'PATCH https://dev.tasks.brazn.one/api/v2/projects/9',
		])
	})

	it('addresses team members by username and task assignees by numeric id', async () => {
		await api.removeTeamMember(3, 'ada')
		await api.removeAssignee(7, 42)

		// The identical `{user}` path segment means a USERNAME on the team routes and a NUMERIC ID
		// on the assignee route. Applying the brief's "member routes take a username" correction
		// uniformly across every `{user}` segment breaks the second call.
		// MUTATION: swapping either identifier makes this red.
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v2/teams/3/members/ada')
		expect(calls[1].url).toBe('https://dev.tasks.brazn.one/api/v2/tasks/7/assignees/42')
	})
})

describe('one/api.js JWT claims (ruling C1)', () => {
	beforeEach(() => {
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})
	})

	it('reads personal-cloud as the only restricted edition', () => {
		api.setToken(makeJwt({brazn_edition: 'personal-cloud'}))
		expect(api.getEdition()).toBe('personal-cloud')
		expect(api.isPersonalEdition()).toBe(true)
		expect(api.hasEditionClaim()).toBe(true)
	})

	it('treats every other edition value, and absence, as unrestricted', () => {
		api.setToken(makeJwt({brazn_edition: 'teams-cloud'}))
		expect(api.isPersonalEdition()).toBe(false)
		expect(api.hasEditionClaim()).toBe(true)

		api.setToken(makeJwt({brazn_edition: 'enterprise-cloud'}))
		// MUTATION: whitelisting `teams-cloud` instead of testing for `personal-cloud` makes this
		// red, and that is the failure that costs a customer access rather than a wasted click.
		expect(api.isPersonalEdition()).toBe(false)

		api.setToken(makeJwt({}))
		expect(api.isPersonalEdition()).toBe(false)
		// Distinct from the line above: there is no edition to NAME, which is what hides the
		// edition line for U - every CI session included.
		expect(api.hasEditionClaim()).toBe(false)

		api.setToken(makeJwt({brazn_edition: ''}))
		expect(api.hasEditionClaim()).toBe(false)
	})

	it('requires write-restriction to be exactly true', () => {
		api.setToken(makeJwt({brazn_write_restricted: true}))
		expect(api.isWriteRestricted()).toBe(true)

		api.setToken(makeJwt({brazn_write_restricted: 'true'}))
		// The claim is stamped only when true, so absence is the PERMITTING case - and a truthy
		// check would read every token minted before the claim existed as write-blocked.
		// MUTATION: relaxing `claims[WRITE_RESTRICTED_CLAIM] === true` to a truthy test makes this
		// red.
		expect(api.isWriteRestricted()).toBe(false)

		api.setToken(makeJwt({}))
		expect(api.isWriteRestricted()).toBe(false)

		api.setToken(null)
		expect(api.isWriteRestricted()).toBe(false)
	})

	it('returns null for a token it cannot decode rather than throwing', () => {
		// The signature is never verified: this is a hint for what to draw, and the server's
		// managed gate is the real refusal. Anything unparsable therefore reads as "claims
		// absent", which is the permissive case.
		// MUTATION: removing the try/catch from parseJwt makes this red - the page would die on
		// boot for a token shape it is not entitled to have an opinion about.
		expect(api.parseJwt('not-a-jwt')).toBeNull()
		expect(api.parseJwt('header..signature')).toBeNull()
		expect(api.parseJwt('header.@@@.signature')).toBeNull()
		expect(api.parseJwt(null)).toBeNull()
	})

	it('decodes the PAYLOAD segment of an unpadded base64url token', () => {
		// A real session token: three segments, base64url, unpadded.
		const encode = (value: unknown) => btoa(JSON.stringify(value))
			.replace(/\+/g, '-')
			.replace(/\//g, '_')
			.replace(/=+$/, '')

		// The header carries a DIFFERENT edition, so the assertion can only be satisfied by
		// reading segment 1. Without this the test would pass while decoding either segment, and
		// "reads the claims out of the right part of the token" would be untested.
		// MUTATION: changing `token.split('.')[1]` to [0] (or to [2]) in parseJwt makes this red.
		const token = [
			encode({alg: 'HS256', typ: 'JWT', brazn_edition: 'teams-cloud'}),
			encode({brazn_edition: 'personal-cloud', id: 1}),
			'signature',
		].join('.')

		expect(api.parseJwt(token)).toMatchObject({brazn_edition: 'personal-cloud', id: 1})

		// TRACED, AND THE CLAIM IS WITHDRAWN RATHER THAN REPEATED. An earlier version of this case
		// said "deleting the padding line from parseJwt makes this red whenever the payload length
		// is not a multiple of four". It does not. WHATWG forgiving-base64 fails only when the
		// length leaves a remainder of 1 mod 4 - a remainder valid base64 can never produce - so
		// remainders of 2 and 3 decode unpadded, and this fixture's payload is one of them. The
		// padding line is defensive for engines that are stricter than the standard, and NOTHING
		// in this repository can prove it load-bearing: which engine happy-dom's atob resolves to,
		// and how tolerant it is, cannot be established without executing (CLAUDE.md section 1).
		// So the padding is not asserted, and neither is the base64url alphabet translation, whose
		// necessity turns on the same unverifiable question - Node's own base64 decoder accepts
		// '-' and '_' as aliases, so an atob built on it would need no translation at all.
		// What this case pins is the decode as a whole and the segment it reads, both of which are
		// properties of api.js rather than of the engine underneath it.
	})
})
