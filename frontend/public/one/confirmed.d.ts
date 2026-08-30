/**
 * Hand-written declarations for confirmed.js — the two result screens for a
 * mailed confirmation (BRA-1475).
 */

/**
 * What this arrival is confirming and with which token, or null when the
 * address carries neither. The DELETION query is read first: an address
 * carrying both must not silently skip the more consequential of the two.
 */
export function confirmationFrom(
	search: string | null | undefined,
	hash: string | null | undefined,
): {kind: 'email' | 'deletion', token: string} | null

/** Six outcomes, six sentences, and none of them is a status code. */
export function confirmedSurface(state: {phase?: string, sentence?: string | null}): string

/** The page's boot. Self-schedules only when `#auth` exists. */
export function boot(): Promise<void>
