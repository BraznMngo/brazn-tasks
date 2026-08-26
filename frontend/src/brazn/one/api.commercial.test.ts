import {describe, it, expect, afterEach, beforeEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'

/*
 * THE COMMERCIAL GUARD - the most important test on this page (bar 8).
 *
 * Several commercial calls answer HTTP 200 and report failure in the body, and the fork's static
 * handler answers an UNROUTED /v1/... with the SPA's index.html at HTTP 200 - which is exactly
 * what CI looks like, because CI starts no commercial service at all. A guard that believed
 * `res.ok` would therefore report every commercial control as working in precisely the
 * environment that cannot run one.
 *
 * THE OUTCOME VOCABULARY IS PER-OPERATION. There is no `'success'` anywhere in the commercial
 * service; each operation declares its own union, several carry no `outcome` field whatsoever,
 * and one answers 204 with no body. Every value asserted below is one api.js cites against
 * `percy-service-27c95232.ts` / `percy-http-27c95232.ts`, and the point of these cases is that a
 * value drifting away from the operation it belongs to is caught here rather than in production.
 *
 * Nothing here is evidence that any /v1 route works. These are stubbed-fetch unit tests of OUR
 * refusal logic (bar 9); /v1 is not E2E testable and no green run may be cited for it.
 */

const ORIGIN = 'https://dev.tasks.brazn.one'

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

function response(body: string, {status = 200, contentType = 'application/json'} = {}): Response {
	return new Response(body, {status, headers: {'content-type': contentType}})
}

function jsonResponse(body: unknown, status = 200): Response {
	return response(JSON.stringify(body), {status})
}

// A real 204: null body, and NO content-type header - which is why the JSON check alone cannot
// tell it from the CI shape, and why the guard needs the operation to declare it.
function noContentResponse(): Response {
	return new Response(null, {status: 204})
}

// What the fork's static handler actually returns for a path it has no route for: the SPA shell,
// at 200, as text/html. This is the CI shape and the reason the content-type check exists.
const SPA_INDEX_HTML = '<!DOCTYPE html><html lang="en"><head><title>Brazn Tasks</title>'
	+ '</head><body><div id="app"></div><script type="module" src="/assets/index.js"></script></body></html>'

describe('one/api.js commercial guard - transport and shape (bar 8, ruling C14)', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({
			fetch: fetchStub as unknown as typeof fetch,
			origin: ORIGIN,
			randomUUID: () => 'idem-key-1',
		})
		api.setToken('access-1')
	})

	it('refuses the CI shape: HTTP 200 carrying the SPA index.html', async () => {
		const res = response(SPA_INDEX_HTML, {contentType: 'text/html; charset=utf-8'})
		expect(res.ok).toBe(true)

		const result = await api.readCommercialResult(res, api.COMMERCIAL_OPS.GET_ENTITLEMENTS)

		expect(result.ok).toBe(false)
		// The reason is pinned, not just `ok`, and that is deliberate. Deleting the content-type
		// check would still leave `ok` false here - res.json() would throw on the HTML and the
		// guard would answer `unparsable` - so an assertion on `ok` alone could not see the
		// mutation at all.
		// MUTATION: deleting the content-type check in readCommercialResult makes this red: the
		// reason becomes 'unparsable'.
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})

	it('refuses the CI shape even for an operation that expects no outcome field', async () => {
		// The sharp version, and the one the per-operation work could have broken: GET_ENTITLEMENTS
		// and the whole subscription family accept a body with NO `outcome`, and the SPA shell has
		// no `outcome` either. What keeps them apart is the content-type check, which runs first.
		// MUTATION: deleting the content-type check makes this red - traced: res.json() throws on the
		// HTML, so the reason becomes 'unparsable'. (It does NOT become a fake success on this
		// operation, which is exactly why the reason is asserted rather than `ok` alone.)
		const result = await api.readCommercialResult(
			response(SPA_INDEX_HTML, {contentType: 'text/html'}),
			api.COMMERCIAL_OPS.CANCEL_SUBSCRIPTION,
		)

		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})

	it('refuses a 200 that PARSES as JSON but is not served as JSON', async () => {
		// A body that would sail through JSON.parse, delivered with a content type that says it is
		// not an API answer.
		const result = await api.readCommercialResult(
			response(JSON.stringify({outcome: 'invited'}), {contentType: 'text/html'}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)

		// MUTATION: deleting the content-type check makes this red - the result becomes ok:true,
		// which is a fake success reported to the user.
		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})

	it('accepts the content types a real service sends, parameters and +json included', async () => {
		const charset = await api.readCommercialResult(
			response(JSON.stringify({outcome: 'invited'}), {contentType: 'application/json; charset=utf-8'}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)
		const problem = await api.readCommercialResult(
			response(JSON.stringify({outcome: 'invited'}), {contentType: 'application/problem+json'}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)

		// The guard must be strict about HTML and lenient about legitimate JSON media types, or it
		// turns every real answer into `not-json` and the page refuses everything.
		// MUTATION: tightening the test to `contentType === 'application/json'` makes this red.
		expect(charset.ok).toBe(true)
		expect(problem.ok).toBe(true)
	})

	it('refuses a 200 whose JSON is malformed, and one that is not an object', async () => {
		const malformed = await api.readCommercialResult(response('{"outcome":'), api.COMMERCIAL_OPS.INVITE_MEMBER)
		expect(malformed.ok).toBe(false)
		expect(malformed.reason).toBe(api.COMMERCIAL_REFUSAL.UNPARSABLE)

		// A bare JSON string is valid JSON and reads, to a careless guard, exactly like the value it
		// is looking for.
		// MUTATION: dropping the `typeof body !== 'object'` check makes this red - `"invited"` has no
		// `outcome` property, so it would fall to the outcome branch and answer 'outcome' rather
		// than 'unparsable'.
		const bare = await api.readCommercialResult(response('"invited"'), api.COMMERCIAL_OPS.INVITE_MEMBER)
		expect(bare.ok).toBe(false)
		expect(bare.reason).toBe(api.COMMERCIAL_REFUSAL.UNPARSABLE)
	})

	it('reports a non-2xx as an HTTP refusal and keeps the server sentence', async () => {
		// `not_administrator` never arrives as an outcome on any of these routes: the service answers
		// it as a BARE 403 (percy-http-27c95232.ts:2850-2853 for invite, :2936-2939 for removal), so
		// res.ok is what carries it.
		const result = await api.readCommercialResult(
			jsonResponse({message: 'You are not the organization administrator.'}, 403),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)

		expect(result.ok).toBe(false)
		expect(result.status).toBe(403)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.HTTP)
		expect(result.message).toBe('You are not the organization administrator.')
	})

	it('recognises NO outcome value when the caller names no operation', async () => {
		// The default descriptor is UNKNOWN, which requires an `outcome` and has an empty affirmative
		// set - so a commercial call added later without a descriptor can only ever refuse.
		// MUTATION: defaulting the `op` parameter to any real descriptor, or giving UNKNOWN a
		// non-empty affirmative set, makes this red.
		const withValue = await api.readCommercialResult(jsonResponse({outcome: 'invited'}))
		const withNone = await api.readCommercialResult(jsonResponse({invitation_id: 'inv-9'}))

		expect(withValue.ok).toBe(false)
		expect(withValue.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
		expect(withNone.ok).toBe(false)
		expect(withNone.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
	})

	it('does not recognise "success", the value that is nowhere in the commercial service', async () => {
		// The defect this file was rewritten for. `outcome: "success"` appears NOWHERE in the
		// commercial service at 27c95232; a single COMMERCIAL_OK = 'success' constant made every
		// commercial control render its refusal path on a genuine success.
		// MUTATION: reintroducing a shared affirmative value of 'success' on any descriptor - or a
		// blanket `body.outcome === 'success'` shortcut in the guard - makes this red.
		for (const op of [
			api.COMMERCIAL_OPS.INVITE_MEMBER,
			api.COMMERCIAL_OPS.ACCEPT_INVITATION,
			api.COMMERCIAL_OPS.REMOVE_ORGANIZATION_MEMBER,
			api.COMMERCIAL_OPS.DECIDE_TEAM_ACCESS_REQUEST,
			api.COMMERCIAL_OPS.PURCHASE_SEATS,
		]) {
			const result = await api.readCommercialResult(jsonResponse({outcome: 'success'}), op)
			expect(result.ok).toBe(false)
			expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
		}
	})
})

/*
 * ONE AFFIRMATIVE AND ONE REFUSAL PER OPERATION.
 *
 * Two kinds of row. `outcome: 'required'` operations project a `MemberInvitation`-style result
 * whose union is declared in the service; `outcome: 'absent'` operations answer data and have no
 * such field at all, so for those the affirmative case IS a body with no `outcome` and the
 * refusal case is one that unexpectedly carries one.
 *
 * `body` values are the real projections, taken from the `json(response, 200, {...})` call in
 * percy-http-27c95232.ts for each route.
 */
interface OpCase {
	name: string
	op: api.CommercialOp
	/**
	 * The shape THIS TABLE believes the operation has, transcribed by hand from the route's own
	 * projection rather than read off the descriptor. It is asserted against the descriptor
	 * below, and it also selects which fail-closed mutation sentence is true for the operation -
	 * the two shapes fail closed in two different branches of readCommercialResult, and one
	 * sentence covering both was wrong for twelve of the seventeen.
	 */
	shape: 'required' | 'absent'
	/** Every value api.js classifies as affirmative, each in its real body shape. */
	affirmative: Array<{label: string, body: Record<string, unknown>}>
	/** A refusal the service really sends, or - for an `absent` op - an outcome appearing at all. */
	refusal: {label: string, body: Record<string, unknown>}
}

const OPERATIONS: OpCase[] = [
	{
		name: 'invite member (POST /v1/organizations/invitations)',
		op: api.COMMERCIAL_OPS.INVITE_MEMBER,
		shape: 'required',
		affirmative: [
			{
				label: 'invited',
				body: {
					outcome: 'invited',
					invited_user_id: 'usr-7',
					invitation: {invitation_id: 'inv-9', status: 'pending', expires_at: '2026-08-24T00:00:00Z'},
					seat_notice: {seats: 3, users: 2, seats_after: 3, proration: null},
				},
			},
			{
				// The judgement call, asserted rather than left to the comment: the invitee already
				// holds a seat, so the administrator's goal state holds and nothing failed.
				label: 'already_member',
				body: {
					outcome: 'already_member',
					invited_user_id: 'usr-7',
					invitation: {invitation_id: 'inv-9', status: 'accepted', expires_at: '2026-08-24T00:00:00Z'},
					seat_notice: null,
				},
			},
		],
		refusal: {
			label: 'not_invitable',
			body: {
				outcome: 'not_invitable',
				invited_user_id: 'usr-7',
				invitation: null,
				seat_notice: null,
				message: 'That address belongs to another organization.',
			},
		},
	},
	{
		name: 'accept invitation (POST /v1/organizations/invitations/accept)',
		op: api.COMMERCIAL_OPS.ACCEPT_INVITATION,
		shape: 'required',
		affirmative: [
			{label: 'admitted', body: {outcome: 'admitted', organization_id: 'org-1'}},
			{label: 'already_member', body: {outcome: 'already_member', organization_id: 'org-1'}},
		],
		refusal: {label: 'no_invitation', body: {outcome: 'no_invitation', organization_id: null}},
	},
	{
		name: 'remove organization member (POST /v1/organizations/members/removal)',
		op: api.COMMERCIAL_OPS.REMOVE_ORGANIZATION_MEMBER,
		shape: 'required',
		affirmative: [
			{label: 'removed', body: {outcome: 'removed', organization_id: 'org-1', member_user_id: 'usr-7'}},
		],
		refusal: {
			// `seat_withdrawn` is a log word from `registerOrganization`, an unrelated operation -
			// the sample table in FINDING-OUTCOME.md lists it under this one and is wrong. It is not
			// in this union, so it fails closed, which is exactly the behaviour wanted for a value
			// nobody could read from the source.
			label: 'seat_withdrawn (not a member-removal value at all)',
			body: {outcome: 'seat_withdrawn', organization_id: 'org-1', member_user_id: 'usr-7'},
		},
	},
	{
		name: 'rename organization (POST /v1/organizations/rename)',
		op: api.COMMERCIAL_OPS.RENAME_ORGANIZATION,
		shape: 'absent',
		affirmative: [
			// The landed handler writes the renamed record straight out, with no `outcome` member
			// (one-apps cloud/service/src/http.ts:3684-3689); refusals are bare statuses.
			{
				label: 'the renamed record, with no outcome field',
				body: {organization_id: 'org-1', organization_name: 'Nordwind Logistik'},
			},
		],
		refusal: {
			// THE OLD ASSUMPTION, PINNED AS THE REFUSAL IT ALWAYS WAS. This row first shipped as the
			// AFFIRMATIVE `outcome: 'renamed'`, read off a log line while the handler was unpushed -
			// independent QA caught that every real success would have opened the refusal modal. An
			// `outcome` arriving on this operation is a vocabulary nothing has read, and refusing it
			// is what would catch the same drift again from the other direction.
			label: "the log word 'renamed', which is not a result field",
			body: {outcome: 'renamed', organization_id: 'org-1', organization_name: 'Nordwind Logistik'},
		},
	},
	{
		name: 'decide team access request (POST /v1/team-access-requests/decide)',
		op: api.COMMERCIAL_OPS.DECIDE_TEAM_ACCESS_REQUEST,
		shape: 'required',
		affirmative: [
			{label: 'approved', body: {outcome: 'approved', invitation_outcome: 'invited'}},
			// A decline is the administrator's decision carried out, not a refusal of it.
			{label: 'declined', body: {outcome: 'declined', invitation_outcome: null}},
		],
		refusal: {
			label: 'not_admitted',
			body: {outcome: 'not_admitted', invitation_outcome: 'not_invitable'},
		},
	},
	{
		name: 'purchase seats (POST /v1/organizations/seats)',
		op: api.COMMERCIAL_OPS.PURCHASE_SEATS,
		shape: 'required',
		affirmative: [
			{
				label: 'changed',
				body: {
					organization_id: 'org-1',
					outcome: 'changed',
					seats: 4,
					users: 2,
					active_teams: 1,
					max_active_teams: 1,
					proration: null,
				},
			},
		],
		refusal: {
			// SeatPurchaseOutcome's two refusals are declared in the service's model.ts, which is not
			// among the extracted files, so NEITHER name could be read. Any value but `changed` fails
			// closed - this row pins that, using a plausible name on purpose.
			label: 'below_seats_used (a refusal whose real name could not be read)',
			body: {
				organization_id: 'org-1',
				outcome: 'below_seats_used',
				seats: 3,
				users: 4,
				active_teams: 1,
				max_active_teams: 1,
				proration: null,
			},
		},
	},
	{
		name: 'list team access requests (GET /v1/team-access-requests)',
		op: api.COMMERCIAL_OPS.LIST_TEAM_ACCESS_REQUESTS,
		shape: 'absent',
		affirmative: [
			{
				label: 'a queue, with no outcome field',
				body: {
					requests: [{
						request_id: 'req-1',
						requester_email: 'ada@example.com',
						message: null,
						team_id: 'team-1',
						requested_at: '2026-08-01T00:00:00Z',
						verified_at: '2026-08-02T00:00:00Z',
					}],
				},
			},
		],
		refusal: {label: 'an unread outcome vocabulary', body: {requests: [], outcome: 'listed'}},
	},
	{
		name: 'confirm team access request (POST /v1/team-access-requests/confirm)',
		op: api.COMMERCIAL_OPS.CONFIRM_TEAM_ACCESS_REQUEST,
		shape: 'absent',
		affirmative: [{label: 'an empty JSON object', body: {}}],
		refusal: {label: 'an unread outcome vocabulary', body: {outcome: 'confirmed'}},
	},
	{
		name: 'cancel subscription (POST /v1/subscription/cancellation)',
		op: api.COMMERCIAL_OPS.CANCEL_SUBSCRIPTION,
		shape: 'absent',
		affirmative: [
			{
				label: 'the three CancellationResult fields',
				body: {user_id: 'usr-1', cancelled_at: '2026-08-17T00:00:00Z', access_ends_at: '2027-01-01T00:00:00Z'},
			},
		],
		refusal: {label: 'an unread outcome vocabulary', body: {user_id: 'usr-1', outcome: 'cancelled'}},
	},
	{
		name: 'set auto-renewal (POST /v1/subscription/auto-renewal)',
		op: api.COMMERCIAL_OPS.SET_SUBSCRIPTION_AUTO_RENEWAL,
		shape: 'absent',
		affirmative: [{label: '{auto_renewal: true}', body: {auto_renewal: true}}],
		refusal: {label: 'an unread outcome vocabulary', body: {auto_renewal: true, outcome: 'started'}},
	},
	{
		name: 'give renewal consent (POST /v1/subscription/renewal-consent)',
		op: api.COMMERCIAL_OPS.GIVE_RENEWAL_CONSENT,
		shape: 'absent',
		affirmative: [{label: '{renewal_consent_at}', body: {renewal_consent_at: '2026-08-17T00:00:00Z'}}],
		refusal: {label: 'an unread outcome vocabulary', body: {outcome: 'recorded'}},
	},
	{
		name: 'resume checkout (POST /v1/checkout/resume)',
		op: api.COMMERCIAL_OPS.RESUME_CHECKOUT,
		shape: 'absent',
		affirmative: [{label: '{user_id, payment}', body: {user_id: 'usr-1', payment: {status: 'open'}}}],
		refusal: {label: 'an unread outcome vocabulary', body: {user_id: 'usr-1', outcome: 'resumed'}},
	},
	{
		name: 'read entitlements (GET /v1/entitlements)',
		op: api.COMMERCIAL_OPS.GET_ENTITLEMENTS,
		shape: 'absent',
		affirmative: [
			{
				label: 'the entitlement projection',
				body: {
					edition: 'teams-cloud',
					seats: {included: 3, used: 2},
					limits: {projects: 10},
					footer: {suppressed: false, full_price_paid: true},
				},
			},
		],
		refusal: {label: 'an unread outcome vocabulary', body: {edition: 'teams-cloud', outcome: 'ok'}},
	},
	{
		name: 'list successor candidates (GET /v1/account/successor-candidates)',
		op: api.COMMERCIAL_OPS.LIST_SUCCESSOR_CANDIDATES,
		shape: 'absent',
		affirmative: [{label: 'a candidate list', body: {candidates: [{user_id: 'usr-2'}]}}],
		refusal: {label: 'an unread outcome vocabulary', body: {candidates: [], outcome: 'listed'}},
	},
	{
		name: 'erase account (POST /v1/account/erasure)',
		op: api.COMMERCIAL_OPS.ERASE_ACCOUNT,
		shape: 'absent',
		// The 204 case is its own test below; this covers a JSON 200, which the route does not send
		// today but which the descriptor must not misread if it ever does.
		affirmative: [{label: 'an empty JSON object', body: {}}],
		refusal: {label: 'an unread outcome vocabulary', body: {outcome: 'erased'}},
	},
	{
		name: 'revoke invitation (POST /v1/organizations/invitations/revoke)',
		op: api.COMMERCIAL_OPS.REVOKE_INVITATION,
		shape: 'absent',
		affirmative: [
			{
				label: 'an invitation record',
				body: {invitation_id: 'inv-9', status: 'revoked', organization_id: 'org-1'},
			},
		],
		refusal: {
			// THE KNOWN RESIDUE, pinned so it is impossible to forget. `revokeMemberInvitation`
			// answers an invitation RECORD with no `outcome`; the `"revoked"` in the service is a log
			// word. If the handler that eventually lands does project an outcome, THIS TEST GOES RED
			// and the value must be read from that handler and added to the descriptor - which is the
			// intended signal, not a bug in the test.
			label: 'the log word "revoked", which is not a result field',
			body: {invitation_id: 'inv-9', outcome: 'revoked'},
		},
	},
	{
		name: 'quote seats (GET /v1/organizations/seats/quote)',
		op: api.COMMERCIAL_OPS.QUOTE_SEATS,
		shape: 'absent',
		affirmative: [
			{
				label: 'a SeatIncreaseQuote with a null proration',
				body: {organization_id: 'org-1', seats: 3, seats_after: 4, proration: null},
			},
		],
		refusal: {label: 'an unread outcome vocabulary', body: {organization_id: 'org-1', outcome: 'quoted'}},
	},
	{
		name: 'transfer administrator (POST /v1/organizations/admin-transfer)',
		op: api.COMMERCIAL_OPS.TRANSFER_ADMINISTRATOR,
		shape: 'absent',
		affirmative: [
			{
				label: 'an AdminTransferResult',
				body: {organization_id: 'org-1', from_user_id: 'usr-1', to_user_id: 'usr-2'},
			},
		],
		refusal: {
			// `transferred` and `not_applicable` are the log words on either side of the return at
			// percy-service-27c95232.ts:4332 / :4323. AdminTransferResult itself has no outcome.
			label: 'the log word "transferred", which is not a result field',
			body: {organization_id: 'org-1', outcome: 'transferred'},
		},
	},
]

describe('one/api.js commercial guard - the per-operation outcome vocabulary', () => {
	it('covers every descriptor except the deliberately-empty UNKNOWN', () => {
		// A new commercial operation must arrive with its own affirmative and refusal cases. Without
		// this the table silently stops covering the surface it claims to.
		// MUTATION: adding a descriptor to COMMERCIAL_OPS without adding a row here makes this red.
		const covered = new Set(OPERATIONS.map(entry => entry.op))
		const declared = Object.entries(api.COMMERCIAL_OPS)
			.filter(([name]) => name !== 'UNKNOWN')
			.map(([, op]) => op)

		expect(declared.filter(op => !covered.has(op))).toEqual([])
		expect(OPERATIONS).toHaveLength(declared.length)
	})

	it('agrees with each descriptor about which SHAPE the operation answers in', () => {
		// The shapes in this table are transcribed by hand from each route's own projection in
		// percy-http-27c95232.ts; `op.shape` is what api.js believes. They are two independent
		// copies on purpose, because the two shapes fail closed in two DIFFERENT branches of
		// readCommercialResult and a descriptor that quietly changed shape would move an operation
		// from one branch to the other with every case below still green.
		//
		// Concretely: flipping INVITE_MEMBER to `absent` would make a body with no `outcome` at
		// all - a truncated or half-written answer - read as a success.
		// MUTATION: changing any descriptor's shape in api.js without changing this table makes
		// this red.
		for (const entry of OPERATIONS) {
			expect(entry.op.shape, entry.name).toBe(entry.shape)
		}
		// Five required, thirteen absent. The counts keep a table-wide mistake (every row copied
		// as 'absent', say) from passing as agreement. RENAME_ORGANIZATION is the thirteenth
		// absent row (BRA-1439) - it briefly sat on the required side of this line, on an assumed
		// union independent QA traced false against the landed handler; see its own row.
		expect(OPERATIONS.filter(entry => entry.shape === 'required')).toHaveLength(5)
		expect(OPERATIONS.filter(entry => entry.shape === 'absent')).toHaveLength(13)
	})

	for (const entry of OPERATIONS) {
		describe(entry.name, () => {
			for (const accepted of entry.affirmative) {
				it(`accepts ${accepted.label}`, async () => {
					const result = await api.readCommercialResult(jsonResponse(accepted.body), entry.op)

					// MUTATION: removing this value from the descriptor's `affirmative` list - or, for an
					// `absent`-shaped operation, making a missing `outcome` a refusal - makes this red.
					// That is the defect being fixed: a control that renders its refusal path on a real
					// success, invisibly, because CI never reaches /v1 (bar 9).
					expect(result.ok).toBe(true)
					expect(result.reason).toBeNull()
					// The body is handed back untouched: api.js models no commercial payload.
					expect(result.body).toEqual(accepted.body)
				})
			}

			it(`refuses ${entry.refusal.label}`, async () => {
				const res = jsonResponse(entry.refusal.body)
				// Stated out loud: the transport succeeded. This is the whole of bar 8.
				expect(res.ok).toBe(true)

				const result = await api.readCommercialResult(res, entry.op)

				// MUTATION: adding this value to the descriptor's `affirmative` list makes this red.
				// For an `absent`-shaped operation, so does returning `ok: true` unconditionally from
				// the OUTCOME_ABSENT branch instead of requiring `body.outcome === undefined`.
				expect(result.ok).toBe(false)
				expect(result.status).toBe(200)
				expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
			})

			it('fails CLOSED on an outcome value from no vocabulary at all', async () => {
				const result = await api.readCommercialResult(jsonResponse({outcome: 'queued'}), entry.op)

				// THE MUTATION IS PER-SHAPE, and this is the correction of a sentence that was
				// wrong for twelve of the seventeen rows. `{outcome: 'queued'}` does not reach the
				// same line of readCommercialResult for both shapes:
				//
				//   required  the OUTCOME_ABSENT branch is skipped, and the affirmative-set check
				//             at the end refuses it.
				//             MUTATION: replacing that set check with a denylist of known failures
				//             (`body.outcome === 'not_invitable'`) makes THESE FIVE red.
				//
				//   absent    the OUTCOME_ABSENT branch refuses it first, on
				//             `body.outcome === undefined`, and the set check is never reached -
				//             so the denylist mutation above leaves these twelve GREEN. It was
				//             traced, not assumed.
				//             MUTATION: dropping the `body.outcome === undefined` condition from
				//             that branch - i.e. returning ok:true for any parsed object - makes
				//             THESE TWELVE red, and a route that grew an outcome to report
				//             something would have it ignored.
				expect(result.ok).toBe(false)
				expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
			})
		})
	}

	it('refuses not_invitable in the body the invite handler ACTUALLY projects', async () => {
		// CORRECTED CASE, and the correction is the point. This used to assert
		// `{outcome: 'not_invitable', message: 'That address belongs to another organization.'}` -
		// a body `POST /v1/organizations/invitations` cannot send. The handler projects exactly
		// four fields and none of them is a message (percy-http-27c95232.ts:2854-2884), so the
		// old case documented a shape the source contradicts, on the ONLY coverage of the invite
		// refusal path. That is CLAUDE.md section 4's "a test that cannot fail for the reason it
		// claims to", and it is why the missing refusal sentences were invisible for a round.
		const result = await api.readCommercialResult(
			jsonResponse({
				outcome: 'not_invitable',
				invited_user_id: 'usr-7',
				invitation: null,
				seat_notice: null,
			}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)

		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
		// MUTATION: adding 'not_invitable' to INVITE_MEMBER's affirmative list makes the two lines
		// above red - an address that can never be invited would report as an invitation sent.
		//
		// AND THERE IS NO SENTENCE. This is the fact the whole refusal-vocabulary map exists for:
		// the caller gets `message: null` and has to name the outcome itself.
		// MUTATION: making readServerMessage fall back to any other field - or to the outcome value
		// - makes this red, and app.js's outcome table would be silently bypassed by a paraphrase.
		expect(result.message).toBeNull()
		expect(result.body).toMatchObject({outcome: 'not_invitable', invitation: null})
	})

	it('keeps a service sentence when there is one - and records that /v1 never sends one', async () => {
		// The guard's `message` read is real and shared, so it is asserted as a mechanism: a body
		// carrying `message` is quoted rather than paraphrased (ruling C4).
		const spoken = await api.readCommercialResult(
			jsonResponse({outcome: 'not_invitable', message: 'Ask your administrator.'}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)
		// MUTATION: dropping `message` from the refusal object returned by the outcome branch makes
		// this red, and the page would paraphrase a refusal it was handed verbatim.
		expect(spoken.message).toBe('Ask your administrator.')

		// STATED PLAINLY SO NOBODY READS THE CASE ABOVE AS A CLAIM ABOUT THE SERVICE: at 27c95232
		// NO /v1 route sends `message`, `detail` or `title`. There are three body writers -
		// `json` (percy-http-27c95232.ts:717), `bare` (:728, a status line with no content type at
		// all) and `fail` (:1778), whose only JSON bodies are `{error: <code>}` for a provisioning
		// failure (:1785, whose comment says the optional message is "deliberately never emitted"),
		// the frozen `upgrade_required` shape at 402 (:1795), and `{error, debug}` behind the
		// off-by-default debugErrors flag (:1827-1830). So `readServerMessage` returns null on
		// every real commercial refusal, and the sentence a user reads comes from app.js's
		// COMMERCIAL_OUTCOME_MESSAGE_KEY or from its COMMERCIAL_STATUS_MESSAGE_KEY. The verbatim
		// path above is defence for a shape the service does not currently produce; the fork's
		// side of ruling C4 - the 409 body with its server-computed `seats_needed` - is where the
		// verbatim rule actually bites, and it is asserted in app.dom.test.ts.
		const asSent = await api.readCommercialResult(
			jsonResponse({error: 'seat_withdrawn'}, 409),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)
		expect(asSent.message).toBeNull()
		expect(asSent.reason).toBe(api.COMMERCIAL_REFUSAL.HTTP)
	})

	it('fails CLOSED when an outcome-bearing operation sends no outcome at all', async () => {
		// Traced: `typeof body.outcome !== 'string'` is what catches this, so `undefined` and a
		// non-string alike are refusals.
		// MUTATION: defaulting a missing outcome to the first affirmative value
		// (`body.outcome ?? op.affirmative[0]`) makes this red.
		const missing = await api.readCommercialResult(
			jsonResponse({invited_user_id: 'usr-7', invitation: null}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)
		expect(missing.ok).toBe(false)
		expect(missing.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)

		// Traced honestly: dropping the `typeof` guard would NOT make the case below red, because
		// `includes` already refuses an array. The typeof guard states the intent; what this case
		// actually pins is that the check is set membership rather than presence.
		// MUTATION: replacing the affirmative-set check with a truthiness test
		// (`if (!body.outcome) return refused`) makes this red - `['invited']` is truthy and would
		// read as a success.
		const notAString = await api.readCommercialResult(
			jsonResponse({outcome: ['invited']}),
			api.COMMERCIAL_OPS.INVITE_MEMBER,
		)
		expect(notAString.ok).toBe(false)
		expect(notAString.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
	})
})

describe('one/api.js commercial guard - 204, the bodiless success', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-key-1'})
		api.setToken('access-1')
	})

	it('accepts 204 for account erasure, whose success carries no body at all', async () => {
		const res = noContentResponse()
		// A 204 has no content-type, so the JSON check cannot tell it from the CI shape by itself.
		expect(res.headers.get('content-type')).toBeNull()

		const result = await api.readCommercialResult(res, api.COMMERCIAL_OPS.ERASE_ACCOUNT)

		// POST /v1/account/erasure answers 204 with nothing (percy-http-27c95232.ts:3071-3076).
		// MUTATION: deleting the 204 branch makes this red - reason becomes 'not-json', and a
		// COMPLETED account erasure is reported to the user as "the service is unavailable" on the
		// one call they cannot retry to find out what really happened.
		expect(result.ok).toBe(true)
		expect(result.status).toBe(204)
		expect(result.body).toBeNull()
		expect(result.reason).toBeNull()
	})

	it('still refuses 204 for an operation that does NOT declare a bodiless success', async () => {
		const result = await api.readCommercialResult(noContentResponse(), api.COMMERCIAL_OPS.QUOTE_SEATS)

		// The 204 branch is narrow on purpose: only the two operations whose success is documented as
		// bodiless opt in, so it can never become a blanket "any empty 2xx is fine".
		// MUTATION: dropping `&& op.noContent` from the 204 branch makes this red.
		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})

	it('does not let the 204 branch admit the CI shape', async () => {
		// The SPA shell is served at 200, never 204, so the branch cannot reach it - stated as a test
		// because that property is the whole justification for the branch existing at all.
		// MUTATION: dropping `res.status === 204` from the branch - i.e. accepting any response for
		// an operation whose success is bodiless - makes this red, and CI's index.html-at-200 would
		// report a completed account erasure that never happened.
		const result = await api.readCommercialResult(
			response(SPA_INDEX_HTML, {contentType: 'text/html'}),
			api.COMMERCIAL_OPS.ERASE_ACCOUNT,
		)

		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})
})

describe('one/api.js commercial calls (bar 6, ruling C17)', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({
			fetch: fetchStub as unknown as typeof fetch,
			origin: ORIGIN,
			randomUUID: () => 'idem-key-1',
		})
		api.setToken('access-1')
	})

	it('routes a live commercial call through the guard, origin-rooted and with no /api prefix', async () => {
		queue = [response(SPA_INDEX_HTML, {contentType: 'text/html'})]

		const result = await api.getEntitlements()

		// Bar 6, written out in full: the commercial service is /v1 with NO /api prefix, and the
		// URL is origin-rooted so it can never re-base onto the fork's /api/v1.
		// MUTATION: building this path with forkV1Url() - i.e. /api/v1/v1/entitlements, the
		// documented top mistake on this project - makes this red.
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/v1/entitlements')
		expect(calls[0].init.credentials).toBe('same-origin')
		// End to end: in CI this call answers 200 and the page still reports it as unavailable.
		expect(result.ok).toBe(false)
		expect(result.reason).toBe(api.COMMERCIAL_REFUSAL.NOT_JSON)
	})

	it('carries each call\'s own descriptor, so one operation cannot pass on another\'s vocabulary', async () => {
		// `invited` is affirmative for the invitation and meaningless everywhere else. Sending it to
		// the seat purchase, which recognises only `changed`, must refuse.
		// MUTATION: giving commercialPost/commercialGet a default `op`, or passing the same
		// descriptor from both call sites, makes this red.
		queue = [
			jsonResponse({outcome: 'invited', invited_user_id: 'usr-7', invitation: null, seat_notice: null}),
			jsonResponse({outcome: 'invited', organization_id: 'org-1', seats: 4}),
		]

		const invited = await api.inviteOrganizationMember({organization_id: 'org-1', email: 'ada@example.com'})
		const purchased = await api.purchaseSeats('org-1', 4)

		expect(invited.ok).toBe(true)
		expect(purchased.ok).toBe(false)
		expect(purchased.reason).toBe(api.COMMERCIAL_REFUSAL.OUTCOME)
	})

	it('accepts the seat purchase only on `changed`', async () => {
		queue = [jsonResponse({organization_id: 'org-1', outcome: 'changed', seats: 4, users: 2})]

		const result = await api.purchaseSeats('org-1', 4)

		// MUTATION: reverting PURCHASE_SEATS to a 'success' affirmative makes this red, and every
		// seat purchase in production reports a failure the customer's card was charged for.
		expect(result.ok).toBe(true)
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/v1/organizations/seats')
	})

	it('sends admin-transfer with exactly three fields and NEVER from_user_id', async () => {
		queue = [jsonResponse({organization_id: 'org-1', from_user_id: 'usr-1', to_user_id: 42})]

		await api.transferAdministrator('org-1', 42)

		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/v1/organizations/admin-transfer')
		// `from_user_id` is the resolved bearer, never a body field. toEqual pins the whole body,
		// so an added field is visible rather than merely un-asserted.
		// MUTATION: adding `from_user_id` to transferAdministrator's payload makes this red.
		expect(JSON.parse(String(calls[0].init.body))).toEqual({
			organization_id: 'org-1',
			to_user_id: 42,
			idempotency_key: 'idem-key-1',
		})
	})

	it('passes to_user_id through UNCHANGED - api.js coerces nothing, so the caller owns the type', async () => {
		// Stated because it is a live hazard rather than a curiosity. The successor picker is a
		// <select>, and every value read out of a form control is a STRING - so a call site that
		// hands `fieldValue(...)` straight to this function puts `"42"` on the wire where the row
		// above puts 42. api.js is not the place to hide that: coercing here would make the seam
		// lie about what the page sends, and ruling C17's reasoning covers the type as much as the
		// name, because an unexpected value shape is exactly what a strict validator answers with
		// 200-plus-a-failure-outcome - the hardest failure on this surface to debug.
		// MUTATION: adding a Number() coercion to transferAdministrator makes this red, and the
		// string case below would stop being visible to anyone reading this file.
		queue = [jsonResponse({organization_id: 'org-1', from_user_id: 'usr-1', to_user_id: '42'})]

		await api.transferAdministrator('org-1', '42' as unknown as number)

		expect(JSON.parse(String(calls[0].init.body))).toEqual({
			organization_id: 'org-1',
			to_user_id: '42',
			idempotency_key: 'idem-key-1',
		})
	})

	it('sends every invitation with an idempotency_key, because the route requires one', async () => {
		// parseInvite requires the key UNCONDITIONALLY and before the optional team_id branch
		// (percy-http-27c95232.ts:1602, UUID_PATTERN at :625), and a null parse is a bare 400
		// (:2833-2834). A body of {organization_id, email} therefore cannot parse: every
		// invitation sent without one is a guaranteed 400, and CI cannot see it because CI never
		// reaches /v1 (bar 9). The key is defaulted at the api.js seam, the same place
		// purchaseSeats and transferAdministrator default theirs.
		// MUTATION: removing the default from inviteOrganizationMember makes this red.
		queue = [jsonResponse({outcome: 'invited', invited_user_id: 'usr-7', invitation: null, seat_notice: null})]

		await api.inviteOrganizationMember({organization_id: 'org-1', email: 'ada@example.com'})

		expect(JSON.parse(String(calls[0].init.body))).toEqual({
			organization_id: 'org-1',
			email: 'ada@example.com',
			idempotency_key: 'idem-key-1',
		})
	})

	it('does not send team_id on an invitation, and issues no request when one is offered', async () => {
		// THE FIELD IS REAL, and saying otherwise here would be the false citation this project
		// treats as a defect in its own right (bar 7): parseInvite allowlists `team_id`
		// (percy-http-27c95232.ts:1598), isIds it (:1607), and :1603-1606 documents that absent
		// means the organization's primary team. It is not sent because the PROTOTYPE HAS NO TEAM
		// PICKER and the prototype is the scope bar (bar 10) - no control on this page can produce
		// a value for it, so a value arriving here came from somewhere that should not exist yet.
		// The assertion is what keeps that true and what keeps the negative test writable.
		// MUTATION: deleting the `'team_id' in body` assertion from inviteOrganizationMember makes
		// this red - the call would fall through and put a request on the wire.
		expect(() => api.inviteOrganizationMember({email: 'ada@example.com', team_id: 3}))
			.toThrow(/team_id/)
		expect(fetchStub).not.toHaveBeenCalled()
	})

	it('sends the rename with exactly the three fields the route\'s grammar names', async () => {
		// THE REVERSAL OF A NEGATIVE TEST, recorded as such. This case used to pin that api.js
		// exported NO rename-organization call (ruling C8.1: the pencil was read-only "until
		// POST /v1/organizations/rename lands"), and the absence was what made "renders disabled,
		// issues no request" testable. The route landed with BRA-1439's commercial half, so the
		// ruling's own condition is met and the positive counterpart takes over: the call exists,
		// its grammar is `{organization_id, organization_name, idempotency_key}` (the ticket
		// comment of 2026-08-26), and the key is defaulted at the api.js seam like the invite's.
		// MUTATION: removing the key default, or adding any field to the body, makes this red -
		// toEqual pins the whole body. The stubbed success is the handler's real one: the renamed
		// record, no outcome member (http.ts:3684-3689).
		queue = [jsonResponse({organization_id: 'org-1', organization_name: 'Nordwind'})]

		const result = await api.renameOrganization({organization_id: 'org-1', organization_name: 'Nordwind'})

		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/v1/organizations/rename')
		expect(JSON.parse(String(calls[0].init.body))).toEqual({
			organization_id: 'org-1',
			organization_name: 'Nordwind',
			idempotency_key: 'idem-key-1',
		})
		expect(result.ok).toBe(true)
	})
})

describe('one/api.js URL construction (bar 6)', () => {
	beforeEach(() => {
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})
	})

	// The origin is module state, and one case below deliberately points it at a path-bearing URL.
	// Restoring it inside that test body would leave every LATER test in this file addressing
	// /one/task.html the moment an assertion above the restore failed - one red case reported as
	// several, with the real one buried. afterEach runs either way.
	afterEach(() => {
		api.configure({origin: ORIGIN})
	})

	it('keeps the three bases apart', () => {
		expect(api.forkV1Url('brazn/organization')).toBe('https://dev.tasks.brazn.one/api/v1/brazn/organization')
		expect(api.forkV2Url('user')).toBe('https://dev.tasks.brazn.one/api/v2/user')
		expect(api.commercialV1Url('organizations/invitations'))
			.toBe('https://dev.tasks.brazn.one/v1/organizations/invitations')
	})

	it('tolerates a leading slash on the path without doubling it', () => {
		expect(api.commercialV1Url('/entitlements')).toBe('https://dev.tasks.brazn.one/v1/entitlements')
		expect(api.forkV2Url('/user')).toBe('https://dev.tasks.brazn.one/api/v2/user')
	})

	it('roots the commercial path at the origin even when the base carries a path', () => {
		// pageOrigin() normally returns location.origin, which has no path - so this injects one to
		// pin the property that matters: the URL is built ROOT-relative. Percy opens the page at
		// /one/task.html, and a base-relative build there would produce /one/v1/... .
		api.configure({origin: 'https://dev.tasks.brazn.one/one/task.html'})

		// MUTATION: dropping the leading slash from commercialV1Url's `/v1/${path}` template makes
		// this red - the result becomes https://dev.tasks.brazn.one/one/v1/entitlements.
		expect(api.commercialV1Url('entitlements')).toBe('https://dev.tasks.brazn.one/v1/entitlements')
		expect(api.forkV2Url('user')).toBe('https://dev.tasks.brazn.one/api/v2/user')
		// The origin is restored by afterEach, not here: a failure above must not leak it.
	})
})
