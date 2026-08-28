import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'

/**
 * the commercial service's own self-service account erasure (BRA-1404) — the complete
 * GDPR Art. 17 flow (successor handover, subscription cancellation, this
 * fork's own task data erasure, all in one call) that `/user/deletion/request`
 * on this backend is deliberately gated away from on a managed instance
 * (`route-classification.json`'s own `service-managed` class). Reached at
 * `/v1/...`, a sibling path to this fork's own `/api/v1/` under the same
 * Traefik-routed host — an absolute URL bypasses the pinned api base the same
 * way `apiV2Url` (`helpers/fetcher.ts`) already does for v2 calls, and for the
 * same reason: the shared axios instance's `baseURL` is not where this lives.
 */

function commercialUrl(path: string): string {
	return new URL(path, window.location.origin).toString()
}

export interface SuccessorCandidate {
	userId: string
}

/**
 * Who this account may hand its organization to before erasing itself
 * (Case 13, BRA-1074). An empty array means no choice has to be offered — a
 * sole member, a non-administrator, or an account with no organization —
 * not a loading or error state, so a caller must not treat "empty" as
 * "ask again later."
 */
export async function fetchSuccessorCandidates(): Promise<SuccessorCandidate[]> {
	const http = AuthenticatedHTTPFactory()
	const {data} = await http.get(commercialUrl('/v1/account/successor-candidates'))
	const candidates: {user_id: string}[] = data?.candidates ?? []
	return candidates.map(c => ({userId: c.user_id}))
}

/**
 * The one call that destroys (BRA-1404). Immediate and irreversible — unlike
 * this fork's own gated `/user/deletion/request`, there is no mailed
 * confirmation and no scheduled grace period: success means the account, its
 * organization membership, its ONE subscription, and every task this
 * fork holds for it are already gone.
 *
 * `successorUserId` is required — the call answers 409 — only when
 * `fetchSuccessorCandidates` returned a non-empty list. This function does
 * not re-check that itself; a caller skipping the fetch and passing null gets
 * the service's own refusal rather than a client-side guess at its rule.
 */
export async function eraseManagedAccount(successorUserId: string | null): Promise<void> {
	const http = AuthenticatedHTTPFactory()
	await http.post(commercialUrl('/v1/account/erasure'), {successor_user_id: successorUserId})
}
