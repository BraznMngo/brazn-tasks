import {computed, reactive, toRefs} from 'vue'
import {acceptHMRUpdate, defineStore} from 'pinia'
import {parseURL} from 'ufo'

import {HTTPFactory} from '@/helpers/fetcher'
import {objectToCamelCase} from '@/helpers/case'

import type {IProvider} from '@/types/IProvider'
import type {MIGRATORS} from '@/views/migrate/migrators'
import type {ProFeature} from '@/constants/proFeatures'
import {InvalidApiUrlProvidedError} from '@/helpers/checkAndSetApiUrl'

export interface ConfigState {
	version: string,
	frontendUrl: string,
	motd: string,
	linkSharingEnabled: boolean,
	maxFileSize: string,
	maxItemsPerPage: number,
	availableMigrators: Array<keyof typeof MIGRATORS>,
	taskAttachmentsEnabled: boolean,
	totpEnabled: boolean,
	enabledBackgroundProviders: Array<'unsplash' | 'upload'>,
	legal: {
		imprintUrl: string,
		privacyPolicyUrl: string,
	},
	caldavEnabled: boolean,
	/**
	 * Where the Organization area links out for billing and membership.
	 * Empty on an instance with no commercial service behind it, and an empty
	 * value renders no link rather than a dead one.
	 */
	braznAccountUrl: string,
	/**
	 * Where somebody without an account is sent to create one.
	 *
	 * This is the sign-in screen's answer to the only question a person with no
	 * account can have. On a managed instance this product creates no accounts —
	 * the commercial service does, at checkout — so the registration form here
	 * cannot succeed and offering it is a dead end (BRA-1444).
	 *
	 * Empty means offer nothing, which is correct for a self-hosted instance
	 * whose own registration form is the real answer.
	 */
	braznCheckoutUrl: string,
	/**
	 * Whether account lifecycle on this instance belongs to the commercial
	 * service. When true, this product must not draw password, address,
	 * second-factor or account-deletion controls: the managed gate refuses those
	 * routes for everyone on the instance, including its administrator, so a
	 * form drawn here is one nobody can submit successfully.
	 *
	 * It is read from the server rather than inferred, because a provisioned
	 * account is created with the local issuer and is therefore indistinguishable
	 * from an ordinary local one by any check a browser can make.
	 */
	braznManagedMode: boolean,
	userDeletionEnabled: boolean,
	taskCommentsEnabled: boolean,
	demoModeEnabled: boolean,
	webhooksEnabled: boolean,
	auth: {
		local: {
			enabled: boolean,
			registrationEnabled: boolean,
		},
		ldap: {
			enabled: boolean,
		},
		openidConnect: {
			enabled: boolean,
			redirectUrl: string,
			providers: IProvider[],
		},
	},
	publicTeamsEnabled: boolean,
	allowIconChanges: boolean,
	enabledProFeatures: string[],
	concurrentWrites: boolean,
}

export const useConfigStore = defineStore('config', () => {
	const state: ConfigState = reactive({
		// These are the api defaults.
		version: '',
		frontendUrl: '',
		motd: '',
		linkSharingEnabled: true,
		maxFileSize: '20MB',
		maxItemsPerPage: 50,
		availableMigrators: [],
		taskAttachmentsEnabled: true,
		totpEnabled: true,
		enabledBackgroundProviders: [],
		legal: {
			imprintUrl: '',
			privacyPolicyUrl: '',
		},
		caldavEnabled: false,
		braznAccountUrl: '',
		braznCheckoutUrl: '',
		braznManagedMode: false,
		userDeletionEnabled: true,
		taskCommentsEnabled: true,
		demoModeEnabled: false,
		webhooksEnabled: false,
		auth: {
			local: {
				enabled: true,
				registrationEnabled: true,
			},
			ldap: {
				enabled: false,
			},
			openidConnect: {
				enabled: false,
				redirectUrl: '',
				providers: [],
			},
		},
		publicTeamsEnabled: false,
		allowIconChanges: true,
		enabledProFeatures: [],
		concurrentWrites: false,
	})

	const migratorsEnabled = computed(() => state.availableMigrators?.length > 0)

	/**
	 * Where this instance makes accounts, for the three screens somebody without
	 * one can reach: the sign-in page, the registration address itself, and the
	 * end of a Google round trip that found no account.
	 *
	 * Defined once, here, because those three have to agree. They did not before
	 * BRA-1444: the sign-in page offered a registration form the server refuses,
	 * and the Google callback said accounts are created by subscribing without
	 * saying where that happens.
	 *
	 * `null` means this product makes its own accounts and its own registration
	 * form is the answer — every self-hosted instance, which is why this is read
	 * from the server rather than assumed.
	 */
	const accountCreationUrl = computed<string | null>(() => {
		if (!state.braznManagedMode) {
			return null
		}

		return state.braznCheckoutUrl || null
	})
	const apiBase = computed(() => {
		const {host, protocol, pathname} = parseURL(window.API_URL)

		// Strip the /api/v1 suffix (and optional trailing slash) to get the deployment base.
		const basePath = pathname
			.replace(/\/api\/v1\/?$/, '')
			.replace(/\/+$/, '')
		return `${protocol}//${host}${basePath}`
	})

	function setConfig(config: ConfigState) {
		Object.assign(state, config)
	}

	function isProFeatureEnabled(name: ProFeature): boolean {
		return state.enabledProFeatures?.includes(name) ?? false
	}

	async function update(): Promise<boolean> {
		const HTTP = HTTPFactory()
		const {data: config} = await HTTP.get('info')

		if (typeof config.version === 'undefined') {
			throw new InvalidApiUrlProvidedError()
		}

		setConfig(objectToCamelCase(config) as ConfigState)
		return !!config
	}

	return {
		...toRefs(state),

		migratorsEnabled,
		accountCreationUrl,
		apiBase,
		setConfig,
		isProFeatureEnabled,
		update,
	}

})

// support hot reloading
if (import.meta.hot) {
	import.meta.hot.accept(acceptHMRUpdate(useConfigStore, import.meta.hot))
}
