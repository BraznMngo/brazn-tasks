import {computed, toValue} from 'vue'

import {useTitle as useTitleVueUse, type UseTitleOptions, type ReadonlyRefOrGetter, type MaybeRef, type MaybeRefOrGetter} from '@vueuse/core'

export function useTitle(
	newTitle:
		| ReadonlyRefOrGetter<string | null | undefined>
		| MaybeRef<string | null | undefined>
		| MaybeRefOrGetter<string | null | undefined> = null,
	options?: UseTitleOptions,
) {
	const pageTitle = computed(() => toValue(newTitle))

	// The product is ONE. "Brazn Tasks" is the name of the fork this is built
	// from, and it belongs in the source offer in the footer, which is a
	// statement about the software — not in the browser tab, which is a
	// statement about the product (BRA-1444).
	const completeTitle = computed(() =>
		(typeof pageTitle.value === 'undefined' || pageTitle.value === '')
			? 'ONE'
			: `${pageTitle.value} | ONE`,
	)

	return useTitleVueUse(completeTitle, options)
}
