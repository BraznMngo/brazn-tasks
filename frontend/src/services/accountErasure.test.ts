import {describe, it, expect, vi, beforeEach} from 'vitest'

const {getMock, postMock} = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}))

vi.mock('@/helpers/fetcher', () => ({
	AuthenticatedHTTPFactory: () => ({get: getMock, post: postMock}),
}))

import {fetchSuccessorCandidates, eraseManagedAccount} from './accountErasure'

describe('accountErasure (BRA-1404)', () => {
	beforeEach(() => {
		getMock.mockReset()
		postMock.mockReset()
	})

	it('fetchSuccessorCandidates calls the commercial service at the site root, not this fork\'s own /api/v1 base', async () => {
		getMock.mockResolvedValue({data: {candidates: [{user_id: '42'}]}})
		await fetchSuccessorCandidates()

		expect(getMock).toHaveBeenCalledTimes(1)
		const url = getMock.mock.calls[0][0]
		expect(url).toBe('http://localhost:3000/v1/account/successor-candidates')
	})

	it('fetchSuccessorCandidates maps the wire shape (user_id) to the function\'s own contract (userId)', async () => {
		getMock.mockResolvedValue({data: {candidates: [{user_id: '42'}, {user_id: '7'}]}})
		const candidates = await fetchSuccessorCandidates()

		expect(candidates).toEqual([{userId: '42'}, {userId: '7'}])
	})

	it('fetchSuccessorCandidates answers an empty array rather than throwing when the service sends no candidates key', async () => {
		getMock.mockResolvedValue({data: {}})
		const candidates = await fetchSuccessorCandidates()

		expect(candidates).toEqual([])
	})

	it('eraseManagedAccount posts successor_user_id, null when none was chosen — DELETE-THE-GUARD: sending undefined instead of null here drops the key, and the service refuses a body missing it the same as a malformed one', async () => {
		postMock.mockResolvedValue({})
		await eraseManagedAccount(null)

		expect(postMock).toHaveBeenCalledWith(
			'http://localhost:3000/v1/account/erasure',
			{successor_user_id: null},
		)
	})

	it('eraseManagedAccount posts the chosen successor', async () => {
		postMock.mockResolvedValue({})
		await eraseManagedAccount('42')

		expect(postMock).toHaveBeenCalledWith(
			'http://localhost:3000/v1/account/erasure',
			{successor_user_id: '42'},
		)
	})

	it('eraseManagedAccount propagates a failure rather than swallowing it', async () => {
		postMock.mockRejectedValue(new Error('409'))

		await expect(eraseManagedAccount(null)).rejects.toThrow('409')
	})
})
