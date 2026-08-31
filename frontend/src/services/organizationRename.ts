import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'

/**
 * Commercial organisation rename (BRA-1479 #5 / BRA-1439 Story 2).
 * Sibling path to this fork's `/api/v1/` under the same Traefik-routed host —
 * absolute URL, same reason as `accountErasure.ts` and `apiV2Url`.
 *
 * Do not invent a second rename route: this is the one call
 * `POST /v1/organizations/rename` (same as ONE `api.renameOrganization`).
 * Success re-delivers the administrator's projection; callers must re-read
 * the name from the FORK (`brazn/organization`) afterwards, never from this
 * response alone.
 *
 * Local Vite must proxy `/v1` to `DEV_PROXY` (see vite.config.ts) or the
 * request never leaves the frontend origin.
 */

function commercialUrl(path: string): string {
	return new URL(path, window.location.origin).toString()
}

export async function renameOrganization(
	organizationId: string,
	organizationName: string,
): Promise<void> {
	const http = AuthenticatedHTTPFactory()
	await http.post(commercialUrl('/v1/organizations/rename'), {
		organization_id: organizationId,
		organization_name: organizationName,
		idempotency_key: crypto.randomUUID(),
	})
}
