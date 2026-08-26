/**
 * Hand-written declarations for join.js, the invitation acceptance page
 * (BRA-1439 Story 5) — the same arrangement api.d.ts and app.d.ts use, so the
 * unit tests under frontend/src/brazn/one/ stay plain .test.ts files.
 */

/** The invitation id out of a query string (`?i=`), or null. */
export function invitationIdFromSearch(search: string | null | undefined): string | null

/** The signup token out of a URL fragment (`#signup_token=`), or null. Pure; storage is the caller's. */
export function signupTokenFromHash(hash: string | null | undefined): string | null

/**
 * Which of the acceptance's two affirmative outcomes happened: 'already-member'
 * for `already_member`, 'accepted' otherwise. Only meaningful on an ok result.
 */
export function acceptedOutcome(result: {outcome?: string | null} | null | undefined): 'accepted' | 'already-member'

/**
 * One state object in, one surface's markup out. `kind` is one of
 * 'missing-link' | 'choices' | 'accepting' | 'done' | 'refused';
 * 'done' reads `outcome`, 'refused' reads `sentence`.
 */
export function joinSurface(state: {
	kind?: string
	outcome?: 'accepted' | 'already-member'
	sentence?: string
} | null | undefined): string

/** The page's boot. Exported for the stubbed-fetch tests; self-schedules only when `#join` exists. */
export function boot(): Promise<void>
