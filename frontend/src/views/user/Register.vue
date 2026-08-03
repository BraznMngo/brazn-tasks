<template>
	<div v-if="configStore.auth.local.registrationEnabled">
		<!--
			Two complete ways to finish, in the order and at the weight
			Percy-Account-Path.md §2 sets: Google full width above the form, the
			same height as the submit, and not a footnote under it.
		-->
		<template v-if="hasOpenIdProviders && !registeredAwaitingConfirmation">
			<XButton
				v-for="(p, k) in openidConnect.providers"
				:key="k"
				variant="secondary"
				class="is-fullwidth mbe-2"
				@click="redirectToProvider(p)"
			>
				{{ $t('user.auth.signUpWith', {provider: p.name}) }}
			</XButton>
			<div class="or-rule">
				<span>{{ $t('user.auth.or') }}</span>
			</div>
		</template>

		<!--
			The one screen where saying an account exists is right: whoever typed
			the address just asserted they own it, and refusing to say leaves
			them at a form that will never accept their input. Percy-Account-Path.md
			§2, and BRA-1072 AC7 makes it the single exception to §4's rule.
		-->
		<Message
			v-if="addressAlreadyRegistered"
			variant="danger"
			class="mbe-4"
		>
			{{ $t('user.auth.emailAlreadyRegistered') }}
			<p class="mbs-2">
				<RouterLink
					:to="{ name: 'user.login' }"
					class="inline-link"
				>
					{{ $t('user.auth.login') }}
				</RouterLink>
				<span aria-hidden="true"> · </span>
				<RouterLink
					:to="{ name: 'user.password-reset.request' }"
					class="inline-link"
				>
					{{ $t('user.auth.resetPassword') }}
				</RouterLink>
			</p>
		</Message>
		<Message
			v-else-if="errorMessage !== ''"
			variant="danger"
			class="mbe-4"
		>
			{{ errorMessage }}
		</Message>

		<!--
			The account exists and the sign-in that follows registration was
			refused because it is not confirmed yet - which is not a failure and
			must not be drawn as one. This is a placeholder for the confirmation
			screen BRA-1072 PR 1 adds; when that lands, this block is replaced by
			a push to `user.confirm` and the address is handed to it. Neither the
			route nor the helper that remembers the address exists on this
			branch, so neither is referenced here.
		-->
		<div
			v-if="registeredAwaitingConfirmation"
			class="has-text-centered"
		>
			<Message variant="success">
				{{ $t('user.auth.registeredCheckInbox') }}
			</Message>
			<XButton
				:to="{ name: 'user.login' }"
				class="mbs-4"
			>
				{{ $t('user.auth.login') }}
			</XButton>
		</div>

		<form
			v-else
			id="registerform"
			@submit.prevent="submit"
		>
			<ErrorSummary :errors="fieldErrors" />
			<FormField
				id="username"
				v-model="credentials.username"
				v-focus
				:label="$t('user.auth.username')"
				name="username"
				:placeholder="$t('user.auth.usernamePlaceholder')"
				required
				type="text"
				autocomplete="username"
				aria-describedby="username-hint"
				:error="usernameError"
				@keyup.enter="submit"
				@focusout="validateUsername(); validateUsernameAfterFirst = true"
				@keyup="handleUsernameKeyup"
			/>
			<p
				id="username-hint"
				class="field-hint"
			>
				{{ $t('user.auth.usernameHint') }}
			</p>
			<FormField
				id="email"
				v-model="credentials.email"
				:label="$t('user.auth.email')"
				name="email"
				:placeholder="$t('user.auth.emailPlaceholder')"
				required
				type="email"
				:error="emailError"
				autocomplete="email"
				@keyup.enter="submit"
				@focusout="validateEmail(); validateEmailAfterFirst = true"
				@keyup="handleEmailKeyup"
			/>
			<div class="field">
				<label
					class="label"
					for="password"
				>{{ $t('user.auth.password') }}</label>
				<Password
					:validate-initially="validatePasswordInitially"
					autocomplete="new-password"
					described-by="password-hint"
					@submit="submit"
					@update:modelValue="v => credentials.password = v"
				/>
				<!--
					The minimum is stated before it can be broken, and the error
					string quotes the same number. Register.test.ts pins the two
					against a literal 8 so they cannot drift apart.
				-->
				<p
					id="password-hint"
					class="field-hint"
				>
					{{ $t('user.auth.passwordMinHint') }}
				</p>
				<p
					v-if="passwordError"
					class="help is-danger"
				>
					{{ passwordError }}
				</p>
			</div>

			<XButton
				id="register-submit"
				class="is-fullwidth"
				:loading="isLoading"
				:aria-busy="isLoading"
				:aria-disabled="isLoading || !everythingValid"
				@click="submit"
			>
				{{ $t('user.auth.createAccount') }}
			</XButton>

			<Message
				v-if="configStore.demoModeEnabled"
				variant="warning"
				class="mbs-4"
			>
				{{ $t('demo.title') }}
				{{ $t('demo.accountWillBeDeleted') }}<br>
				<strong class="is-uppercase">{{ $t('demo.everythingWillBeDeleted') }}</strong>
			</Message>

			<p class="mbs-2">
				{{ $t('user.auth.alreadyHaveAnAccount') }}
				<RouterLink
					:to="{ name: 'user.login' }"
					class="inline-link"
				>
					{{ $t('user.auth.login') }}
				</RouterLink>
			</p>
		</form>
	</div>
	<Message
		v-else
		variant="warning"
	>
		{{ $t('user.auth.registrationDisabled') }}
	</Message>
</template>

<script setup lang="ts">
import {useDebounceFn} from '@vueuse/core'
import {computed, onBeforeMount, reactive, ref, toRaw} from 'vue'
import {useI18n} from 'vue-i18n'

import router from '@/router'
import Message from '@/components/misc/Message.vue'
import ErrorSummary from '@/components/misc/ErrorSummary.vue'
import type {IFieldError} from '@/types/IFieldError'
import {isEmail} from '@/helpers/isEmail'
import Password from '@/components/input/Password.vue'
import FormField from '@/components/input/FormField.vue'
import {parseValidationErrors, type ValidationError} from '@/helpers/parseValidationErrors'

import {useRedirectToLastVisited} from '@/composables/useRedirectToLastVisited'
import {useAuthStore} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'
import {validatePassword} from '@/helpers/validatePasswort'
import {clearSignupToken, readSignupTokenFromFragment} from '@/helpers/signupToken'
import {redirectToProvider} from '@/helpers/redirectToProvider'
import {
	errorCodeOf,
	ERROR_EMAIL_EXISTS,
	ERROR_EMAIL_NOT_CONFIRMED,
	ERROR_USERNAME_EXISTS,
} from '@/helpers/authErrorCodes'

const {t} = useI18n()
const authStore = useAuthStore()
const configStore = useConfigStore()
const {redirectIfSaved} = useRedirectToLastVisited()

const openidConnect = computed(() => configStore.auth.openidConnect)
const hasOpenIdProviders = computed(() => openidConnect.value.enabled && openidConnect.value.providers?.length > 0)

// FIXME: use the `beforeEnter` hook of vue-router
// Check if the user is already logged in, if so, redirect them to the homepage
onBeforeMount(() => {
	// Read the signup token out of the URL fragment and clear it from the
	// address bar before anything else happens on this page. It is remembered
	// for this tab, so it also survives the round trip if they choose Google
	// instead of filling the form in.
	readSignupTokenFromFragment()

	if (authStore.authenticated) {
		router.push({name: 'home'})
	}
})

const credentials = reactive({
	username: '',
	email: '',
	password: '',
})

const isLoading = computed(() => authStore.isLoading)
const errorMessage = ref('')
const addressAlreadyRegistered = ref(false)
const registeredAwaitingConfirmation = ref(false)
const validatePasswordInitially = ref(false)
const serverValidationErrors = ref<Partial<Record<string, string>>>({})

const DEBOUNCE_TIME = 100

// debouncing to prevent error messages when clicking on the log in button
const emailValid = ref(true)
const validateEmailAfterFirst = ref(false)
const validateEmail = useDebounceFn(() => {
	emailValid.value = isEmail(credentials.email)
}, DEBOUNCE_TIME)

const usernameValid = ref<true | string>(true)
const validateUsernameAfterFirst = ref(false)
const validateUsername = useDebounceFn(() => {
	if (credentials.username === '') {
		usernameValid.value = t('user.auth.usernameRequired')
		return
	}

	if (credentials.username.indexOf(' ') !== -1) {
		usernameValid.value = t('user.auth.usernameMustNotContainSpace')
		return
	}

	if (credentials.username.indexOf('://') !== -1 || credentials.username.indexOf('.') !== -1) {
		usernameValid.value = t('user.auth.usernameMustNotLookLikeUrl')
		return
	}

	usernameValid.value = true
}, DEBOUNCE_TIME)

const everythingValid = computed(() => {
	return credentials.username !== '' &&
		credentials.email !== '' &&
		validatePassword(credentials.password) === true &&
		emailValid.value &&
		usernameValid.value === true
})

const usernameError = computed(() => {
	// Client-side validation takes priority
	if (usernameValid.value !== true) {
		return usernameValid.value
	}
	// Show server-side error if present
	return serverValidationErrors.value.username || null
})

const emailError = computed(() => {
	// Client-side validation takes priority
	if (!emailValid.value) {
		return t('user.auth.emailInvalid')
	}
	// Show server-side error if present
	return serverValidationErrors.value.email || null
})

const passwordError = computed(() => {
	// Show server-side error if present
	return serverValidationErrors.value.password || null
})

// What the summary says about the password. Wider than `passwordError`, which
// is only what is rendered under the field: Password.vue draws its own
// client-side message, so repeating it there would say it twice, but leaving it
// out of the summary would list a form as valid while it is not.
const passwordSummaryError = computed(() => {
	if (passwordError.value) {
		return passwordError.value
	}
	if (!validatePasswordInitially.value) {
		return null
	}
	const valid = validatePassword(credentials.password)
	return valid === true ? null : t(valid)
})

const fieldErrors = computed<IFieldError[]>(() => {
	const errors: IFieldError[] = []
	if (usernameError.value) {
		errors.push({target: 'username', label: t('user.auth.username'), message: usernameError.value})
	}
	if (emailError.value) {
		errors.push({target: 'email', label: t('user.auth.email'), message: emailError.value})
	}
	if (passwordSummaryError.value) {
		errors.push({target: 'password', label: t('user.auth.password'), message: passwordSummaryError.value})
	}
	return errors
})

function handleUsernameKeyup() {
	if (validateUsernameAfterFirst.value) {
		validateUsername()
	}
	delete serverValidationErrors.value.username
}

function handleEmailKeyup() {
	if (validateEmailAfterFirst.value) {
		validateEmail()
	}
	delete serverValidationErrors.value.email
}

function isApiValidationError(error: unknown): error is ValidationError {
	return error !== null &&
		typeof error === 'object' &&
		'invalid_fields' in error
}

async function submit() {
	errorMessage.value = ''
	addressAlreadyRegistered.value = false
	serverValidationErrors.value = {}
	validatePasswordInitially.value = true

	// Every field is validated on submit and not only on focusout, so pressing
	// the button on an untouched form produces the summary rather than nothing
	// at all. This is why the button is `aria-disabled` and not `disabled`: a
	// disabled control cannot be pressed, so it can never tell anyone why.
	validateUsername()
	validateUsernameAfterFirst.value = true
	validateEmail()
	validateEmailAfterFirst.value = true

	if (!everythingValid.value) {
		return
	}

	try {
		await authStore.register(toRaw(credentials))
		// The token is consumed server-side by a successful registration, so
		// holding onto it would only offer a spent value to the next attempt.
		clearSignupToken()
		redirectIfSaved()
	} catch (e: unknown) {
		const code = errorCodeOf(e)

		// The account was created; what was refused is the sign-in that follows
		// it, because the server does not sign in an unconfirmed account. That
		// is not a registration failure and drawing it as one leaves a customer
		// staring at an error for something that worked. The token is spent
		// server-side by the registration that just succeeded, so it is cleared
		// here too - keeping it would offer a dead value to the next attempt.
		if (code === ERROR_EMAIL_NOT_CONFIRMED) {
			clearSignupToken()
			registeredAwaitingConfirmation.value = true
			return
		}

		if (code === ERROR_EMAIL_EXISTS) {
			addressAlreadyRegistered.value = true
			return
		}

		if (code === ERROR_USERNAME_EXISTS) {
			serverValidationErrors.value = {username: t('error.1001')}
			return
		}

		// Parse field-specific validation errors
		if (isApiValidationError(e)) {
			const parsed = parseValidationErrors(e)

			if (Object.keys(parsed).length > 0) {
				// Apply field-level errors (computed properties will display them)
				serverValidationErrors.value = parsed
			} else {
				// Fallback to general error message if no field errors
				errorMessage.value = t('user.auth.registrationFailed')
			}
		} else if (e instanceof Object && 'message' in e && typeof e.message === 'string') {
			// Non-validation backend errors - show their message
			errorMessage.value = e.message
		} else {
			errorMessage.value = t('user.auth.registrationFailed')
		}
	}
}
</script>

<style lang="scss" scoped>
// Underline links sitting inside body text so they're not distinguished by color alone
.inline-link {
	text-decoration: underline;
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

.field-hint {
	margin-block: -.5rem .75rem;
	color: var(--grey-500);
	font-size: .85rem;
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
</style>
