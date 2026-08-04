// Shared scaffolding for the four account-screen tests. It exists so the four
// files argue about behaviour rather than about how to mount a view.
import {createI18n} from 'vue-i18n'
import en from '@/i18n/lang/en.json'
import de from '@/i18n/lang/de-DE.json'
import XButton from '@/components/input/Button.vue'

// THESE IMPORTS ARE NOT STRINGS. The @intlify build plugin precompiles
// src/i18n/lang/*.json into message ASTs, so `en.user.auth.password` is an
// object like {type: 0, start: 0, end: 16, ...} and not "Password". vue-i18n
// consumes that form happily, which is why it can be handed straight to
// createI18n - but any test that calls a string method on one throws, and
// `toContain` against one silently compares a string with an object and fails
// whatever the screen rendered. Assert expected copy as literals in the test,
// or through what the component renders.
export const i18n = createI18n({legacy: false, locale: 'en', messages: {en}})
export const i18nDe = createI18n({legacy: false, locale: 'de-DE', messages: {'de-DE': de}})

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

/** The same, rendering in German. */
export const globalMountOptionsDe = {
	...globalMountOptions,
	plugins: [i18nDe],
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
