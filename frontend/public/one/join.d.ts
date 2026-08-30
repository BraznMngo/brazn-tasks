/**
 * Hand-written declarations for join.js, the invitation page (BRA-1475, a
 * rewrite in place of BRA-1439 Story 5) — the same arrangement api.d.ts and
 * app.d.ts use, so the unit tests under frontend/src/brazn/one/ stay plain
 * .test.ts files.
 *
 * FOUR EXPORTS ARE GONE AND THEIR ABSENCE IS THE POINT. `joinSurface`,
 * `acceptedOutcome` and the surfaces they described belonged to a page that
 * accepted the invitation the moment it loaded, using whoever happened to be
 * signed in. That behaviour is the defect this ticket names, not an
 * implementation detail, so the page no longer has an acceptance outcome to
 * report on load and no `choices`, `accepting` or `done` state to render.
 */

/** The invitation handle out of a query string (`?i=`), or null. */
export function invitationIdFromSearch(search: string | null | undefined): string | null

/**
 * The signup token out of a URL fragment (`#signup_token=`), or null. Pure;
 * storage is the caller's. A query-shaped string is REFUSED rather than parsed:
 * the fragment placement is a security property and the parser enforces it.
 */
export function signupTokenFromHash(hash: string | null | undefined): string | null

/**
 * Which refusal the service named, as one word the general error page
 * recognises. Fails closed: an outcome this page has not read becomes
 * 'invitation-failed'.
 */
export function refusalReason(
	result: {outcome?: string | null} | null | undefined,
): string

/**
 * Whether a refusal of the COMPLETION is one the person can act on by changing
 * what they typed. True for a collision, which the task server refuses to
 * disambiguate between an address and a username on purpose — so the page keeps
 * them on the form and says neither.
 */
export function recoverableOnTheForm(
	result: {outcome?: string | null} | null | undefined,
): boolean

/**
 * THE INVITATION SCREEN: a heading, one sentence, three fields, one button.
 * The address is filled in from the invitation and locked.
 */
export function invitationSurface(state: {
	phase?: string
	organizationName?: string | null
	teamName?: string | null
	invitedEmail?: string | null
	username?: string
}): string

/** The one other screen: somebody else is signed in on this browser. */
export function signedInElsewhereSurface(): string

/** The link carried no handle at all. */
export function missingLinkSurface(): string

/** The page's boot. Exported for the stubbed-fetch tests; self-schedules only when `#auth` exists. */
export function boot(): Promise<void>
