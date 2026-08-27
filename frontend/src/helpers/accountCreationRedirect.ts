/**
 * Whether somebody who asked for the registration form should be sent somewhere
 * else instead, and where. `null` means let them have the form.
 *
 * This is one `if` and it is its own function because the ORDER of the two
 * conditions is the whole rule, and getting it backwards breaks the paid
 * customer's only way in rather than merely looking wrong. It is called from a
 * router guard, which is not reachable from a test without importing every view
 * in the application.
 *
 * @param signupToken The token this arrival carries, or '' for none. A token
 * means the commercial service has already decided this person is entitled to
 * an account, and the server accepts a registration that presents one — so the
 * form is right and nothing may divert them. It travels in the URL fragment
 * (see ./signupToken), which no browser sends to any server, so this decision
 * can only be made in the browser.
 *
 * @param checkoutUrl Where this instance makes accounts, or null when it makes
 * its own. Null keeps the form: an instance that has not been told where
 * accounts come from must not invent a destination, and a self-hosted one is
 * registering its own users perfectly well.
 */
export function accountCreationRedirect(
	signupToken: string,
	checkoutUrl: string | null,
): string | null {
	if (signupToken !== '') {
		return null
	}

	return checkoutUrl === '' ? null : checkoutUrl
}
