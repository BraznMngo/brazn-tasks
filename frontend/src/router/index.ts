import { createRouter, createWebHistory } from 'vue-router'
import type { RouteLocation } from 'vue-router'
import {saveLastVisited} from '@/helpers/saveLastVisited'

import {getProjectViewId} from '@/helpers/projectView'
import {parseDateOrString} from '@/helpers/time/parseDateOrString'
import {getNextWeekDate} from '@/helpers/time/getNextWeekDate'
import {LINK_SHARE_HASH_PREFIX} from '@/constants/linkShareHash'
import {REDIRECT_HASH_PREFIX} from '@/constants/redirectHash'
import {AUTH_ROUTE_NAMES} from '@/constants/authRouteNames'
import {PRO_FEATURE} from '@/constants/proFeatures'

import {useAuthStore} from '@/stores/auth'
import {useBaseStore} from '@/stores/base'
import {useConfigStore} from '@/stores/config'
import {useOrganizationStore} from '@/stores/organization'
import {useManagedCapabilities} from '@/composables/useManagedCapabilities'

import Login from '@/views/user/Login.vue'
import Register from '@/views/user/Register.vue'
import LinkSharingAuth from '@/views/sharing/LinkSharingAuth.vue'
import OpenIdAuth from '@/views/user/OpenIdAuth.vue'
import UpcomingTasks from '@/views/tasks/ShowTasks.vue'

import NotFoundComponent from '@/views/404.vue'

const router = createRouter({
	history: createWebHistory(import.meta.env.BASE_URL),
	scrollBehavior(to, from, savedPosition) {
		// If the user is using their forward/backward keys to navigate, we want to restore the scroll view
		if (savedPosition) {
			return savedPosition
		}

		// Scroll to anchor should still work
		if (to.hash && !to.hash.startsWith(LINK_SHARE_HASH_PREFIX) && !to.hash.startsWith(REDIRECT_HASH_PREFIX)) {
			return {el: to.hash}
		}

		// Otherwise just scroll to the top
		return {
			'inset-inline-start': 0,
			'inset-block-start': 0,
		}
	},
	routes: [
		{
			path: '/',
			name: 'home',
			component: () => import('@/views/Home.vue'),
		},
		{
			path: '/:pathMatch(.*)*',
			name: 'not-found',
			component: NotFoundComponent,
		},
		// if you omit the last `*`, the `/` character in params will be encoded when resolving or pushing
		{
			path: '/:pathMatch(.*)',
			name: 'bad-not-found',
			component: NotFoundComponent,
		},
		{
			path: '/login',
			name: 'user.login',
			component: Login,
			meta: {
				title: 'user.auth.login',
				subtitle: 'user.auth.loginSubtitle',
			},
		},
		{
			path: '/get-password-reset',
			name: 'user.password-reset.request',
			component: () => import('@/views/user/RequestPasswordReset.vue'),
			meta: {
				title: 'user.auth.resetPassword',
			},
		},
		{
			path: '/password-reset',
			name: 'user.password-reset.reset',
			component: () => import('@/views/user/PasswordReset.vue'),
			meta: {
				title: 'user.auth.resetPassword',
			},
		},
		// Email confirmation (BRA-1072, docs/Percy-Account-Path.md §3).
		//
		// Before this existed the confirmation link was swallowed by the guard
		// below, stashed in localStorage and turned into a redirect to the
		// login form - which can say "confirmed" and nothing else. It could not
		// say that a link had run out and here is another one, or that one had
		// already been used and that is fine, and those are the two answers
		// that decide whether somebody who mistyped an address ever recovers.
		{
			path: '/confirm',
			name: 'user.confirm',
			component: () => import('@/views/user/ConfirmEmail.vue'),
			meta: {
				title: 'user.confirm.title',
			},
		},
		{
			path: '/register',
			name: 'user.register',
			// FIXME: use dynamic imports
			// component: () => import('@/views/user/Register.vue'),
			component: Register,
			meta: {
				title: 'user.auth.createAccount',
			},
		},
		// The Organization area (BRA-917). `requiresOrganizationAdmin` is checked
		// in the navigation guard below, and what it checks is the SERVER's
		// answer: the guard loads the organization and refuses when the server
		// will not return one. Nothing here reads a local role.
		//
		// This is the discovery half of AC1 and it is not the enforcement.
		// Every one of these views calls an API route that refuses a
		// non-administrator on its own, so a member who defeats the guard by
		// any means reaches seven pages that show them nothing.
		{
			path: '/organization',
			name: 'organization',
			component: () => import('@/views/organization/OrganizationSettings.vue'),
			redirect: {name: 'organization.overview'},
			meta: {requiresOrganizationAdmin: true},
			children: [
				{
					path: '/organization/overview',
					name: 'organization.overview',
					component: () => import('@/views/organization/settings/OrganizationOverview.vue'),
				},
				{
					path: '/organization/members',
					name: 'organization.members',
					component: () => import('@/views/organization/settings/OrganizationMembers.vue'),
				},
				{
					path: '/organization/seats',
					name: 'organization.seats',
					component: () => import('@/views/organization/settings/OrganizationSeats.vue'),
				},
				{
					path: '/organization/teams',
					name: 'organization.teams',
					component: () => import('@/views/organization/settings/OrganizationTeams.vue'),
				},
				{
					path: '/organization/administration',
					name: 'organization.administration',
					component: () => import('@/views/organization/settings/OrganizationAdministration.vue'),
				},
				{
					path: '/organization/general',
					name: 'organization.general',
					component: () => import('@/views/organization/settings/OrganizationGeneral.vue'),
				},
				{
					path: '/organization/billing',
					name: 'organization.billing',
					component: () => import('@/views/organization/settings/OrganizationBilling.vue'),
				},
			],
		},
		{
			path: '/user/settings',
			name: 'user.settings',
			component: () => import('@/views/user/Settings.vue'),
			redirect: {name: 'user.settings.general'},
			children: [
				{
					path: '/user/settings/avatar',
					name: 'user.settings.avatar',
					component: () => import('@/views/user/settings/Avatar.vue'),
				},
				{
					path: '/user/settings/caldav',
					name: 'user.settings.caldav',
					component: () => import('@/views/user/settings/Caldav.vue'),
					beforeEnter: async () => {
						const {useConfigStore} = await import('@/stores/config')
						if (!useConfigStore().caldavEnabled) {
							return {name: 'user.settings.general'}
						}
					},
				},
				{
					path: '/user/settings/data-export',
					name: 'user.settings.data-export',
					component: () => import('@/views/user/settings/DataExport.vue'),
				},
				{
					path: '/user/settings/feeds',
					name: 'user.settings.feeds',
					component: () => import('@/views/user/settings/AtomFeed.vue'),
				},
				{
					path: '/user/settings/deletion',
					name: 'user.settings.deletion',
					component: () => import('@/views/user/settings/Deletion.vue'),
				},
				{
					path: '/user/settings/email-update',
					name: 'user.settings.email-update',
					component: () => import('@/views/user/settings/EmailUpdate.vue'),
				},
				{
					path: '/user/settings/general',
					name: 'user.settings.general',
					component: () => import('@/views/user/settings/General.vue'),
				},
				{
					path: '/user/settings/password-update',
					name: 'user.settings.password-update',
					component: () => import('@/views/user/settings/PasswordUpdate.vue'),
				},
				{
					path: '/user/settings/totp',
					name: 'user.settings.totp',
					component: () => import('@/views/user/settings/TOTP.vue'),
					beforeEnter: async () => {
						const {useConfigStore} = await import('@/stores/config')
						if (!useConfigStore().totpEnabled || !useAuthStore().info?.isLocalUser) {
							return {name: 'user.settings.general'}
						}
					},
				},
				{
					path: '/user/settings/api-tokens',
					name: 'user.settings.apiTokens',
					component: () => import('@/views/user/settings/ApiTokens.vue'),
				},
				{
					path: '/user/settings/sessions',
					name: 'user.settings.sessions',
					component: () => import('@/views/user/settings/Sessions.vue'),
				},
				{
					path: '/user/settings/webhooks',
					name: 'user.settings.webhooks',
					component: () => import('@/views/user/settings/Webhooks.vue'),
				},
				{
					path: '/user/settings/bots',
					name: 'user.settings.bots',
					component: () => import('@/views/user/settings/BotUsers.vue'),
				},
				{
					path: '/user/settings/migrate',
					name: 'migrate.start',
					component: () => import('@/views/migrate/Migration.vue'),
				},
				{
					path: '/migrate/csv',
					name: 'migrate.csv',
					component: () => import('@/views/migrate/MigrationCSV.vue'),
				},
				{
					path: '/migrate/:service',
					name: 'migrate.service',
					component: () => import('@/views/migrate/MigrationHandler.vue'),
					props: route => ({
						service: route.params.service as string,
						code: route.query.code as string,
					}),
				},
			],
		},
		{
			path: '/user/export/download',
			name: 'user.export.download',
			component: () => import('@/views/user/DataExportDownload.vue'),
		},
		{
			path: '/share/:share/auth',
			name: 'link-share.auth',
			// FIXME: use dynamic imports
			// component: () => import('@/views/sharing/LinkSharingAuth.vue'),
			component: LinkSharingAuth,
		},
		{
			path: '/tasks/:id',
			name: 'task.detail',
			component: () => import('@/views/tasks/TaskDetailView.vue'),
			props: route => ({ taskId: Number(route.params.id as string) }),
		},
		{
			path: '/tasks/by/upcoming',
			name: 'tasks.range',
			component: UpcomingTasks,
			props: route => ({
				dateFrom: parseDateOrString(route.query.from as string, new Date()),
				dateTo: parseDateOrString(route.query.to as string, getNextWeekDate()),
				showNulls: route.query.showNulls === 'true',
				showOverdue: route.query.showOverdue === 'true',
			}),
		},
		{
			// Redirect old list routes to the respective project routes
			// see: https://router.vuejs.org/guide/essentials/dynamic-matching.html#catch-all-404-not-found-route
			path: '/lists:pathMatch(.*)*',
			name: 'lists',
			redirect(to) {
				return {
					path: to.path.replace('/lists', '/projects'),
					query: to.query,
					hash: to.hash,
				}
			},
		},
		{
			path: '/projects',
			name: 'projects.index',
			component: () => import('@/views/project/ListProjects.vue'),
		},
		{
			path: '/projects/new',
			name: 'project.create',
			component: () => import('@/views/project/NewProject.vue'),
			meta: {
				showAsModal: true,
				requiresProjectCreateCapability: true,
			},
		},
		{
			path: '/projects/:parentProjectId/new',
			name: 'project.createFromParent',
			component: () => import('@/views/project/NewProject.vue'),
			props: route => ({ parentProjectId: Number(route.params.parentProjectId as string) }),
			meta: {
				showAsModal: true,
				requiresProjectCreateCapability: true,
			},
		},
		{
			path: '/projects/:projectId/settings/edit',
			name: 'project.settings.edit',
			component: () => import('@/views/project/settings/ProjectSettingsEdit.vue'),
			props: route => ({ projectId: Number(route.params.projectId as string) }),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/background',
			name: 'project.settings.background',
			component: () => import('@/views/project/settings/ProjectSettingsBackground.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/duplicate',
			name: 'project.settings.duplicate',
			component: () => import('@/views/project/settings/ProjectSettingsDuplicate.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/share',
			name: 'project.settings.share',
			component: () => import('@/views/project/settings/ProjectSettingsShare.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/webhooks',
			name: 'project.settings.webhooks',
			component: () => import('@/views/project/settings/ProjectSettingsWebhooks.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/delete',
			name: 'project.settings.delete',
			component: () => import('@/views/project/settings/ProjectSettingsDelete.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/archive',
			name: 'project.settings.archive',
			component: () => import('@/views/project/settings/ProjectSettingsArchive.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/projects/:projectId/settings/views',
			name: 'project.settings.views',
			component: () =>  import('@/views/project/settings/ProjectSettingsViews.vue'),
			meta: {
				showAsModal: true,
			},
			props: route => ({ projectId: Number(route.params.projectId as string) }),
		},
		{
			path: '/projects/:projectId/settings/edit',
			name: 'filter.settings.edit',
			component: () => import('@/views/filters/FilterEdit.vue'),
			meta: {
				showAsModal: true,
			},
			props: route => ({ projectId: Number(route.params.projectId as string) }),
		},
		{
			path: '/projects/:projectId/settings/delete',
			name: 'filter.settings.delete',
			component: () => import('@/views/filters/FilterDelete.vue'),
			meta: {
				showAsModal: true,
			},
			props: route => ({ projectId: Number(route.params.projectId as string) }),
		},
		{
			path: '/projects/:projectId/info',
			name: 'project.info',
			component: () => import('@/views/project/ProjectInfo.vue')			,
			meta: {
				showAsModal: true,
			},
			props: route => ({ projectId: Number(route.params.projectId as string) }),
		},
		{
			path: '/projects/:projectId',
			name: 'project.index',
			redirect(to) {
				const viewId = getProjectViewId(Number(to.params.projectId as string))

				if (viewId) {
					console.debug('Replaced list view with', viewId)
				}

				return {
					name: 'project.view',
					params: {
						projectId: parseInt(to.params.projectId as string),
						viewId: viewId ?? 0,
					},
				}
			},
		},
		{
			path: '/projects/:projectId/:viewId',
			name: 'project.view',
			component: () => import('@/views/project/ProjectView.vue'),
			props: route => ({ 
				projectId: parseInt(route.params.projectId as string),
				viewId: route.params.viewId ? parseInt(route.params.viewId as string): undefined,
			}),
		},
		{
			path: '/teams',
			name: 'teams.index',
			component: () => import('@/views/teams/ListTeams.vue'),
		},
		{
			path: '/teams/new',
			name: 'teams.create',
			component: () =>  import('@/views/teams/NewTeam.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/teams/:id/edit',
			name: 'teams.edit',
			component: () => import('@/views/teams/EditTeam.vue'),
		},
		{
			path: '/labels',
			name: 'labels.index',
			component: () => import('@/views/labels/ListLabels.vue'),
		},
		{
			path: '/labels/new',
			name: 'labels.create',
			component: () => import('@/views/labels/NewLabel.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/filters/new',
			name: 'filters.create',
			component: () => import('@/views/filters/FilterNew.vue'),
			meta: {
				showAsModal: true,
			},
		},
		{
			path: '/auth/openid/:provider',
			name: 'openid.auth',
			component: OpenIdAuth,
		},
		{
			path: '/oauth/authorize',
			name: 'oauth.authorize',
			component: () => import('@/views/user/OAuthAuthorize.vue'),
		},
		{
			path: '/about',
			name: 'about',
			component: () => import('@/views/About.vue'),
		},
		{
			path: '/time-tracking',
			name: 'time-tracking',
			component: () => import('@/views/time-tracking/TimeTracking.vue'),
			meta: {
				requiresTimeTracking: true,
				title: 'timeTracking.title',
			},
		},
		{
			path: '/admin',
			component: () => import('@/views/admin/AdminShell.vue'),
			meta: {
				requiresAdminPanel: true,
				adminMode: true,
			},
			children: [
				{
					path: '',
					name: 'admin.overview',
					component: () => import('@/views/admin/OverviewView.vue'),
				},
				{
					path: 'users',
					name: 'admin.users',
					component: () => import('@/views/admin/UsersView.vue'),
				},
				{
					path: 'projects',
					name: 'admin.projects',
					component: () => import('@/views/admin/ProjectsView.vue'),
				},
			],
		},
	],
})

export async function getAuthForRoute(to: RouteLocation, authStore) {
	// vue-router already decoded to.hash once, so slicing off the prefix yields the original
	// fullPath (e.g. /oauth/authorize?...) losslessly — no extra decodeURIComponent needed.
	const redirectDest = to.name === 'user.login' && to.hash.startsWith(REDIRECT_HASH_PREFIX)
		? to.hash.slice(REDIRECT_HASH_PREFIX.length)
		: ''

	if (authStore.authUser || authStore.authLinkShare) {
		// An already-signed-in browser that opens a copied /login#redirect=<oauth.authorize> URL
		// must run the OAuth flow with its existing session instead of short-circuiting to home.
		// The destination has no redirect hash, so the second guard pass just early-returns (#2654).
		if (redirectDest) {
			return redirectDest
		}
		return
	}

	// Check if password reset token is in query params
	const resetToken = to.query.userPasswordReset as string | undefined
	
	// Redirect to password reset page if we have a token stored
	if (resetToken && to.name !== 'user.password-reset.reset') {
		return {name: 'user.password-reset.reset', query: { userPasswordReset: resetToken }}
	}

	if (typeof resetToken === 'undefined' && to.name === 'user.password-reset.reset') {
		return {name: 'user.login'}
	}

	// The confirmation link the mail carries lands on the app root with the
	// token in a query parameter, so it is caught here wherever it arrives and
	// handed to the screen that owns it. The token is not stashed anywhere on
	// the way: Confirm.vue reads it from the query and clears it from the
	// address bar itself.
	const emailConfirmToken = to.query.userEmailConfirm as string | undefined
	if (emailConfirmToken && to.name !== 'user.confirm') {
		return {name: 'user.confirm', query: {userEmailConfirm: emailConfirmToken}}
	}

	// Keep the destination in the address bar (not just per-browser localStorage) so a native
	// client's /oauth/authorize URL stays copyable into another browser. Hash, not query, so the
	// embedded OAuth params never reach access logs (#2654). Pass fullPath raw: vue-router encodes
	// the hash itself, so an extra encodeURIComponent here would be double-encoded in the URL.
	if (to.name === 'oauth.authorize') {
		return {
			name: 'user.login',
			hash: REDIRECT_HASH_PREFIX + to.fullPath,
		}
	}

	// Fold the hash destination into localStorage: it's the only bridge that survives the
	// external OIDC round-trip out of the SPA, so redirectIfSaved() works after any auth method.
	// vue-router already decoded to.hash once, so it equals the fullPath we wrote above as-is.
	if (to.hash.startsWith(REDIRECT_HASH_PREFIX)) {
		const destination = to.hash.slice(REDIRECT_HASH_PREFIX.length)
		const resolved = router.resolve(destination)
		saveLastVisited(resolved.name as string, resolved.params, resolved.query)
	}

	// Check if the route the user wants to go to is a route which needs authentication. We use this to
	// redirect the user after successful login.
	//
	// The localStorage confirmation token this used to consult is gone: nothing
	// writes it any more, so both clauses that read it were constants.
	const isValidUserAppRoute = !AUTH_ROUTE_NAMES.has(to.name as string)

	if (isValidUserAppRoute) {
		saveLastVisited(to.name as string, to.params, to.query)
		return {name: 'user.login'}
	}
}

/**
 * Where a person who is entitled to something lands when they ask for a screen
 * this product does not give them. It is the account screen, which is where the
 * subscription each of the three paid kinds of person sees is drawn.
 */
const ONE_TASKS_HOME = {name: 'user.settings.general'}

/**
 * Whether somebody's plan entitles them to a screen here at all.
 *
 * Written as the two answers that are refused rather than as a list of the plans
 * that are allowed, and that is deliberate: a list of paid plans signs out every
 * customer of the next plan we sell, on the day we sell it. The community
 * edition is a self-hosted installation, which buys none of this, and a session
 * carrying no plan at all has bought nothing either.
 *
 * This is the one place where the absence of the plan claim is read as
 * restrictive. Everywhere else absence is the permissive reading, and taking
 * that reading here would hand every screen to anybody holding any session.
 */
function isEntitledToOneTasks(edition: string | null): boolean {
	return edition !== null && edition !== 'community'
}

/**
 * Whether an address is one of the screens this product gives a paying person:
 * their account, the organisation they run, and a task somebody sent them a
 * link to.
 *
 * The organisation screens are named here because they are part of the surface,
 * not because everybody may open them. The guard below is what refuses them,
 * and it deliberately answers a member and a former administrator differently -
 * so this must not turn either of those two answers into a third one.
 *
 * Both names of the address-matches-nothing screen are here for the same
 * reason: it is the answer that guard gives a member who guessed the
 * organisation address, and sending it somewhere else would tell them the area
 * exists.
 */
function isOneTasksScreen(name: string): boolean {
	return name.startsWith('user.settings')
		|| name.startsWith('organization')
		|| name === 'task.detail'
		|| name === 'user.export.download'
		|| name === 'not-found'
		|| name === 'bad-not-found'
}

router.beforeEach(async (to, from) => {
	const authStore = useAuthStore()

	await authStore.checkAuth()

	// On the hosted product, and only there, every person is sent to the screen
	// their plan and their role entitle them to, and is let no further. Which
	// installations that means, and why the question has to be asked of the
	// server rather than worked out here, is written where it is asked below.
	//
	// This is asked on every move between screens
	// rather than once at sign-in, so a tab that has been open since before a
	// subscription ended is refused the same as a fresh one, and typing an
	// address is refused the same as following a link.
	//
	// Somebody who is not signed in is dealt with further down, by
	// getAuthForRoute. Somebody holding a link share is not one of these people
	// at all: they were given one project by somebody who has a plan, and they
	// have none of their own to read.
	//
	// The server can already refuse to serve anything but these screens
	// (brazn.restricteduionly), and that is off. It works on documents and
	// cannot see who is asking, so it can never tell these four kinds of person
	// apart; the plan is only readable here.
	if (authStore.authUser) {
		// The instance is asked first, and none of the rule below is applied
		// unless it says yes. The plan the rule reads is only ever written into
		// a login by an instance running as the hosted product; every other
		// installation of this fork - a self-hosted copy, and the one continuous
		// integration starts - writes no plan into anybody's login, so reading a
		// missing plan there as "entitled to nothing" would send every person on
		// it to the sign-in screen while holding a perfectly good session.
		//
		// The wait is the same one the licence check below takes, for the same
		// reason: on a directly typed address this guard can run before the
		// instance has answered, and the value would then be its own starting
		// default rather than the instance's answer.
		//
		// That default is "not the hosted product", and it is the safe direction
		// in both of the ways it can be reached. A server too old to publish the
		// field leaves it untouched, and so does a server that has not answered
		// yet; both readings let everybody through, which is what every
		// installation other than the hosted one wants. The opposite default
		// would lock out the whole of a self-hosted instance on a field its
		// server never sent.
		const baseStore = useBaseStore()
		await baseStore.appReady
		const configStore = useConfigStore()

		if (configStore.braznManagedMode) {
			const name = to.name as string
			// Sign-in, registering, resetting a password, confirming an address and
			// the consent screen a desktop app opens. Turning any of these away
			// leaves somebody with no way back to their account.
			const isAuthScreen = AUTH_ROUTE_NAMES.has(name) || name === 'oauth.authorize'

			if (!isEntitledToOneTasks(authStore.managedEdition)) {
				// The sign-in screen, and nothing else. They are not signed out:
				// the session is real and it is theirs, it simply buys no screen
				// here, and destroying it would take away the one place they can
				// still act.
				if (!isAuthScreen) {
					return {name: 'user.login'}
				}
			} else if (!isAuthScreen && !isOneTasksScreen(name)) {
				return ONE_TASKS_HOME
			}
		}
	}

	if (to.meta?.requiresAdminPanel) {
		// Await config/auth hydration so the license check doesn't race the empty default
		// on direct /admin navigation. appReady resolves without waiting on router.isReady(),
		// so awaiting it here doesn't deadlock the initial navigation.
		const baseStore = useBaseStore()
		await baseStore.appReady
		const configStore = useConfigStore()
		const featureOn = configStore.isProFeatureEnabled(PRO_FEATURE.ADMIN_PANEL)
		// isAdmin comes from /user, not the JWT; force-fetch in case checkAuth() was debounced.
		if (authStore.info?.isAdmin === undefined) {
			await authStore.refreshUserInfo()
		}
		const isAdmin = authStore.info?.isAdmin === true
		if (!featureOn || !isAdmin) {
			return {name: 'not-found'}
		}
	}

	if (to.meta?.requiresOrganizationAdmin) {
		// The server decides. The store holds an organization only after
		// GET /brazn/organization returned one, and that route refuses anybody
		// who is not the single administrator - so this is not a local role
		// check dressed up as one.
		//
		// A member reaching here by URL or by a stale tab lands on 'not-found',
		// which is what AC1 means by "cannot discover": the same answer a route
		// that was never registered gives, rather than a refusal that confirms
		// the area exists.
		const organizationStore = useOrganizationStore()

		// Asked on EVERY navigation inside the area, not only the first. A role
		// that is taken away between two page changes is the case this exists
		// for, and a guard that only ever asked once would leave somebody
		// looking at controls the server has already stopped honouring.
		const wasAdministrator = organizationStore.isAdministrator
		await organizationStore.load()

		if (!organizationStore.isAdministrator) {
			// Two different people arrive here and they must not get the same
			// answer. Somebody who never had the area gets what a route that
			// was never registered gives - AC1's bar is discovery, and a
			// refusal that named the area would confirm it exists. Somebody
			// whose role changed under them gets told their view is out of
			// date, because the alternative is a 404 for a page they were
			// legitimately reading a moment ago.
			if (wasAdministrator) {
				organizationStore.markStale()
				return
			}
			return {name: 'not-found'}
		}
	}

	if (to.meta?.requiresProjectCreateCapability) {
		// A UI hint, not the real gate (BRA-1342): the four entry points that
		// link here already hide themselves when this is false, so this only
		// covers a direct URL, a stale tab, or a bookmark. The server's managed
		// gate (ruleProjectCreate) is what actually refuses the create call.
		const {capabilities} = useManagedCapabilities()
		if (!capabilities.value.projectCreate) {
			return {name: 'not-found'}
		}
	}

	if (to.meta?.requiresTimeTracking) {
		const baseStore = useBaseStore()
		await baseStore.appReady
		const configStore = useConfigStore()
		if (!configStore.isProFeatureEnabled(PRO_FEATURE.TIME_TRACKING)) {
			return {name: 'not-found'}
		}
	}

	if(from.hash && from.hash.startsWith(LINK_SHARE_HASH_PREFIX)) {
		to.hash = from.hash
	}

	if (to.hash.startsWith(LINK_SHARE_HASH_PREFIX) && !authStore.authLinkShare) {
		saveLastVisited(to.name as string, to.params, to.query)
		return {
			name: 'link-share.auth',
			params: {
				share: to.hash.replace(LINK_SHARE_HASH_PREFIX, ''),
			},
		}
	}

	const newRoute = await getAuthForRoute(to, authStore)
	if(newRoute) {
		// A string target (the decoded redirect destination for an authed browser) already
		// carries its own query/path and no redirect hash, so navigate to it verbatim — don't
		// re-attach to.hash or it would re-enter the redirect loop.
		if (typeof newRoute === 'string') {
			return newRoute
		}
		return {
			hash: to.hash,
			...newRoute,
		}
	}

	// to.fullPath keeps the redirect hash url-encoded while to.hash is decoded, so the endsWith
	// check below never matches and would re-append the hash forever. The hash is already on the
	// URL here, so skip the re-attach (#2654).
	if (to.hash.startsWith(REDIRECT_HASH_PREFIX)) {
		return
	}

	if(!to.fullPath.endsWith(to.hash)) {
		return to.fullPath + to.hash
	}
})

export default router
