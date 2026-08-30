/**
 * Hand-written declarations for error.js — the general error page (BRA-1475).
 */

/**
 * The heading and body keys for a reason, falling to the general pair. The set
 * is CLOSED: `reason` arrives in the address, so anybody can put anything there.
 */
export function pairForReason(reason: string | null | undefined): {title: string, body: string}

/** The reason out of the query, or null. */
export function reasonFromSearch(search: string | null | undefined): string | null

/** The page's markup. The service's own sentence is an extra line, never a replacement. */
export function errorSurface(state: {reason?: string | null, sentence?: string | null}): string

/** The page's boot. Self-schedules only when `#auth` exists. */
export function boot(): Promise<void>
