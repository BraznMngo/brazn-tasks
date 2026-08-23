import {describe, it, expect, beforeEach} from 'vitest'

import * as api from '../../../public/one/api.js'

/*
 * The "connect Google to my account afterward" half of frontend/public/one/api.js: the app-root
 * URL builder (not an /api/v1 or /api/v2 path — Google's own registered redirect_uri) and the
 * Google authorize URL it feeds into. Both are pure — neither makes a request — because the
 * authenticated connect call itself is made from frontend/src/views/user/OpenIdAuth.vue, a
 * separate bundle this page cannot call into (see buildOpenIdAuthorizeUrl's own comment in
 * api.js for why).
 *
 * See api.session.test.ts for why the import looks like a relative path into public/.
 */

const ORIGIN = 'https://dev.tasks.brazn.one'

describe('one/api.js OpenID connect', () => {
	beforeEach(() => {
		api.resetSession()
		api.configure({origin: ORIGIN})
	})

	it('forkAppUrl resolves against the app root, not /api/v1 or /api/v2', () => {
		expect(api.forkAppUrl('auth/openid/google')).toBe(`${ORIGIN}/auth/openid/google`)
	})

	it('buildOpenIdAuthorizeUrl carries all five parameters Google requires', () => {
		const provider = {
			key: 'google',
			auth_url: 'https://accounts.google.com/o/oauth2/v2/auth',
			client_id: 'client-123',
			scope: 'openid email profile',
		}
		const authorizeUrl = new URL(api.buildOpenIdAuthorizeUrl(provider, 'state-abc'))

		expect(authorizeUrl.origin + authorizeUrl.pathname).toBe('https://accounts.google.com/o/oauth2/v2/auth')
		expect(authorizeUrl.searchParams.get('client_id')).toBe('client-123')
		expect(authorizeUrl.searchParams.get('redirect_uri')).toBe(`${ORIGIN}/auth/openid/google`)
		expect(authorizeUrl.searchParams.get('response_type')).toBe('code')
		expect(authorizeUrl.searchParams.get('scope')).toBe('openid email profile')
		expect(authorizeUrl.searchParams.get('state')).toBe('state-abc')
	})
})
