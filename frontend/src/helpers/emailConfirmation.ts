// Email confirmation: the shape of a link, and the address the person who
// registered is waiting on mail at (BRA-1072, docs/Percy-Account-Path.md §3).

// The server issues confirmation tokens as 64 alphanumeric characters
// (utils.CryptoRandomString(64), alphabet a-zA-Z0-9). Pinned here as a literal
// rather than derived from anything, so a change on either side shows up as a
// disagreement instead of being absorbed silently.
const CONFIRM_TOKEN_PATTERN = /^[0-9a-zA-Z]{64}$/

/**
 * Whether this could be a link we issued.
 *
 * Mail clients break long links across lines, and what arrives is then a
 * fragment of a token or a token with a newline in the middle of it. That is
 * worth telling apart from a link the server has never heard of: the answer is
 * the same - here is a new one - but the sentence explaining why is not, and a
 * person who is told "we do not recognise this" when their mail client mangled
 * it has no idea what to do differently.
 *
 * A token of the right shape is still checked by the server. This only decides
 * whether it is worth asking.
 */
export function looksLikeConfirmToken(token: string): boolean {
	return CONFIRM_TOKEN_PATTERN.test(token)
}

// Where the address is kept between registering and the confirmation screen.
//
// sessionStorage rather than the URL: the address is the customer's, and a
// query parameter puts it in this instance's access logs and in any Referer a
// linked page would send. sessionStorage rather than localStorage because it
// must not outlive the tab - the next person to use a shared machine should
// not be shown somebody else's address.
const PENDING_ADDRESS_KEY = 'pendingConfirmationEmail'

/**
 * Remembers, for this tab, the address a confirmation link has just been sent
 * to. Called by registration; the confirmation screen quotes it back, because
 * a typo in that field is otherwise invisible forever.
 */
export function rememberPendingConfirmation(email: string) {
	if (email !== '') {
		window.sessionStorage.setItem(PENDING_ADDRESS_KEY, email)
	}
}

/**
 * The address this tab is waiting on, or an empty string. Empty is an ordinary
 * case, not a fault: a confirmation link is usually opened on a different
 * device from the one the form was filled in on.
 */
export function getPendingConfirmation(): string {
	return window.sessionStorage.getItem(PENDING_ADDRESS_KEY) ?? ''
}

/**
 * Forgets the address. Called once the confirmation has landed.
 */
export function forgetPendingConfirmation() {
	window.sessionStorage.removeItem(PENDING_ADDRESS_KEY)
}
