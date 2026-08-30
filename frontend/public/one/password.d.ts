/**
 * Hand-written declarations for password.js — one document, two states
 * (BRA-1475).
 */

/**
 * The reset token this arrival carries, from the fragment first and the query
 * (`?userPasswordReset=`) second, or null.
 */
export function resetTokenFrom(
	search: string | null | undefined,
	hash: string | null | undefined,
): string | null

/** State one: ask for a link. */
export function requestSurface(state: {
	phase?: string
	email?: string
	sent?: boolean
	signInUrl?: string
}): string

/** State two: set the new password. */
export function setSurface(state: {phase?: string, done?: boolean}): string

/** The page's boot. Self-schedules only when `#auth` exists. */
export function boot(): Promise<void>
