<template>
	<div class="password-field">
		<input
			id="password"
			ref="inputRef"
			class="input"
			name="password"
			:placeholder="$t('user.auth.passwordPlaceholder')"
			required
			:type="passwordFieldType"
			:autocomplete="autocomplete"
			:aria-invalid="isValid !== true ? true : undefined"
			:aria-describedby="describedByIds"
			@keyup.enter="e => $emit('submit', e)"
			@focusout="() => {validate(); validateAfterFirst = true}"
			@keyup="() => {validateAfterFirst ? validate() : null}"
			@input="handleInput"
		>
		<BaseButton
			v-tooltip="passwordFieldType === 'password' ? $t('user.auth.showPassword') : $t('user.auth.hidePassword')"
			class="password-field-type-toggle"
			:aria-label="passwordFieldType === 'password' ? $t('user.auth.showPassword') : $t('user.auth.hidePassword')"
			@click="togglePasswordFieldType"
		>
			<Icon :icon="passwordFieldType === 'password' ? 'eye' : 'eye-slash'" />
		</BaseButton>
	</div>
	<p
		v-if="isValid !== true"
		:id="errorId"
		class="help is-danger"
		role="alert"
	>
		{{ isValid }}
	</p>
</template>

<script lang="ts" setup>
import {computed, ref, watchEffect} from 'vue'
import {useDebounceFn} from '@vueuse/core'
import {useI18n} from 'vue-i18n'
import BaseButton from '@/components/base/BaseButton.vue'
import {validatePassword} from '@/helpers/validatePasswort'

const props = withDefaults(defineProps<{
	modelValue: string,
	// This prop is a workaround to trigger validation from the outside when the user never had focus in the input.
	validateInitially?: boolean,
	validateMinLength?: boolean,
	autocomplete?: string,
	// The id of a hint element outside this component that describes the field,
	// e.g. the "at least 8 characters" line. Announced alongside the error
	// rather than instead of it.
	describedBy?: string,
}>(), {
	validateMinLength: true,
	autocomplete: 'current-password',
	describedBy: '',
})

const emit = defineEmits<{
	'update:modelValue': [value: string],
	'submit': [event: Event],
}>()
const {t} = useI18n()
const passwordFieldType = ref('password')
const password = ref('')
// eslint-disable-next-line vue/no-setup-props-reactivity-loss
const isValid = ref<true | string>(props.validateInitially === true ? true : '')
const validateAfterFirst = ref(false)
const inputRef = ref<HTMLInputElement | null>(null)
const errorId = computed(() => isValid.value !== true ? 'password-error' : undefined)
const describedByIds = computed(() => {
	const ids = [props.describedBy, errorId.value].filter(id => id !== '' && id !== undefined)
	return ids.length > 0 ? ids.join(' ') : undefined
})

const validate = useDebounceFn(() => {
	const valid = validatePassword(password.value, props.validateMinLength)
	isValid.value = valid === true ? true : t(valid)
}, 100)

watchEffect(() => props.validateInitially && validate())

function togglePasswordFieldType() {
	passwordFieldType.value = passwordFieldType.value === 'password'
		? 'text'
		: 'password'
}

function handleInput(e: Event) {
	password.value = (e.target as HTMLInputElement)?.value
	emit('update:modelValue', password.value)
}

// The input is deliberately uncontrolled - see the autofill note on the parent
// components - so a parent cannot empty it by clearing its own model. Sign-in
// clears the password on a rejection and keeps the username, which needs this.
defineExpose({
	clear() {
		password.value = ''
		if (inputRef.value !== null) {
			inputRef.value.value = ''
		}
		emit('update:modelValue', '')
	},
})
</script>

<style scoped>
.password-field {
	position: relative;
}

.password-field-type-toggle {
	position: absolute;
	color: var(--grey-400);
	inset-block-start: 50%;
	inset-inline-end: 1rem;
	transform: translateY(-50%);
}
</style>
