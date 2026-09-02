/**
 * Hand-written declarations for auth-shell.js — what the five signed-out
 * documents share (BRA-1475).
 */

/** HTML-escape. Every one of these pages renders a sentence the SERVER wrote. */
export function esc(value: unknown): string

/** A catalogue string, escaped. */
export function tx(key: string, params?: Record<string, unknown>): string

/** Load the catalogue before anything paints. Honours a stored signed-out choice. */
export function loadStrings(): Promise<void>

/** Paint the card into `#auth`, append the language selector, and reveal the stage. */
export function renderAuth(html: string): void

/** The brand mark. Both files ship; the stylesheet picks by theme. */
export function brandBlock(): string

/** Language selector markup (BRA-1444). Appended by `renderAuth`. */
export function languageBlock(): string

/** Remembered signed-out language, or null. Same localStorage key as Vue. */
export function getStoredLanguage(): string | null

/** Persist a signed-out language choice. */
export function saveLanguage(locale: string): void

/** Wire the language selector; call once from each page boot with that page's render. */
export function installAuthLanguage(rerender: () => void): void

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
/**
 * A password field wrapped in its reveal control. `type="button"` on the
 * control is load-bearing: a bare button inside a form submits it.
 */
export function passwordField(id: string, labelKey: string, attrs?: string): string

/**
 * Install the one delegated listener that drives every reveal control on the
 * page. Delegated from `document`, so it survives every re-render; it changes
 * the input's `type` property rather than re-rendering, because re-rendering
 * would discard what the person has typed.
 */
export function installPasswordReveal(): void
