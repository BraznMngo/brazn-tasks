# ONE Tasks restricted views — unit test inventory (BRA-1357 / BRA-1358)

Ruling C6 asked for this: the spec set named one test file and no behaviours, while `app.js`
holds the highest-risk logic on the page. One row per test case: **file → behaviour → the
mutation that must make it fail**.

CLAUDE.md §4 is the reason the third column exists, and the reason each sentence is a specific
production edit rather than "if the code were wrong". **Four mutation claims in this repository
have now been traced FALSE**, every one of them visible only by tracing and none by reading — so
every sentence below was traced against the shipped module before it was written. Where tracing
changed the claim it is said out loud:

* `app.gating` "hides only what GATES\_THAT\_HIDE names" — removing the gate from the hide list
  produces **enabled**, not disabled.
* `api.commercial` "`{outcome: 'queued'}` fails closed" — the two shapes fail closed in two
  different branches, so one sentence was true for five of the seventeen operations or for
  twelve, never for both.
* `api.requests` `parseJwt` padding — withdrawn entirely; it cannot be shown load-bearing from
  this repository at all (see the note at the end of that section).
* `app.dom` "a 403 organization read is not an error" — the claimed mutation (removing the fold
  in `api.getOrganization`) leaves all three assertions **green**, because `loadOrganization`
  nulls the organization on the catch path too. Corrected in place, and the fold now has an
  assertion for which that mutation IS true.

The pattern in all four is the same: the sentence named a change that was *upstream of* what the
assertions actually read. Trace from the assertion backwards, not from the intent forwards.

## Mechanics (ruling C5, settled — not re-litigated here)

* `frontend/package.json` → `"test:unit": "vitest --dir ./src"`, so the tests live under
  `frontend/src/brazn/one/` and import the page by **relative** path
  (`../../../public/one/api.js`). TypeScript resolves `./api.js` to the hand-written
  `public/one/api.d.ts` beside it, which keeps every file a plain `.test.ts` with no
  `@ts-expect-error` anywhere.
* Fixtures — `task.html`, the six catalogues — are imported with **`?raw`**, i.e. as data. That
  resolves to vite/client's `*?raw` ambient declaration rather than to a file in the TS program,
  and it avoids `node:fs`, which would not type-check: `tsconfig.app.json` sets `"types": []` and
  also includes these test files.
* Environment is `happy-dom` (`frontend/vite.config.ts`). Nothing here touches the network:
  `api.js` takes its fetch through `configure()`, and `i18n.js` — which has no seam — has the
  global stubbed and unstubbed around it.
* **No Playwright spec, for `/v1` or for fork routes** (ruling C7). Nothing below may be reported
  as evidence that a commercial route works; CI starts no commercial service.

## Seven files

**No case counts are recorded here, deliberately.** An earlier version of this file carried
them and they went stale inside one afternoon — `api.commercial.test.ts` was listed at 16 while
its `OPERATIONS` table alone generates more than fifty. A hand-maintained number that nothing
checks is the same defect ruling C18 struck `PAGE_VERSION` for, and a stale number in the
mutation ledger is worse than none: it is the one file whose whole purpose is being accurate
about what is proven. Count them with `vitest --dir ./src` if you need the number; it is the only
source that cannot be wrong.

| File | Covers |
| -- | -- |
| `api.session.test.ts` | refresh on load, the single in-flight refresh, 401 → refresh → retry once → terminal |
| `api.commercial.test.ts` | **the commercial guard (bar 8)**, the per-operation outcome vocabulary, and origin-rooted `/v1` construction (bar 6). Most of its cases are generated from the 17-row `OPERATIONS` table: one per affirmative value, one refusal and one fail-closed per operation |
| `api.requests.test.ts` | the `X-Vikunja-Format` discipline, the single task read, the destructive-PUT merge, JWT claims |
| `i18n.test.ts` | exact-tag negotiation, the fallback chain, the shipped-catalogue audit |
| `app.gating.test.ts` | the role matrix through `decideGate`, fact derivation, routing, the login hand-off, the action registry |
| `app.seats.test.ts` | the seat formula against the literal contract `3 * (teams_used + 1)` |
| `app.dom.test.ts` | the DOM applier, the one shared refusal path, hydration, organization/roster facts |

---

## `api.session.test.ts`

| Behaviour | Mutation that must make it fail |
| -- | -- |
| Refresh on load hits `/api/v1/user/token/refresh` with `credentials: 'same-origin'` and no bearer | Change `forkV1Url('user/token/refresh')` to `forkV2Url(...)`, or `credentials` to `'omit'`, in `performRefresh` |
| Two concurrent `refreshSession()` calls share ONE request | Delete the `if (refreshInFlight === null)` guard in `refreshSession` — the call count becomes 2 |
| A 401 is retried exactly once, replayed with the freshly minted bearer | Return the 401 response instead of replaying it in `authedFetch`, or build `authInit(init)` once before the refresh instead of twice |
| A second 401 is terminal: three requests, no fourth | Replace the `markSessionLost(); throw` after the replay with another refresh-and-retry — the count becomes 5 |
| A refused refresh drops the token, fires `onSessionLost`, and issues nothing afterwards | Delete the `if (sessionLost) throw new SessionLostError()` guard at the top of `authedFetch` — a third request appears |
| `refreshSession()` answers null once terminal, without a request | Delete `if (sessionLost) return Promise.resolve(null)` from `refreshSession` |
| A transport failure on the refresh is terminal, not a thrown `TypeError` | Remove the try/catch around the refresh in `performRefresh` — `initSession()` rejects instead of resolving false |
| A listener registered AFTER the session was lost is still called | Delete the `if (sessionLost) { listener(); … }` branch from `onSessionLost` |

## `api.commercial.test.ts` — the most important file on this page

**There is no `COMMERCIAL_OK` and no `outcome: 'success'`.** A single shared affirmative constant
was the defect FINDING-OUTCOME.md was raised for: `'success'` appears nowhere in the commercial
service, so every commercial control would have rendered its refusal path on a real success,
invisibly, because CI never reaches `/v1` (bar 9). The constant is gone, each operation carries
its own descriptor, and the rows below are written against the shipped file rather than against
the design it replaced.

**The two shapes fail closed in two different branches**, and that distinction is what an earlier
version of this ledger got wrong. `OUTCOME_REQUIRED` operations are refused by the
affirmative-set check at the end of `readCommercialResult`; `OUTCOME_ABSENT` ones are refused
earlier, by `body.outcome === undefined`, and never reach that check at all. A mutation sentence
naming one branch is true for five of the seventeen operations or for twelve, never for both.

| Behaviour | Mutation that must make it fail |
| -- | -- |
| Each operation accepts every affirmative value api.js cites for it, in the route's real body shape | Remove that value from the descriptor's `affirmative` list; for an `absent`-shaped operation, make a missing `outcome` a refusal |
| Each operation refuses a value the service really sends as a failure, keeping the service's sentence | Add that value to the descriptor's `affirmative` list |
| `{outcome: 'queued'}` — a vocabulary from nowhere — fails CLOSED, **on both shapes** | Two different edits. For the five `required` rows: replace the affirmative-set check with a denylist of known failures. For the twelve `absent` rows: drop `body.outcome === undefined` from the `OUTCOME_ABSENT` branch. Neither edit reddens the other group |
| The table's hand-transcribed shape matches each descriptor's `shape` | Change any descriptor's shape in `api.js` — e.g. `INVITE_MEMBER` to `absent`, after which a body with no `outcome` at all reads as a success |
| `outcome: 'success'` is recognised by nothing | Reintroduce a shared affirmative `'success'`, or a blanket `body.outcome === 'success'` shortcut in the guard |
| A body with no `outcome` at all fails CLOSED on an outcome-bearing operation | Default a missing outcome to the descriptor's first affirmative value |
| **The CI shape**: 200 + `text/html` + the SPA `index.html` is `not-json` | Delete the content-type check — the reason becomes `unparsable`. Note `ok` stays false either way, which is exactly why the assertion pins `reason` |
| 204 with no body is a success **only** for the operations that declare one | Delete the 204 branch (a completed account erasure reports as "service unavailable"); or drop `&& op.noContent` from it; or drop `res.status === 204`, after which the CI shape reports an erasure that never happened |
| A 200 that PARSES as JSON but is served as `text/html` still fails closed | Delete the content-type check — the result becomes `ok: true`, i.e. a fake success |
| `application/json; charset=utf-8` and `application/problem+json` are accepted | Tighten the test to `contentType === 'application/json'` — every real answer would become `not-json` |
| Malformed JSON is `unparsable`; a bare JSON string `"success"` is too | Drop the `typeof body !== 'object'` check — the bare string reports `outcome` instead of `unparsable` |
| A non-2xx is `http`, with the server sentence kept | Fold the non-2xx branch into the outcome branch |
| `not_invitable` is refused **in the four-field body the invite handler really projects**, with `message: null` | **Corrected case.** It used to be asserted against `{outcome, message}` — a body `POST /v1/organizations/invitations` cannot send (percy-http-27c95232.ts:2854-2884 projects `outcome`, `invited_user_id`, `invitation`, `seat_notice` and nothing else). The old case passed, its own mutation traced, and it still documented a shape the source contradicts on the only coverage of the invite refusal path. Mutation now: add `not_invitable` to the affirmative list (the refusal half), or make `readServerMessage` fall back to any other field (the `message: null` half, which is what makes app.js's outcome table load-bearing) |
| **No `/v1` route sends `message`, `detail` or `title` at all**, so the verbatim path is defence rather than coverage | Stated, not asserted as service behaviour: the three body writers are `json` (:717), `bare` (:728 — a status line with no content type) and `fail` (:1778), whose only JSON bodies are `{error: <code>}` (:1785, "deliberately never emitted" of the optional message), the frozen 402 `upgrade_required` shape (:1795), and `{error, debug}` behind the off-by-default flag (:1827). The shared `readServerMessage` mechanism is still asserted; ruling C4's verbatim rule bites on the FORK's 409, which `app.dom.test.ts` covers |
| A live call runs through the guard and is addressed `/v1/…` with NO `/api` | Build the path with `forkV1Url()` — `/api/v1/v1/entitlements`, the documented top mistake here |
| `admin-transfer` sends exactly `{organization_id, to_user_id, idempotency_key}` | Add `from_user_id` to `transferAdministrator`'s payload — it is the resolved bearer, never a body field |
| `to_user_id` goes on the wire **unchanged**, number or string | Add a `Number()` coercion to `transferAdministrator`. The seam must not hide the type: a `<select>` value is a string, so a call site passing `fieldValue(...)` straight through sends `"42"`, and ruling C17's reasoning covers the value shape as much as the field name |
| Every invitation carries an `idempotency_key` | Remove the default from `inviteOrganizationMember`. `parseInvite` requires the key unconditionally and UUID-checks it (`percy-http-27c95232.ts:1602`), and a null parse is a bare 400 — so an invitation without one is a guaranteed 400 that CI can never see |
| An invitation carrying `team_id` throws and issues no request | Delete the `'team_id' in body` assertion from `inviteOrganizationMember` (ruling C17). **Corrected claim:** the field is NOT invented — `parseInvite` allowlists it at `percy-http-27c95232.ts:1598` and forwards it at :2841. It is not sent because the prototype has no team picker (bar 10), and the row is written that way now |
| No rename-organization call is exported at all | Add any `renameOrganization*` export to `api.js` — ruling C8.1 needs its absence to keep "renders disabled, issues no request" testable |
| The three bases stay apart (`/api/v1`, `/api/v2`, `/v1`) | Point any of `forkV1Url` / `forkV2Url` / `commercialV1Url` at another base |
| A leading slash on the path does not double up | Delete `stripLeadingSlash` |
| The commercial URL is ROOT-relative even when the base carries a path | Drop the leading slash from `commercialV1Url`'s `/v1/${path}` template — from `/one/task.html` it becomes `/one/v1/…` |

## `api.requests.test.ts`

| Behaviour | Mutation that must make it fail |
| -- | -- |
| The description PATCH carries `X-Vikunja-Format: markdown` | Delete the third argument to `patchTaskInternal` in `updateTaskDescription` — the server then stores Markdown as literal text |
| A battery of ten other writes (3 of them PATCHes) carries it on NONE | Move the header from `updateTaskDescription`'s argument into `patchTaskInternal`'s own headers — `patchTask`, `renameTeam` and `renameTeamRootProject` start carrying it |
| A comment update is a PUT with `?format=markdown` and no header | Replace it with the PATCH-plus-header SPEC-BACKEND row 13 proposed |
| `patchTask` refuses a `description` key and issues nothing | Delete the `'description' in patch` assertion — the call falls through and PATCHes a description with no header |
| The task is read ONCE, `?format=markdown`, `expand` repeated per value | Change `format` to `html` (SPEC-ROLES J2), or add a second read for the description (ruling C13) |
| General settings are GET → merged → PUT, minus `extra_settings_links` | Send the bare patch — `language` and `timezone` vanish and the server writes them away |
| The organization read folds 403 → null and keeps 500 an error | Remove the 403 fold (the null case reddens); widen it to `status >= 400` (the 500 case reddens) |
| `serverMessage` reads `message ?? detail ?? title` across all three envelopes | Read only `message` — the RFC 9457 body's sentence becomes null and the page paraphrases a refusal |
| Team create goes to `PUT /api/v1/brazn/organization/teams`, the only route that works | Point `createOrganizationTeam` at `forkV2Url('teams')`, which is service-managed and 403s for everyone |
| Renaming a team issues BOTH writes, team then root project | **The mutation is true of the helper and the helper is NOT the shipped journey.** Deleting either call from `renameTeamEverywhere` does redden this row — but `renameTeamEverywhere` has no caller in `app.js`, `view-task.js` or `view-settings.js`. The `save-team` action calls `api.renameTeam` and `api.renameTeamRootProject` separately and says why, so deleting either of THOSE two calls reddens nothing at all. The brief lists "rename team needs two writes" as a mis-wired call that must be corrected, and the correction as shipped is unguarded. See "Deliberately NOT tested" for why that gap is not closed here |
| Team members are addressed by username, task assignees by numeric id | Swap either identifier — the `{user}` path segment means different things on the two routes |
| `personal-cloud` is read from the `brazn_edition` claim | Change the `PERSONAL_EDITION` comparison |
| Any other edition value, and absence, is unrestricted | Whitelist `teams-cloud` instead of testing for `personal-cloud` — the failure that costs a customer access |
| Write restriction must be exactly `true` | Relax `claims[…] === true` to a truthy test — `'true'` would read as restricted, as would every legacy token |
| An undecodable token reads as "claims absent" rather than throwing | Remove the try/catch from `parseJwt` — the page dies on boot over a token shape it has no opinion about |
| The claims come from the **payload** segment of an unpadded base64url token, not the header | Change `token.split('.')[1]` to `[0]` or `[2]` in `parseJwt`. The fixture's header carries a different `brazn_edition` on purpose, so nothing else can satisfy the assertion |

**A withdrawn mutation claim, kept visible rather than deleted.** This file used to say "deleting
the padding line from `parseJwt` makes this red whenever the payload length is not a multiple of
four — which is most of the time." **It is false.** WHATWG forgiving-base64 fails only at a
length of 1 mod 4, a remainder valid base64 cannot produce; 2 and 3 decode unpadded. Neither the
padding line nor the base64url alphabet translation can be shown load-bearing from this
repository at all, because which `atob` happy-dom resolves to — and how tolerant it is — cannot
be established without executing (CLAUDE.md §1; Node's own base64 decoder accepts `-` and `_` as
aliases, which would make the translation redundant there too). Both lines stay in `api.js` as
defence against a stricter engine; neither is asserted, and the case above pins what *is* a
property of our code. This was the third wrong mutation claim found in this repository; a fourth
followed it in `app.dom.test.ts` (see the header) — §4's warning is not theoretical.

## `i18n.test.ts`

| Behaviour | Mutation that must make it fail |
| -- | -- |
| Exact-tag negotiation; `'es'` never becomes `'es-ES'` | Add a primary-subtag match (`tag.split('-')[0]`) to `negotiateLanguage` |
| The stored preference beats `navigator.languages` | Push `navigatorLanguages` before `preferred` in the candidate list — both tags in the fixture are supported, so only the ORDER can satisfy it |
| Every tag in `SUPPORTED_LOCALES` has a catalogue file | Add a tag without shipping `public/one/i18n/<tag>.json` |
| Fallback chain negotiated → en → key path, including a missing INTERMEDIATE node | Delete the `typeof node !== 'object'` guard in `lookup()` — it throws instead of falling through |
| A present-but-empty value counts as missing | Drop the `node !== ''` condition in `lookup()` — `t()` returns an empty label |
| A missing regional catalogue degrades to en and keeps the page alive | Remove the try/catch around the overlay fetch in `init()` |
| `en` is a hard dependency: `init()` rejects when it 404s | Wrap the en fetch in the same try/catch as the overlay |
| A missing key warns exactly ONCE and returns the key path | Delete the `warned` Set from `t()` — the count becomes 2 |
| Params, plural branches, and the vue-i18n `{'@'}` literal escape | Delete the literal branch from `interpolate()`'s replacer — `{'@'}ada` ships to the screen |
| All six catalogues parse and carry real copy (per-file and total floors) | Any change that makes the value walker visit nothing — the floors exist so the audit below cannot pass vacuously. **They are an anti-vacuity guard and NOT a coverage assertion**, which is stated in the test because the number reads like one: the real counts are en 348 and 107–151 elsewhere, so 54–69% of the page falls back to English outside `en`. Every missing key is missing from `frontend/src/i18n/lang/<locale>.json` too, so raising the floors would redden this build for somebody else's translation process. The coverage figure lives in `docs/one-tasks-restricted-views.md`, with its method |
| No shipped catalogue value names the product Vikunja | Write an upstream string back in, e.g. "Export your Vikunja data" at `user.export.title` — the same failure `fork-guards.yml` catches upstream |
| No key path contains a `.` in its own name | Add one — `t()` splits on `.`, so the key would be unreachable and render as visible debug text |

## `app.gating.test.ts`

| Behaviour | Mutation that must make it fail |
| -- | -- |
| The matrix: 12 controls × 5 roles (OA/TA/M/P/U) = 21 disabled, 9 hidden, 30 enabled; every disabled decision carries a reason AND a message key, every hidden one a reason and NO key | Return `disabled(null)` anywhere, or drop an entry from `DENY_MESSAGE_KEY`. The two counts are asserted so the per-branch checks cannot go vacuous |
| Only `admin` and `edition` hide; everything else disables | Remove `admin` from `GATES_THAT_HIDE` — **traced: the state becomes `enabled`, not `disabled`**, because no rule in `DISABLE_ORDER` covers `admin` and the unknown-token sweep accepts it. An organization tab live for an account with no organization |
| Two failing gates resolve in a fixed order (`teams` before `write`) | Move `write` before `teams` in `DISABLE_ORDER` |
| "Roster unavailable" outranks "not an administrator" | Swap the two guards in `decideGate`'s `team-admin` case — the page would state an admin bit it never saw |
| An unrecognised gate token fails CLOSED | Delete the trailing token sweep in `decideGate` — a typo would resolve to "no requirement" |
| Every message key **in `DENY_MESSAGE_KEY`** exists in the shipped `en.json`, and the two hidden reasons carry none | Rename any `DENY_MESSAGE_KEY` value without adding the key — the refusal renders as a raw key path, and only a console warning would say so. **Traced correction:** the sweep is over the exported table, not over the matrix. `decideGate` can never emit `COMMERCIAL` or `SERVER` — the two refusal describers write those — so a matrix-driven sweep left two of the entries unchecked and this sentence was false for them |
| Every key in **`COMMERCIAL_OUTCOME_MESSAGE_KEY`** exists too — the third table, reachable from neither sweep above | Rename any value without adding the key. `decideGate` cannot emit these and `DENY_MESSAGE_KEY` does not list them, because the value comes off the wire rather than out of a gate — so both existing sweeps miss all ten, and a rename would ship a dotted path onto a refusal surface |
| "No teams yet" is not "we cannot read this team" | Drop the `hasAnyTeam()` check from `teamFact` — an administrator with three teams and a control scoped to none of them would be told the organization has none. The paired half: falling back to `NO_TEAM` for a NAMED team the roster does not list would claim a team the payload said exists is absent |
| The five chrome actions, and the two attribute hooks, are exactly what ships | Re-register `return-signin` or `data-nav`, or restore `data-nav` to `ATTRIBUTE_HOOKS`. Both were registered handlers for names no module emits, which reads to the next person as a live affordance |
| The `/login` hand-off happens once and then refuses to loop | Delete `if (marker === true) return false` from `shouldHandOffToLogin`. `/login` is a vue-router path the restricted-UI lockout redirects back to this page, so an unconditional hand-off is `ERR_TOO_MANY_REDIRECTS` on exactly the instance the lockout exists for |
| A user-pressed Sign in still hands off | Drop the `force` short-circuit — the terminal surface's only control would do nothing |
| With storage unusable, `redirectCount > 0` stands in for the marker | Return `true` unconditionally for a null marker — every storage-less browser loops again |
| Edition comes from the JWT claim, never from the organization read | Source `personalEdition` from the organization payload (SPEC-UI §5.4) — with no organization loaded the fact would be false (ruling C1) |
| Absence of the claim means "no edition to name", not personal | Default a missing claim to `personal-cloud` — every CI session and every legacy token restricted |
| Any non-personal edition is Teams, including an unseen one | Switch `editionMessageKey` to a `=== 'teams-cloud'` test |
| `writeRestricted` is surfaced from the claim; absence permits | Invert the default |
| Only a positive integer is a task id | Replace the `/^[1-9][0-9]*$/` test with `Number.isFinite` — `'1e3'`, `' 12 '`, `'0x0c'` and `''` all build URLs the API 404s |
| A view with nothing to render falls back to settings, not to an error | Delete `clampView()` |
| `?tab=organization` clamps to `account` for a non-administrator | Delete the tab clamp from `resolveRoute` |
| A route round-trips through the query string | Change the serialisation without changing the parser |
| The seven chrome actions app.js owns are registered | Add or remove one |
| Registering a duplicate action name throws | Delete the `actions.has(name)` check from `registerActions` — two views could claim one hook and the last loaded would win silently |

## `app.seats.test.ts`

The contract — `seats_purchased >= 3 * (teams_used + 1)`, member count excluded — is transcribed
by hand at the top of the file. Nothing is asserted against a value the page computed.

| Behaviour | Mutation that must make it fail |
| -- | -- |
| `SEATS_PER_TEAM` equals the contract's literal 3 | Change the constant in `app.js` |
| `3 * (teams_used + 1)` for a hand-written table of six team counts | Any change to `requiredSeatsForTeams` or to `readSeatMeter`'s use of it |
| Member count is not in the formula | Fold members (or members + pending, as the prototype does at line 602) back into `requiredForNextTeam` |
| A server ratio of `0` is honoured, not read as 3 | Restore the prototype's `seats_per_team \|\| 3` |
| **No ratio sent → no requirement stated.** `seatsPerTeam` and `requiredForNextTeam` are both null | Restore `const ratio = seatsPerTeam ?? SEATS_PER_TEAM` in `readSeatMeter` — the page would state a seat requirement the server never sent, on the one number a customer is asked to spend money against. `SEATS_PER_TEAM` is the drift comparison and the tests' contract literal; it is not a fallback |
| The drift warning fires when server and page disagree, and NOT when they agree | Delete the warning (positive half); make it unconditional (the negative half is asserted first for exactly that reason) |
| A null `seats_purchased` is unknown — neither zero nor unlimited | Coerce it to 0 — `meetsNextTeamRule` becomes false and the page tells a customer to buy seats they may already own |
| `can_create_team` is read as sent, on a payload where the rule DISAGREES with it | Recompute it from `meetsNextTeamRule` |
| `fillRatio` clamps to 1 and is null with no denominator | Drop the `purchased <= 0` guard — Infinity/NaN reaches the style attribute |
| A null organization (the 403 case) answers safely | Dereference `org.seats_occupied` without the optional chain — a TypeError for the most common role on the instance |
| Non-numeric counts are rejected, not coerced | Replace `intOrNull` with `Number(value)` — `'9'` renders as a seat count the server never sent |

## `app.dom.test.ts`

Fixture: the **shipped** `task.html` body, injected into happy-dom minus its own `<script>` tag,
plus the **shipped** `en.json` served to `i18n.js`. Import order is load-bearing — `app.js`
self-schedules `boot()` only when `#app` exists, and the shell is injected after the imports have
been evaluated.

| Behaviour | Mutation that must make it fail |
| -- | -- |
| The shell really carries `#app`, `#modalRoot`, `#toastRoot`, `#a11yLive` and `template#brandLogo` | Remove one from `task.html` — without this row every test below would pass while testing nothing |
| Each decision is written out: `.hidden`, or `.is-refused` + `data-deny-reason` + the sentence; a hidden node explains nothing | Point `DENY_MESSAGE_KEY[DENY.PERSONAL]` at a key `en.json` lacks (the sentence assertion); render a refusal on the hidden branch (the hidden assertion) |
| Refusal shape per element: button `aria-disabled` and NOT `disabled`, input `readOnly`, select `disabled` | Set `el.disabled = true` for buttons in `refuseControl` — a screen-reader user could never reach the reason we just wrote |
| A later render releases a control fully | Delete any line of `releaseControl` — the control stays dead after the subscription that unlocked it |
| A `server` refusal survives a re-gate; a `gate` one does not | Drop the `{source: 'gate'}` argument from `applyDecision`'s `clearRefusal` calls |
| The server's sentence renders verbatim and as TEXT, and beats a message key | Assign through `innerHTML` in `renderRefusal` (the XSS half); prefer `messageKey` over `message` in `refusalText` (the verbatim half) |
| `applyGates` gates the root node itself | Delete the `nodes.unshift(scope)` line — `openModal` gates a root whose own child carries the gate |
| `isRefused` walks ancestors, and refuses a null element | Replace the `closest(...)` call with a `classList` check on the element itself |
| All five `data-i18n*` forms are applied | Drop a row from `I18N_ATTRIBUTES` — that attribute ships English to every locale silently |
| Hydration reaches inside the `<template>`, which a page-wide walk cannot see | Make `hydrateI18n` reject a `DocumentFragment` root, or drop `hydrateShell`'s explicit template pass |
| One 403 roster degrades one row; the other two teams keep their real `readable`/`admin` bits | Replace `Promise.allSettled` with `Promise.all` in `loadTeams` — `reloadOrganization()` rejects and the whole Team tab blanks on the roster the administrator was always going to be refused |
| `admin` comes from `members[].admin` for the acting user, never from organization administration | Set `admin: facts.orgAdmin` (or true for every readable team) in `loadTeams` |
| A 403 organization read means "no organization surface", not an error | **Corrected claim — the fourth wrong mutation sentence found here.** This row used to say "remove the fold in `api.getOrganization`". **Traced and false:** without the fold `getOrganization()` throws, `loadOrganization` catches it and sets `state.organization = null` anyway, so `orgAdmin` is still false, `teams` is still `{}` and the call count is still 1 — all three assertions stay green. The real mutation is in the DERIVATION the test pins: source `orgAdmin` from anything but the read returning 200 (e.g. `api.hasEditionClaim()`), and the fixture's `teams-cloud` token turns the Organization tab live for an account the server answered 403 for. The fold is now pinned by its own assertion — `getOrganizationError()` is null after a 403 — for which "remove the fold" IS the true mutation |
| A NON-403 organization failure stays an error and renders `organization.unavailable.*` with a retry | Delete the `organizationNotice()` term from `pageNotices()` — a 500 becomes byte-identical to the 403 that hides the tabs, and `getOrganizationError()` goes back to an export nothing reads. The paired negative (no notice for the 403) reddens on keying the notice off `state.organization === null` instead, which would show every non-administrator a failure for the ordinary answer |
| A refused GROUP refuses the form controls inside it, in the shape each element type announces | Delete the `querySelectorAll` loop from `refuseControl` — traced: nothing else reaches a descendant, because `applyGates` walks `[data-requires]` only. `pointer-events:none` and `isRefused()` still block the mouse and the handlers, so the field silently discards typing instead of announcing that it is refused. The `.refusal-text` half reddens on widening `REFUSABLE_DESCENDANTS` to `*` |
| A released group releases its descendants too — but NOT one that owns its own refusal | Delete the `querySelectorAll` loop from `releaseControl` (the release half); drop its `is-refused`/`data-deny-reason` skip (the ownership half, which would re-enable `rename-org` — a control refused in the markup for a route that does not exist — because an unrelated gate passed) |
| A node whose own gate passes stays refused inside a refused group | Delete the `parentElement.closest('.is-refused')` re-refusal from `applyDecision`'s enabled branch — document order means the group is decided first, so the release would strip the announcement it just made |
| A fork 409 renders the server's own sentence, `seats_needed` included, verbatim | Map the 409 to a `t()` key instead of to `err.serverMessage` |
| The commercial CI shape reports as *unavailable*; a spoken refusal is quoted; a bare status renders a SENTENCE and never the number | Map `not-json` onto the generic request-failed key (the CI half); restore `messageParams: {status}` and an `HTTP {status}` catalogue value (the status half — this is the blocker: `HTTP 403` was what an administrator who was not the organization administrator read after pressing Invite) |
| The refusal `outcome` reaches a sentence: `not_invitable`, `below_users`, `still_administrator` | Delete the `COMMERCIAL_REFUSAL.OUTCOME` branch from `describeCommercialRefusal` — every 200-with-refusal falls back to "That did not work. Nothing was changed.", which is true of every refusal and therefore names none of them. The server's own sentence still wins: moving the outcome lookup above the `message` branch reddens that half |
| `not_admitted` renders its nested `invitation_outcome`, which is the half that matters | Drop the `not_admitted` branch from `commercialOutcomeMessageKey` — an administrator is told the approval seated nobody and never told which of "buy more seats" and "that address belongs to another organization" it was |
| An unclassified outcome, and an inherited `Object.prototype` member, both fail closed | Default the lookup to any real key; replace `outcomeKey`'s `hasOwnProperty` guard with a bare index, after which `{outcome: 'constructor'}` hands a function to `t()` |
| Every enumerated bare status (401/402/403/404/409/5xx) renders a sentence, and NEVER the number | Empty `COMMERCIAL_STATUS_MESSAGE_KEY`. The `messageParams`/`not.toContain(status)` half is what stops the number coming back by any route, including a new key whose value interpolates it. The 403 sentence is asserted NOT to name the organization administrator: this function has no operation handle, so it cannot tell an organization-scoped 403 from an account-scoped one, and naming a cause on a coin-flip is worse than naming none |
| A bodiless FORK refusal gets its own sentence, from its own table | Point `describeForkError` at `COMMERCIAL_STATUS_MESSAGE_KEY` — its 403 says "the subscription service would not allow that", which names the wrong service for a refusal that came from the fork's managed gate. The two tables are separate because a bare 403 means different things on the two sides |
| Every `COMMERCIAL_OUTCOME_MESSAGE_KEY` value resolves to real English through `t()` | Rename any value without adding the key — `t()` returns the dotted path, and a refusal surface renders a raw key. The sibling assertion in `app.gating.test.ts` proves the key EXISTS; this one proves the loaded catalogue resolves it |

---

## Deliberately NOT tested, and why

* **"The avatar stays hidden without the provider call" (ruling C12).** `StoreUploadedAvatar`
  already sets `AvatarProvider = "upload"` and `avatar_provider` is in `baseUserUpdateColumns`,
  so that assertion would fail today. It would document a fiction, which §4 rates worse than no
  test. The second call is still made — for idempotence and for upstream drift, which is what
  `api.js` says at `saveAvatar`.
* **Anything Playwright** (ruling C7). Not against `/v1` — bar 9 forbids it and the environment
  is cross-origin — and not against fork routes either: nothing navigates to `/one/task.html`
  today, and funding a same-origin Playwright project would mean editing `playwright.config.ts`,
  an upstream file outside the patch surface.
* **`view-task.js` / `view-settings.js` render output.** They ship no `.d.ts`, and their contract
  with `app.js` (emit gated nodes, let `applyGates` decide) is asserted from the applier's side
  instead. Named here so the gap is a decision rather than an oversight.

  **This gap now has a named cost, recorded rather than left to be rediscovered.** Three
  behaviours live only in those modules and are therefore unguarded:

  1. **The two-write team rename.** `save-team` calls `api.renameTeam` then
     `api.renameTeamRootProject`; deleting either reddens nothing, because the covered helper
     (`renameTeamEverywhere`) has no caller. This is a *brief-mandated* correction with no test
     behind it, which is the worst shape on the list.
  2. **The affirmative branch below the guard.** `already_member` getting its own sentence and no
     pending row is bar 8's second half, and it is asserted only as a descriptor property in
     `api.commercial.test.ts`, never as what the page renders.
  3. **`describeCommercial`'s per-operation refinement of the `one.error.http` sentinel.**
     `app.dom.test.ts` pins the sentinel's identity and that its rendered value is prose rather
     than a status number, so the blocker stays closed either way — but that the 403 becomes "you
     do not administer this organization" for an organization-scoped call, and stays generic for
     an account-scoped one, is unasserted.

  Closing these needs a `view-settings.d.ts` and a mounted-shell harness for the two view
  modules. That is a layout addition beyond the declared set (ruling C15), so it is reported
  rather than smuggled in.
* **`boot()` end to end.** It is one-shot by design (i18n init, the formatters and the listeners
  are all one-shot) and re-entrancy machinery would be more code than the case it serves. Its
  parts — `initSession`, `initI18n`, `loadOrganization`, `loadTeams`, `applyGates` — are covered
  individually.
