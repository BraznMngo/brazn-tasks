<template>
	<SideNavShell :navigation-items="navigationItems" />
</template>

<script setup lang="ts">
import {computed} from 'vue'
import {useI18n} from 'vue-i18n'

import {useTitle} from '@/composables/useTitle'
import SideNavShell from '@/components/misc/SideNavShell.vue'

// The Organization area is a SECOND INSTANCE of the shell the user settings
// already use, not a new layout (BRA-921 §3.1). It inherits the fork's
// responsive behaviour, its active-item styling and its stacking breakpoint,
// and adds no layout primitive of its own.
//
// The seven entries are the seven pages, in the order the design lists them:
// Overview first, because it is now the first page an administrator sees.
const {t} = useI18n({useScope: 'global'})
useTitle(() => t('organization.title'))

const navigationItems = computed(() => [
	{title: t('organization.overview.title'), routeName: 'organization.overview'},
	{title: t('organization.members.title'), routeName: 'organization.members'},
	{title: t('organization.seats.title'), routeName: 'organization.seats'},
	{title: t('organization.teams.title'), routeName: 'organization.teams'},
	{title: t('organization.administration.title'), routeName: 'organization.administration'},
	{title: t('organization.general.title'), routeName: 'organization.general'},
	{title: t('organization.billing.title'), routeName: 'organization.billing'},
])
</script>
