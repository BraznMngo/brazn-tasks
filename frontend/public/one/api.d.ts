/**
 * Hand-written declarations for ./api.js (ruling C5).
 *
 * `frontend/tsconfig.app.json` includes `src/**\/*`, so the unit tests in
 * `frontend/src/brazn/one/` are type-checked by `vue-tsc --build`. TypeScript
 * resolves the test's `import {...} from '../../../public/one/api.js'` to this
 * file with no config change, which is what lets the tests be plain `.test.ts`
 * with no `@ts-expect-error` scattered through them.
 *
 * Response bodies are declared `any` deliberately. api.js does not model the
 * fork's payloads — it hands them back untouched — and inventing interfaces
 * here would be a second, unchecked copy of shapes that live in Go
 * (`pkg/models/`), free to drift from them silently. The shapes worth pinning
 * are pinned in the tests instead.
 */

/* --- injection ---------------------------------------------------- */

export interface ApiConfig {
	fetch?: typeof fetch | null
	origin?: string | null
	randomUUID?: (() => string) | null
}

export function configure(config?: ApiConfig): void
export function newIdempotencyKey(): string

/* --- bases -------------------------------------------------------- */

export const FORK_V1_BASE: string
export const FORK_V2_BASE: string

export function forkV1Url(path: string): string
export function forkV2Url(path: string): string
export function commercialV1Url(path: string): string

/* --- errors ------------------------------------------------------- */

/** One entry of an RFC 9457 `errors[]` array, as Huma and this fork emit them. */
export interface ForkErrorDetail {
	/** e.g. "body.reactions". Null when the server named no location. */
	readonly location: string | null
	/** e.g. "expected object". Null when the server sent only a location. */
	readonly message: string | null
}

export class ForkError extends Error {
	readonly status: number
	readonly body: any
	readonly url: string
	/**
	 * `message ?? detail ?? title` from the body, with the body's own
	 * `errors[]` sentences appended in parentheses when it carried any.
	 * Render verbatim (ruling C4) — every word of it came off the wire.
	 */
	readonly serverMessage: string | null
	/**
	 * The body's `errors[]`, uncapped and unformatted, for a caller that wants
	 * to render the failing fields as a list rather than as one sentence.
	 * Always an array; empty when the body carried none.
	 */
	readonly details: readonly ForkErrorDetail[]
	/** The upstream numeric error code, present only on v2 problem+json bodies. */
	readonly code: number | null
}

export class SessionLostError extends Error {}

/* --- session ------------------------------------------------------ */

export function getToken(): string | null
export function setToken(token: string | null): void
export function hasSession(): boolean
export function isSessionLost(): boolean
/** Returns an unsubscribe function. Fires immediately if already terminal. */
export function onSessionLost(listener: () => void): () => void
export function resetSession(): void
/** Resolves to the new access token, or null once the state is terminal. */
export function refreshSession(): Promise<string | null>
/** Refresh on load. True when a session was established. */
export function initSession(): Promise<boolean>

/* --- JWT claims (ruling C1) --------------------------------------- */

export const PERSONAL_EDITION: string
export function parseJwt(token: string | null): Record<string, any> | null
export function getEdition(): string | null
export function isPersonalEdition(): boolean
export function hasEditionClaim(): boolean
export function isWriteRestricted(): boolean

/* --- the commercial guard (ruling C14) ---------------------------- */

/**
 * The outcome vocabulary is PER-OPERATION and there is no `'success'` anywhere
 * in the commercial service. Each descriptor names the shape that operation's
 * body has and the affirmative values it may carry, every one cited in api.js
 * against `client-service-27c95232`.
 */
export interface CommercialOp {
	/** `'required'`: the body must carry `outcome`. `'absent'`: it must not. */
	readonly shape: 'required' | 'absent'
	/** The affirmative values, empty when none could be read from the source. */
	readonly affirmative: readonly string[]
	/** True when a documented success is 204 with no body (erasure, confirm). */
	readonly noContent: boolean
	/**
	 * True for the four §16 routes that have no HTTP handler at the verified
	 * commit. A 404 on one of these means "not shipped yet", not "not found" —
	 * and nothing but this flag can tell the two apart, because what answers is
	 * a bare 404 where the commercial service is routed and the SPA shell at 200
	 * where it is not. `app.js` owns the wording.
	 */
	readonly contractOnly: boolean
}

export interface CommercialOps {
	readonly INVITE_MEMBER: CommercialOp
	readonly ACCEPT_INVITATION: CommercialOp
	/**
	 * PRE-EXISTING GAP, closed here because this interface is being edited
	 * anyway. BRA-1469 added `LIST_INVITATIONS` to api.js and not to this file,
	 * so `api.commercial.test.ts:269` has been failing the typecheck ever since
	 * — a test naming an operation TypeScript was told does not exist.
	 */
	readonly LIST_INVITATIONS: CommercialOp
	readonly REMOVE_ORGANIZATION_MEMBER: CommercialOp
	readonly RENAME_ORGANIZATION: CommercialOp
	readonly LIST_TEAM_ACCESS_REQUESTS: CommercialOp
	readonly DECIDE_TEAM_ACCESS_REQUEST: CommercialOp
	readonly CONFIRM_TEAM_ACCESS_REQUEST: CommercialOp
	readonly CANCEL_SUBSCRIPTION: CommercialOp
	readonly SET_SUBSCRIPTION_AUTO_RENEWAL: CommercialOp
	readonly GIVE_RENEWAL_CONSENT: CommercialOp
	readonly RESUME_CHECKOUT: CommercialOp
	readonly GET_ENTITLEMENTS: CommercialOp
	/** POST /v1/accounts/conversion/claims — mint a trial→paid claim (BRA-1442). */
	readonly ISSUE_CONVERSION_CLAIM: CommercialOp
	readonly LIST_SUCCESSOR_CANDIDATES: CommercialOp
	readonly ERASE_ACCOUNT: CommercialOp
	readonly REVOKE_INVITATION: CommercialOp
	readonly QUOTE_SEATS: CommercialOp
	readonly PURCHASE_SEATS: CommercialOp
	readonly TRANSFER_ADMINISTRATOR: CommercialOp
	/**
	 * BRA-1475. Read the organisation and team behind an invitation handle, with
	 * no session. Answers `state`, not `outcome`, so its shape is OUTCOME_ABSENT.
	 */
	readonly INVITATION_SUMMARY: CommercialOp
	/**
	 * BRA-1475. Create the account, spend the token, take the seat and join the
	 * team. EVERY outcome arrives at HTTP 200, refusals included, so a caller
	 * must branch on `result.outcome` in every case.
	 */
	readonly INVITATION_COMPLETION: CommercialOp
	/**
	 * BRA-1475. Is this username free? Answers `status`, not `outcome`, so its
	 * shape is OUTCOME_ABSENT - and that guard is load-bearing rather than a
	 * formality: an unrouted `/v1/...` is answered with the app shell at HTTP
	 * 200, which without the content-type check would read as a body with no
	 * outcome and let every name through.
	 */
	readonly INVITATION_USERNAME: CommercialOp
	/** Recognises nothing; the default when a caller names no operation. */
	readonly UNKNOWN: CommercialOp
}
export const COMMERCIAL_OPS: CommercialOps

export interface CommercialRefusalReasons {
	readonly HTTP: 'http'
	readonly NOT_JSON: 'not-json'
	readonly UNPARSABLE: 'unparsable'
	readonly OUTCOME: 'outcome'
	/** `fetch` itself rejected — nothing answered. Distinct from NOT_JSON. */
	readonly NETWORK: 'network'
}
export const COMMERCIAL_REFUSAL: CommercialRefusalReasons

export interface CommercialResult {
	ok: boolean
	/** 0 when no Response was produced at all (`reason: 'network'`). */
	status: number
	/** Null on a 204 success — `POST /v1/account/erasure` sends no body at all. */
	body: any
	/** The service's own sentence when it sent one. Render verbatim. */
	message: string | null
	/**
	 * The `outcome` word the service used, on BOTH halves of the verdict.
	 *
	 * `reason` names the refusal MECHANISM and is the same code (`'outcome'`)
	 * for every declined invitation, so it cannot say which refusal happened;
	 * this can. Branch on it when `ok` too — `invited` and `already_member` are
	 * both affirmative and mean different things.
	 *
	 * Null means the body named no outcome, which is ordinary: it is the shape
	 * of every `OUTCOME_ABSENT` operation and of every bare-status refusal.
	 * Never read null as "the service said no".
	 */
	outcome: string | null
	/** A machine reason code; null when `ok`. Map to a `t()` key in app.js. */
	reason: 'http' | 'not-json' | 'unparsable' | 'outcome' | 'network' | null
}

export function readCommercialResult(res: Response, op?: CommercialOp): Promise<CommercialResult>

/* --- the declared payloads (api.js §6b) --------------------------- *
 *
 * `proration` is typed `Record<string, any>` and stays opaque on purpose:
 * `SeatProration` is declared in the commercial service's `billing.ts`, which
 * is not among the files extracted at 27c95232, so naming a member of it would
 * be a guess (bar 7) about the one payload on this surface that carries money.
 * What IS readable is that null means the change costs nothing now.
 */

export interface SeatNotice {
	seats: number | null
	users: number | null
	/** Equal to `seats` when the organization already has room — that equality is the test. */
	seats_after: number | null
	/** Null = costs nothing now. An ordinary answer, never an error. */
	proration: Record<string, any> | null
}

export interface InvitationRecord {
	invitation_id: string | null
	/** `'pending' | 'accepted' | 'revoked'`. There is no `'expired'` — compare `expires_at`. */
	status: string | null
	expires_at: string | null
}

export interface SeatQuote {
	organization_id: string | null
	seats: number | null
	seats_after: number | null
	/** Null = costs nothing now (no invoiced period, or no billed seat added). */
	proration: Record<string, any> | null
}

/** Off an INVITE result. Null when nothing was offered, or utilisation was unreadable. */
export function readSeatNotice(result: CommercialResult): SeatNotice | null
/** Off an INVITE result. Null is the honest "nothing recorded, nothing to withdraw". */
export function readInvitationRecord(result: CommercialResult): InvitationRecord | null
export function readInvitedUserId(result: CommercialResult): string | null
/** Null when the result was not affirmative — distinct from a quote of nothing. */
export function readSeatQuote(result: CommercialResult): SeatQuote | null
/**
 * `null` = no answer. `[]` = an ordinary answer meaning no choice has to be
 * offered; the sole-member administrator may still erase. Do not collapse them.
 *
 * The ids are OPAQUE COMMERCIAL ids and cannot be joined against the fork
 * roster's numeric `user_id`. See api.js for the citations.
 */
export function readSuccessorCandidates(result: CommercialResult): string[] | null
/** `invitation_outcome` off the team-access decision; the only thing that words `not_admitted`. */
export function readInvitationOutcome(result: CommercialResult): string | null

/* --- fork: user and settings -------------------------------------- */

export function getCurrentUser(): Promise<any>
export function getAvatarProvider(): Promise<any>
export function listTimezones(): Promise<string[]>
export function saveGeneralSettings(
	patch: Record<string, any>,
	frontendPatch?: Record<string, any>,
): Promise<any>
export function uploadAvatar(file: Blob): Promise<any>
export function setAvatarProvider(provider: string): Promise<any>
export function saveAvatar(file: Blob): Promise<{uploaded: any, provider: any}>

/**
 * The upload generation, bumped by `saveAvatar` after BOTH of its calls and
 * nowhere else. Key any avatar cache on `username + this` so a fresh upload
 * cannot be answered with the picture already on screen.
 */
export function getAvatarGeneration(): number
/**
 * GET /api/v2/avatar/{username}?size= — image bytes, or null for every failure
 * (no username, non-2xx, empty body, lost session, network error). Never
 * throws, and never marks the session lost: an avatar must not sign anyone out.
 */
export function getAvatarBlob(username: string, size?: number): Promise<Blob | null>

export function changeEmail(newEmail: string, password: string): Promise<any>
export function changePassword(oldPassword: string, newPassword: string): Promise<any>
export function requestExport(password: string): Promise<any>
export function downloadExport(password: string): Promise<Blob>
/** 403 for every account: the route is `service-managed`. See api.js. */
export function requestAccountDeletion(password: string): Promise<any>
export function cancelAccountDeletion(password: string): Promise<any>
export function getInfo(): Promise<any>
export function forkAppUrl(path: string): string
export function buildOpenIdAuthorizeUrl(
  provider: {key: string, auth_url: string, client_id: string, scope: string},
  state: string,
): string

/* --- fork: task --------------------------------------------------- */

export const TASK_EXPAND: readonly string[]

export function getTask(taskId: number | string, options?: {expand?: string[]}): Promise<any>
/** The only call that sends `X-Vikunja-Format`. */
export function updateTaskDescription(taskId: number | string, descriptionMarkdown: string): Promise<any>
/**
 * Throws if `patch` carries `description` — that must go through updateTaskDescription.
 *
 * Sends `application/merge-patch+json` and always carries `reactions: null` and
 * `subscription: null`, which RFC 7386 removes from AutoPatch's merged PUT body.
 * Without that removal the merged body fails schema validation and every task
 * write answers "validation failed". See the block comment on
 * `PATCH_EXCISED_TASK_FIELDS` in api.js.
 */
export function patchTask(taskId: number | string, patch: Record<string, any>): Promise<any>
export function deleteTask(taskId: number | string): Promise<any>
export function duplicateTask(taskId: number | string): Promise<any>

export function listProjects(options?: {
	page?: number
	perPage?: number
	q?: string
	isArchived?: boolean
}): Promise<any>
export function searchTasks(q: string, options?: {page?: number, perPage?: number}): Promise<any>

export const RELATION_KINDS: readonly string[]
export function addRelation(
	taskId: number | string,
	otherTaskId: number | string,
	relationKind: string,
): Promise<any>
export function removeRelation(
	taskId: number | string,
	relationKind: string,
	otherTaskId: number | string,
): Promise<any>

export const SUBSCRIPTION_ENTITIES: readonly string[]
export function subscribe(entity: string, entityId: number | string): Promise<any>
export function unsubscribe(entity: string, entityId: number | string): Promise<any>

/* --- fork: comments ----------------------------------------------- */

export function listComments(taskId: number | string, options?: {
	orderBy?: 'asc' | 'desc'
	page?: number
	perPage?: number
}): Promise<any>
export function createComment(taskId: number | string, comment: string): Promise<any>
export function updateComment(
	taskId: number | string,
	commentId: number | string,
	comment: string,
): Promise<any>
export function deleteComment(taskId: number | string, commentId: number | string): Promise<any>

/* --- fork: labels ------------------------------------------------- */

export function listLabels(options?: {q?: string, page?: number, perPage?: number}): Promise<any>
export function createLabel(title: string, hexColor?: string): Promise<any>
export function listTaskLabels(taskId: number | string): Promise<any>
export function addTaskLabel(taskId: number | string, labelId: number): Promise<any>
export function removeTaskLabel(taskId: number | string, labelId: number): Promise<any>

/* --- fork: attachments -------------------------------------------- */

/** Null when the instance has task attachments disabled (all four routes 404). */
export function listAttachments(taskId: number | string): Promise<any>
/** Resolves the `{success, errors}` envelope untouched — 201 can carry failures. */
export function uploadAttachments(taskId: number | string, files: Iterable<Blob>): Promise<any>
export function downloadAttachment(
	taskId: number | string,
	attachmentId: number | string,
	options?: {previewSize?: string},
): Promise<Blob>
export function deleteAttachment(taskId: number | string, attachmentId: number | string): Promise<any>

/* --- fork: assignees ---------------------------------------------- */

export function listAssignees(taskId: number | string, options?: {q?: string}): Promise<any>
export function addAssignee(taskId: number | string, userId: number): Promise<any>
/** `userId` is NUMERIC here, unlike the team-member routes. See api.js. */
export function removeAssignee(taskId: number | string, userId: number): Promise<any>
export function searchProjectUsers(projectId: number | string, q: string): Promise<any>

/** GET /api/v2/users?q= — the instance-wide user search (BRA-1439 Story 8). */
export function searchUsers(q: string): Promise<any>

/* --- fork: organization and teams --------------------------------- */

/** Null on 403 — the ordinary answer for a non-administrator (ruling C1.5). */
export function getOrganization(): Promise<any>
export function listTeams(options?: {
	page?: number
	perPage?: number
	q?: string
	includePublic?: boolean
}): Promise<any>
/** Can 403 legitimately; callers must use Promise.allSettled (ruling C11). */
export function getTeam(teamId: number | string): Promise<any>
/** The only working team-create route. 409 body must be rendered verbatim. */
export function createOrganizationTeam(name: string): Promise<any>
export function deleteOrganizationTeam(teamId: number | string): Promise<any>
export function renameTeam(teamId: number | string, name: string): Promise<any>
export function renameTeamRootProject(projectId: number | string, title: string): Promise<any>
/** Both writes; one alone drifts the team and its root project apart. */
export function renameTeamEverywhere(
	teamId: number | string,
	projectId: number | string,
	name: string,
): Promise<{team: any, project: any}>
export function addTeamMember(teamId: number | string, username: string, admin?: boolean): Promise<any>
/** Team-scoped removal. NOT the same as removeOrganizationMember. */
export function removeTeamMember(teamId: number | string, username: string): Promise<any>
export function toggleTeamMemberAdmin(teamId: number | string, username: string): Promise<any>

/* --- commercial /v1: live today ----------------------------------- *
 *
 * None of these throws on a refusal, INCLUDING a `/v1` 401 — that is an
 * ordinary `reason: 'http'` with `status: 401`, never a fork-session loss. The
 * one thing they can throw is `SessionLostError`, and only when the fork's own
 * token refresh is refused. See `commercialFetch` in api.js.
 *
 * EVERY ID ON THIS SERVICE IS A STRING (`isId`, client-http-27c95232:1448) —
 * an opaque commercial account id, not the fork's numeric user id. A number is
 * a bare 400.
 */

/**
 * Throws if `body` carries `team_id`: the field is real, but the prototype has
 * no team picker so nothing on this page can produce one (bar 10, ruling C17).
 * `idempotency_key` is REQUIRED by the route and is defaulted here — omitting
 * it is an unconditional 400.
 *
 * The reply carries four fields: read them through `result.outcome`,
 * `readInvitationRecord`, `readSeatNotice` and `readInvitedUserId`. There is no
 * top-level `invitation_id` and no `id`.
 */
export function inviteOrganizationMember(
	body: Record<string, any>,
	idempotencyKey?: string,
): Promise<CommercialResult>
/** Body is `{invitation_id}` and nothing else — no idempotency key. */
export function acceptOrganizationInvitation(body: Record<string, any>): Promise<CommercialResult>
/**
 * Available but deliberately not surfaced by the page (ruling C8.2).
 * Body is `{organization_id, member_user_id}` and nothing else — no key.
 */
export function removeOrganizationMember(body: Record<string, any>): Promise<CommercialResult>

/**
 * POST /v1/organizations/rename (BRA-1439 Story 2). `idempotency_key` is
 * defaulted at this seam, like the invite's.
 */
export function renameOrganization(
	body: Record<string, any>,
	idempotencyKey?: string,
): Promise<CommercialResult>
export function listTeamAccessRequests(query?: Record<string, any>): Promise<CommercialResult>
export function decideTeamAccessRequest(body: Record<string, any>): Promise<CommercialResult>
export function confirmTeamAccessRequest(body: Record<string, any>): Promise<CommercialResult>
/**
 * `idempotency_key` is the ONLY permitted field and it is defaulted here, so
 * there is no body parameter: any other field is an unconditional 400.
 */
export function cancelSubscription(idempotencyKey?: string): Promise<CommercialResult>
/** The only valid body is `{}`, so there is no parameter to get wrong. */
export function setSubscriptionAutoRenewal(): Promise<CommercialResult>
/** The only valid body is `{}`, so there is no parameter to get wrong. */
export function giveRenewalConsent(): Promise<CommercialResult>
export function resumeCheckout(body?: Record<string, any>): Promise<CommercialResult>
export function getEntitlements(): Promise<CommercialResult>
/**
 * `{candidates: [{user_id}]}` — ids only. Read through `readSuccessorCandidates`.
 *
 * The ids are the COMMERCIAL service's own and CANNOT be resolved to names:
 * `OrganizationMember.UserID` is the fork's local `int64` row id and nothing a
 * browser can read carries the commercial one. An empty list is an ordinary
 * answer meaning no choice has to be offered, NOT a refusal and NOT a bar to
 * erasure.
 */
export function listSuccessorCandidates(): Promise<CommercialResult>
/**
 * The delete-account path; the fork's own deletion request is 403 for everyone.
 * Body is `{successor_user_id}` or nothing — "nobody" is lawful, and is what
 * the sole-member administrator has.
 */
export function eraseAccount(body?: Record<string, any>): Promise<CommercialResult>

/* --- commercial /v1: contract only -------------------------------- */

/** `invitationId` is nested on the invite reply: `body.invitation.invitation_id`. */
export function revokeOrganizationInvitation(
	organizationId: string,
	invitationId: string,
): Promise<CommercialResult>
export function quoteSeats(organizationId: string, seats: number): Promise<CommercialResult>
export function purchaseSeats(
	organizationId: string,
	seats: number,
	idempotencyKey?: string,
): Promise<CommercialResult>
/** POST /v1/accounts/conversion/claims — mint a trial→paid claim (BRA-1442). */
export function issueTrialConversionClaim(): Promise<CommercialResult>
/**
 * No `from_user_id` parameter exists: it is the resolved bearer, never a body field.
 *
 * `toUserId` is a STRING on the wire — `AdminTransferRequest.to_user_id: string`
 * (client-service-27c95232:535), validated by `isId`. `number` is still
 * accepted by this declaration only because `api.commercial.test.ts:673` passes
 * one; that test pins a shape the service would answer 400 for and should be
 * changed to `'42'`, after which this union narrows to `string`.
 */
export function transferAdministrator(
	organizationId: string,
	toUserId: number | string,
	idempotencyKey?: string,
): Promise<CommercialResult>

/* --- BRA-1475: opening and closing a session ----------------------- *
 *
 * Everything above assumes a session already exists. These are what a
 * SIGNED-OUT person needs, and they did not exist here until the sign-in page
 * did.
 */

/**
 * POST /api/v1/login. `username` accepts an email address too. Resolves to the
 * access token, which is adopted into this module's session state.
 *
 * Rejects with a `ForkError` whose `.code` distinguishes the two replies that
 * are FOLLOW-UPS rather than failures: 1017 wants a second-factor passcode,
 * 1012 says the address is unconfirmed.
 */
export function signIn(credentials: {
	username: string
	password: string
	totpPasscode?: string
}): Promise<string>

/** POST /api/v1/auth/openid/{provider}/callback. Resolves to the access token. */
export function completeOpenIdSignIn(
	providerKey: string,
	callback: {code: string, redirectUrl: string, totpPasscode?: string},
): Promise<string>

/**
 * POST /api/v1/user/logout. Resolves to the provider's own logout address for a
 * session opened through an identity provider, else null. The LOCAL session is
 * dropped whatever the server answered.
 */
export function signOut(): Promise<string | null>

/**
 * POST /api/v1/user/password/token. The answer is the same whether or not an
 * account exists — that is the published contract, and a caller must not
 * report otherwise.
 */
export function requestPasswordReset(email: string): Promise<any>

/** POST /api/v1/user/password/reset. A spent or expired token is a `ForkError` with code 1009. */
export function setNewPassword(token: string, newPassword: string): Promise<any>

/** POST /api/v1/user/confirm — spend an email-confirmation token. Unauthenticated. */
export function confirmEmailAddress(token: string): Promise<any>

/**
 * POST /api/v1/user/deletion/confirm — AUTHENTICATED, unlike every other mailed
 * token in this product: the account comes from the session and the token is
 * only the second factor.
 */
export function confirmAccountDeletion(token: string): Promise<any>

/**
 * POST /api/v1/oauth/authorize — the desktop application's destination.
 * Answers `{code, redirect_uri, state}`.
 */
export function authorizeDesktopClient(params: {
	response_type: string
	client_id: string
	redirect_uri: string
	state?: string
	code_challenge: string
	code_challenge_method: string
}): Promise<any>

/* --- BRA-1475: the invitation, from a browser with no session ------ */

/**
 * Whether a handle and a token are the shape the service will accept — a handle
 * of 1 to 128 characters and a token of exactly 43 from the base64url alphabet.
 * Checked before the request so a mangled link is not a bodiless 400 the page
 * could only report as "something went wrong".
 */
export function invitationCredentialsAreWellFormed(
	invitationId: string | null | undefined,
	signupToken: string | null | undefined,
): boolean

/** POST /v1/invitations/summary. Unauthenticated. Consumes nothing. */
export function readInvitationSummary(request: {
	invitationId: string | null
	signupToken: string | null
}): Promise<CommercialResult>

/**
 * POST /v1/invitations/completion. Unauthenticated. There is NO `email` field
 * and its absence is the guarantee: the address comes from the token's own
 * binding, so no caller can choose which mailbox the account is made for.
 */
export function completeInvitation(request: {
	invitationId: string | null
	signupToken: string | null
	username: string
	password: string
}): Promise<CommercialResult>

export interface InvitationSummary {
	/**
	 * `usable`, `already_member`, `invitation_withdrawn`, `invitation_expired` or
	 * `token_expired` — and null for a state this page has not read, which every
	 * caller must treat as "not usable". All five arrive at HTTP 200.
	 */
	state: string | null
	organizationName: string | null
	/** Stable id that distinguishes two companies with the same display name (BRA-1495). */
	organizationId: string | null
	/** Only `usable` carries these two; the other four answer with nulls. */
	teamName: string | null
	invitedEmail: string | null
}

/** The four fields the summary delivers. `state` is the verdict, not `ok`. */
export function readInvitationSummaryBody(result: CommercialResult): InvitationSummary
/**
 * Is this username free? — the invitation form's live check.
 *
 * `taken` and `invalid` are both the service ANSWERING, and both block:
 * `invalid` means the task server would refuse that string whoever held it.
 * `unknown` is the fail-open answer and covers only NOT KNOWING — offline, a
 * timeout, a bodiless refusal, and a body this page did not recognise. The
 * caller must always allow submission on it.
 */
export function checkInvitationUsername(request: {
	invitationId: string | null
	signupToken: string | null
	username: string
}): Promise<'free' | 'taken' | 'invalid' | 'unknown'>
