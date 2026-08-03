<script setup lang="ts">
import {nextTick, ref, watch} from 'vue'

import type {IFieldError} from '@/types/IFieldError'

const props = defineProps<{errors: IFieldError[]}>()

const root = ref<HTMLDivElement | null>(null)

// A state change moves focus to the screen just reached (Percy-Account-Path.md
// §5): when the summary appears, focus lands on it rather than staying wherever
// the submit button was. Only on the transition into an error state — refocusing
// on every keystroke that changes an error would trap the cursor here.
watch(
	() => props.errors.length > 0,
	async (showing, wasShowing) => {
		if (showing && !wasShowing) {
			await nextTick()
			root.value?.focus()
		}
	},
)

// The entries are real in-page links, so they work by their href alone. The
// handler only adds focus: an href jump scrolls the field into view but leaves
// the caret where it was, which is the half of "go to this field" that matters
// to somebody using a keyboard.
function focusField(event: MouseEvent, target: string) {
	const el = document.getElementById(target)
	if (el === null) {
		return
	}
	event.preventDefault()
	el.focus()
	el.scrollIntoView({block: 'center'})
}
</script>

<template>
	<div
		v-if="errors.length > 0"
		ref="root"
		class="error-summary mbe-4"
		role="alert"
		tabindex="-1"
	>
		<p class="error-summary-title">
			{{ $t('user.auth.errorSummaryTitle') }}
		</p>
		<ul class="error-summary-list">
			<li
				v-for="error in errors"
				:key="error.target"
			>
				<a
					:href="'#' + error.target"
					class="error-summary-link"
					@click="event => focusField(event, error.target)"
				>{{ error.label }}: {{ error.message }}</a>
			</li>
		</ul>
	</div>
</template>

<style lang="scss" scoped>
.error-summary {
	border: 2px solid var(--danger);
	border-radius: $radius;
	padding: .75rem 1rem;
	background: hsla(var(--danger-h), var(--danger-s), var(--danger-l), .05);
}

.error-summary-title {
	font-weight: bold;
	margin-block-end: .5rem;
}

.error-summary-list {
	margin: 0;
	padding-inline-start: 1.25rem;
	list-style: disc;
}

// Underlined, not coloured: the error state must never be carried by colour
// alone (Percy-Account-Path.md §5).
.error-summary-link {
	color: var(--danger);
	text-decoration: underline;
}
</style>
