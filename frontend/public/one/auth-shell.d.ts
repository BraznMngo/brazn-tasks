/**
 * Hand-written declarations for auth-shell.js — what the five signed-out
 * documents share (BRA-1475).
 */

/** HTML-escape. Every one of these pages renders a sentence the SERVER wrote. */
export function esc(value: unknown): string

/** A catalogue string, escaped. */
export function tx(key: string, params?: Record<string, unknown>): string

/** Load the catalogue before anything paints. Negotiates from the browser alone. */
export function loadStrings(): Promise<void>

/** Paint the card into `#auth` and reveal the stage. */
export function renderAuth(html: string): void

/** The brand mark. Both files ship; the stylesheet picks by theme. */
export function brandBlock(): string

/** The one place errors appear, empty until there is something to say. */
export function bannerBlock(): string

/** Put a sentence in the one error place, as TEXT, or clear it with null. */
export function showError(message: string | null | undefined): void

/** Send somebody to the general error page. The reason travels in the query, the sentence does not. */
export function sendToErrorPage(reason: string, sentence?: string | null): void

/** Read and consume the sentence a sender left behind. */
export function takeErrorSentence(): string | null

/** The address of one of our own documents. */
export function pageUrl(name: string, parts?: {search?: string, hash?: string}): string

/** Navigate to one of ours, unless we are already standing on it. True when it navigated. */
export function goToPage(name: string, parts?: {search?: string, hash?: string}): boolean

/** The official four-colour Google mark, as inline SVG. */
export function googleMark(): string

/** The server's own sentence when it sent one, else the catalogue fallback. */
export function forkErrorSentence(err: unknown, fallbackKey: string): string
