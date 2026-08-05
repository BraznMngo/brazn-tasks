<template>
	<Message
		v-if="managedMode"
		variant="info"
	>
		<p class="has-text-weight-bold">
			{{ $t('user.settings.managedElsewhere.title') }}
		</p>
		<p>{{ $t('user.settings.managedElsewhere.text') }}</p>
		<XButton
			v-if="commercialUrl"
			variant="secondary"
			:href="commercialUrl"
		>
			{{ $t('user.settings.managedElsewhere.action') }}
		</XButton>
	</Message>
</template>

<script setup lang="ts">
import {computed} from 'vue'

import Message from '@/components/misc/Message.vue'
import {useConfigStore} from '@/stores/config'
import {useCommercialUrl} from '@/composables/useCommercialUrl'

/**
 * What the password, address and second-factor screens say once they have
 * stopped drawing a form nobody could submit.
 *
 * ONE component for the three of them, so the explanation is worded once. It is
 * deliberately silent when managed mode is off: the other reason those screens
 * render nothing is an account that signs in through an identity provider, and
 * telling that person their subscription handles it would be false. Their screen
 * stays exactly as it was.
 */
const configStore = useConfigStore()

const managedMode = computed(() => configStore.braznManagedMode)
const commercialUrl = useCommercialUrl()
</script>
