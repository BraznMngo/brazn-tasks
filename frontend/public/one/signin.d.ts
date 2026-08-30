/**
 * Hand-written declarations for signin.js — one job, three destinations
 * (BRA-1475).
 */

export interface DesktopAuthorization {
	response_type: string
	client_id: string
	redirect_uri: string
	code_challenge: string
	code_challenge_method: string
	state: string
}

/**
 * The five parameters a desktop application puts in the address, or null when
 * this is not a desktop authorization. All five are required; `state` is
 * optional and echoed back untouched.
 */
export function desktopAuthorizationFrom(
	search: string | null | undefined,
): DesktopAuthorization | null

/**
 * The destination this arrival wants to be returned to, read from the FRAGMENT.
 * A query-shaped string is refused rather than parsed.
 */
export function destinationFromHash(hash: string | null | undefined): string | null

/**
 * The destination, only if it resolves to this origin or to an origin the
 * server published. Anything else is null — a sign-in page is the most valuable
 * place in a product to have an open redirect.
 */
export function allowedDestination(
	raw: string | null | undefined,
	origin: string,
	publishedUrls?: readonly string[],
): string | null

/** The provider list from `GET /api/v1/info`, or an empty list. */
export function openIdProviders(info: any): Array<Record<string, any>>

/** The provider key out of `/auth/openid/{provider}`, or null. */
export function openIdProviderFromPath(pathname: string | null | undefined): string | null

/** One function, three surfaces: the form, the working state, the desktop exchange. */
export function signInSurface(state: {
	phase?: string
	needsTotp?: boolean
	username?: string
	providers?: Array<Record<string, any>>
	checkoutUrl?: string | null
	passwordUrl?: string
}): string

/** The page's boot. Self-schedules only when `#auth` exists. */
export function boot(): Promise<void>
