<template>
	<div>
		<Message
			v-if="errorMessage"
			variant="danger"
		>
			{{ errorMessage }}
		</Message>
		<Message
			v-if="errorMessageFromQuery"
			variant="danger"
			class="mbs-2"
		>
			{{ errorMessageFromQuery }}
		</Message>
		<Message v-if="loading && !needsTotp">
			{{ $t('user.auth.authenticating') }}
		</Message>

		<form
			v-if="needsTotp"
			@submit.prevent="submitTotpAndRestart"
		>
			<Message class="mbe-2">
				{{ $t('user.auth.openIdTotpRequired') }}
			</Message>
			<FormField
				id="openIdTotpPasscode"
				ref="totpInput"
				v-model="totpPasscode"
				v-focus
				:label="$t('user.auth.totpTitle')"
				autocomplete="one-time-code"
				:placeholder="$t('user.auth.totpPlaceholder')"
				required
				type="text"
				inputmode="numeric"
			/>
			<XButton
				:loading="loading"
				:disabled="!totpPasscode"
				class="mbs-2"
				@click="submitTotpAndRestart"
			>
				{{ $t('user.auth.openIdTotpSubmit') }}
			</XButton>
		</form>
	</div>
</template>


<script setup lang="ts">
import {ref, computed, onMounted} from 'vue'
import {useRoute} from 'vue-router'
import {useI18n} from 'vue-i18n'

import {getErrorText} from '@/message'
import Message from '@/components/misc/Message.vue'
import FormField from '@/components/input/FormField.vue'
import {useRedirectToLastVisited} from '@/composables/useRedirectToLastVisited'
import {redirectToProvider} from '@/helpers/redirectToProvider'
import {refreshToken} from '@/helpers/auth'
import {AuthenticatedHTTPFactory, apiV2Url} from '@/helpers/fetcher'

import {useAuthStore} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'
import type {IProvider} from '@/types/IProvider'

defineOptions({name: 'Auth'})

const {t} = useI18n({useScope: 'global'})

const route = useRoute()
const {redirectIfSaved} = useRedirectToLastVisited()

const authStore = useAuthStore()
const configStore = useConfigStore()

const loading = computed(() => authStore.isLoading)
const errorMessage = ref('')
const errorMessageFromQuery = computed(() => route.query.error)

const needsTotp = ref(false)
const totpPasscode = ref('')

function pendingTotpKey(provider: string): string {
	return `openid_pending_totp_${provider}`
}

function findProvider(providerKey: string): IProvider | undefined {
	return configStore.auth.openidConnect.providers?.find((p: IProvider) => p.key === providerKey)
}

/**
 * Google's registered redirect_uri never changes, so it is always this
 * route — including for "connect Google from my account settings"
 * (`/one/settings.html`'s restricted UI has no auth landing page of its own;
 * see the plan doc). These two keys are how that page hands off: written to
 * `sessionStorage` (not the `state` key above, which is this page's OWN
 * localStorage marker for an ordinary sign-in, and must not collide with it)
 * right before the redirect to Google.
 *
 * MUST MATCH `OIDC_LINK_STATE_KEY`/`OIDC_LINK_PROVIDER_KEY`/
 * `OIDC_LINK_RETURN_KEY` in frontend/public/one/view-settings.js BYTE FOR
 * BYTE. Separate bundles, no shared constant, no test that would catch a
 * drift — grep that file before renaming any of these here.
 */
const LINK_STATE_KEY = 'one.settings.oidcLinkState'
const LINK_PROVIDER_KEY = 'one.settings.oidcLinkProvider'
const LINK_RETURN_KEY = 'one.settings.oidcLinkReturnTo'

function readLinkRequest(): {state: string, provider: string} | null {
	const state = sessionStorage.getItem(LINK_STATE_KEY)
	const provider = sessionStorage.getItem(LINK_PROVIDER_KEY)
	return state !== null && provider !== null ? {state, provider} : null
}

/**
 * Finishes a connect-from-settings round trip with a full-page redirect back
 * to the settings page — a plain navigation, not an SPA route change, since
 * `/one/settings.html` and this Vue app are separate documents. `message ===
 * null` is success; otherwise it is the sentence to show, verbatim, exactly
 * as this page already shows the server's own sentence for an ordinary
 * sign-in failure (ruling C4 in the settings page's own file header).
 */
function finishLink(message: string | null): void {
	const returnTo = sessionStorage.getItem(LINK_RETURN_KEY) ?? '/one/settings.html?tab=account'
	sessionStorage.removeItem(LINK_STATE_KEY)
	sessionStorage.removeItem(LINK_PROVIDER_KEY)
	sessionStorage.removeItem(LINK_RETURN_KEY)

	const url = new URL(returnTo, window.location.origin)
	if (message === null) {
		url.searchParams.set('openid_linked', '1')
	} else {
		url.searchParams.set('openid_link_error', message)
	}
	window.location.href = url.toString()
}

/**
 * The authenticated counterpart to authenticateWithCode() below, called only
 * once the caller has already confirmed this callback's provider and state
 * match the stored link request (see isThisCallbackTheLink) — this function
 * itself no longer re-checks either. It shares nothing with the sign-in path
 * on purpose: signing in REPLACES whatever session existed (removeToken(), a
 * fresh authStore.openIdAuth()), which is exactly wrong here — this must
 * attach an identity to the session already open in another tab/window, not
 * end it.
 *
 * THE TOKEN PROBLEM THIS FUNCTION EXISTS TO SOLVE: `/one/settings.html`
 * keeps its access token in an in-memory JS variable that a full-page
 * navigation to Google and back does not survive — this Vue bundle just
 * loaded fresh and has no bearer token of its own for that session. What
 * DOES survive is the HttpOnly refresh cookie, so `refreshToken(true)`
 * (`@/helpers/auth`, the same primitive checkAuth()/renewToken() use, called
 * directly here because both of those skip the refresh entirely when there
 * is no token ALREADY in localStorage to renew — precisely this page's
 * situation) mints a fresh JWT for whoever that cookie belongs to, which
 * `AuthenticatedHTTPFactory()` then picks up automatically for the actual
 * connect call.
 */
async function authenticateAsLink(providerKey: string) {
	if (typeof route.query.error !== 'undefined') {
		finishLink(typeof route.query.message === 'string' ? route.query.message : t('user.auth.openIdGeneralError'))
		return
	}

	try {
		await refreshToken(true)
	} catch {
		finishLink(t('user.auth.openIdGeneralError'))
		return
	}

	try {
		await AuthenticatedHTTPFactory().post(
			apiV2Url(`user/settings/connect/openid/${encodeURIComponent(providerKey)}`),
			{code: route.query.code as string},
		)
		finishLink(null)
	} catch (e) {
		finishLink(getErrorText(e))
	}
}

async function authenticateWithCode() {
	errorMessage.value = ''

	const providerKey = route.params.provider as string

	// MUST match THIS callback (provider and state), not merely exist — a
	// leftover marker from an abandoned "Connect Google" attempt (closed tab,
	// browser back button; finishLink() is the only code that ever clears it,
	// and it never ran) would otherwise hijack a completely unrelated, later
	// ordinary sign-in in the same tab into the link-completion branch. A
	// marker that does not match this callback is not this callback's
	// concern either way, so it is cleared here rather than left to
	// potentially misroute a THIRD callback after this one.
	const linkRequest = readLinkRequest()
	const isThisCallbackTheLink = linkRequest !== null
		&& linkRequest.provider === providerKey
		&& (typeof route.query.error !== 'undefined' || route.query.state === linkRequest.state)
	if (linkRequest !== null && !isThisCallbackTheLink) {
		sessionStorage.removeItem(LINK_STATE_KEY)
		sessionStorage.removeItem(LINK_PROVIDER_KEY)
		sessionStorage.removeItem(LINK_RETURN_KEY)
	}
	if (isThisCallbackTheLink) {
		await authenticateAsLink(providerKey)
		return
	}

	if (typeof route.query.error !== 'undefined') {
		sessionStorage.removeItem(pendingTotpKey(providerKey))
		errorMessage.value = typeof route.query.message !== 'undefined'
			? route.query.message as string
			: t('user.auth.openIdGeneralError')
		return
	}

	const state = localStorage.getItem('state')
	if (typeof route.query.state === 'undefined' || route.query.state !== state) {
		sessionStorage.removeItem(pendingTotpKey(providerKey))
		errorMessage.value = t('user.auth.openIdStateError')
		return
	}

	// sessionStorage (not localStorage): per-tab, cleared on tab close.
	const pendingPasscode = sessionStorage.getItem(pendingTotpKey(providerKey)) ?? undefined
	if (pendingPasscode) {
		sessionStorage.removeItem(pendingTotpKey(providerKey))
	}

	try {
		await authStore.openIdAuth({
			provider: providerKey,
			code: route.query.code as string,
			totpPasscode: pendingPasscode,
		})

		redirectIfSaved()
	} catch (e) {
		const err = e as {response?: {data?: {code?: number}}}
		if (err?.response?.data?.code === 1017) {
			needsTotp.value = true
			return
		}
		errorMessage.value = getErrorText(e)
	}
}

async function submitTotpAndRestart() {
	if (!totpPasscode.value) {
		return
	}

	const providerKey = route.params.provider as string
	const provider = findProvider(providerKey)
	if (!provider) {
		errorMessage.value = t('user.auth.openIdGeneralError')
		return
	}

	sessionStorage.setItem(pendingTotpKey(providerKey), totpPasscode.value)
	// The auth code is single-use; restart the OIDC flow so the next callback reads the stashed passcode.
	redirectToProvider(provider)
}

onMounted(() => authenticateWithCode())
</script>
