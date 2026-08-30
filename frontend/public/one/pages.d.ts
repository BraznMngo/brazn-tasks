/**
 * Hand-written declarations for pages.js, the browser-side half of the
 * site-wide lockout (BRA-1475).
 */

export interface OneDocument {
	readonly name: string
	readonly file: string
	/** What the SERVER must route to this file. A trailing `/*` means a prefix. */
	readonly answersAt: readonly string[]
}

/** Every document this front end ships. One list, in one place. */
export const ONE_DOCUMENTS: readonly OneDocument[]

/** The query names a mailed link carries, and the document each one belongs to. */
export const QUERY_DOCUMENTS: Readonly<Record<string, string>>

/**
 * The address of one of our documents. THROWS on a name that is not one of
 * ours, so a typo is a visible failure rather than a quiet trip into the old
 * application.
 */
export function oneUrl(
	name: string,
	base: string | URL,
	parts?: {search?: string, hash?: string},
): string

/** Which of our documents answers a given address, or null when none does. */
export function documentForAddress(
	pathname: string | null | undefined,
	search?: string | null,
): string | null

/**
 * Whether the browser is already standing on the named document — the
 * redirect-loop guard's browser half.
 */
export function isCurrentDocument(
	name: string,
	pathname: string | null | undefined,
	search?: string | null,
): boolean
