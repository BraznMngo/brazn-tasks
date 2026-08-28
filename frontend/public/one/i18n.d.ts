// Hand-written declarations for ./i18n.js.
//
// frontend/tsconfig.app.json includes src/**/*, so the unit tests under frontend/src/brazn/one/
// are type-checked by `vue-tsc --build`. They import this module by relative path
// (../../../public/one/i18n.js); TS resolves './i18n.js' to './i18n.d.ts' with no config change,
// and a file reached through an import enters the program even though public/ is outside
// `include`. Shipping this file is what keeps the tests importable without touching any upstream
// tsconfig (ruling C5).

/** Interpolation values. `count` additionally selects the plural branch of a `a | b` value. */
export interface TranslationParams {
	count?: number
	[param: string]: string | number | undefined
}

/** The six launch languages, as exact tags. No regional widening is performed. */
export type SupportedLocale = 'en' | 'es-ES' | 'de-DE' | 'fr-FR' | 'zh-CN' | 'ja-JP'

/** The locale `t()` is currently resolving against. `'en'` until {@link init} resolves. */
export declare function currentLocale(): SupportedLocale

/** The frozen list of exact tags this page ships. Callers must not mutate it. */
export declare function supportedLocales(): readonly SupportedLocale[]

/**
 * Pick an exact tag from the user's `settings.language` and `navigator.languages`.
 * Anything not in the supported list resolves to `'en'` — `'es'` never becomes `'es-ES'`.
 */
export declare function negotiateLanguage(
	preferred: string | null | undefined,
	navigatorLanguages?: readonly string[],
): SupportedLocale

/**
 * Load `en` (hard dependency) and, when different, the negotiated catalogue as an overlay.
 * Rejects only when `en` itself cannot be loaded — the page has no string layer in that case.
 * Resolves to the locale actually in use, which is `'en'` when the overlay failed to load.
 *
 * Call this once, after `GET /api/v2/user` resolves the language, and keep the shell hidden
 * until it settles: the page renders exactly once afterwards (ruling C10).
 */
export declare function init(
	preferredLanguage: string | null | undefined,
	navigatorLanguages?: readonly string[],
): Promise<SupportedLocale>

/**
 * Resolve a dotted key: negotiated language -> `en` -> the key path itself.
 * Returning the key is the last resort and always warns; it never returns an empty string.
 */
export declare function t(key: string, params?: TranslationParams): string

/**
 * The name to show for a project. A project whose stored title is the literal `'Inbox'` — the
 * one the server gives every account on registration — reads as the translated "Your Tasks";
 * anything else is its own title. Use this at every place a project title is printed.
 */
export declare function projectTitle(project: {title?: string | null} | null | undefined): string
