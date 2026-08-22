import {describe, it, expect, vi, beforeEach} from 'vitest'

import {useRedirectToLastVisited} from './useRedirectToLastVisited'

const {resolveMock} = vi.hoisted(() => ({
	resolveMock: vi.fn(),
}))

vi.mock('vue-router', () => ({
	useRouter: () => ({resolve: resolveMock}),
}))

describe('useRedirectToLastVisited', () => {
	beforeEach(() => {
		localStorage.clear()
		resolveMock.mockReset()
		resolveMock.mockImplementation((route: {name?: string}) => ({
			href: `/resolved/${route.name ?? ''}`,
		}))
		Object.defineProperty(window, 'location', {
			value: {href: ''},
			writable: true,
			configurable: true,
		})
	})

	// The restricted-UI lockout (brazn.restricteduionly) is enforced entirely
	// server-side, and only ever sees a real HTTP request. router.push() would
	// change the route inside the already-loaded SPA through the History API
	// and never reach the server, so the lockout's redirect to
	// /one/settings.html would never fire after sign-in. A real navigation is
	// the fix, so these tests assert window.location.href changes, not that
	// router.push was called.
	it('navigates the whole page home when nothing was saved', () => {
		const {redirectIfSaved} = useRedirectToLastVisited()

		redirectIfSaved()

		expect(resolveMock).toHaveBeenCalledWith({name: 'home'})
		expect(window.location.href).toBe('/resolved/home')
	})

	it('navigates the whole page to the saved route, and clears it', () => {
		localStorage.setItem('lastVisited', JSON.stringify({
			name: 'task.detail',
			params: {id: '42'},
			query: {foo: 'bar'},
		}))

		const {redirectIfSaved} = useRedirectToLastVisited()
		redirectIfSaved()

		expect(resolveMock).toHaveBeenCalledWith({
			name: 'task.detail',
			params: {id: '42'},
			query: {foo: 'bar'},
		})
		expect(window.location.href).toBe('/resolved/task.detail')
		expect(localStorage.getItem('lastVisited')).toBeNull()
	})
})
