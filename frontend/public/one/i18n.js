// String layer for the ONE Tasks restricted views.
//
// Plain ES module, no build step: Vite copies frontend/public/ verbatim into dist/ and the Go
// binary serves it as-is, so nothing here may rely on a bundler, a transform or a framework.
//
// The catalogues under ./i18n/ are trimmed copies of frontend/src/i18n/lang/*.json. They carry
// only the keys this page asks for. A key with no translation in a language is OMITTED from that
// language's file rather than blanked, because the resolver below treats a missing key as
// "fall through to en" and a present-but-empty string as a bug worth surfacing.

// The Vue app's SUPPORTED_LOCALES are exact tags (frontend/src/i18n/index.ts:13-47). We match
// that list exactly for the six launch languages and never widen a region: 'es' must NOT become
// 'es-ES'. Widening is what produces a page that is half-translated in a locale nobody shipped.
// ar-SA / fa-IR / he-IL are deliberately absent: the page's CSS still uses physical properties
// (translateX, background-position, object-position), so RTL is out of scope for this change.
const SUPPORTED_LOCALES = Object.freeze(['en', 'es-ES', 'de-DE', 'fr-FR', 'zh-CN', 'ja-JP']);

// frontend/src/i18n/index.ts:51 — DEFAULT_LANGUAGE, and :64 fallbackLocale. Both are 'en'.
const DEFAULT_LOCALE = 'en';

// en is loaded first and kept for the whole session; `active` is the negotiated overlay and stays
// null when the negotiated language IS en, or when its catalogue failed to load.
let baseCatalogue = null;
let activeCatalogue = null;
let activeLocale = DEFAULT_LOCALE;

// One warning per missing key. Without this a key missing inside a render loop floods the console
// and buries the first occurrence, which is the one that tells you where it came from.
const warned = new Set();

export function currentLocale() {
 return activeLocale;
}

// Exposed for the fork-guards key audit and for tests; callers must not mutate it.
export function supportedLocales() {
 return SUPPORTED_LOCALES;
}

// Exact-tag negotiation. `preferred` is settings.language from GET /user (the fork returns user
// settings snake_cased, but this key is a single word so it is 'language' either way).
// Anything not in SUPPORTED_LOCALES falls to en rather than to a near neighbour.
export function negotiateLanguage(preferred, navigatorLanguages) {
 const candidates = [];
 if (preferred) candidates.push(String(preferred));
 for (const tag of navigatorLanguages || []) candidates.push(String(tag));
 for (const tag of candidates) {
  if (SUPPORTED_LOCALES.includes(tag)) return tag;
 }
 return DEFAULT_LOCALE;
}

async function fetchCatalogue(locale) {
 // Same-origin by construction: the page is served from /one/task.html on the same host, which is
 // the whole reason the session cookie works here (bar 3). Never point this at another origin.
 const res = await fetch(`./i18n/${locale}.json`, {credentials: 'same-origin'});
 if (!res.ok) throw new Error(`catalogue ${locale}: HTTP ${res.status}`);
 return await res.json();
}

// Boot ordering, ruling C10: the shell stays hidden until this resolves and the page renders ONCE
// afterwards. Rendering before the language is known produces an English flash and a second
// hydration pass, and the second pass is where stale DOM state leaks through.
//
// There is deliberately no PAGE_VERSION handshake (ruling C18): it would be hand-maintained with
// nothing checking it. The fork-guards step that asserts every t() literal exists in
// ./i18n/en.json is the real protection.
export async function init(preferredLanguage, navigatorLanguages) {
 const locale = negotiateLanguage(
  preferredLanguage,
  navigatorLanguages || (typeof navigator !== 'undefined' ? navigator.languages : []),
 );

 // en is a hard dependency, not a fallback. If it fails the page has no string layer at all, so
 // this rejects and app.js renders its error state rather than a page full of key paths.
 baseCatalogue = await fetchCatalogue(DEFAULT_LOCALE);

 activeLocale = locale;
 activeCatalogue = null;
 if (locale !== DEFAULT_LOCALE) {
  try {
   activeCatalogue = await fetchCatalogue(locale);
  } catch (err) {
   // A missing or broken regional catalogue is survivable: every key resolves against en instead.
   // Losing the whole page over it would be worse than showing English.
   console.warn(`[one/i18n] ${locale} catalogue unavailable, using ${DEFAULT_LOCALE}`, err);
   activeLocale = DEFAULT_LOCALE;
  }
 }

 if (typeof document !== 'undefined' && document.documentElement) {
  document.documentElement.lang = activeLocale;
 }
 return activeLocale;
}

// Resolve a full dotted path. A missing INTERMEDIATE object counts the same as a missing leaf:
// es-ES has no `organization` node at all, so `organization.seats.title` has to fail at depth 1
// and fall through to en rather than throw.
function lookup(catalogue, key) {
 if (!catalogue) return undefined;
 let node = catalogue;
 for (const part of key.split('.')) {
  if (node === null || typeof node !== 'object') return undefined;
  node = node[part];
 }
 return typeof node === 'string' && node !== '' ? node : undefined;
}

// vue-i18n pluralisation: branches are separated by '|'. Two branches are singular/plural, three
// are zero/one/other. ja-JP writes single-branch values for the relation kinds (no '|' at all),
// so a value with one branch has to pass through untouched.
function selectBranch(value, params) {
 if (value.indexOf('|') === -1) return value;
 const branches = value.split('|').map(s => s.trim());
 const count = params && params.count;
 if (typeof count !== 'number') return branches[0];
 if (branches.length === 2) return count === 1 ? branches[0] : branches[1];
 if (branches.length >= 3) return count === 0 ? branches[0] : count === 1 ? branches[1] : branches[2];
 return branches[0];
}

// Two placeholder forms, both inherited from the fork's catalogues:
//   {name} / {0}  — interpolate from params
//   {'x'}         — a literal, used to escape characters vue-i18n's message compiler would eat.
//                   '@' is the linked-message marker, so frontend/src/i18n/lang/en.json writes
//                   user.auth.emailPlaceholder as "e.g. frederic{'@'}example.com". Our own values
//                   carrying '@' are escaped the same way, because they live in that same file and
//                   are compiled by vue-i18n there even though this page parses them itself.
function interpolate(value, params) {
 return value.replace(/\{(?:'([^']*)'|([^{}]*))\}/g, (match, literal, name) => {
  if (literal !== undefined) return literal;
  const key = String(name).trim();
  if (params && Object.prototype.hasOwnProperty.call(params, key)) return String(params[key]);
  return match;
 });
}

// Fallback chain, in order: negotiated language -> en -> the key path itself.
// The key path is the documented last resort and is always accompanied by a console warning so it
// is findable; a blank label would be worse than an English one, and worse than the key.
export function t(key, params) {
 const raw = lookup(activeCatalogue, key) ?? lookup(baseCatalogue, key);
 if (raw === undefined) {
  if (!warned.has(key)) {
   warned.add(key);
   console.warn(`[one/i18n] missing key: ${key}`);
  }
  return key;
 }
 return interpolate(selectBranch(raw, params), params);
}
