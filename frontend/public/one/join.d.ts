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
export function refusalReason(word: string | null | undefined): string

/**
 * Whether a refusal of the COMPLETION is one the person can act on by changing
 * what they typed. True for a collision, which the service refuses to
 * disambiguate between an address and a username on purpose — so the page keeps
 * them on the form, where nothing has been spent and a different username can go
 * in immediately, and says neither.
 */
export function recoverableOnTheForm(word: string | null | undefined): boolean

/** The UTF-8 byte length of a value. The service bounds a password in BYTES, not characters. */
export function byteLength(value: unknown): number

/**
 * Already a member — from either route, and NOT an error in either. Nothing was
 * spent or created, so the way in is the sign-in page and the credentials they
 * already have.
 */
export function alreadyMemberSurface(state: {organizationName?: string | null}): string

/**
 * The account and the seat exist and the team join did not: a partial success,
 * said as one. Until the task server is deployed this is what EVERY completion
 * answers.
 */
export function teamUnavailableSurface(): string

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
/**
 * The catalogue key a verdict earns, or null when it earns none. One place
 * decides both whether the form blocks and what it says, so the two can never
 * disagree. The two sentences must not say each other's thing: `invalid` must
 * not imply anybody holds the name.
 */
export function usernameBlockedKey(verdict: string | null | undefined): string | null

/**
 * Whether the invitation form should refuse to submit right now.
 *
 * Blocks on a DEFINITE answer about the name currently in the field: `taken` or
 * `invalid`. Not yet checked, still in flight, could not be checked, checked and
 * free, and a verdict about a name since edited all ALLOW — the line is "does
 * the service know", not "is the news bad", and the service is still the only
 * thing that decides at submission.
 */
export function usernameIsBlocked(
	current: string | null | undefined,
	checkedName: string | null | undefined,
	verdict: string | null | undefined,
): boolean
