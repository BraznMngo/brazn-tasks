import {afterEach, beforeEach, describe, expect, it} from 'vitest'

import {clearSignupToken, getSignupToken, readSignupTokenFromFragment} from './signupToken'

// The token used throughout is the value from the contract's own conformance
// fixture: 43 unpadded base64url characters. Quoted rather than invented, so a
// change to the shape the contract requires shows up here too.
const TOKEN = 'EXAMPLE_signup_token_43_chars_not_a_secret1'

function visit(url: string) {
	window.history.replaceState(null, '', url)
}

describe('signupToken', () => {
	beforeEach(() => {
		window.sessionStorage.clear()
		visit('/register')
	})

	afterEach(() => {
		window.sessionStorage.clear()
	})

	it('reads the token out of the fragment', () => {
		visit(`/register#signup_token=${TOKEN}`)

		expect(readSignupTokenFromFragment()).toBe(TOKEN)
	})

	// The obligation the contract names: the fragment must not be left in the
	// address bar or in history, because that is the one place it does not
	// escape on its own.
	it('removes the fragment from the address bar', () => {
		visit(`/register#signup_token=${TOKEN}`)

		readSignupTokenFromFragment()

		expect(window.location.hash).toBe('')
		expect(window.location.pathname).toBe('/register')
	})

	// A query parameter is the form the contract refuses, because a URL reaches
	// access logs. Nothing here may read one.
	it('ignores a token offered as a query parameter', () => {
		visit(`/register?signup_token=${TOKEN}`)

		expect(readSignupTokenFromFragment()).toBe('')
		expect(getSignupToken()).toBe('')
	})

	// The Google route leaves this origin and returns on another page, so a
	// second read that carries no fragment must not lose what the first one
	// found.
	it('keeps the token across a page that carries no fragment', () => {
		visit(`/register#signup_token=${TOKEN}`)
		readSignupTokenFromFragment()

		visit('/auth/openid/google?code=whatever')

		expect(readSignupTokenFromFragment()).toBe(TOKEN)
		expect(getSignupToken()).toBe(TOKEN)
	})

	it('reports no token when none was ever offered', () => {
		expect(readSignupTokenFromFragment()).toBe('')
		expect(getSignupToken()).toBe('')
	})

	it('forgets the token once it has been used', () => {
		visit(`/register#signup_token=${TOKEN}`)
		readSignupTokenFromFragment()

		clearSignupToken()

		expect(getSignupToken()).toBe('')
	})
})
