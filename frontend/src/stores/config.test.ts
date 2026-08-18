import {describe, it, expect, beforeEach} from 'vitest'
import {setActivePinia, createPinia} from 'pinia'
import {computed} from 'vue'

import {objectToCamelCase} from '@/helpers/case'

import {useConfigStore, type ConfigState} from './config'

describe('config store', () => {
	beforeEach(() => {
		setActivePinia(createPinia())
	})

	describe('isProFeatureEnabled', () => {
		it('returns true when the feature is in the enabledProFeatures list', () => {
			const store = useConfigStore()
			store.enabledProFeatures = ['admin_panel']
			expect(store.isProFeatureEnabled('admin_panel')).toBe(true)
		})

		it('returns false for features not present in the list', () => {
			const store = useConfigStore()
			store.enabledProFeatures = ['admin_panel']
			expect(store.isProFeatureEnabled('time_tracking')).toBe(false)
		})

		it('returns false when the list is empty (free mode)', () => {
			const store = useConfigStore()
			store.enabledProFeatures = []
			expect(store.isProFeatureEnabled('admin_panel')).toBe(false)
		})

		it('reacts to store updates when wrapped in computed', () => {
			const store = useConfigStore()
			store.enabledProFeatures = []
			const enabled = computed(() => store.isProFeatureEnabled('admin_panel'))
			expect(enabled.value).toBe(false)
			store.enabledProFeatures = ['admin_panel']
			expect(enabled.value).toBe(true)
		})
	})

	// The routing rule in the router reads this value to decide whether it
	// applies at all, and everything that makes that rule safe rests on which
	// way it falls when the server says nothing. A server too old to publish the
	// field, or a self-hosted copy of this fork, must be read as NOT the hosted
	// product - reading silence the other way confines every signed-in person on
	// such an instance to the sign-in screen while their session is perfectly
	// valid, which is exactly the failure this default exists to prevent.
	//
	// Both cases go through the conversion the real response goes through, so
	// the pair also pins the snake-cased name the server actually sends.
	describe('braznManagedMode', () => {
		// Seeding the store's default as `true` instead of `false` makes exactly
		// this test fail and leaves the control below passing - checked by making
		// that change and running the file. Deleting the default line altogether
		// fails both, because the store publishes the keys its starting state
		// had and a key that arrives later is not among them.
		it('stays false when the server answers without the field', () => {
			const store = useConfigStore()

			store.setConfig(objectToCamelCase({
				version: '1.0.0',
				brazn_account_url: '',
			}) as ConfigState)

			expect(store.braznManagedMode).toBe(false)
		})

		// The control for the test above. Without it, a `braznManagedMode` that
		// no server answer could ever move would pass the first test forever,
		// and the pair would attest a default that is really a dead value.
		it('is true when the server answers that this instance is managed', () => {
			const store = useConfigStore()

			store.setConfig(objectToCamelCase({
				version: '1.0.0',
				brazn_managed_mode: true,
			}) as ConfigState)

			expect(store.braznManagedMode).toBe(true)
		})
	})
})
