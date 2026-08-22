<template>
	<div>
		<Message
			v-if="confirmedEmailSuccess"
			variant="success"
			text-align="center"
			class="mbe-4"
		>
			{{ $t('user.auth.confirmEmailSuccess') }}
		</Message>
		<Message
			v-if="errorMessage"
			variant="danger"
			class="mbe-4"
		>
			{{ errorMessage }}
		</Message>

		<DesktopLogin v-if="isDesktop" />

		<!-- Same order/weight as registration: Google full width above the form, then the form (Percy-Account-Path.md §4). -->
		<template v-if="!isDesktop && hasOpenIdProviders">
			<XButton
				v-for="(p, k) in openidConnect.providers"
				:key="k"
				variant="secondary"
				class="is-fullwidth mbe-2 oidc-button"
				@click="redirectToProvider(p)"
			>
				<!-- Official 4-colour Google "G" (developers.google.com/identity/branding-guidelines) - XButton's `icon` prop only takes a single-colour FontAwesome glyph. -->
				<svg
					v-if="isGoogleProvider(p)"
					class="oidc-icon"
					viewBox="0 0 18 18"
					aria-hidden="true"
				>
					<path
						fill="#4285F4"
						d="M17.64 9.2c0-.637-.057-1.251-.164-1.84H9v3.481h4.844c-.209 1.125-.843 2.078-1.796 2.717v2.258h2.908c1.702-1.567 2.684-3.874 2.684-6.615z"
					/>
					<path
						fill="#34A853"
						d="M9 18c2.43 0 4.467-.806 5.956-2.18l-2.908-2.259c-.806.54-1.837.86-3.048.86-2.344 0-4.328-1.584-5.036-3.711H.957v2.332C2.438 15.983 5.482 18 9 18z"
					/>
					<path
						fill="#FBBC05"
						d="M3.964 10.71A5.41 5.41 0 013.682 9c0-.593.102-1.17.282-1.71V4.958H.957A8.996 8.996 0 000 9c0 1.452.348 2.827.957 4.042l3.007-2.332z"
					/>
					<path
						fill="#EA4335"
						d="M9 3.58c1.321 0 2.508.454 3.44 1.345l2.582-2.58C13.463.891 11.426 0 9 0 5.482 0 2.438 2.017.957 4.958L3.964 7.29C4.672 5.163 6.656 3.58 9 3.58z"
					/>
				</svg>
				{{ $t('user.auth.loginWith', {provider: p.name}) }}
			</XButton>
			<div
				v-if="localAuthEnabled || ldapAuthEnabled"
				class="or-rule"
			>
				<span>{{ $t('user.auth.or') }}</span>
			</div>
		</template>

		<form
			v-if="!isDesktop && (localAuthEnabled || ldapAuthEnabled)"
			id="loginform"
			@submit.prevent="submit"
		>
			<ErrorSummary :errors="fieldErrors" />
			<FormField
				id="username"
				ref="usernameRef"
				v-focus
				:label="$t('user.auth.usernameEmail')"
				name="username"
				:placeholder="$t('user.auth.usernamePlaceholder')"
				required
				type="text"
				autocomplete="username"
				:error="usernameValid ? null : $t('user.auth.usernameRequired')"
				@keyup.enter="submit"
				@focusout="validateUsernameField()"
			/>
			<div class="field">
				<div class="label-with-link">
					<label
						class="label"
						for="password"
					>{{ $t('user.auth.password') }}</label>
					<RouterLink
						v-if="localAuthEnabled"
						:to="{ name: 'user.password-reset.request' }"
						class="reset-password-link"
					>
						{{ $t('user.auth.forgotPassword') }}
					</RouterLink>
				</div>
				<Password
					ref="passwordRef"
					v-model="password"
					:validate-initially="validatePasswordInitially"
					:validate-min-length="false"
					autocomplete="current-password"
					@submit="submit"
				/>
			</div>
			<FormField
				v-if="needsTotpPasscode"
				id="totpPasscode"
				ref="totpPasscode"
				v-focus
				:label="$t('user.auth.totpTitle')"
				autocomplete="one-time-code"
				:placeholder="$t('user.auth.totpPlaceholder')"
				required
				type="text"
				inputmode="numeric"
				@keyup.enter="submit"
			/>
			<FormCheckbox
				v-model="rememberMe"
				:label="$t('user.auth.remember')"
			/>

			<XButton
				id="login-submit"
				class="is-fullwidth"
				:loading="isLoading"
				:aria-busy="isLoading"
				:aria-disabled="isLoading"
				@click="submit"
			>
				{{ $t('user.auth.login') }}
			</XButton>
			<p
				v-if="registrationEnabled"
				class="mbs-2"
			>
				{{ $t('user.auth.noAccountYet') }}
				<RouterLink
					:to="{ name: 'user.register' }"
					type="secondary"
					class="inline-link"
				>
					{{ $t('user.auth.createAccount') }}
				</RouterLink>
			</p>
		</form>
	</div>
</template>

<script setup lang="ts">
import {computed, onBeforeMount, ref} from 'vue'
import {useI18n} from 'vue-i18n'
import {useRouter} from 'vue-router'
import {useDebounceFn} from '@vueuse/core'

import Message from '@/components/misc/Message.vue'
import Password from '@/components/input/Password.vue'
import FormField from '@/components/input/FormField.vue'
import FormCheckbox from '@/components/input/FormCheckbox.vue'
import ErrorSummary from '@/components/misc/ErrorSummary.vue'
import type {IFieldError} from '@/types/IFieldError'
import type {IProvider} from '@/types/IProvider'
import DesktopLogin from '@/views/user/DesktopLogin.vue'

import {getErrorText} from '@/message'
import {redirectToProvider} from '@/helpers/redirectToProvider'
import {useRedirectToLastVisited} from '@/composables/useRedirectToLastVisited'
import {isDesktopApp} from '@/helpers/desktopAuth'
import {
	errorCodeOf,
	httpStatusOf,
	ERROR_EMAIL_NOT_CONFIRMED,
	HTTP_TOO_MANY_REQUESTS,
} from '@/helpers/authErrorCodes'

import {useAuthStore, JUST_LOGGED_OUT_KEY} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'

import {useTitle} from '@/composables/useTitle'

const TOTP_REQUIRED_CODE = 1017

const {t} = useI18n({useScope: 'global'})
useTitle(() => t('user.auth.login'))

const router = useRouter()
const authStore = useAuthStore()
const configStore = useConfigStore()
const {redirectIfSaved} = useRedirectToLastVisited()

const registrationEnabled = computed(() => configStore.auth.local.registrationEnabled)
const localAuthEnabled = computed(() => configStore.auth.local.enabled)
const ldapAuthEnabled = computed(() => configStore.auth.ldap.enabled)

const openidConnect = computed(() => configStore.auth.openidConnect)
const hasOpenIdProviders = computed(() => openidConnect.value.enabled && openidConnect.value.providers?.length > 0)

function isGoogleProvider(provider: IProvider): boolean {
	return provider.name.toLowerCase().includes('google')
}

const isLoading = computed(() => authStore.isLoading)
const isDesktop = isDesktopApp()

const confirmedEmailSuccess = ref(false)
const errorMessage = ref('')
const password = ref('')
const passwordRef = ref<{clear: () => void} | null>(null)
const validatePasswordInitially = ref(false)
const rememberMe = ref(false)

const authenticated = computed(() => authStore.authenticated)

onBeforeMount(() => {
	authStore.verifyEmail().then((confirmed) => {
		confirmedEmailSuccess.value = confirmed
	}).catch((e: Error) => {
		errorMessage.value = e.message
	})

	// Check if the user is already logged in, if so, redirect them to the homepage.
	// We intentionally use router.push here instead of redirectIfSaved() because
	// this hook also fires when Login.vue re-mounts inside the authenticated layout
	// after a successful login. Using redirectIfSaved() here would clear the saved
	// route before the submit() handler gets a chance to use it.
	if (authenticated.value) {
		router.push({name: 'home'})
		return
	}

	// Don't auto-redirect right after an explicit logout, otherwise we'd
	// immediately re-authenticate the user we just logged out.
	if (sessionStorage.getItem(JUST_LOGGED_OUT_KEY)) {
		sessionStorage.removeItem(JUST_LOGGED_OUT_KEY)
		return
	}

	// When the login page offers nothing but a single OIDC provider, skip it
	// and send the user straight there.
	if (
		!localAuthEnabled.value &&
		!ldapAuthEnabled.value &&
		hasOpenIdProviders.value &&
		openidConnect.value.providers.length === 1
	) {
		redirectToProvider(openidConnect.value.providers[0])
	}
})

const usernameValid = ref(true)
const usernameRef = ref<HTMLInputElement | null>(null)
const validateUsernameField = useDebounceFn(() => {
	usernameValid.value = usernameRef.value?.value !== ''
}, 100)

const fieldErrors = computed<IFieldError[]>(() => {
	const errors: IFieldError[] = []
	if (!usernameValid.value) {
		errors.push({
			target: 'username',
			label: t('user.auth.usernameEmail'),
			message: t('user.auth.usernameRequired'),
		})
	}
	if (validatePasswordInitially.value && password.value === '') {
		errors.push({
			target: 'password',
			label: t('user.auth.password'),
			message: t('user.auth.passwordRequired'),
		})
	}
	return errors
})

const needsTotpPasscode = computed(() => authStore.needsTotpPasscode)
const totpPasscode = ref<HTMLInputElement | null>(null)

/**
 * What a rejected sign-in says.
 *
 * ONE MESSAGE FOR A WRONG USERNAME AND A WRONG PASSWORD. The API answers both
 * with the same error code, and nothing here splits them apart again: two
 * messages let anyone holding a list of addresses find out which of them are
 * customers. Percy-Account-Path.md §4.
 */
function rejectionMessage(e: unknown): string {
	// The one place the product may admit an account exists, because it is only
	// reached by someone who has already proved they know the password. Error
	// code 1012 is the only thing that produces it - no other rejection does.
	if (errorCodeOf(e) === ERROR_EMAIL_NOT_CONFIRMED) {
		return t('user.auth.notConfirmedYet')
	}

	// The rate limiter refuses before any handler runs, so its answer carries a
	// status and no error code. Phrased against this browser rather than the
	// account: "this account is locked" would leak exactly what the single
	// rejection message above exists to withhold.
	if (httpStatusOf(e) === HTTP_TOO_MANY_REQUESTS) {
		return t('user.auth.tooManyAttempts')
	}

	return getErrorText(e)
}

async function submit() {
	errorMessage.value = ''
	// Some browsers prevent Vue bindings from working with autofilled values.
	// To work around this, we're manually getting the values here instead of relying on vue bindings.
	// For more info, see https://kolaente.dev/vikunja/frontend/issues/78
	const credentials = {
		username: usernameRef.value?.value,
		password: password.value,
		longToken: rememberMe.value,
	}

	if (credentials.username === '' || credentials.password === '') {
		// Trigger the validation error messages
		validateUsernameField()
		validatePasswordInitially.value = true
		return
	}

	if (needsTotpPasscode.value) {
		credentials.totpPasscode = totpPasscode.value?.value
	}

	try {
		await authStore.login(credentials)
		authStore.setNeedsTotpPasscode(false)

		redirectIfSaved()
	} catch (e) {
		if (errorCodeOf(e) === TOTP_REQUIRED_CODE && !credentials.totpPasscode) {
			// Not a rejection: the form grows a passcode field and the password
			// already typed is submitted again with it.
			return
		}

		// Username kept, password cleared. Percy-Account-Path.md §4: whoever was
		// rejected retypes one field rather than two, and a screen left sitting
		// on a rejection is not holding a password in a visible input.
		passwordRef.value?.clear()

		errorMessage.value = rejectionMessage(e)
	}
}
</script>

<style lang="scss" scoped>
// No horizontal button margin: every button on this screen is now full width
// and stacked, and 0.4rem on the end of a 100% width is 0.4rem of overflow.
.reset-password-link {
	display: inline-block;
}

// Underline links sitting inside body text so they're not distinguished by color alone
.inline-link {
	text-decoration: underline;
}

.label-with-link {
	display: flex;
	justify-content: space-between;
	margin-block-end: .5rem;

	.label {
		margin-block-end: 0;
	}
}

.or-rule {
	display: flex;
	align-items: center;
	gap: .75rem;
	margin-block: 1rem;
	color: var(--grey-500);

	&::before,
	&::after {
		content: "";
		flex: 1;
		border-block-start: 1px solid var(--grey-200);
	}
}

// 44px minimum target size (Percy-Account-Path.md §5). Scoped to this screen
// rather than applied globally, because nothing else here has been measured.
:deep(.input),
:deep(.button) {
	min-block-size: 2.75rem;
}

:deep(.password-field-type-toggle) {
	display: inline-flex;
	align-items: center;
	justify-content: center;
	min-block-size: 2.75rem;
	min-inline-size: 2.75rem;
}

// Inset "pressed into the surface" treatment; :deep() rather than a global
// .input rule since the rest of the app's inputs haven't been measured against it.
:deep(.input) {
	border: 1px solid transparent;
	border-radius: 14px;
	background-color: var(--neumorphic-input-bg);
	box-shadow:
		inset 3px 3px 7px var(--neumorphic-shadow-dark),
		inset -3px -3px 7px var(--neumorphic-shadow-light);

	&:focus {
		border-color: hsla(var(--primary-hsl), 0.4);
		box-shadow:
			inset 2px 2px 5px var(--neumorphic-shadow-dark),
			inset -2px -2px 5px var(--neumorphic-shadow-light),
			0 0 0 3px hsla(var(--primary-hsl), 0.12);
	}
}

// The raised counterpart: buttons sit ABOVE the surface rather than pressed
// into it, so their shadow is a normal (non-inset) soft pair instead.
:deep(.button) {
	border-radius: 14px;
	text-transform: none;
	font-weight: 600;
	font-size: 0.95rem;
}

:deep(.button.is-primary) {
	// A fixed gradient rather than the theme's --primary, in both modes: the
	// mockup's button is a specific blue-to-violet regardless of light/dark,
	// the same way .button's own --button-text-color is pinned white above
	// rather than following the theme.
	background: linear-gradient(135deg, #2563eb, #7c3aed);
	box-shadow: 0 10px 24px hsla(255, 60%, 55%, 0.28);
	border: 0;

	&:hover {
		background: linear-gradient(135deg, #2554d1, #6c2fd4);
	}
}

.oidc-button.button.is-outlined {
	background-color: var(--white);
	border: 1px solid var(--grey-200);
	box-shadow:
		3px 3px 8px var(--neumorphic-shadow-dark),
		-3px -3px 8px var(--neumorphic-shadow-light);
	color: var(--grey-800);

	&:hover {
		border-color: var(--grey-300);
		color: var(--grey-800);
	}
}

.oidc-icon {
	inline-size: 18px;
	block-size: 18px;
	flex: none;
	// Vue strips the whitespace-only text node between this svg and the label
	// that follows it, so the gap has to come from margin instead.
	margin-inline-end: .5rem;
}
</style>
