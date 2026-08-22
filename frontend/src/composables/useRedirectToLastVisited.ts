import {useRouter} from 'vue-router'
import {getLastVisited, clearLastVisited} from '@/helpers/saveLastVisited'

export function useRedirectToLastVisited() {

	const router = useRouter()

	function getLastVisitedRoute() {
		const last = getLastVisited()
		if (last === null) {
			return null
		}

		clearLastVisited()
		return {
			name: last.name,
			params: last.params,
			query: last.query,
		}
	}

	// A real navigation, not router.push(). This runs right after sign-in, and
	// the restricted-UI lockout (brazn.restricteduionly) is enforced entirely
	// server-side: it only ever sees an actual HTTP request landing on the Go
	// static handler. router.push() changes the route inside the already-loaded
	// SPA through the History API and never reaches the server, so with the
	// lockout on, the post-login landing silently stayed on the full
	// application instead of the redirect to /one/settings.html the lockout
	// depends on. window.location.href always issues a real request, so the
	// server-side redirect fires when the lockout is on and this is an
	// ordinary same-origin navigation when it is off.
	function redirectIfSaved() {
		const lastRoute = getLastVisitedRoute()
		const target = lastRoute ?? {name: 'home'}
		// router.resolve() throws synchronously for a route name the current
		// build no longer has (a renamed/removed route saved by a stale client
		// before a deploy), unlike router.push()'s rejected promise. This runs
		// right after a successful sign-in inside the same try/catch as the
		// auth call in Login.vue/Register.vue/OpenIdAuth.vue, so an uncaught
		// throw here would surface as a false "login failed" message for a
		// user who actually authenticated. Falling back to home keeps this
		// from ever failing the sign-in it follows.
		try {
			window.location.href = router.resolve(target).href
		} catch {
			window.location.href = router.resolve({name: 'home'}).href
		}
	}

	return {
		redirectIfSaved,
		getLastVisitedRoute,
	}
}
