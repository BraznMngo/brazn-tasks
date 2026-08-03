// Shared scaffolding for the four account-screen tests. It exists so the four
// files argue about behaviour rather than about how to mount a view.
import {createI18n} from 'vue-i18n'
import en from '@/i18n/lang/en.json'
import XButton from '@/components/input/Button.vue'

export const i18n = createI18n({legacy: false, locale: 'en', messages: {en}})

/**
 * A RouterLink that keeps the route name where a test can see it. The default
 * stub renders `to` as "[object Object]", which makes "this links to sign in"
 * unassertable.
 */
export const RouterLinkStub = {
	props: ['to'],
	template: '<a class="router-link" :data-to="typeof to === \'string\' ? to : to?.name"><slot /></a>',
}

export const globalMountOptions = {
	plugins: [i18n],
	// XButton is registered globally by main.ts, which a component test never
	// runs. Registering the real one rather than stubbing it keeps the button
	// markup - the class, the slot, the fallthrough attributes - real, which is
	// what several of these tests are looking at.
	components: {XButton},
	directives: {
		focus: {},
		tooltip: {},
	},
	stubs: {
		RouterLink: RouterLinkStub,
		Icon: true,
		DesktopLogin: true,
	},
}

/**
 * Lets the 100ms debounced validators fire and the resulting render settle.
 * The views debounce on purpose, so a test that does not wait sees the form as
 * it was before it was checked.
 */
export async function settle(ms = 250) {
	await new Promise(resolve => setTimeout(resolve, ms))
}

/** An API rejection shaped the way the HTTP client hands one to a view. */
export function apiRejection(code: number | undefined, message: string, status = 412) {
	return {
		message: 'Request failed with status code ' + status,
		response: {
			status,
			data: code === undefined ? {message} : {code, message},
		},
	}
}
