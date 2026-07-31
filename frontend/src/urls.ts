// AGPL-3.0 section 13: the corresponding source for the version being run must
// be offered to anyone interacting with it over a network. PoweredByLink puts
// this URL in the sidebar footer and on public link-share pages, so the offer is
// reachable from the running application. See NOTICE.md for the full
// modification notice.
export const SOURCE_CODE = 'https://github.com/BraznMngo/brazn-tasks'

// Upstream project this fork is derived from. Brazn Tasks is not affiliated
// with, endorsed by, or a product of the Vikunja project.
export const UPSTREAM_PROJECT = 'https://vikunja.io'

// Keeps a query string on the URL so PoweredByLink can append &utm_medium=.
export const POWERED_BY = `${SOURCE_CODE}?utm_source=powered_by`

// Upstream documentation, referenced descriptively — we have no docs of our own
// and the behaviour these pages describe is unchanged from upstream. Kept here
// rather than inlined at the call sites so that pointing them at our own docs
// later is one edit, not a hunt through components.
export const CALDAV_DOCS = 'https://vikunja.io/docs/caldav/'
export const WEBHOOKS_DOCS = 'https://vikunja.io/docs/webhooks/'
