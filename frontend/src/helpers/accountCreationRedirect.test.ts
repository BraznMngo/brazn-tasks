import {describe, it, expect} from 'vitest'

import {accountCreationRedirect} from './accountCreationRedirect'

const CHECKOUT = 'https://brazn.one/checkout'
const TOKEN = 'EXAMPLE_signup_token_43_chars_not_a_secret1'

describe('accountCreationRedirect', () => {
	// BRA-1444. The registration form cannot create an account on a managed
	// instance — the server refuses POST /register — so somebody who reaches it
	// with nothing goes where accounts are actually made.
	it('sends an empty-handed arrival to checkout', () => {
		expect(accountCreationRedirect('', CHECKOUT)).toBe(CHECKOUT)
	})

	// THE ONE THAT MATTERS. `/register#signup_token=…` is how somebody who has
	// already paid or been invited reaches this form (BRA-1071), and the server
	// accepts a registration carrying a good token. Diverting them to checkout
	// would send a paying customer back to pay again, and it is the fragment —
	// which never reaches the server — that distinguishes them.
	//
	// MUTATION: deleting the signupToken branch from accountCreationRedirect
	// makes this fail and the two below it still pass, which is why the token
	// case is asserted against a configured checkout address rather than on its
	// own.
	it('never diverts somebody who is carrying a signup token', () => {
		expect(accountCreationRedirect(TOKEN, CHECKOUT)).toBeNull()
	})

	// An instance that has not been told where accounts come from must not
	// invent a destination. This is the fail-safe for a managed deployment whose
	// checkout address was never configured.
	it('keeps the form when no checkout address is known', () => {
		expect(accountCreationRedirect('', null)).toBeNull()
		expect(accountCreationRedirect('', '')).toBeNull()
	})
})
