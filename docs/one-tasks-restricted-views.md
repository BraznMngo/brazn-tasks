# ONE Tasks restricted views

The restricted views are a static page under `frontend/public/one/`, shipped for BRA-1357
(task detail) and BRA-1358 (subscription settings). They are plain ES modules and plain CSS:
no Vue, no framework, no build step. Vite copies `frontend/public/` into `dist/` verbatim
(no `publicDir` override in `frontend/vite.config.ts`) and `frontend/embed.go` embeds `dist/`
into the binary, so what is committed is byte-for-byte what is served.

The canonical URLs are

```
https://dev.tasks.brazn.one/one/settings.html          general entry point
https://dev.tasks.brazn.one/one/task.html?task=123     deep link to one task
```

These paths are deliberately unlike any route the Vue application owns, so the two can never
collide: the SPA has `/tasks/:id` and `/user/settings/general`, and this page has neither.

**Always with the filename.** `/one/` alone resolves to a directory inside the embed FS and
`pkg/routes/static.go:180-181` answers a directory with the SPA's `index.html` at HTTP 200.
The same fallback is why `/one/task.html` answers 200 with the SPA on any build that does not
carry this page yet — a missing file reaches `next(c)`, 404s, and is answered with the shell.

Settings and the task detail are **two documents**, not one document with a `?view=` switch:
`settings.html` is the general entry point and `task.html` is reachable only by deep link. Each
carries `data-default-view`, and they share one stylesheet, `one.css`. `?view=` still overrides,
but it is not what a link should carry.

Anything that links here —
Percy's `tauri_plugin_opener::open_url`, a mail template, a support article — must carry the
filename.

`?task=<id>` selects the task, `?view=task|settings` selects the view (defaulting to `task`
when `?task=` is present and `settings` otherwise), and `?tab=account|organization|team`
selects the settings tab. A missing or non-numeric `?task=` renders the settings view rather
than an error.

The page is never embedded in an iframe. That would make the refresh cookie third-party, and
it then fails silently for some users.

---

## The restricted-UI lockout — `brazn.restricteduionly`

One config key, `brazn.restricteduionly` (`VIKUNJA_BRAZN_RESTRICTEDUIONLY`), declared at
`pkg/config/config.go` beside the other `brazn.*` keys. **It ships `false`, and that default is
load-bearing rather than a placeholder:** every Playwright spec in this repository drives the
Vue SPA, so an instance that turned this on unintentionally would have no usable interface at
all for any user. Turning it on is a per-environment deploy decision, never a code one.

### Where it intercepts, and why that is the whole design

`serveIndexFile` is the function that injects the SPA config into `<div id="app"></div>`, and it
has exactly two call sites in `static()`: the not-found fallback (`pkg/routes/static.go:170`) and
the directory case (`:181`). The lockout replaces both with `braznServeAppShell`.

**That alone was not enough, and the earlier claim that it was — "the Vue SPA is delivered by
exactly one function" — was false.** `dist/index.html` is itself a real file in the embed FS, and
`static()` serves real files verbatim, so `GET /index.html` reached `serveFile` at HTTP 200
carrying the whole SPA without `braznServeAppShell` ever running. That document loads the hashed
`/assets/*.js` entry chunk, which is served normally, and the SPA's router is client-side, so
from there every route in the application worked. **One guessable URL defeated the entire
lockout.**

So the directory branch's condition consults a fork predicate as well:

```go
if info.IsDir() || braznBlocksAppShell(name) {
	return braznServeAppShell(c, assetFs)
}
```

`braznBlocksAppShell`, with the key on, blocks the root index exactly and any other `.html` under
`dist/` that is not under `dist/one/`. The second clause costs nothing today — Vite has a single
HTML entry and `frontend/public/` holds exactly one `.html`, the restricted page — and it is what
stops an `.html` dropped into `frontend/public/` later from silently reopening the hole. **The
`/one/` exemption is load-bearing:** without it the restricted page would be diverted, its
computed target would be itself, and the loop guard below would answer 404 — a locked-down
instance with no interface at all.

**`braznBlocksAppShell` returns false whenever the key is off**, which is why it is a function
rather than a comparison written inline in `static.go`. Inline and ungated, `/index.html` would go
through `serveIndexFile` on every instance, lockout or not — losing the ETag `serveFile` gives it
and gaining an injected config that upstream does not put there. With the key off this fork is
byte-identical to upstream.

The helper, the predicate and the target function live in `pkg/routes/static_brazn.go`, because
`static.go` is upstream-derived and every line we leave in it is re-resolved by hand on each
upstream merge. What it carries of this feature is **three changed lines**: the two calls, and one
extra term on the condition above the second of them.

Interception is **late**, after `next(c)` has already run. That is not an implementation detail,
it is the point:

| Request | Answer with the key on |
| -- | -- |
| `/api/…` | untouched — `static()` returns early for it (`static.go:140-142`) |
| any registered non-`/api` route: `/dav/*`, `/.well-known/*`, `/feeds/*`, `/health` | **untouched, the handler answers**, because `next(c)` runs before the fallback |
| `/one/task.html` and every asset under `/one/` | served normally, by the ordinary file path with its usual ETag and cache headers |
| `/index.html`, and any other `.html` in `dist/` outside `/one/` | **302 → `/one/task.html`** — this is the hole above, and it is the one row that is not "served normally" |
| `/favicon.ico`, `/robots.txt`, `/assets/*`, and every other real non-`.html` file in `dist/` | served normally, for the same reason as `/one/` |
| `/tasks/{id}`, `{id}` a bare run of digits | **302 → `/one/task.html?task={id}`** |
| anything else, including `/` and `/one/` itself | **302 → `/one/task.html`** |
| `/one/task.html` when that file is **missing** from the build | **404** — see the loop guard below |

Redirecting rather than 404ing keeps the deep links people already hold working, and as a side
effect gives the originally requested URL shape without a static file having to own the path.

An earlier revision intercepted *before* the file lookup and therefore had to name the
exceptions itself. It got them wrong: with the key on it redirected `/dav`, `/.well-known/*`,
`/health` and `/feeds/*` at the restricted page, which breaks every CalDAV client and blinds
monitoring. Intercepting late fixes that **by construction** rather than through an allowlist
somebody has to keep in step with `routes.go`.

**The invariant: while the key is on, this server never delivers the Vue SPA, for any path.** It
is carried by three things together — the not-found fallback, the directory branch, and
`braznBlocksAppShell` on that same branch — and not by `serveIndexFile` having a single caller,
which was the false premise the first two passes rested on.

### What the key does not do: an installed service worker keeps serving the SPA

**Flipping the key does not evict a service worker a browser has already installed.**
`frontend/src/sw.ts:15` precaches the build manifest, which contains `index.html`, and
workbox-precaching's `directoryIndex` defaults to `index.html`, so a navigation to `/` is answered
from the precache without reaching this server at all. `:18-22` additionally registers a
StaleWhileRevalidate route whose pattern includes `html`.

So the invariant above is a statement about **the server**, and it overstates what a config flip
achieves **operationally**. A browser that visited the instance before the flip keeps rendering
the Vue SPA for `/` from its own cache. Existing sessions need a service-worker update or a cache
reset; until one happens, those users are not locked out. This is not softened anywhere else in
this document: turning the key on is not, by itself, a way to take the SPA away from people who
already have it.

### The redirect-loop guard

Late interception introduces exactly one hazard, and it is guarded rather than tolerated. On a
build where `/one/task.html` is missing from `dist/` — CI's stub `dist/` is precisely that, and
so is a mis-built image — the request for that page falls through to `braznServeAppShell`, whose
target for it is *itself*:

```
GET /one/task.html
-> dist/one/task.html does not exist -> no route matches -> echo answers 404
-> static() takes its fallback and calls braznServeAppShell
-> the target computed for /one/task.html is /one/task.html
-> 302 Location: /one/task.html
-> the browser asks for it again, and again, forever.
```

So when the computed target equals the request's own cleaned path, the answer is **404**. A
missing page has to read as missing. This is the only request that can trip it: every other
target either differs from the cleaned path or carries a `?task=` query, which a cleaned path
never has. The loop is written out in the code comment so the guard is not deleted later as
paranoia.

Note what the guard does *not* do: `/one/anything-missing` is not 404ed by it, because its
target differs from it. That path redirects to `/one/task.html`, which a correct build then
serves.

### The redirect target

`/tasks/{id}` keeps its id, so a deep link somebody already holds still opens the task it named.
The id must be a **bare run of digits** rather than merely parseable: it is the only
caller-supplied text that reaches the `Location` header, and `/tasks/+1` would arrive at the page
as a space. `/tasks/-1`, `/tasks/12ab` and `/tasks/123/subtask` all fall back to the bare page.

### RESOLVED: authentication stays reachable while the lockout is on

This was an open defect — `/login` is a vue-router path, so the lockout redirected it to the
restricted page, which found no session and handed back to `/login`, forever. A locked-down
instance had no way in.

`braznServeAppShell` now serves the app shell for `/login`, `/register`, `/password-reset`,
`/get-password-reset` and anything under `/auth/` (the OIDC return), instead of redirecting them.
`TestStaticRestrictedUIKeepsAuthenticationReachable` covers all five.

**This is a deliberate, narrow hole in "the SPA is never delivered", and its size is worth
stating plainly.** Serving the shell at `/login` serves the whole application, because the router
is client-side — someone who signs in and then types a path can stay in it. The alternative was a
second sign-in form, which bar 4 forbids and which would be a worse answer anyway: another
credential surface to keep correct.

What the lockout still buys with the hole open: every *ordinary* way in is closed. The root, every
task path, every settings path and every deep link a user actually holds all land on the
restricted page, and a successful sign-in returns to `/`, which is redirected. The Vue application
stops being where people are sent and becomes somewhere they have to deliberately go.

## The two-API split

This is the single most common mistake on this project, so it is stated first and every path
in this document carries its prefix.

**There are two HTTP APIs on the same host and the same origin, in two different codebases.**

| Prefix | Service | What lives there |
| -- | -- | -- |
| `/api/v1/…`, `/api/v2/…` | the Vikunja fork, this repository | tasks, comments, labels, attachments, assignees, projects, user settings, teams, and the organization **read model** |
| `/v1/…` — **no `/api`** | the Percy commercial service, a separate codebase | invitations, seats, subscription, entitlements, account erasure, join requests |

### Why searching this repository never finds the commercial routes

Because they are not in it. `/v1/organizations/invitations` is served by Percy, whose source
is a different repository; nothing under `pkg/routes/` declares it and no amount of grepping
here will turn it up. **A `/v1` route that this repository cannot find is the expected
result, not a missing route.** The reverse mistake is just as easy: `/api/v1/user/password`
and `/api/v2/subscriptions/…` read like commercial paths and are not — they are fork routes.

Two consequences the page is built around:

1. **The commercial service must be addressed origin-rooted.** The shared axios instances in
   `frontend/src/` pin `baseURL` to the fork's `/api/v1`, so a relative `/v1/...` re-bases and
   silently becomes `/api/v1/v1/...`. This page does not use axios, but the same trap applies
   to any relative path, so `api.js` builds commercial URLs as
   `new URL('/v1/' + path, window.location.origin)`.
2. **`commercialV1Url()` is reimplemented locally.** The helper in
   `frontend/src/helpers/fetcher.ts` is on the unmerged PR #50, and importing from
   `frontend/src/` into this page is forbidden regardless — it would put the page itself inside
   the patch surface, and the page is deliberately outside it: a static file under
   `frontend/public/one/` edits no upstream file. The two-line `new URL(...)` is the whole
   dependency.

   (The change as a whole is **not** at zero patch surface, and the earlier framing that said so
   is corrected here. The restricted-UI lockout touches `pkg/config/config.go` and two lines of
   `pkg/routes/static.go`, and adds `pkg/routes/static_brazn.go` — that is CLAUDE.md section 3
   area 2, backend managed-mode enforcement, and it is inside the permitted surface by design
   rather than an exception to it. The **page** adds nothing to it; `frontend/src/` gains only
   the new test directory and appended `one.*` keys in `i18n/lang/en.json`.)

`brazn_managed_mode` on `/api/v1/info` and `braznManagedMode` in `stores/config.ts` are also
on PR #50. **The browser has no way to learn that the instance is managed today**, so the page
never asks: it renders what the server permits and surfaces what it refuses.

### Refusal shapes, and why `res.ok` is never enough

Managed-mode route classification (`pkg/routes/route-classification.json`) governs the fork:

| Classification | Answer |
| -- | -- |
| `managed: "disabled"` | bare **404** |
| `managed: "service-managed"` | **403** for everyone, including an instance admin |
| organization read, non-administrator | **403 — the ordinary answer.** Render nothing; this is not an error |
| organization read, anything else | **an error, and the only distinction this call draws.** 500, a network failure or an HTML error page renders the `organization.unavailable.*` notice with a retry, above whichever view is drawn |

That last row is load-bearing and was missing from the page for a round. `state.organization` is
null for a 403 *and* for a 500, and the Organization and Team tabs are gated on it being non-null
— so without the notice an administrator who hit a transient failure saw exactly the screen a
demoted account sees: two tabs silently gone, no banner, no toast, no retry. The tabs still
cannot be drawn (every control on them renders out of a payload that did not arrive), but the
page now says so instead of pretending the user is somebody else. The 403 path is unchanged and
stays silent.

The commercial service is different and is the reason bar 8 exists: **several `/v1` calls
return HTTP 200 and report failure in the body's `outcome`.** Worse, on any instance with no
Percy in front of it — which is exactly what CI is — a `/v1/...` request does not start with
`/api/`, so `pkg/routes/static.go:152-170` falls through to the app shell and answers with
the **SPA's `index.html` at HTTP 200 with `Content-Type: text/html`**. `res.ok` is `true` and
`res.json()` throws a `SyntaxError` that a naive `catch` reports as a network error.

`readCommercialResult` in `api.js` therefore takes the whole `Response` and requires **all
three** of:

1. `res.ok`, and
2. a JSON content type that actually parses, and
3. an affirmative `outcome` in the body.

Anything else — including an `outcome` value we do not recognise — is a refusal. It fails
closed, and it never throws: every commercial call resolves to a result object the UI renders
as a refusal surface next to the control.

Where the server gives us a sentence, we render **the server's own sentence verbatim** — the
409 body on team creation, the managed-gate refusal. A refusal is never paraphrased.

### What a refused control actually says, and where each sentence comes from

Reading the body correctly is only half of bar 8. The other half is turning what was read into a
sentence, and there are exactly four sources, tried in this order:

1. **The server's own words**, whenever the body carried any (`message ?? detail ?? title`).
   Ruling C4. This is the path the fork's 409 takes, and its server-computed `seats_needed` is
   the reason paraphrasing is forbidden — a translated guess could state a number the server
   would refuse.
2. **The refusal `outcome`**, for the commercial 200-with-failure. `COMMERCIAL_OUTCOME_MESSAGE_KEY`
   in `app.js` maps all ten declared refusal values — `not_invitable`, `invitation_expired`,
   `invitation_revoked`, `no_invitation`, `at_seat_ceiling`, `not_a_member`,
   `still_administrator`, `not_admitted`, `below_users`, `below_active_teams` — to a `t()` key,
   each cited to the union that declares it. A `not_admitted` decision reads its nested
   `invitation_outcome` first, because that is the half that distinguishes "buy more seats" from
   "that address belongs to another organization" (`percy-http-27c95232.ts:3251-3264`).
3. **The HTTP status**, for a bodiless refusal. `COMMERCIAL_STATUS_MESSAGE_KEY` covers 401, 402,
   403, 404, 409 and 5xx; the fork has its own separate table, because a bare 403 there is the
   managed gate rather than `not_administrator` and one shared sentence would be wrong on
   whichever side lost. **Every commercial sentence here is scope-neutral by design.** The
   describer is handed the result alone, with no operation handle, so its 403 cannot say "you
   are not the organization administrator": that is true of invite, removal and the join queue,
   and false of the account-scoped calls (erasure, the successor list). Naming a cause on a
   coin-flip is worse than naming none. A view that *knows* which operation it called may
   replace the key with a sharper one — every caller spreads the refusal object — but it may
   never fall back to the status.
4. **The generic sentence**, "That did not work. Nothing was changed." — true of every refusal by
   construction, and therefore the last resort rather than the first.

**No status code ever reaches a rendered string.** It used to: `one.error.http` was literally
`HTTP {status}`, so an administrator who was not *the* organization administrator pressed Invite
and read `HTTP 403` — on the instances where the commercial service is genuinely routed. Where it
is not (CI, and any instance without Percy in front of it) the content-type check produced the
graceful "we could not reach the subscription service" instead, so the readable sentence appeared
only in the environment that cannot actually run the call. That asymmetry is fixed in both
directions.

**One honest limitation, because it changes how source 1 should be read.** At `27c95232` **no
`/v1` route sends `message`, `detail` or `title` at all.** The commercial service has three body
writers — `json` (`percy-http-27c95232.ts:717`), `bare` (`:728`, a status line with no content
type) and `fail` (`:1778`) — and `fail`'s only JSON bodies are `{error: <code>}` for a
provisioning failure (`:1785`, whose comment says the optional message is "deliberately never
emitted"), the frozen `upgrade_required` shape at 402 (`:1795`), and `{error, debug}` behind an
off-by-default flag. So source 1 is currently unreachable on the commercial side and every
`/v1` refusal a user reads comes from source 2 or 3. The verbatim path stays in the code, and
stays first, because it is the correct rule and the service may start sending sentences; it is
just not, today, where any commercial sentence comes from.

---

## Every control, its endpoint, and its refusal shape

Controls that the gate disables rather than hides carry `data-deny-reason`, and the reason is
shown next to the control. Surfaces are hidden only when the whole surface is absent for that
user — the Organization and Team tabs when the organization read 403s. A missing affordance
reads as a bug; a disabled one carrying the server's own answer reads as an answer.

### Session

| Control | Method and path | Refusal shape |
| -- | -- | -- |
| Session refresh on load | `POST /api/v1/user/token/refresh` | 401 → single retry → hand off to the fork's existing login route |
| Instance info | `GET /api/v1/info` | the only call that sends no bearer |

`/api/v1` is used for the refresh **because the refresh cookie's `Path` is hardcoded to v1**
(`pkg/modules/auth/auth.go:60-72`); the browser never sends it to a v2 refresh, which
therefore always 401s. Every request uses `credentials: 'same-origin'` and never `omit`. The
access token lives in a module variable, never `localStorage`.

### Task view

| Control | Method and path | Refusal shape |
| -- | -- | -- |
| Read the task | `GET /api/v2/tasks/{id}?format=markdown` | one read, kept; there is no description-only endpoint and two reads can disagree |
| Save description | `PATCH /api/v2/tasks/{id}` with `X-Vikunja-Format: markdown` | `managed:"task-move"` refusal rendered inline |
| Title, done, due/start/end date, priority, progress, colour | `PATCH /api/v2/tasks/{id}` — **no** format header | as above |
| Delete task | `DELETE /api/v2/tasks/{id}` | inline refusal |
| Duplicate | `POST /api/v2/tasks/{id}/duplicate` | inline refusal |
| Move to project | `PATCH /api/v2/tasks/{id}` | disabled under Personal edition, with the generic `one.deny.personalEdition` sentence |
| Project picker (for the move) | `GET /api/v2/projects` | control disabled if the read fails |
| Add relation | `POST /api/v2/tasks/{task}/relations` | inline refusal |
| Remove relation | `DELETE /api/v2/tasks/{task}/relations/{kind}/{otherTask}` | inline refusal |
| Relation task search | `GET /api/v2/tasks?q=` | control disabled if the read fails |
| Subscribe / unsubscribe | `POST` / `DELETE /api/v2/subscriptions/{entity}/{entityID}` | inline refusal |
| List comments | `GET /api/v2/tasks/{task}/comments` | — |
| Add comment | `POST /api/v2/tasks/{task}/comments?format=markdown` | inline refusal |
| Edit comment | `PUT /api/v2/tasks/{task}/comments/{id}?format=markdown` | inline refusal |
| Delete comment | `DELETE /api/v2/tasks/{task}/comments/{id}` | inline refusal |
| Label search / create | `GET /api/v2/labels?q=` · `POST /api/v2/labels` | inline refusal |
| Task labels read / add / remove | `GET` · `POST /api/v2/tasks/{id}/labels` · `DELETE /api/v2/tasks/{id}/labels/{label}` | `{label}` is the numeric label id |
| Attachments list / upload | `GET` · `POST /api/v2/tasks/{task}/attachments` (multipart) | inline refusal |
| Attachment download | `GET /api/v2/tasks/{task}/attachments/{id}[?preview_size=]` | raw bytes, real MIME in `Content-Type` |
| Attachment delete | `DELETE /api/v2/tasks/{task}/attachments/{id}` | inline refusal |
| Assignees list / add / remove | `GET` · `POST /api/v2/tasks/{task}/assignees` · `DELETE /api/v2/tasks/{task}/assignees/{userId}` | inline refusal |
| Assignee picker search | `GET /api/v2/projects/{id}/users/search?q=` | control disabled if the read fails |

**`X-Vikunja-Format: markdown` goes on the description PATCH and on nothing else.** AutoPatch
is GET→merge→PUT inside one request, so any *other* PATCH carrying that header round-trips the
untouched description through the converter and silently corrupts it — no error, just a
description that degrades on an edit that never touched it. `patchTask` in `api.js` throws an
internal assertion rather than accept a `description` key, which makes "the header is on
exactly one PATCH" true by construction instead of by convention.

Note that `PATCH /api/v2/tasks/{id}` is classified `access-expanding`, `managed: "task-move"`
(`route-classification.json:363`). **Every task PATCH passes the task-move rule, not just an
explicit move**, so a refusal on an ordinary edit is an expected answer under Personal edition
and is surfaced rather than swallowed.

Ruling C8.4 asks only that Move be *disabled with a reason* under Personal, and it is — with the
same `one.deny.personalEdition` sentence every other Teams-gated control carries. A dedicated
`one.deny.taskMove` string exists in the catalogue and is **not wired**: the gating engine is
pure and decides from the token, not from which control carries it, so pointing one control at a
different sentence needs a per-control override the DOM contract does not have. That is a
deliberate non-change, not an oversight; the key is left in place because the wording is the
right one if the override is ever added.

### Settings — account

| Control | Method and path | Refusal shape |
| -- | -- | -- |
| Read user and preferences | `GET /api/v2/user` | preferences arrive in this call |
| Display name, timezone | `GET` → merge → `PUT /api/v2/user/settings/general` | **PUT replaces**, so the merge is mandatory |
| Timezone list | `GET /api/v2/user/timezones` | sorted client-side; documented unsorted |
| Avatar upload | `PUT /api/v2/user/settings/avatar` (multipart), **then** `PUT /api/v2/user/settings/avatar/provider` `{avatar_provider:"upload"}` | two calls; without the second the image is stored and never shown |
| Change email | `PUT /api/v2/user/settings/email` | visible under managed mode |
| Change password | `POST /api/v2/user/password` | visible under managed mode |
| Request data export | `POST /api/v2/user/export/request` | inline refusal |
| Download data export | `POST /api/v2/user/export/download` | returns a blob |
| Delete account | `POST /v1/account/erasure` | commercial `outcome` refusal |
| Successor picker (before deletion) | `GET /v1/account/successor-candidates` | commercial refusal; control disabled |
| Cancel scheduled deletion | `POST /api/v2/user/deletion/cancel` | works locally and is kept |

Email, password and TOTP are all **visible** under managed mode. An earlier note claiming
they are hidden was reversed by commit `f203aae6`.

Account deletion is the one control that moves between services. The local route
`POST /api/v2/user/deletion/request` is `service-managed` and answers 403, and the trap is
that `service.enableuserdeletion` defaults to true so `/api/v1/info` still advertises the
feature. The control therefore calls the commercial `POST /v1/account/erasure`. *Cancel
scheduled deletion* stays on the fork, because it works there.

### Settings — organization and teams

The whole surface is gated on `GET /api/v1/brazn/organization` returning 200. **A 403 is the
ordinary answer for anyone who is not the organization administrator**, and the Organization
and Team tabs are then simply absent. It is not an error and nothing is rendered about it.
Visibility here is *not* gated on edition.

| Control | Method and path | Refusal shape |
| -- | -- | -- |
| Organization read (and the seat meter) | `GET /api/v1/brazn/organization` | 403 → tabs hidden entirely; **anything else** → `organization.unavailable.*` notice with a retry |
| Team detail / roster | `GET /api/v2/teams/{id}` | **can 403**; that row degrades to disabled with `one.deny.rosterUnavailable` |
| Create team | `PUT /api/v1/brazn/organization/teams` `{name}` | **409 body rendered verbatim** when seats cap the team count |
| Rename team | `PATCH /api/v2/teams/{id}` **and** `PATCH /api/v2/projects/{projectId}` | two writes; one alone drifts |
| Add existing member | `POST /api/v2/teams/{team}/members` `{username, admin}` | disabled with `one.deny.personalEdition` under Personal |
| Remove member from team | `DELETE /api/v2/teams/{team}/members/{username}` | **username, not numeric id** |
| Invite by email | `POST /v1/organizations/invitations` | commercial `outcome` refusal |
| Organization display name | *no route anywhere* | rendered **read-only** with `one.deny.renameOrg` |

**Four rows were removed from the two tables above and are listed below instead.** They named
`GET /api/v2/teams`, `DELETE /api/v1/brazn/organization/teams/{id}`,
`POST /api/v2/teams/{team}/members/{username}/admin` and
`GET /api/v2/user/settings/avatar/provider` as shipped controls. **None of them has a control.**
No `data-action`, no button, and no caller in either view module — the wrappers exist in `api.js`
and nothing reaches them. The prototype has none of the four either, so under bar 10 the *page*
is right and the table was wrong; a table headed "every control, its endpoint, and its refusal
shape" that lists four controls that do not exist is worse than a shorter table.

`GET /api/v2/teams/{id}` can 403 because `Team.CanRead` requires membership and the
organization administrator is commonly *not* a member of the commercially-provisioned primary
team. The team reads are issued with `Promise.allSettled` rather than `Promise.all`, so one
403 degrades a single row instead of blanking the whole tab.

Only `PUT /api/v1/brazn/organization/teams` creates a team. The v1 and v2 team-create routes
are `service-managed` and answer 403.

The **required-seats rule is `seats_purchased >= 3 × (teams_used + 1)` and it ignores member
count.** The seat meter reads the fork's organization endpoint, not the commercial service.

The ratio in that rule is the payload's own `seats_per_team`, and **the page has no fallback for
it.** Not the prototype's `seats_per_team || 3`, where a legitimate 0 reads as 3, and not a
gentler `?? 3` either: when the field is absent the requirement is *unknown*, `requiredForNextTeam`
is null, and the view says `organization.teams.capped.unknown` — "we cannot read how many seats
you have bought" — which is true. Filling the gap from the page's own copy of the constant would
state a number the server never sent, on the one number a customer is asked to spend money
against. `SEATS_PER_TEAM` in `app.js` survives only as the value a drift warning compares the
server's against, and as the literal the tests pin the contract to. In practice the field is
never absent from a 200 (`pkg/models/brazn_organization.go:200` sets it unconditionally), which
is exactly why the null path must not guess.

Edition is decoded from the session JWT's `brazn_edition` claim
(`pkg/modules/auth/auth.go:183`): `personal-cloud` means Personal, and **anything else,
including the claim being absent, means the permissive Teams-shaped UI**. This matches
`frontend/src/composables/useManagedCapabilities.ts:26,62-66`, which is the fork's shipped copy
of the same decision; the constant is reimplemented locally rather than imported. Write
restriction comes from `brazn_write_restricted`, where **absence is the permitting case**.
Both are hints for drawing the UI. The server's managed gate is the real refusal, and it is
never trusted to the client.

---

## Commercial `/v1` routes that are live but not surfaced

`api.js` implements these because they are part of the commercial contract this page is
written against, but **no control on the page calls them.** They are listed so nobody
concludes the page forgot them:

`POST /v1/organizations/invitations/accept` · `POST /v1/organizations/members/removal` ·
`GET /v1/team-access-requests` · `POST /v1/team-access-requests/decide` ·
`POST /v1/team-access-requests/confirm` · `POST /v1/subscription/cancellation` ·
`POST /v1/subscription/auto-renewal` · `POST /v1/subscription/renewal-consent` ·
`POST /v1/checkout/resume` · `GET /v1/entitlements`

The prototype is the scope bar: what it leaves out is not wanted in this change.

### Fork routes that are implemented but not surfaced

Same rule, same reason, and these four used to be listed as shipped controls in the tables above:

| Method and path | Wrapper in `api.js` | Why there is no control |
| -- | -- | -- |
| `GET /api/v2/teams` | `listTeams` | the team list the page renders comes from the organization read, which carries `teams[]` already; a second list would be a second source that can disagree |
| `DELETE /api/v1/brazn/organization/teams/{id}` | `deleteOrganizationTeam` | the prototype has no delete-team control at all |
| `POST /api/v2/teams/{team}/members/{username}/admin` | the admin toggle | the prototype has no admin toggle; the roster renders the bit, read-only |
| `GET /api/v2/user/settings/avatar/provider` | `getAvatarProvider` | nothing on the page needs to read the provider back; the avatar upload SETS it (the second of the two calls) and the rendered avatar comes from the user payload |

Two commercial exports go further and are unreachable **by construction**, not by choice:
`confirmTeamAccessRequest` and `resumeCheckout` both check the percy.works relay's shared service
credential (`percy-http-27c95232.ts:3278` and `:2172`), so a user bearer is 401 unconditionally.
They are kept because they are part of the contract this page is written against, and because a
descriptor that is honest about the wire shape is better than one that pretends the call can
work. **This is the disposition of the "drop the export" recommendation their comments refer
to: it was considered and declined, deliberately, and the comments now point here rather than at
a recommendation nobody acted on.**

---

## Contract-only commercial routes

These five are **built against the documented contract and are not live.** The calls are
written and will work the moment the route lands; until then the commercial guard's
content-type check turns the SPA's `index.html` into an honest refusal instead of a fake
success. Nothing here was probed and nothing here may be reported as verified.

| Method and path | Body / query | Status |
| -- | -- | -- |
| `POST /v1/organizations/invitations/revoke` | `{organization_id, invitation_id}` | wired to the revoke control; service logic exists, route does not |
| `GET /v1/organizations/seats/quote` | `?organization_id=&seats=` | wired; no charge, same shape as the `seat_notice` that already renders on invite |
| `POST /v1/organizations/seats` | `{organization_id, seats, idempotency_key}` | wired to the add-seats control |
| `POST /v1/organizations/admin-transfer` | `{organization_id, to_user_id, idempotency_key}` | wired to the transfer control |
| `POST /v1/organizations/rename` | `{organization_id, organization_name, idempotency_key}` | **documentation only — deliberately not implemented** |

`from_user_id` on the admin transfer **is the resolved bearer and is never a body field.**
`transferAdministrator` in `api.js` takes no parameter for it, so a caller cannot send one.

Bodies are sent exactly as the contract states. Where a body is not documented, the call sends
what is documented and nothing more — an invented field is the kind of thing a strict
validator rejects with a 200 and a failure `outcome`, which is the hardest failure on this
page to debug. In particular `POST /v1/organizations/invitations` does **not** carry a
`team_id`; that field was invented and has been dropped.

`POST /v1/organizations/rename` is not implemented at all, and the absence of the export is
the mechanism rather than an oversight — see below.

**Three of these four are wired to live-looking, enabled buttons**, and that is a deliberate
position rather than an omission. `revoke-invite`, `add-seat`/`confirm-seats` and
`transfer-admin`/`confirm-transfer` all call a route that does not exist yet. What the user gets
today depends on the instance, and both answers are honest sentences:

* where the commercial service **is** routed, a bare 404 → "The subscription service does not
  have that. Nothing was changed.";
* where it is **not** (CI, and any instance with no Percy in front of it), the SPA shell at 200
  fails the content-type check → "We could not reach the subscription service, so nothing was
  changed."

Neither is `HTTP 404`, which is what they used to be, and neither claims a success. The
alternative — refusing all three in the markup, the way `rename-org` is refused — was considered
and not taken: `rename-org` has *no route anywhere and no service method*, which is a permanent
fact, while these three have working service logic and land the moment a handler does. A control
disabled today that silently starts working is a better failure than a control the page has to
be re-edited to re-enable. **If the route slips, revisit this**: the argument depends on
"shortly", and it is the one judgement here that a date could invalidate.

---

## What is deliberately not shipped

### Organization rename — no route exists anywhere

This is the only control on the page with no route on either service: no commercial route, no
service method, and `models.Organization` has no `Name` field at all. The field is therefore
**rendered read-only with a reason** rather than removed, because the contract says read-only
until `POST /v1/organizations/rename` lands, and because a field that is present and disabled
is testable — the negative test asserts that it renders disabled and issues no request.
`api.js` deliberately exports no rename function: exporting one would make that test
unwritable.

### Organization-level member removal — live, but out of scope

`POST /v1/organizations/members/removal` is live today and releases the seat. It is **not
surfaced**, because the prototype has no organization-level removal and the prototype is the
scope bar. The prototype's modal says, of the only removal it offers, that the person *remains
part of the organization and keeps access to any other teams they belong to*, and its button
reads *Remove from team*.

So the page ships **team-scoped removal only**, `DELETE /api/v2/teams/{team}/members/{username}`.
These are two different operations against two different services and they are not collapsed
into one control. Surfacing the organization-level one is a product decision and a new ticket.

### The successor picker shows an opaque id, and cannot do better from the browser

`GET /v1/account/successor-candidates` projects `{candidates: [{user_id}]}` and nothing else
(`percy-http-27c95232.ts:2986-2988`) — `AccountRecord` carries no name and no mailbox, which is
also why erasure genuinely destroys an address rather than leaving a copy behind. The page used
to join those ids against the organization roster to put a name on each row. **That join could
never match**, and removing it is the fix:

* the candidate id is the **commercial** account id (`percy-service-27c95232.ts:522` — "A
  commercial id, never the fork's");
* `Organization.Members[].user_id` is `u.ID`, **this instance's own row id**
  (`pkg/models/brazn_organization.go:478`), and `pkg/modules/auth/entitlement.go:193-195` states
  the distinction outright.

Stringifying both sides — which is what the code did, blaming a type difference — cannot make two
id spaces meet. The join missed on every row and each label already fell back to the id, so the
visible behaviour is unchanged; what is gone is code claiming a resolution it could not perform.
Worse in the tail: `opaqueID` admits a bare numeric string (`entitlement.go:191`), so a
coincidental collision would have put the **wrong person's name** against a stranger's id, on the
two most irreversible flows this page has.

**The real fix is a fork change and is outside bar 1, so it is reported and not built.** Nothing
the browser holds maps a commercial account id to a fork row: `Subject.UserID` — the fork's own
copy of the commercial id — is never put on a member row. Surfacing it on `OrganizationMember`
would let the picker show names. Until then a wrong name is worse than an opaque one.

### The Import cards — removed entirely, not just disabled

Both import flows commit against routes that answer 404 under managed mode. The CSV wizard is
the dangerous one: `detect` and `preview` **succeed first**, so a user completes two working
steps and is then told "Not Found". The whole **card** is removed, not just its final button —
two working steps followed by a refusal is a worse outcome than the feature being absent. The
Vikunja/Brazn ZIP import has no working half and is removed with it.

### The pasted-token input — removed

API-token provisioning is `managed: "disabled"` and answers 404 permanently, so no user can
ever mint a token to paste. The page uses the existing session cookie and the fork's existing
login route instead; it does not touch auth, and there is no `/one/login.html` or callback
page.

`parseJwt` is kept. It decodes the session token the page already holds in order to read the
edition and write-restriction claims — it is not reading a pasted token. The signature is
deliberately not verified client-side, because this is a hint for drawing the UI and not a
policy layer.

### Also removed from the prototype

The embedded `API_MAPPING` table and its validators (28 KB of design-time inventory, and the
only reason the page fetched the OpenAPI document); the 68 KB base64 logo, replaced by
`one/logo-light.v1.png` and `one/logo-dark.v1.png`; the demo/live duality and all 73 `isLive(`
branches; the prototype's control bar (backend URL, token field, task-id field, role switcher,
Validate API); the `back` stub, because the host app draws its own chrome; and the editor
toolbar with both `execCommand` calls — which removes the stored-XSS sink rather than
sanitising it.

The `.v1` in the logo filenames is load-bearing and permanent. `static.go:246` serves
`image/*` with `immutable`, which is never revalidated even with an ETag present, and the
service worker caches it a second time. A changed logo under an unchanged filename would never
reach an existing user. A new logo is a new version suffix and a matching edit in `task.html`,
always.

---

## Languages: what actually ships translated

The page negotiates **exact tags** over the six launch languages — `en`, `es-ES`, `de-DE`,
`fr-FR`, `zh-CN`, `ja-JP` — from `settings.language` first and `navigator.languages` second, and
falls back requested language → `en` → the key path. `'es'` never becomes `'es-ES'`: widening a
region is what produces a page half-translated into a locale nobody shipped.

The catalogues under `frontend/public/one/i18n/` are **trimmed copies of
`frontend/src/i18n/lang/*.json`**, carrying only the keys this page asks for. A key with no
translation in a language is omitted from that file rather than blanked, because the resolver
treats a missing key as "fall through to `en`" and a present-but-empty value as a bug.

**Stated plainly, because a reader will otherwise assume otherwise: the `one.*` namespace ships
in English only.** Those are new strings, and the project's rule — the ticket's, restated by
ruling C10 — is that new keys go into `frontend/src/i18n/lang/en.json` and reach the other
languages through the fork's translation process, not through this change.

**More than half of what a user reads on this page is English-only in the other five languages.**
As this change ships, 178 of the page's 316 distinct keys are `one.*`; the remaining 138 come
from upstream namespaces and are localised wherever upstream localised them. A German user
therefore sees the translated upstream strings (tasks, settings, teams and the whole
`organization.*` namespace) and English for everything this page added.

An earlier version of this paragraph said **sixty-eight**, which understated the untranslated
surface by more than a factor of two on the one paragraph whose entire job is to size it
honestly. The figure above is hand-counted and will drift; the *proportion* — over half — is the
durable claim, and the count is reproducible with the same sweep `fork-guards.yml` uses: every
quoted string in `frontend/public/one/*.{js,html}` anchored to a top-level namespace of
`public/one/i18n/en.json`, deduplicated. Do not update the number without recounting it.

The gap is wider still for `es-ES`, `fr-FR`, `zh-CN` and `ja-JP`, and **not because the trim
dropped anything**: `organization.*` exists upstream in `de-DE` alone. Those four have no
upstream translation of the organization surface to copy, so the page has none either. Machine
authoring it here would put unreviewed customer-facing copy into four languages, which is a worse
trade than an English sentence that is at least correct.

What CI does enforce is the direction that matters: every key the page uses resolves to a
non-empty string in `frontend/public/one/i18n/en.json`, and every `one.*` key in that trimmed
catalogue also exists in `frontend/src/i18n/lang/en.json` — both are steps in `fork-guards.yml`.
A key that exists in neither renders as its own dotted path to a customer, which is what happened
to `one.settings.deleteAccount.cancel` and `one.page.title` before those steps were believed.

## Testability: what is proven, and what is not

Stated plainly, because a green CI run on this page is much weaker evidence than it looks.

### Fork routes are covered by unit tests

`/api/v1` and `/api/v2` request construction, the refresh/retry state machine, the
`X-Vikunja-Format` placement, the snake_case preference reads, the seat formula and the i18n
fallback resolution are covered by Vitest specs under `frontend/src/brazn/one/`, all against
`vi.stubGlobal('fetch', …)`. They run in `test-frontend-unit`.

Tests must live under `frontend/src/` — `"test:unit": "vitest --dir ./src"` means a test placed
beside the page in `public/` is never discovered and never runs.

### `/v1` is not E2E testable, for three standing reasons

1. **CI runs managed mode off.** `pkg/config/config.go:530` sets `BraznManagedMode` to false by
   default, and nothing in `.github/` sets it — no workflow, job, step, `env:` block or
   service. Every managed-mode refusal the page is designed around (404 on `disabled`, 403 on
   `service-managed`, 403 on the organization read) therefore never occurs in CI.
2. **CI starts no commercial service.** There is no Percy in any job. And the failure is worse
   than absence: as described above, the fork answers an unrouted `/v1/...` with the SPA's
   `index.html` at **HTTP 200**, so an accidentally un-stubbed `/v1` call in a test would get a
   200 and could be mistaken for success.
3. **There is no CORS, and the E2E environment is cross-origin.** Playwright runs the frontend
   on `:4173` and the API on `:3456` with `VIKUNJA_CORS_ENABLE: 1` — the exact configuration
   this page does not work in, since it depends on being same-origin with both APIs for the
   session cookie, and the commercial service answers no CORS preflight at all.

**All `/v1` behaviour is therefore reported as covered by stubbed-fetch unit tests, never as
verified.** No `/v1` control may be called verified on the strength of a green CI run.

### There is no Playwright spec for this page at all

Not for `/v1`, and not for the fork routes either. Nothing navigates to `/one/task.html` in any
job, `playwright.config.ts` is an upstream file this change does not touch, and the E2E
environment is cross-origin for the reason above. **Every piece of automated evidence for this
page is a unit test or a fork-guards step.** A green CI run must not be read as browser
coverage.

### What green CI does prove

That the page is present and complete, that every module parses, that every relative specifier
resolves, that every catalogue is valid JSON, that every i18n key the page uses exists in its
catalogue, that nothing references another origin, that no shipped string calls the product
Vikunja, and that the request-construction and state-machine logic behave as specified against
stubbed fetch.

### What it does not prove

That the page renders. That any managed-mode branch is correct. That any `/v1` call works.
That caching behaves as read from the source. That a prefix deploy (`VIKUNJA_FRONTEND_BASE`
other than `/`) works — the Dockerfile default is `/` and no workflow overrides it. That the
service worker does not serve a stale copy — Playwright sets `serviceWorkers: 'block'`, so
none of that is exercised anywhere.

Two CI jobs are decorative here and must not be cited as evidence: `frontend-typecheck` runs
`continue-on-error: true`, and `frontend-lint` and `frontend-stylelint` glob `src/**` only, so
they never see this page.

### The hand-written `.d.ts` files are enforced by nothing — REPORTED, NOT FIXED

Ruling C5 chose sibling declaration files (`public/one/{api,i18n,app}.d.ts`) over
`@ts-expect-error`, and that choice is real: the tests are plain `.test.ts` with no suppression
anywhere. **What is not real is any check that the declarations agree with the modules.**

`frontend/tsconfig.app.json` sets `"composite": true`, and with `composite` TypeScript requires
every file in the program to be matched by `include`. Its patterns are `env.d.ts`,
`env.config.d.ts`, `src/**/*.d.ts`, `src/**/*`, `src/**/*.vue`, `src/**/*.json` and
`tailwind.config.js` — **nothing under `public/`**. The test files *are* included (`src/**/*`,
and the only `exclude` is `src/**/__tests__/*`), and they import `../../../public/one/api.js`,
which resolves to `public/one/api.d.ts`. That is a program file outside every include pattern,
which is **TS6307**. `tsconfig.vitest.json` extends the same config with `"exclude": []`, so it
inherits the condition. No pre-existing `src/` file imports from `public/`, so this path is new
with this change.

It does not turn CI red — `frontend-typecheck` is `continue-on-error: true`, `vite.config.ts`
declares no `typecheck` block for Vitest, and eslint globs `src/**` only. **That is the finding**,
not the error itself: the type mechanism the ruling chose is inert, so a `.d.ts` that drifts from
its module is caught by nobody.

**This was reasoned, not executed** (CLAUDE.md §1 — nothing runs on the authoring host). The fix
is one line — add `"public/one/*.d.ts"` to `tsconfig.app.json`'s `include` — but
`tsconfig.app.json` is an upstream file and is outside the layout ruling C15 declares, so it is
reported here rather than edited. Confirm with a single `pnpm typecheck` before taking it.

### Real verification is manual

`/v1` behaviour is confirmed by hand on `dev.tasks.brazn.one`, signed in as a real user
against a real Percy, at the canonical URL with the filename. That is the only place the
commercial service, managed mode and same-origin credentials all exist at once.

---

## Deferred accessibility debt

Shipped now, because each is small and load-bearing:

* `#a11yLive` — a single `role="status" aria-live="polite"` region. Toasts are the only report
  of every write result **and** every failure, and bar 8 makes failure reporting load-bearing,
  so `app.js` mirrors each toast into it. The prototype's toast had no `aria-live` at all.
* `:focus-visible` — one CSS rule, `outline: 2px solid var(--primary)`.
* Disabled controls use `aria-disabled="true"` on buttons rather than the `disabled` attribute,
  because a `disabled` button is not focusable and a screen-reader user could never reach the
  refusal reason written next to it. Inputs and textareas use `readOnly` (still focusable,
  still announced); selects use `disabled`, because `readOnly` does not exist on a select.
* **A refused GROUP reaches its own form controls.** `data-requires` frequently sits on a wrapper
  — the inline label line is a `<div>` with the input inside it — and marking only the wrapper
  left that input announced as *editable*. Nothing was ever written: the stylesheet's
  `pointer-events:none` stops a mouse and the handler guard walks ancestors, so both the click
  and the Enter paths were already inert. But `pointer-events` is not an accessibility API, and
  a keyboard or screen-reader user could reach the field, type into it, and watch the typing be
  discarded with no explanation — which is the worst shape a refusal can take. The applier now
  recurses into every `input`, `textarea`, `select` and `button` inside a refused node and marks
  each in the shape its element type can announce. Headings, prose and the `.refusal-text` itself
  are deliberately untouched: the sentence explaining the refusal must never be marked
  unavailable. Releasing a group reverses all of it, except for a control that carries its own
  `data-deny-reason` — that refusal belongs to the view, not to the gate.
* The two hidden file pickers (`#attachmentInput`, `#avatarInput`) carry `tabindex="-1"`, which
  keeps two unnamed controls out of the tab order. A focusable control with no accessible name
  is worse than one that cannot be reached.

Deferred, enumerated precisely, each with its severity:

| Item | Severity |
| -- | -- |
| `<label for>` on 42 controls | high |
| `role="dialog"`, `aria-modal`, focus trap and focus restore on 24 modals | high |
| `aria-label` on 4 icon-only button kinds | medium |
| tablist / tab / tabpanel semantics on the 3 tab strips | medium |
| menu semantics and outside-click close on the popover | medium |
| focus preservation across a re-render | medium |
| contrast audit at font sizes ≤ 11 px (needs tooling) | medium |
| `aria-expanded` on `.disclosure` | low |
| `role="progressbar"` on `.seat-meter`, `aria-valuetext` on `#progress` | low |
| a real `<h1>` and a corrected heading order | low |
| `prefers-reduced-motion` | low |

The modal close button did get the accessible name it never had, because that one is a single
attribute. The rest of the dialog semantics stay deferred together, since a focus trap without
`role="dialog"` is half a fix.

### Right-to-left is out of scope, deliberately

`ar-SA`, `fa-IR` and `he-IL` are **not** enabled. Layout uses logical properties throughout so
a future RTL pass is mechanical, but three physical properties remain and would each be wrong
under RTL: the `.select` caret's `background-position` and `padding-right`, and the
`object-position` and `translateX` uses recorded in `i18n.js`. Enabling an RTL language before
those are converted would ship a visibly broken layout.

### Button labels

Buttons flex and keep their label on one line — no fixed widths and no truncation of button
labels. German is the long case and it is the one that breaks fixed-width buttons. One residual
risk is recorded rather than papered over: no CSS length can measure a *placeholder*, so a long
translated placeholder can still overflow its input.

---

## Language coverage on day one

Launch languages are EN, ES, DE, FR, ZH and JA, with trimmed catalogues under
`frontend/public/one/i18n/`. Every user-facing string goes through `t()`; none is hardcoded.

The fallback chain is **requested language → `en` → the key path itself**, resolved *per key*
rather than per catalogue, with a missing intermediate object treated the same as a missing
leaf. `en.json` is a hard dependency, not a fallback: if it fails to load, the page cannot
render text and that is a fatal state.

Coverage of the upstream catalogues is uneven and this is expected, not a defect: `es-ES` is
missing roughly half the keys, and the fork's own `organization` tree exists only in `en` and
`de-DE`. Every seat, member, team and billing string will render in English for ES, FR, ZH and
JA users at launch. That is the fallback working as designed.

`fork-guards.yml` asserts that every key the page passes to `t()` resolves to a non-empty
string in `frontend/public/one/i18n/en.json`. A key that exists nowhere does not throw — `t()`
returns the key path, and the customer reads `one.settings.deleteAccount.cancel` in a button.
That degrades only at runtime, on a page no browser-driven job ever opens, which is exactly the
silent failure CLAUDE.md section 4 exists to stop.

## Where this page's strings live, and why not in the app catalogue

**The page's strings live only in `frontend/public/one/i18n/*.json`.** They are deliberately
**not** duplicated into `frontend/src/i18n/lang/en.json`, and that file is byte-identical to
`brazn/main` on this branch.

BRA-1358 says new user-facing strings go through `frontend/src/i18n/lang/en.json`. That rule
was written for a Vue implementation and is structurally incompatible with the shape actually
shipped, which CI proved rather than argued:

* upstream's `check-translations` job fails on **dead keys** — anything present in
  `frontend/src/i18n/lang/en.json` but not referenced by code it scans;
* it scans `frontend/src`, and this page's code is under `frontend/public/one/`;
* so every key added for this page is dead by that definition. Adding them produced
  **175 dead-key failures**, one per string.

There is no configuration that satisfies both without editing an upstream script, which the
patch surface does not permit.

**The consequence, stated plainly rather than discovered later:** this page's strings do not
flow through the Vue app's translation pipeline. The six catalogues under
`frontend/public/one/i18n/` are committed, complete for the launch languages, and are the
artifact a translator edits. A Crowdin sync will neither update nor overwrite them.

A guard step asserting the opposite (`Every one.* string … is in the app catalogue too`) was
written and then removed, because it enforced the incompatible half of the rule. Its intent —
no string reaching a customer untranslated — is served instead by the guard that asserts every
key the page calls exists in its own `en.json`.
