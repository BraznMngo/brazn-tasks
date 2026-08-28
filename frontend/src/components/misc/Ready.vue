<template>
	<!-- This is a workaround to get the sw to "see" the to-be-cached version of the offline background image -->
	<div
		class="offline"
		style="height: 0;width: 0;"
	/>
	<div
		v-if="!online"
		class="app offline"
	>
		<div class="offline-message">
			<h1 class="title">
				{{ $t('offline.title') }}
			</h1>
			<p>{{ $t('offline.text') }}</p>
		</div>
	</div>
	<template v-else-if="baseStore.ready">
		<slot />
	</template>
	<section v-else-if="baseStore.error !== ''">
		<NoAuthWrapper :show-subtitle="false">
			<p v-if="baseStore.error === ERROR_NO_API_URL">
				{{ $t('ready.noApiUrlConfigured') }}
			</p>
			<Message
				v-else
				variant="danger"
				class="mbe-4"
			>
				<p>
					{{ $t('ready.errorOccured') }}<br>
					{{ baseStore.error }}
				</p>
				<p>
					{{ $t('ready.checkApiUrl') }}
				</p>
			</Message>
			<ApiConfig
				:configure-open="true"
				@foundApi="baseStore.loadApp()"
			/>
		</NoAuthWrapper>
	</section>
	<CustomTransition name="fade">
		<section
			v-if="baseStore.loading"
			class="vikunja-loading"
		>
			<!--
				The real ONE logo, the same pair the sign-in screens, the signed-in
				header and the ONE pages use. What stood here was the placeholder
				"BT" app mark, which BRA-1444 replaced on this splash first because
				it is the first thing a customer sees; the header followed, and the
				placeholder files are gone.
			-->
			<img
				class="logo light"
				src="/one/logo-light.v1.png"
				width="155"
				height="72"
				:alt="$t('misc.brandName')"
			>
			<img
				class="logo dark"
				src="/one/logo-dark.v1.png"
				width="155"
				height="72"
				:alt="$t('misc.brandName')"
			>
			<p>
				<span class="loader-container is-loading-small is-loading" />
				{{ $t('ready.loading') }}
			</p>
		</section>
	</CustomTransition>
</template>

<script lang="ts" setup>
import ApiConfig from '@/components/misc/ApiConfig.vue'
import Message from '@/components/misc/Message.vue'
import CustomTransition from '@/components/misc/CustomTransition.vue'
import NoAuthWrapper from '@/components/misc/NoAuthWrapper.vue'

import {ERROR_NO_API_URL} from '@/helpers/checkAndSetApiUrl'

import {useOnline} from '@/composables/useOnline'
import {useBaseStore} from '@/stores/base'

const online = useOnline()
const baseStore = useBaseStore()
</script>

<style lang="scss" scoped>
// stylelint-disable no-invalid-position-declaration

.vikunja-loading {
	display: flex;
	justify-content: center;
	align-items: center;
	block-size: 100vh;
	inline-size: 100vw;
	flex-direction: column;
	position: fixed;
	inset-block-start: 0;
	inset-inline-start: 0;
	inset-block-end: 0;
	inset-inline-end: 0;
	background: var(--grey-100);
	z-index: 99;
}

.logo {
	margin-block-end: 1rem;
	inline-size: 132px;
	block-size: auto;
}

// The theme-paired pair, same three-state selectors as NoAuthWrapper and
// frontend/public/one/one.css: prefers-color-scheme alone cannot see the
// `.dark` class the palette also switches on.
.logo.dark {
	display: none;
}

@media screen {
	:root.dark .logo.light {
		display: none;
	}

	:root.dark .logo.dark {
		display: inline-block;
	}
}

@media screen and (prefers-color-scheme: dark) {
	:root:not(.light) .logo.light {
		display: none;
	}

	:root:not(.light) .logo.dark {
		display: inline-block;
	}
}

.loader-container {
	margin-inline-end: 1rem;

	&.is-loading::after {
		border-inline-start-color: var(--grey-400);
		border-block-end-color: var(--grey-400);
	}
}

// Deliberately a flat colour, not artwork: the upstream llama photo was the
// Vikunja mascot and is not ours to ship. Kept dark in both colour schemes so
// the white .offline-message text below stays readable.
.offline {
	background: hsl(215deg 27.9% 16.9%);
	block-size: 100vh;
}

.offline-message {
	text-align: center;
	position: absolute;
	inline-size: 100vw;
	inset-block-end: 5vh;
	color: $white;
	padding: 0 1rem;
}

.title {
	text-align: center;
	color: $white;
	font-weight: 700 !important;
	font-size: 1.5rem;
}
</style>
