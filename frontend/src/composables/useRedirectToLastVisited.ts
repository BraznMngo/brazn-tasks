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
		window.location.href = router.resolve(target).href
	}

	return {
		redirectIfSaved,
		getLastVisitedRoute,
	}
}
