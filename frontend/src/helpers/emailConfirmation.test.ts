import {beforeEach, describe, expect, it} from 'vitest'

import {
	forgetPendingConfirmation,
	getPendingConfirmation,
	looksLikeConfirmToken,
	rememberPendingConfirmation,
} from './emailConfirmation'

describe('looksLikeConfirmToken', () => {
	// Pinned against the contract - utils.CryptoRandomString(64) over
	// [a-zA-Z0-9] - rather than against whatever the pattern happens to be, so
	// a change on the server side shows up here as a disagreement.
	it('accepts 64 alphanumeric characters', () => {
		expect(looksLikeConfirmToken('a'.repeat(64))).toBe(true)
		expect(looksLikeConfirmToken('A1b2C3d4'.repeat(8))).toBe(true)
	})

	it('refuses a token that is the wrong length', () => {
		expect(looksLikeConfirmToken('a'.repeat(63))).toBe(false)
		expect(looksLikeConfirmToken('a'.repeat(65))).toBe(false)
		expect(looksLikeConfirmToken('')).toBe(false)
	})

	// What a mail client actually does to a long link: breaks it across two
	// lines, so what arrives carries whitespace or has been cut in half.
	it('refuses a token a mail client broke', () => {
		expect(looksLikeConfirmToken('a'.repeat(32) + '\n' + 'a'.repeat(31))).toBe(false)
		expect(looksLikeConfirmToken('a'.repeat(32) + ' ' + 'a'.repeat(31))).toBe(false)
	})

	it('refuses characters the server never puts in one', () => {
		expect(looksLikeConfirmToken('a'.repeat(63) + '-')).toBe(false)
		expect(looksLikeConfirmToken('a'.repeat(63) + '%')).toBe(false)
	})
})

describe('the pending address', () => {
	beforeEach(() => {
		window.sessionStorage.clear()
	})

	it('is remembered and given back', () => {
		rememberPendingConfirmation('someone@example.com')

		expect(getPendingConfirmation()).toBe('someone@example.com')
	})

	it('is an empty string when nothing was remembered', () => {
		expect(getPendingConfirmation()).toBe('')
	})

	it('is forgotten once the confirmation has landed', () => {
		rememberPendingConfirmation('someone@example.com')
		forgetPendingConfirmation()

		expect(getPendingConfirmation()).toBe('')
	})

	// sessionStorage, not localStorage: the next person on a shared machine
	// must not be shown somebody else's address.
	it('is kept out of localStorage entirely', () => {
		rememberPendingConfirmation('someone@example.com')

		expect(window.localStorage.getItem('pendingConfirmationEmail')).toBeNull()
	})
})
