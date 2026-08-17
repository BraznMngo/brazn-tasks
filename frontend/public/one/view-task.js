/**
 * ONE Tasks restricted views — the Task Details view (BRA-1357).
 *
 * Plain ES module, no framework, no build step: Vite copies `public/` verbatim, so every import
 * below has to be resolvable by the browser exactly as written.
 *
 * WHAT THIS FILE IS. It is the `task` view registered with `app.js`, and nothing else. It renders
 * the markup for `#app`, owns the task-view actions, and holds the loaded task in the per-view
 * scratch `app.js` hands out. It boots nothing, decides no gate and formats no date of its own —
 * `app.js` owns all three (see `app.d.ts`, "THIS IS ALSO THE VIEW MODULES' CONTRACT").
 *
 * PORTED FROM THE PROTOTYPE, which is the authority on design, layout and scope (bar 10):
 * `taskHeader()` 1002-1011, `repeatBuilder()` 1019-1026, `taskProperties()` 1028-1040,
 * `taskContent()` 1045-1052, `resourcesPanel()` 1053-1059, `commentsPanel()` 1060-1063,
 * `morePopover()` 1078-1084, and the five task modals 1143-1146 / 1216. Every `isLive()` branch
 * collapses to the live arm; the demo arm, the role switcher and the editor toolbar are gone.
 * `taskSummary()` (1064-1074) is NOT ported: it is dead in the pristine file — defined, never
 * called — and dead code renders nothing, so bar 10 covers it (SPEC-UI DISPUTES 4).
 *
 * IMPORT-TIME PURITY, same contract as api.js and app.js. Importing this module registers the
 * view and its actions and does nothing else: no fetch, no DOM read, no listener. The non-click
 * listeners this view needs (`change`, `input`, `blur`, and the file picker) are installed on the
 * first `mount()`, which only ever runs against a real shell.
 *
 * NO HARDCODED USER-FACING STRINGS. Every one is `t('key')` or a value off the wire. The two
 * exceptions are marked where they occur and are both `aria-hidden` icon glyphs, never read out
 * and never translated.
 */

'use strict';

import * as api from './api.js';
import {t, currentLocale} from './i18n.js';
import {
  DENY,
  applyGates,
  readGateFacts,
  registerActions,
  registerView,
  requestRender,
  getViewState,
  setViewState,
  getUser,
  editionMessageKey,
  formatDateTime,
  formatNumber,
  openModal,
  closeModal,
  toast,
  renderRefusal,
  describeForkError,
  isRefused,
} from './app.js';

/*
 * `t` and `currentLocale` come from i18n.js rather than app.js because app.js exports neither —
 * it re-exports no string layer at all (app.d.ts). SPEC-UI §5.8 puts inline `t('key')` calls in
 * the module that builds the dynamic markup, which is this one, so the import is the contract
 * rather than a hole in it. Nothing else outside app.js and api.js is imported.
 */

/** The per-view scratch namespace. `app.js` never reads inside it (app.js §2). */
const NS = 'task';

/* ------------------------------------------------------------------ *
 * 1. Wire vocabularies — value in, `t()` key out
 * ------------------------------------------------------------------ */

/**
 * Priority is an int on the wire, 0..5 (pkg/models/tasks.go:86, and the prototype's
 * PRIORITY_TO_INT at line 566). The catalogue holds the display names; the integers are
 * identifiers and are never shown, which is ruling C10's rule for every wire value on this page.
 */
const PRIORITIES = Object.freeze([
  [0, 'task.priority.unset'],
  [1, 'task.priority.low'],
  [2, 'task.priority.medium'],
  [3, 'task.priority.high'],
  [4, 'task.priority.urgent'],
  [5, 'task.priority.doNow'],
]);

/**
 * Repeat units. `seconds` is what `repeat_after` takes (pkg/models/tasks.go:82).
 *
 * Months and years are the prototype's approximations (30 and 365 days, lines 716-722) and are
 * kept rather than corrected: the server has no calendar-month interval except `repeat_mode: 1`,
 * which `repeatPayload` below emits for the one case that maps onto it exactly (every 1 month,
 * default mode). Inventing a truer encoding would be inventing a field.
 */
const REPEAT_UNITS = Object.freeze([
  ['days', 86400, 'task.repeat.days', 'one.task.repeat.unitDays'],
  ['weeks', 7 * 86400, 'task.repeat.weeks', 'one.task.repeat.unitWeeks'],
  ['months', 30 * 86400, 'one.task.repeat.months', 'one.task.repeat.unitMonths'],
  ['years', 365 * 86400, 'one.task.repeat.years', 'one.task.repeat.unitYears'],
]);

/** The three quick presets the prototype offers (1022). Values, not labels. */
const REPEAT_PRESETS = Object.freeze([
  [1, 'days', 'task.repeat.everyDay'],
  [1, 'weeks', 'task.repeat.everyWeek'],
  [30, 'days', 'task.repeat.every30d'],
]);

/**
 * The five reminder choices (prototype 1143 / `reminderPayloadFromChoice` 707-715), keyed on a
 * stable token rather than on the English label the prototype switched on — a `switch` over
 * display copy breaks the moment the copy is translated, which is exactly what this page does.
 *
 * `relative_to` values are the server's (pkg/models/task_reminder.go:40-42).
 */
const REMINDER_CHOICES = Object.freeze([
  ['hour-before-due', 'one.task.reminders.hourBeforeDue'],
  ['day-before-due', 'one.task.reminders.dayBeforeDue'],
  ['at-due', 'one.task.reminders.atDueTime'],
  ['day-before-start', 'one.task.reminders.dayBeforeStart'],
  ['custom', 'one.task.reminders.customDateTime'],
]);

/** `relative_to` -> the catalogue name for that date. */
const REMINDER_RELATION_KEY = Object.freeze({
  due_date: 'one.task.reminders.typeDue',
  start_date: 'one.task.reminders.typeStart',
  end_date: 'one.task.reminders.typeEnd',
});

/**
 * The task colours, prototype `TASK_COLOR_HEX` (570). The hex is the wire value of `hex_color`,
 * WITHOUT the leading '#' (pkg/models/tasks.go:96).
 */
const TASK_COLORS = Object.freeze([
  ['', 'one.common.none'],
  ['3a7afe', 'one.task.color.blue'],
  ['6c5ce7', 'one.task.color.purple'],
  ['ff4d4f', 'one.task.color.red'],
  ['23c16b', 'one.task.color.green'],
]);

/**
 * The prototype's icon set, trimmed to the fourteen the task view actually draws (ICON, 510-540).
 * `ICON.back` is gone with the `back` stub — the host app draws its own chrome.
 */
const ICON = Object.freeze({
  check: '<path d="M5 12.5 10 17l9-10"/>',
  chevron: '<path d="m8 10 4 4 4-4"/>',
  user: '<circle cx="12" cy="8" r="3"/><path d="M5 19a7 7 0 0 1 14 0"/>',
  flag: '<path d="M5 21V5"/><path d="M5 6h10l-1.5 3L15 12H5"/>',
  progress: '<circle cx="12" cy="12" r="9"/><path d="M12 3v9h9"/>',
  calendar: '<rect x="4" y="5" width="16" height="15" rx="2"/><path d="M8 3v4M16 3v4M4 10h16"/>',
  repeat: '<path d="M17 2l4 4-4 4"/><path d="M3 11V9a3 3 0 0 1 3-3h15"/><path d="M7 22l-4-4 4-4"/><path d="M21 13v2a3 3 0 0 1-3 3H3"/>',
  more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13M10 11v5M14 11v5"/>',
  bell: '<path d="M6 16v-5a6 6 0 1 1 12 0v5l2 3H4z"/><path d="M10 20a2 2 0 0 0 4 0"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v5h5"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  close: '<path d="M6 6l12 12M18 6 6 18"/>',
  upload: '<path d="M12 15V3M8 7l4-4 4 4"/><path d="M5 20h14"/>',
});

const ic = (name) => `<svg viewBox="0 0 24 24" aria-hidden="true">${ICON[name] ?? ''}</svg>`;

/**
 * The three popover glyphs the prototype uses in place of icons (1082): favourite, duplicate and
 * task colour. They are DECORATIVE and `aria-hidden`, exactly as `ic()` marks its SVGs, so they
 * carry no meaning a screen reader loses and nothing to translate. Kept rather than replaced with
 * new artwork, which would be a redesign (bar 10).
 */
const GLYPH = Object.freeze({favorite: '☆', duplicate: '⧉', color: '◉'});

const glyph = (name) => `<span aria-hidden="true">${GLYPH[name] ?? ''}</span>`;

/* ------------------------------------------------------------------ *
 * 2. Escaping and small formatters
 * ------------------------------------------------------------------ */

/**
 * Every value that reaches the markup goes through here. The task, its comments and its
 * attachment filenames are user-authored content from a different codebase's database; the
 * prototype's `esc()` (542) is the same four replacements plus the apostrophe, which is added
 * because attribute values below are written with double quotes and a stray `'` in a filename
 * would otherwise survive into an `aria-label` unescaped.
 */
function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

/**
 * A `<input type="date">` value from a wire timestamp.
 *
 * The Go zero time is `0001-01-01T00:00:00Z` and means UNSET, not "the year 1" — the fork sends
 * it for every date the user never filled in. The prototype's `dateInput` (607) has no guard for
 * it and paints `0001-01-01` into the due-date field of every task with no due date, so the guard
 * is added here rather than ported. Same test as `app.js`'s `applyFormat`.
 */
function dateInputValue(value) {
  if (value === null || value === undefined || value === '') return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '';
  return date.toISOString().slice(0, 10);
}

/**
 * A date-input value back to a wire timestamp, prototype `dateIso` (608).
 *
 * Midday, not midnight: a date picked as 2026-03-01 in a UTC-13 zone is 2026-02-28 at midnight
 * UTC, and the user would watch their due date move a day every time the page reloaded.
 */
function dateInputToIso(value) {
  return value ? new Date(`${value}T12:00:00`).toISOString() : null;
}

/** Two initials for the avatar circle, prototype `initials` (597). */
function initials(person) {
  const name = String(person?.name || person?.username || '');
  const letters = name.split(/\s+/).filter(Boolean).map((part) => part[0]).join('');
  return (letters.slice(0, 2) || '?').toUpperCase();
}

/** A user's display name, falling back to the username the fork always sends. */
function personName(person) {
  return String(person?.name || person?.username || '');
}

/**
 * A v2 list envelope to an array. Every `/api/v2` list operation answers `{items, total, page,
 * per_page, total_pages}` (pkg/routes/api/v2/types.go:27-33); a few fields on the task itself are
 * bare arrays. Prototype `asItems` (606), minus its adapter-shaped `body` cases.
 */
function items(payload) {
  if (Array.isArray(payload)) return payload;
  if (Array.isArray(payload?.items)) return payload.items;
  return [];
}

/**
 * A file size for the attachment row's meta line.
 *
 * `Intl.NumberFormat`'s unit style rather than a `t()` key: the unit name and its placement are
 * locale data, not copy, and there is no size key in the catalogue to reach for. Falls back to
 * the bare number if the runtime has no unit support, which is a number in every language.
 */
function fileSize(bytes) {
  if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes <= 0) return '';
  const kilobytes = Math.max(1, Math.round(bytes / 1024));
  try {
    return new Intl.NumberFormat(currentLocale(), {
      style: 'unit', unit: 'kilobyte', unitDisplay: 'short',
    }).format(kilobytes);
  } catch {
    return formatNumber(kilobytes);
  }
}

/**
 * Join the parts of a meta line with the catalogue's own separator, dropping the empties so a
 * file with no mime type does not render a leading bullet.
 */
function joinMeta(parts) {
  return parts.filter((part) => part !== '' && part !== null && part !== undefined)
    .join(t('one.common.separator'));
}

/* ------------------------------------------------------------------ *
 * 3. Reading the task payload
 * ------------------------------------------------------------------ */

/**
 * The project name for the kicker chip.
 *
 * Three sources, in falling order of truth: the projects list when it loaded, the project half of
 * the task's own `identifier` (`PROJ-12` — pkg/models/tasks.go:475-480), and finally the numeric
 * project id. The prototype's third fallback is the literal `'Project ' + id` (747), which is an
 * untranslatable English string; the bare id is the same information with nothing to translate.
 */
function projectLabel(state) {
  const id = state.task?.project_id;
  const found = state.projects.find((project) => String(project?.id) === String(id));
  if (found) return String(found.title ?? '');
  const identifier = String(state.task?.identifier ?? '');
  const dash = identifier.lastIndexOf('-');
  if (dash > 0) return identifier.slice(0, dash);
  return typeof id === 'number' ? formatNumber(id) : '';
}

/**
 * The bucket the task sits in, when the read asked for it. `bucket_id` is only populated when the
 * task is reached through a view with buckets (pkg/models/tasks.go:134-136), so an empty string
 * here is ordinary and the kicker simply omits the segment.
 */
function bucketLabel(task) {
  const buckets = Array.isArray(task?.buckets) ? task.buckets : [];
  const current = buckets.find((bucket) => String(bucket?.id) === String(task?.bucket_id));
  return String(current?.title ?? buckets[0]?.title ?? '');
}

/** `identifier` is the server's own display form and already carries its `#` or project prefix. */
function taskIdentifier(task) {
  const identifier = String(task?.identifier ?? '');
  if (identifier !== '') return identifier;
  const index = task?.index ?? task?.id;
  return typeof index === 'number' ? `#${formatNumber(index)}` : '';
}

/**
 * `repeat_after` seconds + `repeat_mode` -> the builder's three controls. Prototype `loadRepeat`
 * (729-738), with its state mutation replaced by a returned value.
 *
 * `repeat_mode: 1` is the calendar-month mode and ignores `repeat_after` entirely
 * (pkg/models/tasks.go:84), so it reads back as "every 1 month" and nothing else.
 */
function readRepeat(task) {
  const mode = Number(task?.repeat_mode) || 0;
  const seconds = Number(task?.repeat_after) || 0;
  if (mode === 1) return {active: true, mode: 'default', every: 1, unit: 'months'};
  if (seconds <= 0) return {active: false, mode: 'default', every: 1, unit: 'days'};
  const named = mode === 2 ? 'current' : 'default';
  if (seconds % (7 * 86400) === 0) return {active: true, mode: named, every: seconds / (7 * 86400), unit: 'weeks'};
  if (seconds % 86400 === 0) return {active: true, mode: named, every: seconds / 86400, unit: 'days'};
  return {active: true, mode: named, every: Math.max(1, Math.round(seconds / 86400)), unit: 'days'};
}

function repeatSeconds(repeat) {
  const unit = REPEAT_UNITS.find((entry) => entry[0] === repeat.unit) ?? REPEAT_UNITS[0];
  return Math.max(1, Number(repeat.every) || 1) * unit[1];
}

/** The builder's three controls -> the two wire fields. Prototype `repeatPayload` (724-728). */
function repeatPayload(repeat) {
  if (!repeat.active) return {repeat_after: 0, repeat_mode: 0};
  if (repeat.unit === 'months' && Number(repeat.every) === 1 && repeat.mode === 'default') {
    return {repeat_after: 0, repeat_mode: 1};
  }
  return {repeat_after: repeatSeconds(repeat), repeat_mode: repeat.mode === 'current' ? 2 : 0};
}

/** The interval as a sentence fragment: "2 days", "1 month". */
function repeatIntervalText(repeat) {
  const unit = REPEAT_UNITS.find((entry) => entry[0] === repeat.unit) ?? REPEAT_UNITS[0];
  const count = Math.max(1, Number(repeat.every) || 1);
  return t(unit[3], {count});
}

/**
 * One reminder as a sentence.
 *
 * The relative form is composed the way the fork's own `ReminderDetail.vue` (:210-249) composes
 * it — `task.reminder.before` / `.after` with the unit and the date name interpolated — with one
 * difference forced by the trimmed catalogue: our unit keys (`one.task.reminders.unitDays`) carry
 * `{count}` inside the value, whereas the fork's `time.units.*` are bare and take `{amount}`
 * alongside. `time.units.*` was not trimmed into this page's catalogue, so `{amount}` is passed
 * empty and the resulting double space is collapsed. Collapsing whitespace is safe on these two
 * frames and is confined to them; it is not applied to any other translated value.
 */
function reminderText(reminder) {
  const relativeTo = String(reminder?.relative_to ?? '');
  const relationKey = REMINDER_RELATION_KEY[relativeTo];
  if (relationKey !== undefined) {
    const period = Number(reminder?.relative_period) || 0;
    const type = t(relationKey);
    if (period === 0) return t('one.task.reminders.atType', {type});
    const absolute = Math.abs(period);
    const unitKey = absolute % 86400 === 0
      ? 'one.task.reminders.unitDays'
      : absolute % 3600 === 0 ? 'one.task.reminders.unitHours' : 'one.task.reminders.unitMinutes';
    const divisor = absolute % 86400 === 0 ? 86400 : absolute % 3600 === 0 ? 3600 : 60;
    const count = Math.max(1, Math.round(absolute / divisor));
    const unit = t(unitKey, {count});
    const frame = period < 0 ? 'task.reminder.before' : 'task.reminder.after';
    return t(frame, {amount: '', unit, type}).replace(/\s+/g, ' ').trim();
  }
  const absolute = formatDateTime(reminder?.reminder);
  return absolute !== '' ? absolute : t('one.task.reminders.fallback');
}

/**
 * `related_tasks` is a map of relation kind -> tasks (pkg/models/task_relation.go:107-109).
 * Flattened into rows, each carrying the two path segments its DELETE needs.
 */
function relationRows(task) {
  const map = task?.related_tasks;
  if (map === null || typeof map !== 'object') return [];
  const rows = [];
  for (const [kind, related] of Object.entries(map)) {
    for (const other of related ?? []) {
      rows.push({
        kind,
        otherId: other?.id,
        title: [taskIdentifier(other), String(other?.title ?? '')].filter(Boolean).join(' '),
      });
    }
  }
  return rows;
}

/**
 * A relation kind's display name. Singular: one row names one task.
 *
 * ELEVEN LITERAL KEYS, NOT ONE TEMPLATE LITERAL. Ruling C10 says in as many words that if a `t()`
 * call is dynamic, make it non-dynamic — the fork-guards key step proves keys exist by sweeping
 * quoted namespace-anchored strings out of the source, and a key assembled at runtime is invisible
 * to it. Its template-literal fallback can only check that the PARENT object exists, so a single
 * renamed leaf would have degraded to a raw key path on screen with CI green. The wire values are
 * `api.RELATION_KINDS` (pkg/models/task_relation.go:89) and the set is closed, so writing them out
 * costs nothing and buys per-leaf coverage.
 *
 * An unrecognised kind returns the wire value rather than `t()`'s key-path fallback: a server that
 * grows a twelfth relation kind should show its own name for it, not a dotted key path. (The wire
 * value is not itself written into this comment as a key-shaped string — the fork-guards sweep
 * matches quoted namespace-anchored paths in COMMENTS too, so an illustrative one would fail the
 * build for a key nothing calls.)
 */
const RELATION_KIND_LABEL = Object.freeze({
  subtask: () => t('task.relation.kinds.subtask', {count: 1}),
  parenttask: () => t('task.relation.kinds.parenttask', {count: 1}),
  related: () => t('task.relation.kinds.related', {count: 1}),
  duplicateof: () => t('task.relation.kinds.duplicateof', {count: 1}),
  duplicates: () => t('task.relation.kinds.duplicates', {count: 1}),
  blocking: () => t('task.relation.kinds.blocking', {count: 1}),
  blocked: () => t('task.relation.kinds.blocked', {count: 1}),
  precedes: () => t('task.relation.kinds.precedes', {count: 1}),
  follows: () => t('task.relation.kinds.follows', {count: 1}),
  copiedfrom: () => t('task.relation.kinds.copiedfrom', {count: 1}),
  copiedto: () => t('task.relation.kinds.copiedto', {count: 1}),
});

function relationKindLabel(kind) {
  const read = RELATION_KIND_LABEL[kind];
  return read === undefined ? String(kind ?? '') : read();
}

/* ------------------------------------------------------------------ *
 * 4. Loading
 * ------------------------------------------------------------------ */

/**
 * The whole task in ONE read (ruling C13): `GET /api/v2/tasks/{id}?format=markdown&expand=buckets`.
 *
 * Labels, assignees, attachments, reminders, relations, `is_favorite` and `subscription` all come
 * back on that single payload (pkg/models/tasks.go:78-124), so the prototype's three extra list
 * calls (789-793) are dropped: two reads of the same facts can disagree with each other, and the
 * only thing they bought was a second copy.
 *
 * Comments are the exception and stay a separate read — `order_by` is a parameter of the comment
 * list, and the toggle has to be able to re-ask for the other order without re-reading the task.
 *
 * The projects list and the project's user list are `allSettled` and non-fatal: the task renders
 * without them, with the identifier prefix standing in for the project name.
 */
async function loadTask(taskId, {orderBy}) {
  const task = await api.getTask(taskId, {expand: ['buckets']});

  const personal = readGateFacts().personalEdition;
  const [comments, projects, projectUsers] = await Promise.allSettled([
    api.listComments(taskId, {orderBy, perPage: 100}),
    api.listProjects({perPage: 100}),
    // The assignee select is disabled for a personal account (ruling C4 keeps it visible), so
    // there is nobody to choose and no reason to spend the call. Same guard the prototype puts on
    // it at line 803, for the same reason.
    personal || typeof task?.project_id !== 'number'
      ? Promise.resolve(null)
      : api.searchProjectUsers(task.project_id, ''),
  ]);

  return {
    task,
    comments: comments.status === 'fulfilled' ? items(comments.value) : [],
    projects: projects.status === 'fulfilled' ? items(projects.value) : [],
    projectUsers: projectUsers.status === 'fulfilled' ? items(projectUsers.value) : [],
  };
}

/**
 * Start the load exactly once per task id.
 *
 * `mount()` runs after every render, so this has to be idempotent: `status` is undefined only
 * before the first attempt, which is the one call that starts a request. Everything else — a
 * reload after a write, a comment re-order — goes through `reload()` and never through here.
 */
function ensureLoaded(ctx) {
  const state = getViewState(NS);
  if (state.taskId === ctx.route.taskId && state.status !== undefined) return;

  setViewState(NS, {
    taskId: ctx.route.taskId,
    status: 'loading',
    task: null,
    comments: [],
    projects: [],
    projectUsers: [],
    error: null,
    // Reset with the task, not merged forward: `setViewState` is a shallow merge, so a draft
    // typed against task 12 would otherwise reappear in task 13's comment box.
    commentDraft: '',
    commentOrder: state.commentOrder ?? 'asc',
    resourceTab: state.resourceTab ?? 'attachments',
    scheduleOpen: state.scheduleOpen === true,
  });

  const taskId = ctx.route.taskId;
  loadTask(taskId, {orderBy: getViewState(NS).commentOrder}).then((loaded) => {
    // A second task id may have been routed to while this one was in flight; the late answer must
    // not overwrite the newer request's state.
    if (getViewState(NS).taskId !== taskId) return;
    setViewState(NS, {...loaded, status: 'ready', error: null});
  }).catch((err) => {
    if (getViewState(NS).taskId !== taskId) return;
    if (err instanceof api.SessionLostError) return; // app.js owns the terminal surface.
    console.error('[one/view-task] task load failed', err);
    setViewState(NS, {status: 'failed', error: err});
  }).finally(() => {
    requestRender();
  });
}

/** Re-read after a write. Keeps the current status on screen until the new payload lands. */
async function reload() {
  const state = getViewState(NS);
  if (state.taskId === null || state.taskId === undefined) return;
  const loaded = await loadTask(state.taskId, {orderBy: state.commentOrder});
  if (getViewState(NS).taskId !== state.taskId) return;
  setViewState(NS, {...loaded, status: 'ready', error: null});
  requestRender();
}

/* ------------------------------------------------------------------ *
 * 5. Writing — one path, and it reports every outcome
 * ------------------------------------------------------------------ */

/**
 * Every task PATCH goes through here.
 *
 * `PATCH /api/v2/tasks/{id}` is ACCESS-EXPANDING and carries `managed:"task-move"`
 * (route-classification.json:363), so a refusal on an ordinary edit — a title, a checkbox — is a
 * possible answer under Personal edition and not a bug. It is rendered on the control that caused
 * it, verbatim, and toasted so it is announced (bar 8); it is never swallowed and the field is
 * never left looking saved.
 *
 * `el` is the control to hang the sentence on. The refusal survives on screen because a FAILED
 * write does not reload and therefore does not re-render.
 *
 * The write and the re-read are deliberately in SEPARATE try blocks. Folding them into one would
 * report a successful write as a failure whenever the re-read afterwards happened to fail — which
 * is a fake failure, and as dishonest as the fake success bar 8 forbids.
 */
async function patchField(el, patch, successKey, successParams) {
  try {
    await api.patchTask(getViewState(NS).taskId, patch);
  } catch (err) {
    reportWriteFailure(el, err);
    return;
  }
  if (successKey) toast(t(successKey, successParams));
  await refreshAfterWrite();
}

/**
 * Re-read after a write that ALREADY SUCCEEDED. A failure here is not a failed write and is not
 * reported as one: the view falls to its load-failed surface, which carries the server's own
 * sentence and a retry. Leaving the pre-write values on screen instead would show the user a page
 * that quietly disagrees with the server.
 */
async function refreshAfterWrite() {
  try {
    await reload();
  } catch (err) {
    if (err instanceof api.SessionLostError) return;
    console.error('[one/view-task] the re-read after a write failed', err);
    setViewState(NS, {status: 'failed', error: err});
    requestRender();
  }
}

/**
 * The one failure path for an inline control: the server's own sentence on the control, and the
 * same sentence in the toast and the live region.
 *
 * A `SessionLostError` is deliberately silent — `app.js` already owns a terminal surface for it
 * (`onSessionLost`), and a second report of the same fact reads as a retryable error. It is
 * swallowed rather than rethrown because several callers are fired from a plain event listener,
 * where a rethrow is an unhandled rejection and nothing more.
 */
function reportWriteFailure(el, err) {
  if (err instanceof api.SessionLostError) return;
  console.error('[one/view-task] write failed', err);
  const refusal = describeForkError(err);
  if (el !== null && el !== undefined) renderRefusal(el, refusal);
  toast(refusalSentence(refusal));
}

/**
 * The same, for a control that lives inside a modal: the sentence goes in the modal foot and the
 * MODAL STAYS OPEN, so whatever the user typed is still there and the refusal is still next to
 * it. Closing the modal first and toasting the failure loses both.
 */
function reportModalFailure(err) {
  if (err instanceof api.SessionLostError) return;
  console.error('[one/view-task] modal write failed', err);
  const refusal = describeForkError(err);
  const foot = document.querySelector('#modalRoot .modal-foot') ?? document.querySelector('#modalRoot .modal-body');
  if (foot !== null) renderRefusal(foot, refusal);
  toast(refusalSentence(refusal));
}

/** The server's own words when it sent any; a catalogue sentence only when it did not. */
function refusalSentence(refusal) {
  return refusal.message ?? t(refusal.messageKey ?? 'one.error.requestFailed', refusal.messageParams);
}

/* ------------------------------------------------------------------ *
 * 6. Render
 * ------------------------------------------------------------------ */

/**
 * The logo pair, cloned out of the shell's `#brandLogo` template (task.html). Two `<img>`s, and
 * CSS picks by theme — SPEC-UI §5.2 rules out swapping `src` in JS, and a `<picture>` with
 * `prefers-color-scheme` cannot see the `.dark` class `app.js` sets from `color_schema`.
 */
function brandLogo() {
  const template = document.getElementById('brandLogo');
  return template?.innerHTML ?? '';
}

/**
 * The header's identity block. Prototype `taskUserSummary()` (997-1001).
 *
 * The edition line carries `data-requires="edition"`: U — every unentitled session, and every CI
 * run — has no edition claim, so there is nothing true to print and `app.js` removes the node
 * (T13). The role beside it is derived from the same facts, never from a switcher.
 *
 * The avatar is initials only. `GET /api/v2/avatar/{username}` exists but `api.js` exports no way
 * to build that URL, and this file may not reach past what `api.js` offers (bar 7 covers routes
 * this page has no wiring for). The prototype's own fallback is initials.
 */
function userSummary(facts) {
  const user = getUser();
  const roleKey = facts.orgAdmin
    ? 'one.role.administrator'
    : facts.personalEdition ? 'one.role.personalUser' : 'one.role.teamMember';
  const edition = editionMessageKey(facts);
  return `<div class="task-user-summary">
    <div class="task-user-avatar">${esc(initials(user))}</div>
    <div class="task-user-meta">
      <strong>${esc(personName(user))}</strong>
      <span>${esc(t(roleKey))}</span>
      <small data-requires="edition">${edition === null ? '' : esc(t(edition))}</small>
    </div>
  </div>`;
}

function header(facts) {
  return `<header class="task-topbar">
    ${brandLogo()}
    <div class="product-label">${esc(t('one.brand.taskPage'))}</div>
    ${userSummary(facts)}
  </header>`;
}

/**
 * The blocking surfaces. Same shape as `app.js`'s own `loadSurface` — this view cannot reach that
 * function (it is private) and duplicating six lines of markup is cheaper than widening app.js's
 * exported surface for it.
 */
function surface(titleKey, detail, action) {
  return `${header(readGateFacts())}<div class="load-surface"><div class="card">
    <h2>${esc(t(titleKey))}</h2>
    ${detail === '' ? '' : `<p class="refusal-text" data-refusal-source="server">${esc(detail)}</p>`}
    ${action === '' ? '' : `<p style="margin-top:14px">${action}</p>`}
  </div></div>`;
}

/**
 * The label chips and the inline add-a-label entry. Prototype 1008.
 *
 * THE PLACEHOLDER IS `one.task.labelPlaceholder`, NOT `task.label.placeholder`, and the choice is
 * load-bearing rather than cosmetic. `.label-inline-input` is `flex:1 1 12ch` inside the
 * `min-inline-size:128px` `.label-entry` chip, and `font:inherit` resolves against
 * `.tag-chip{font-size:11px}` — so the field is ~12 characters wide, and no CSS length can
 * measure a placeholder into the chip's intrinsic size. task.html:546-551 states the remedy in
 * as many words: *"The remedy is a short placeholder key, not more CSS. This is the one
 * translated string on the page that sizing alone cannot guarantee."*
 *
 * `task.label.placeholder` is the longest string available for this box — "Type to add a label…"
 * is 20 characters in English and "Beginne zu schreiben, um ein Label hinzuzufügen…" is 47 in
 * de-DE — so it was cut in EVERY launch language, English included. Replacing the prototype's
 * hardcoded English with an upstream key was right in principle and is exactly what broke it.
 * `one.task.labelPlaceholder` is "Add label…" (10) and fits, which is why it was authored.
 *
 * SCOPE NOTE, recorded here because this is the control that absorbed it: the prototype's
 * `addLabelModal` (`newLabel` / `save-label`, prototype-pristine.html) is NOT ported, and this
 * inline chip is the single add-a-label path. A deliberate reduction under bar 10 rather than an
 * oversight — `one.task.labelExample` ("e.g. Launch") is that modal's orphaned placeholder key
 * and is the visible trace of it. Reported for `docs/` rather than left to be inferred.
 */
function labelLine(task) {
  const labels = Array.isArray(task?.labels) ? task.labels : [];
  const chips = labels.map((label) => `<span class="tag-chip">${esc(label?.title ?? '')}
    <button data-action="remove-label" data-label-id="${esc(label?.id)}"
      aria-label="${esc(t('task.label.removeLabel', {label: label?.title ?? ''}))}">${ic('close')}</button>
  </span>`).join('');
  return `<div class="quick-line" id="labelLine" data-requires="teams write">${chips}
    <span class="tag-chip label-entry">
      <input id="inlineLabelInput" class="label-inline-input"
        placeholder="${esc(t('one.task.labelPlaceholder'))}" aria-label="${esc(t('task.detail.actions.label'))}">
      <button data-action="save-label-inline"
        aria-label="${esc(t('task.detail.actions.label'))}">${ic('check')}</button>
    </span>
  </div>`;
}

function taskHead(state, facts) {
  const task = state.task;
  const bucket = bucketLabel(task);
  const done = task?.done === true;
  return `<section class="task-head"><div>
    <div class="task-kicker">
      <button class="project-chip" data-action="move" data-requires="teams write"
        >${esc(projectLabel(state))} ${ic('chevron')}</button>
      ${bucket === '' ? '' : `<span>${esc(t('one.common.separator'))}</span><span>${esc(bucket)}</span>`}
    </div>
    <div class="task-title-row">
      <span class="task-id">${esc(taskIdentifier(task))}</span>
      <input id="taskTitle" class="task-title-input" data-requires="write"
        value="${esc(task?.title ?? '')}" aria-label="${esc(t('task.attributes.title'))}">
    </div>
    ${labelLine(task)}
  </div><div class="task-head-actions">
    <button class="btn ${done ? 'done success' : 'primary'} done-btn" data-action="toggle-done"
      data-requires="write">${ic('check')} ${esc(t(done ? 'task.detail.undone' : 'task.detail.done'))}</button>
    <button class="icon-btn" data-action="toggle-more"
      aria-label="${esc(t('one.task.moreActions'))}">${ic('more')}</button>
  </div></section>`;
}

/**
 * The assignee select. Prototype 1031, with its `isTeams()` hide replaced by a gate.
 *
 * RECORDED DEVIATION, already flagged in app.js's `GATES_THAT_HIDE`: SPEC-ROLES T6/T7 make this
 * and the label line **H** for a personal account, copying the prototype's `isTeams() ? … : ''`.
 * Ruling C4 reserves hiding for "the whole surface is absent" and disables everything else with a
 * reason, so both render disabled here. The single-assignee shape is the prototype's.
 */
function assigneeField(state) {
  const assignees = Array.isArray(state.task?.assignees) ? state.task.assignees : [];
  const current = assignees[0] ?? null;
  const options = [...state.projectUsers];
  if (current !== null && !options.some((user) => user?.id === current.id)) options.unshift(current);
  return `<div><label class="label">${ic('user')} ${esc(t('task.attributes.assignees'))}</label>
    <select class="select" id="assignee" data-requires="teams write">
      <option value="">${esc(t('one.task.unassigned'))}</option>
      ${options.map((user) => `<option value="${esc(user?.id)}"${
        // `current?.id === user?.id` would be `undefined === undefined` on an unassigned task with
        // a malformed roster row, and would silently select it.
        current !== null && current.id === user?.id ? ' selected' : ''}
        >${esc(personName(user))}</option>`).join('')}
    </select></div>`;
}

function priorityField(task) {
  const current = Number(task?.priority) || 0;
  return `<div><label class="label">${ic('flag')} ${esc(t('task.attributes.priority'))}</label>
    <select class="select" id="priority" data-requires="write">
      ${PRIORITIES.map(([value, key]) => `<option value="${value}"${value === current ? ' selected' : ''}
        >${esc(t(key))}</option>`).join('')}
    </select></div>`;
}

/**
 * `percent_done` is 0..1 on the wire (pkg/models/tasks.go:98) and 0..100 in the control, which is
 * the one conversion this view performs on a number.
 */
function progressField(task) {
  const percent = Math.round((Number(task?.percent_done) || 0) * 100);
  return `<div><label class="label">${ic('progress')} ${esc(t('task.attributes.percentDone'))}</label>
    <div class="progress-wrap"><div class="progress-head">
      <span class="help" style="margin:0">${esc(t('one.task.progressManual'))}</span>
      <span class="pill blue" id="progressText">${esc(t('one.task.percent', {percent: formatNumber(percent)}))}</span>
    </div>
    <input class="progress" id="progress" type="range" min="0" max="100" step="10"
      value="${percent}" data-requires="write"
      aria-label="${esc(t('task.attributes.percentDone'))}"></div></div>`;
}

/** The repeat builder. Prototype `repeatBuilder()` (1019-1026). */
function repeatBuilder(task) {
  const repeat = readRepeat(task);
  const presets = REPEAT_PRESETS.map(([every, unit, key]) => {
    const on = repeat.active && repeat.every === every && repeat.unit === unit;
    return `<button class="repeat-preset${on ? ' on' : ''}" data-action="repeat-preset"
      data-every="${every}" data-unit="${unit}" data-requires="write">${esc(t(key))}</button>`;
  }).join('');
  return `<div class="repeat-builder"><div class="repeat-top">
    <div><label class="label" style="margin:0">${ic('repeat')} ${esc(t('task.attributes.repeat'))}</label></div>
    <div class="repeat-presets">${presets}</div>
    ${repeat.active ? `<button class="repeat-clear" data-action="repeat-clear" data-requires="write"
      aria-label="${esc(t('task.detail.removeRepeat'))}">${ic('close')}</button>` : ''}
  </div>
  <div class="repeat-fields">
    <div><label class="label">${esc(t('task.repeat.mode'))}</label>
      <select class="select" id="repeatMode" data-requires="write">
        <option value="default"${repeat.mode === 'default' ? ' selected' : ''}>${esc(t('misc.default'))}</option>
        <option value="current"${repeat.mode === 'current' ? ' selected' : ''}>${esc(t('task.repeat.fromCurrentDate'))}</option>
      </select></div>
    <div><label class="label">${esc(t('one.task.repeat.interval'))}</label>
      <div class="repeat-each">
        <span class="label-inline">${esc(t('task.repeat.each'))}</span>
        <input class="input" id="repeatEvery" type="number" min="1" step="1" value="${repeat.every}"
          data-requires="write" aria-label="${esc(t('one.task.repeat.interval'))}">
        <select class="select" id="repeatUnit" data-requires="write"
          aria-label="${esc(t('one.task.repeat.interval'))}">
          ${REPEAT_UNITS.map(([name, , key]) => `<option value="${name}"${repeat.unit === name ? ' selected' : ''}
            >${esc(t(key))}</option>`).join('')}
        </select>
      </div></div>
  </div>
  <div class="help">${esc(repeat.active
    ? t('one.task.repeat.summary', {interval: repeatIntervalText(repeat)})
    : t('one.task.repeat.hint'))}</div></div>`;
}

/** Prototype `taskProperties()` (1028-1040). */
function taskProperties(state) {
  const task = state.task;
  const reminders = Array.isArray(task?.reminders) ? task.reminders : [];
  const scheduleOpen = state.scheduleOpen === true;
  return `<div class="properties">
    <section class="card prop-card">
      <div class="prop-head"><div class="prop-head-left">
        <div class="card-title">${esc(t('one.task.statusOwnership.title'))}</div>
        <div class="card-sub">${esc(t('one.task.statusOwnership.subtitle'))}</div>
      </div></div>
      <div class="prop-grid">${assigneeField(state)}${priorityField(task)}${progressField(task)}</div>
    </section>
    <section class="card prop-card">
      <div class="prop-head"><div class="prop-head-left">
        <div class="card-title">${esc(t('one.task.schedule.title'))}</div>
        <div class="card-sub">${esc(t('one.task.schedule.subtitle'))}</div>
      </div>
      <button class="disclosure${scheduleOpen ? ' open' : ''}" data-action="toggle-schedule"
        >${esc(t('one.common.more'))} ${ic('chevron')}</button></div>
      <div class="prop-grid two">
        <div><label class="label">${ic('calendar')} ${esc(t('task.attributes.dueDate'))}</label>
          <input class="input" id="due" type="date" value="${esc(dateInputValue(task?.due_date))}"
            data-requires="write" aria-label="${esc(t('task.attributes.dueDate'))}"></div>
        <div><label class="label">${ic('bell')} ${esc(t('task.attributes.reminders'))}</label>
          <!-- 'inline-size:100%' fills the cell so the chevron sits at the right edge, and
               'min-inline-size:max-content' is what stops it CLIPPING.

               THIS IS THE HALF THAT LIVES HERE; the other half is in the stylesheet, and the
               two must be read together. '.prop-grid.two' is 'repeat(2,minmax(min-content,1fr))'
               (task.html:369) — it was 'minmax(0,1fr)', a floor of 0, and this comment described
               that older value until the stylesheet moved. A floor of 0 tied a
               'white-space:nowrap' label to a box that can be narrower than the label itself and
               the label then painted outside its own border box, which is the exact prototype
               defect task.html's "TRANSLATED LABELS" note claims to have fixed. German
               ("Eine Erinnerung hinzufügen…") is over the ~223px cell at the page's own desktop
               width.

               The inline 'min-inline-size' is kept rather than retired against that floor,
               because the floor alone is not enough: used width is max(min-width, width)
               (task.html:370-372), so it is what keeps 'inline-size:100%' from being the
               binding constraint inside the cell the track then sizes. Same treatment as
               '.done-btn,.team-action-btn': grow, never clip. -->
          <button class="btn" style="inline-size:100%;min-inline-size:max-content;justify-content:space-between;font-weight:500"
            data-action="reminders" data-requires="write">${esc(reminders.length === 0
              ? t('task.addReminder')
              : t('one.task.reminderCount', {count: reminders.length}))}${ic('chevron')}</button></div>
      </div>
      ${scheduleOpen ? `<div class="schedule-advanced"><div class="prop-grid two">
        <div><label class="label">${ic('calendar')} ${esc(t('task.attributes.startDate'))}</label>
          <input class="input" id="start" type="date" value="${esc(dateInputValue(task?.start_date))}"
            data-requires="write" aria-label="${esc(t('task.attributes.startDate'))}"></div>
        <div><label class="label">${ic('calendar')} ${esc(t('task.attributes.endDate'))}</label>
          <input class="input" id="end" type="date" value="${esc(dateInputValue(task?.end_date))}"
            data-requires="write" aria-label="${esc(t('task.attributes.endDate'))}"></div>
        ${repeatBuilder(task)}
      </div></div>` : ''}
    </section>
  </div>`;
}

/**
 * The description. A PLAIN `<textarea>` holding Markdown — no contenteditable, no toolbar, no
 * `execCommand`. Deleting the editor removes the stored-XSS sink rather than sanitising it
 * (BRIEF, "Description field"). `.editor` keeps the taller box; task.html already restyles it as
 * a textarea.
 *
 * Read is `?format=markdown` on the one task read; the write is the only PATCH on this page that
 * carries `X-Vikunja-Format` (api.js `updateTaskDescription`).
 */
function descriptionSection(task) {
  return `<section class="card section-card">
    <div class="section-head"><div>
      <div class="card-title">${esc(t('task.attributes.description'))}</div>
      <div class="card-sub">${esc(t('one.task.description.subtitle'))}</div>
    </div></div>
    <textarea id="description" class="editor" data-requires="write"
      placeholder="${esc(t('one.task.description.placeholder'))}"
      aria-label="${esc(t('task.attributes.description'))}">${esc(task?.description ?? '')}</textarea>
  </section>`;
}

/** Prototype `resourcesPanel()` (1053-1059). */
function resourcesPanel(state) {
  const task = state.task;
  if (state.resourceTab === 'relations') {
    const rows = relationRows(task);
    return `<div class="relation-list">${rows.length === 0
      ? `<div class="empty-state">${esc(t('task.relation.noneYet'))}</div>`
      : rows.map((row) => `<div class="relation-row">
          <span class="relation-type">${esc(relationKindLabel(row.kind))}</span>
          <div class="row-grow"><div class="row-title">${esc(row.title)}</div></div>
          <button class="row-menu" data-action="remove-relation" data-kind="${esc(row.kind)}"
            data-other="${esc(row.otherId)}" data-requires="write"
            aria-label="${esc(t('task.relation.delete'))}">${ic('close')}</button>
        </div>`).join('')}
      <div style="margin-top:10px"><button class="btn small" data-action="add-relation"
        data-requires="write">${ic('plus')} ${esc(t('task.detail.actions.relatedTasks'))}</button></div>
    </div>`;
  }
  const attachments = Array.isArray(task?.attachments) ? task.attachments : [];
  return `<div class="file-list">${attachments.length === 0
    ? `<div class="empty-state">${esc(t('one.task.attachments.empty'))}</div>`
    : attachments.map((attachment) => `<div class="file-row">
        <div class="file-icon">${ic('file')}</div>
        <div class="row-grow">
          <div class="row-title">${esc(attachment?.file?.name ?? '')}</div>
          <div class="row-meta">${esc(joinMeta([attachment?.file?.mime, fileSize(attachment?.file?.size)]))}</div>
        </div>
        <button class="row-menu" data-action="remove-file" data-attachment="${esc(attachment?.id)}"
          data-requires="write" aria-label="${esc(t('task.attachment.delete'))}">${ic('close')}</button>
      </div>`).join('')}
    <div style="margin-top:10px"><button class="btn small" data-action="upload" data-requires="write"
      >${ic('upload')} ${esc(t('task.attachment.upload'))}</button></div>
  </div>`;
}

function resourcesSection(state) {
  const task = state.task;
  const attachments = Array.isArray(task?.attachments) ? task.attachments.length : 0;
  const relations = relationRows(task).length;
  const tab = state.resourceTab === 'relations' ? 'relations' : 'attachments';
  return `<section class="card section-card">
    <div class="section-head"><div>
      <div class="card-title">${esc(t('one.task.resources.title'))}</div>
      <div class="card-sub">${esc(t('one.task.resources.subtitle'))}</div>
    </div><div class="tabs">
      <button data-resource="attachments" class="${tab === 'attachments' ? 'on' : ''}"
        >${esc(t('task.attachment.title'))} <span>${esc(formatNumber(attachments))}</span></button>
      <button data-resource="relations" class="${tab === 'relations' ? 'on' : ''}"
        >${esc(t('task.attributes.relatedTasks'))} <span>${esc(formatNumber(relations))}</span></button>
    </div></div>${resourcesPanel(state)}</section>`;
}

/** Prototype `commentsPanel()` (1060-1063). */
function commentsSection(state, facts) {
  const user = getUser();
  const comments = state.comments.map((comment) => `<div class="comment">
    <div class="avatar">${esc(initials(comment?.author))}</div>
    <div><div>
      <span class="comment-author">${esc(personName(comment?.author))}</span>
      <span class="comment-time">${esc(formatDateTime(comment?.created))}</span>
    </div>
    <div class="comment-text">${esc(comment?.comment ?? '')}</div></div>
  </div>`).join('');
  return `<section class="card section-card">
    <div class="section-head"><div>
      <div class="card-title">${esc(t('task.comment.title'))}</div>
      <div class="card-sub">${esc(t(facts.personalEdition
        ? 'one.task.comments.subtitlePersonal'
        : 'one.task.comments.subtitleTeams'))}</div>
    </div>
    <button class="btn small ghost" data-action="toggle-comment-order">${esc(t(state.commentOrder === 'asc'
      ? 'task.comment.sortOldestFirst'
      : 'task.comment.sortNewestFirst'))}</button></div>
    <div class="comment-list">${comments}</div>
    <div class="comment-box" style="margin-top:10px">
      <div class="avatar">${esc(initials(user))}</div>
      <div class="comment-editor">
        <!-- The draft is re-emitted because a render REPLACES #app.innerHTML, and a render is
             fired by controls the user did not touch: 'refreshAfterWrite' after any other write,
             and 'syncRoleDrift'. Half a typed comment vanishing because a due date saved in the
             same minute is unsaved work thrown away, which F2 forbids. The description textarea
             is protected by the capture-phase blur handler; this box had no equivalent. -->
        <textarea id="commentText" placeholder="${esc(t('one.task.comments.placeholder'))}"
          aria-label="${esc(t('task.comment.comment'))}">${esc(state.commentDraft ?? '')}</textarea>
        <div class="comment-actions"><button class="btn small primary" data-action="comment"
          data-requires="write">${esc(t('task.comment.comment'))}</button></div>
      </div>
    </div>
  </section>`;
}

/** Prototype `taskContent()` (1045-1052). */
function taskContent(state, facts) {
  const task = state.task;
  const updated = formatDateTime(task?.updated);
  return `<div class="content-grid"><div class="stack">
    ${descriptionSection(task)}
    ${resourcesSection(state)}
    ${commentsSection(state, facts)}
  </div><div class="task-footer">
    <span>${esc(t('misc.createdBy', {0: personName(task?.created_by)}))}${esc(t('one.common.separator'))}${esc(
      t('task.detail.updated', {0: updated === '' ? t('one.common.justNow') : updated}))}</span>
    <button class="action-link danger" data-action="delete" data-requires="write"
      >${ic('trash')} ${esc(t('one.task.deleteTask'))}</button>
  </div></div>`;
}

/**
 * THE VIEW'S RENDER. Emits every gated node and lets `applyGates` decide its fate — a node is
 * never omitted because a gate is false (ruling C4, and app.d.ts `ViewModule.render`).
 */
export function render(ctx) {
  const state = getViewState(NS);

  if (state.status === 'deleted') {
    return surface('task.detail.deleteSuccess', '', '');
  }
  if (state.status === 'failed') {
    const detail = refusalSentence(describeForkError(state.error));
    return surface('one.error.loadFailed', detail,
      `<button class="btn" data-action="retry">${esc(t('organization.retry'))}</button>`);
  }
  if (state.status !== 'ready' || state.task === null) {
    return surface('misc.loading', '', '');
  }

  return header(ctx.facts)
    + `<div class="task-shell">${taskHead(state, ctx.facts)}${taskProperties(state)}${taskContent(state, ctx.facts)}</div>`;
}

/**
 * Runs after insertion and BEFORE gates (app.d.ts `ViewModule.mount`), so nothing here may assume
 * a control's refused state. It starts the load, and installs the listeners for the events the
 * delegated click registry cannot carry.
 */
export function mount(root, ctx) {
  installListeners();
  ensureLoaded(ctx);
}

/* ------------------------------------------------------------------ *
 * 7. Modals
 * ------------------------------------------------------------------ */

/**
 * The prototype's modal factory (629), rebuilt on `app.js`'s `openModal`. `openModal` hydrates
 * `data-i18n*` and applies the gates to what is inserted, so a gated control inside a modal is
 * refused on open rather than on click.
 *
 * The dialog semantics the prototype lacks — `role="dialog"`, `aria-modal`, focus trap and
 * restore — stay deferred (SPEC-UI §7.9); the close button gets the accessible name it never had,
 * because that one is a single attribute.
 */
function modal(titleKey, body, foot) {
  return openModal(`<div class="modal-scrim" data-modal-scrim="true"><div class="modal">
    <div class="modal-head"><h3>${esc(t(titleKey))}</h3>
      <button class="icon-btn" data-action="modal-close"
        aria-label="${esc(t('misc.closeDialog'))}">${ic('close')}</button></div>
    <div class="modal-body">${body}</div>
    ${foot === '' ? '' : `<div class="modal-foot">${foot}</div>`}
  </div></div>`);
}

const cancelButton = () => `<button class="btn" data-action="modal-close">${esc(t('misc.cancel'))}</button>`;

/**
 * Move task. Prototype `moveModal()` (1145).
 *
 * The picker's data source is `GET /api/v2/projects` — ruling C3 requires it to be verified
 * before wiring, and `api.listProjects` records the verification: route-classification.json holds
 * 265 rows and not one is a GET (it classifies only mutating routes), so the check is against the
 * registration site `projects-list` (pkg/routes/api/v2/projects.go:41). The route exists, so the
 * control is wired rather than disabled.
 */
function moveModal(state) {
  const current = String(state.task?.project_id ?? '');
  const options = state.projects.map((project) => `<option value="${esc(project?.id)}"${
    String(project?.id) === current ? ' selected' : ''}>${esc(project?.title ?? '')}</option>`).join('');

  // The projects read is `allSettled` on load, so an empty list means it failed rather than that
  // the account has no projects — every account has an Inbox. A picker with nothing in it must
  // not offer a Move button that can only fail, so the button is refused in the MARKUP here
  // instead of through `data-requires`: `applyGates` calls `releaseControl` on a passing gate and
  // would strip a manually applied refusal straight back off.
  const noDestinations = options === '';
  const confirm = noDestinations
    ? `<button class="btn primary is-refused" data-action="confirm-move" aria-disabled="true"
        >${esc(t('one.task.move.title'))}</button>
       <p class="refusal-text" data-refusal-source="server">${esc(t('one.error.requestFailed'))}</p>`
    : `<button class="btn primary" data-action="confirm-move" data-requires="teams write"
        >${esc(t('one.task.move.title'))}</button>`;

  modal('one.task.move.title', `<div class="card-sub">${esc(t('one.task.move.explanation'))}</div>
    <label class="label" style="margin-top:6px">${esc(t('one.task.move.destination'))}</label>
    <select class="select" id="moveProject" aria-label="${esc(t('one.task.move.destination'))}">${options}</select>`,
  `${cancelButton()}${confirm}`);
}

/** Reminders. Prototype `remindersModal()` (1143). */
function remindersModal(state) {
  const reminders = Array.isArray(state.task?.reminders) ? state.task.reminders : [];
  const rows = reminders.length === 0
    ? `<div class="empty-state">${esc(t('one.task.reminders.empty'))}</div>`
    : reminders.map((reminder, index) => `<div class="file-row">
        <div class="file-icon">${ic('bell')}</div>
        <div class="row-grow"><div class="row-title">${esc(reminderText(reminder))}</div></div>
        <button class="row-menu" data-action="remove-reminder" data-index="${index}" data-requires="write"
          aria-label="${esc(t('task.removeReminder'))}">${ic('close')}</button>
      </div>`).join('');
  modal('task.attributes.reminders', `<div id="reminderList">${rows}</div>
    <label class="label" style="margin-top:8px">${esc(t('task.addReminder'))}</label>
    <select class="select" id="newReminder" aria-label="${esc(t('task.addReminder'))}">
      ${REMINDER_CHOICES.map(([value, key]) => `<option value="${value}">${esc(t(key))}</option>`).join('')}
    </select>
    <label class="label" style="margin-top:8px">${esc(t('one.task.reminders.customDateTime'))}</label>
    <input class="input" id="customReminder" type="datetime-local"
      aria-label="${esc(t('one.task.reminders.customDateTime'))}">`,
  `<button class="btn" data-action="modal-close">${esc(t('misc.close'))}</button>
   <button class="btn primary" data-action="add-reminder" data-requires="write"
     >${esc(t('task.addReminder'))}</button>`);
}

/**
 * Add relation. Prototype `relationModal()` (1144).
 *
 * The task picker's data source is `GET /api/v2/tasks?q=` — verified the same way as the project
 * picker, at the registration site `tasks-list` (pkg/routes/api/v2/task_collection.go:135). The
 * free-text field is the prototype's; `save-relation` resolves it below.
 */
function relationModal() {
  modal('task.relation.add', `<label class="label">${esc(t('task.relation.select'))}</label>
    <select class="select" id="relationType" aria-label="${esc(t('task.relation.select'))}">
      ${api.RELATION_KINDS.map((kind) => `<option value="${esc(kind)}">${esc(relationKindLabel(kind))}</option>`).join('')}
    </select>
    <label class="label">${esc(t('one.task.relationTarget'))}</label>
    <input class="input" id="relationTask" placeholder="${esc(t('task.relation.searchPlaceholder'))}"
      aria-label="${esc(t('one.task.relationTarget'))}">`,
  `${cancelButton()}<button class="btn primary" data-action="save-relation" data-requires="write"
    >${esc(t('task.detail.actions.relatedTasks'))}</button>`);
}

/** Delete this task. Prototype `deleteModal()` (1146). */
function deleteModal() {
  modal('task.detail.delete.header', `<div class="notice">
    <strong>${esc(t('misc.cannotBeUndone'))}</strong>${esc(t('task.detail.delete.text2'))}</div>`,
  `${cancelButton()}<button class="btn danger" data-action="confirm-delete" data-requires="write"
    >${esc(t('one.task.deleteTask'))}</button>`);
}

/** Task colour. Prototype `taskColorModal()` (1216). */
function colorModal(state) {
  const current = String(state.task?.hex_color ?? '').replace('#', '').toLowerCase();
  modal('task.detail.actions.color', `<label class="label">${esc(t('task.detail.actions.color'))}</label>
    <select class="select" id="taskColor" aria-label="${esc(t('task.detail.actions.color'))}">
      ${TASK_COLORS.map(([hex, key]) => `<option value="${esc(hex)}"${hex === current ? ' selected' : ''}
        >${esc(t(key))}</option>`).join('')}
    </select>`,
  `${cancelButton()}<button class="btn primary" data-action="save-color" data-requires="write"
    >${esc(t('misc.save'))}</button>`);
}

/**
 * The more-menu. Prototype `morePopover()` (1078-1084).
 *
 * It is appended to `<body>`, outside `#app`, so `app.js`'s post-render `applyGates` never
 * reaches it — the gates are applied here, explicitly, against the same facts. Without this the
 * five write actions inside it would stay enabled for a write-restricted account.
 *
 * `Escape` and `resize` close it; `app.js` owns both listeners. It is deliberately NOT closed by
 * an outside click, which is the prototype's behaviour and is on the deferred a11y list.
 */
function morePopover() {
  const trigger = document.querySelector('[data-action="toggle-more"]');
  if (trigger === null) return;
  const existing = document.getElementById('morePopover');
  if (existing !== null) {
    existing.remove();
    return;
  }
  const task = getViewState(NS).task;
  const box = trigger.getBoundingClientRect();
  const popover = document.createElement('div');
  popover.id = 'morePopover';
  popover.className = 'popover';
  popover.style.top = `${box.bottom + 6 + window.scrollY}px`;
  popover.style.left = `${Math.min(box.right - 220, window.innerWidth - 230) + window.scrollX}px`;
  popover.innerHTML = `
    <button data-action="toggle-subscribe" data-requires="write">${ic('bell')} ${esc(t(task?.subscription
      ? 'task.subscription.unsubscribe'
      : 'task.subscription.subscribe'))}</button>
    <button data-action="toggle-favorite" data-requires="write">${glyph('favorite')} ${esc(t(task?.is_favorite === true
      ? 'task.detail.actions.unfavorite'
      : 'task.detail.actions.favorite'))}</button>
    <button data-action="duplicate" data-requires="write"
      >${glyph('duplicate')} ${esc(t('task.detail.actions.duplicate'))}</button>
    <button data-action="task-color" data-requires="write"
      >${glyph('color')} ${esc(t('task.detail.actions.color'))}</button>
    <button class="danger" data-action="delete" data-requires="write"
      >${ic('trash')} ${esc(t('one.task.deleteTask'))}</button>`;
  document.body.appendChild(popover);
  applyGates(popover, readGateFacts());
}

function closePopover() {
  document.getElementById('morePopover')?.remove();
}

/* ------------------------------------------------------------------ *
 * 8. Inline label add — search, create if absent, attach
 * ------------------------------------------------------------------ */

/**
 * The inline chip's three steps, as the prototype specifies them: find an exact title match among
 * the labels this account can already use, create one only when there is none, then attach it.
 *
 * The match is exact and case-insensitive on purpose: `GET /api/v2/labels?q=` is a substring
 * search, so a query of "ui" would otherwise attach "Build UI" and the user would never know
 * which label they got.
 */
async function saveInlineLabel(el) {
  const input = document.getElementById('inlineLabelInput');
  const title = String(input?.value ?? '').trim();
  if (title === '') return;
  try {
    const found = items(await api.listLabels({q: title, perPage: 100}))
      .find((label) => String(label?.title ?? '').toLowerCase() === title.toLowerCase());
    const label = found ?? await api.createLabel(title);
    await api.addTaskLabel(getViewState(NS).taskId, label.id);
  } catch (err) {
    reportWriteFailure(el ?? input, err);
    return;
  }
  toast(t('task.label.addSuccess'));
  await refreshAfterWrite();
}

/**
 * Resolve what the user typed in the relation modal to one task.
 *
 * Prototype `resolveRelationTask` (964-977). A bare number is read as a task id; anything else is
 * searched, and an exact match on id, index, identifier or title wins over a single result.
 *
 * RETURNS NULL RATHER THAN GUESSING when nothing matches or several do. The two sentences that
 * case wants — "no task matches" and "more than one task matches" — have no key in this page's
 * catalogue and this file may not add one, so the caller falls back to the generic
 * `one.error.requestFailed`, whose "nothing was changed" is at least true. Reported as a gap.
 */
async function resolveRelationTask(query) {
  const raw = String(query ?? '').trim();
  if (raw === '') return null;
  if (/^[1-9][0-9]*$/.test(raw)) return api.getTask(raw);

  const needle = raw.toLowerCase();
  const bare = needle.replace(/^#/, '');
  const found = items(await api.searchTasks(raw.replace(/^#/, ''), {perPage: 50}));
  const exact = found.find((task) => String(task?.id) === bare
    || String(task?.index) === bare
    || String(task?.identifier ?? '').toLowerCase() === needle
    || String(task?.title ?? '').toLowerCase() === needle);
  if (exact !== undefined) return exact;
  return found.length === 1 ? found[0] : null;
}

/* ------------------------------------------------------------------ *
 * 9. The actions
 * ------------------------------------------------------------------ */

/**
 * Registered by name, on `app.js`'s one delegated document listener. Re-registering a name throws
 * there, which is what keeps this view and `view-settings.js` from silently claiming the same
 * hook. The spellings are the prototype's, unchanged (SPEC-UI §5.5).
 *
 * `app.js` refuses to dispatch to a control inside `.is-refused` / `[aria-disabled="true"]`, so no
 * handler below re-checks the gate; what each one does re-check is the state it needs.
 */
registerActions({
  /* --- the resource tab strip (an ATTRIBUTE_HOOK, not a data-action) --- */
  'data-resource': (event, el) => {
    const value = el.getAttribute('data-resource');
    if (value !== 'attachments' && value !== 'relations') return;
    setViewState(NS, {resourceTab: value});
    requestRender();
  },

  /* --- head ------------------------------------------------------- */
  'toggle-done': (event, el) => {
    const done = getViewState(NS).task?.done === true;
    return patchField(el, {done: !done}, done ? 'task.undoneSuccess' : 'task.doneSuccess');
  },
  'toggle-more': () => morePopover(),
  'toggle-schedule': () => {
    setViewState(NS, {scheduleOpen: getViewState(NS).scheduleOpen !== true});
    requestRender();
  },

  /* --- labels ----------------------------------------------------- */
  'save-label-inline': (event, el) => saveInlineLabel(el),
  'remove-label': async (event, el) => {
    try {
      await api.removeTaskLabel(getViewState(NS).taskId, el.getAttribute('data-label-id'));
    } catch (err) {
      reportWriteFailure(el, err);
      return;
    }
    toast(t('task.label.removeSuccess'));
    await refreshAfterWrite();
  },

  /* --- repeat ----------------------------------------------------- */
  'repeat-preset': (event, el) => patchField(el, repeatPayload({
    active: true,
    mode: 'default',
    every: Math.max(1, parseInt(el.getAttribute('data-every') ?? '1', 10) || 1),
    unit: el.getAttribute('data-unit') ?? 'days',
  }), 'task.detail.updateSuccess'),
  'repeat-clear': (event, el) => patchField(el, repeatPayload({active: false}), 'task.detail.updateSuccess'),

  /* --- reminders -------------------------------------------------- */
  reminders: () => remindersModal(getViewState(NS)),
  'add-reminder': async () => {
    const choice = document.getElementById('newReminder')?.value ?? '';
    const custom = document.getElementById('customReminder')?.value ?? '';
    const addition = reminderFromChoice(choice, custom);
    if (addition === null) return;
    const existing = Array.isArray(getViewState(NS).task?.reminders) ? getViewState(NS).task.reminders : [];
    try {
      await api.patchTask(getViewState(NS).taskId, {reminders: [...existing, addition]});
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    toast(t('task.detail.updateSuccess'));
    await refreshAfterWrite();
    // Reopened against the fresh payload: the list the user is looking at has to be the list the
    // server now holds, and the row indices the remove buttons carry are positions in it.
    remindersModal(getViewState(NS));
  },
  'remove-reminder': async (event, el) => {
    const index = Number(el.getAttribute('data-index'));
    const existing = Array.isArray(getViewState(NS).task?.reminders) ? [...getViewState(NS).task.reminders] : [];
    if (!Number.isInteger(index) || index < 0 || index >= existing.length) return;
    existing.splice(index, 1);
    try {
      await api.patchTask(getViewState(NS).taskId, {reminders: existing});
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    toast(t('task.detail.updateSuccess'));
    await refreshAfterWrite();
    remindersModal(getViewState(NS));
  },

  /* --- move ------------------------------------------------------- */
  move: () => moveModal(getViewState(NS)),
  'confirm-move': async () => {
    const select = document.getElementById('moveProject');
    const projectId = Number(select?.value);
    if (!Number.isInteger(projectId) || projectId <= 0) return;
    // The destination's name is read off the option BEFORE the write: after it, the projects list
    // is briefly the old one, and after the re-read the toast would be racing the payload.
    const destination = select.options[select.selectedIndex]?.textContent ?? '';
    try {
      await api.patchTask(getViewState(NS).taskId, {project_id: projectId});
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('task.movedToProject', {project: destination}));
    await refreshAfterWrite();
  },

  /* --- attachments ------------------------------------------------ */
  upload: () => {
    document.getElementById('attachmentInput')?.click();
  },
  'remove-file': async (event, el) => {
    try {
      await api.deleteAttachment(getViewState(NS).taskId, el.getAttribute('data-attachment'));
    } catch (err) {
      reportWriteFailure(el, err);
      return;
    }
    toast(t('one.toast.attachmentRemoved'));
    await refreshAfterWrite();
  },

  /* --- relations -------------------------------------------------- */
  'add-relation': () => relationModal(),
  'save-relation': async () => {
    const query = document.getElementById('relationTask')?.value ?? '';
    const kind = document.getElementById('relationType')?.value ?? '';
    if (String(query).trim() === '' || !api.RELATION_KINDS.includes(kind)) return;
    try {
      const target = await resolveRelationTask(query);
      if (target === null || typeof target?.id !== 'number') {
        // Nothing matched, or several did. Do not pick one: an inverse relation is created
        // server-side, so guessing wrong writes to a task the user never named.
        const foot = document.querySelector('#modalRoot .modal-foot');
        if (foot !== null) {
          renderRefusal(foot, {messageKey: 'one.error.requestFailed', reason: DENY.SERVER, source: 'server'});
        }
        return;
      }
      await api.addRelation(getViewState(NS).taskId, target.id, kind);
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('one.toast.relationAdded'));
    await refreshAfterWrite();
  },
  'remove-relation': async (event, el) => {
    try {
      await api.removeRelation(
        getViewState(NS).taskId,
        el.getAttribute('data-kind'),
        el.getAttribute('data-other'),
      );
    } catch (err) {
      reportWriteFailure(el, err);
      return;
    }
    toast(t('one.toast.relationRemoved'));
    await refreshAfterWrite();
  },

  /* --- comments --------------------------------------------------- */
  'toggle-comment-order': async (event, el) => {
    const next = getViewState(NS).commentOrder === 'asc' ? 'desc' : 'asc';
    let comments;
    try {
      comments = await api.listComments(getViewState(NS).taskId, {orderBy: next, perPage: 100});
    } catch (err) {
      // The order is stored only once the server has answered in it. Flipping it first would
      // leave the toggle claiming an order the list on screen is not in.
      reportWriteFailure(el, err);
      return;
    }
    setViewState(NS, {commentOrder: next, comments: items(comments)});
    requestRender();
  },
  comment: async (event, el) => {
    const text = String(document.getElementById('commentText')?.value ?? '').trim();
    if (text === '') return;
    try {
      await api.createComment(getViewState(NS).taskId, text);
    } catch (err) {
      // The draft is deliberately NOT cleared here: the comment was not accepted, so the text is
      // still the user's only copy of it.
      reportWriteFailure(el, err);
      return;
    }
    setViewState(NS, {commentDraft: ''});
    toast(t('task.comment.addedSuccess'));
    await refreshAfterWrite();
  },

  /* --- the more-menu and the footer ------------------------------- */
  'toggle-subscribe': async (event, el) => {
    closePopover();
    const subscribed = getViewState(NS).task?.subscription;
    try {
      if (subscribed) await api.unsubscribe('task', getViewState(NS).taskId);
      else await api.subscribe('task', getViewState(NS).taskId);
    } catch (err) {
      reportWriteFailure(el, err);
      return;
    }
    toast(t(subscribed
      ? 'task.subscription.unsubscribeSuccessTask'
      : 'task.subscription.subscribeSuccessTask'));
    await refreshAfterWrite();
  },
  'toggle-favorite': (event, el) => {
    closePopover();
    const favorite = getViewState(NS).task?.is_favorite === true;
    return patchField(el, {is_favorite: !favorite},
      favorite ? 'one.toast.favoriteRemoved' : 'one.toast.favoriteAdded');
  },
  duplicate: async (event, el) => {
    closePopover();
    try {
      await api.duplicateTask(getViewState(NS).taskId);
    } catch (err) {
      reportWriteFailure(el, err);
      return;
    }
    // No re-read: the copy is a DIFFERENT task and this view is still showing the original, which
    // the duplicate did not change.
    toast(t('task.detail.duplicateSuccess'));
  },
  'task-color': () => {
    closePopover();
    colorModal(getViewState(NS));
  },
  'save-color': async () => {
    const hex = document.getElementById('taskColor')?.value ?? '';
    try {
      await api.patchTask(getViewState(NS).taskId, {hex_color: hex});
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('one.toast.colorUpdated'));
    await refreshAfterWrite();
  },

  /* --- delete ----------------------------------------------------- */
  delete: () => {
    closePopover();
    deleteModal();
  },
  'confirm-delete': async () => {
    try {
      await api.deleteTask(getViewState(NS).taskId);
      closeModal();
      // The task is gone: there is nothing left to re-read and no list to return to, so the view
      // becomes a terminal surface rather than reloading into a 404.
      setViewState(NS, {status: 'deleted', task: null});
      requestRender();
      toast(t('task.detail.deleteSuccess'));
    } catch (err) {
      reportModalFailure(err);
    }
  },
});

/** One reminder choice -> the wire shape. Prototype `reminderPayloadFromChoice` (707-715). */
function reminderFromChoice(choice, custom) {
  const relative = (seconds, to) => ({reminder: null, relative_period: seconds, relative_to: to});
  if (choice === 'hour-before-due') return relative(-3600, 'due_date');
  if (choice === 'day-before-due') return relative(-86400, 'due_date');
  if (choice === 'at-due') return relative(0, 'due_date');
  if (choice === 'day-before-start') return relative(-86400, 'start_date');
  if (choice === 'custom') {
    // No date picked is not an error and not a default: silently adding "one day before due" for
    // someone who chose Custom and typed nothing would set a reminder they never asked for.
    if (!custom) return null;
    const at = new Date(custom);
    if (Number.isNaN(at.getTime())) return null;
    return {reminder: at.toISOString(), relative_period: 0, relative_to: null};
  }
  return null;
}

/* ------------------------------------------------------------------ *
 * 10. The listeners the click registry cannot carry
 * ------------------------------------------------------------------ */

let listenersInstalled = false;

/**
 * `change`, `input`, `blur` and the file picker.
 *
 * `app.js`'s registry is click-only, so these four are delegated from `document` and keyed on
 * element ID exactly as the prototype does (1488-1533, 1534). Delegation is what makes wholesale
 * `innerHTML` re-rendering safe: no handler is ever bound to a node a re-render replaces.
 *
 * Installed on the first `mount()` rather than at import time so importing this module in a test
 * with no shell attaches nothing — the same import-time purity contract as api.js and app.js.
 *
 * Every handler re-checks `isRefused`: `aria-disabled` does not stop an event the way `disabled`
 * does, and `readOnly` on an input still fires `blur`.
 */
function installListeners() {
  if (listenersInstalled || typeof document === 'undefined') return;
  listenersInstalled = true;

  document.addEventListener('change', (event) => {
    const el = event.target;
    if (!isTaskReady() || isRefused(el)) return;

    switch (el.id) {
      case 'assignee':
        return void changeAssignee(el);
      case 'priority':
        return void patchField(el, {priority: Number(el.value) || 0}, 'task.detail.updateSuccess');
      case 'due':
        return void patchField(el, {due_date: dateInputToIso(el.value)}, 'task.detail.updateSuccess');
      case 'start':
        return void patchField(el, {start_date: dateInputToIso(el.value)}, 'task.detail.updateSuccess');
      case 'end':
        return void patchField(el, {end_date: dateInputToIso(el.value)}, 'task.detail.updateSuccess');
      case 'repeatMode':
      case 'repeatEvery':
      case 'repeatUnit':
        return void patchField(el, repeatPayload(repeatFromControls()), 'task.detail.updateSuccess');
      case 'progress':
        return void patchField(el, {percent_done: (Number(el.value) || 0) / 100}, 'task.detail.updateSuccess');
      default:
    }
  });

  // The live percentage echo. It writes ONE text node and never re-renders: a render on every
  // pixel of a range drag would rebuild the page under the user's thumb.
  document.addEventListener('input', (event) => {
    // The comment draft is kept in the view scratch as it is typed. `setViewState` does NOT
    // render (app.js), so this costs one assignment per keystroke and nothing else; the value is
    // read back by `commentsSection` on the next render and cleared once the comment is posted.
    if (event.target?.id === 'commentText') {
      setViewState(NS, {commentDraft: String(event.target.value ?? '')});
      return;
    }
    if (event.target?.id !== 'progress' || isRefused(event.target)) return;
    // `readOnly` does nothing to a range input, so a refused slider still drags. The `change`
    // handler above refuses the write; without this guard the pill would echo a percentage the
    // server was never asked to store. Recorded: the honest fix is a `type=range` case in
    // app.js's `refuseControl`, which is not this file.
    const label = document.getElementById('progressText');
    if (label === null) return;
    label.textContent = t('one.task.percent', {percent: formatNumber(Number(event.target.value) || 0)});
  });

  // Capture phase: `blur` does not bubble.
  document.addEventListener('blur', (event) => {
    const el = event.target;
    if (!isTaskReady() || isRefused(el)) return;
    const task = getViewState(NS).task;

    if (el.id === 'taskTitle') {
      const title = String(el.value ?? '').trim();
      // An empty title is rejected by the model (pkg/models/tasks.go:68, minLength 1), so an
      // emptied field is restored rather than sent and refused.
      if (title === '' || title === task?.title) {
        el.value = task?.title ?? '';
        return;
      }
      void patchField(el, {title}, 'task.detail.updateSuccess');
      return;
    }

    if (el.id === 'description') {
      const description = String(el.value ?? '');
      if (description === (task?.description ?? '')) return;
      // THE ONLY WRITE ON THIS PAGE THAT CARRIES `X-Vikunja-Format` (api.js
      // `updateTaskDescription`). Any other PATCH carrying it would round-trip the untouched
      // stored HTML through the converter and quietly degrade it.
      void saveDescription(el, description);
    }
  }, true);

  // Enter in the inline label chip commits it, prototype 1563.
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' || event.target?.id !== 'inlineLabelInput') return;
    if (!isTaskReady() || isRefused(event.target)) return;
    event.preventDefault();
    void saveInlineLabel(event.target);
  });

  // The shell's file picker (task.html). It is created once and never replaced, so this binds
  // directly rather than delegating.
  document.getElementById('attachmentInput')?.addEventListener('change', async (event) => {
    const input = event.target;
    const files = [...(input.files ?? [])];
    // Cleared before the upload so picking the same file twice in a row still fires `change`.
    input.value = '';
    if (files.length === 0 || !isTaskReady()) return;
    await uploadAttachments(files);
  });
}

function isTaskReady() {
  const state = getViewState(NS);
  return state.status === 'ready' && state.task !== null && state.task !== undefined;
}

/** The repeat builder's three controls, read at the moment of the change. */
function repeatFromControls() {
  return {
    active: true,
    mode: document.getElementById('repeatMode')?.value === 'current' ? 'current' : 'default',
    every: Math.max(1, parseInt(document.getElementById('repeatEvery')?.value ?? '1', 10) || 1),
    unit: document.getElementById('repeatUnit')?.value ?? 'days',
  };
}

async function saveDescription(el, description) {
  try {
    await api.updateTaskDescription(getViewState(NS).taskId, description);
  } catch (err) {
    reportWriteFailure(el, err);
    return;
  }
  toast(t('task.detail.updateSuccess'));
  await refreshAfterWrite();
}

/**
 * The single-assignee UI the prototype models: clear whoever is assigned, then assign the pick.
 *
 * `DELETE /api/v2/tasks/{id}/assignees/{user}` takes a NUMERIC user id here, unlike the identical
 * `{user}` segment on the team-member routes which takes a username (api.js `removeAssignee`).
 */
async function changeAssignee(el) {
  const taskId = getViewState(NS).taskId;
  const existing = Array.isArray(getViewState(NS).task?.assignees) ? getViewState(NS).task.assignees : [];
  const next = Number(el.value) || 0;
  try {
    for (const assignee of existing) {
      if (assignee?.id !== next) await api.removeAssignee(taskId, assignee.id);
    }
    if (next > 0 && !existing.some((assignee) => assignee?.id === next)) {
      await api.addAssignee(taskId, next);
    }
  } catch (err) {
    // Sequential, not parallel, and it stops at the first refusal: a half-applied swap that
    // removed the old assignee and then failed to add the new one leaves the task unassigned, and
    // the re-read below is what puts the true state back on screen either way.
    reportWriteFailure(el, err);
    await refreshAfterWrite();
    return;
  }
  toast(t(next > 0 ? 'task.assignee.assignSuccess' : 'task.assignee.unassignSuccess'));
  await refreshAfterWrite();
}

/**
 * Attachment upload. `POST /api/v2/tasks/{task}/attachments` answers **201 with a per-file
 * `errors` array**: partial success is the designed behaviour (api.js `uploadAttachments`,
 * pkg/routes/api/v2/task_attachments.go:72). `res.ok` is a lie here for the same reason bar 8
 * says it is on the commercial service, so the envelope is branched on rather than the status,
 * and a partial failure reports the server's own sentence instead of a success toast.
 */
async function uploadAttachments(files) {
  let result;
  try {
    result = await api.uploadAttachments(getViewState(NS).taskId, files);
  } catch (err) {
    reportWriteFailure(document.querySelector('[data-action="upload"]'), err);
    return;
  }

  // BOTH FIELDS ARE CITED. `AttachmentUploadResult` declares exactly two — `errors` and
  // `success` — at pkg/web/files/task_attachment.go:40-43, and `BuildUploadResult` (:47-52) fills
  // `Success` with the attachments that were created. The subtraction is the fallback for a
  // response that carried neither, not the primary reading.
  const errors = Array.isArray(result?.errors) ? result.errors : [];
  const added = Array.isArray(result?.success) ? result.success.length : files.length - errors.length;

  // The re-read comes FIRST even on a partial failure: the files that did land are real and the
  // list has to show them. The refusal is written afterwards, onto the re-rendered button, so it
  // survives the render rather than being wiped by it.
  await refreshAfterWrite();

  if (errors.length > 0) {
    const message = String(errors[0]?.message ?? '');
    const trigger = document.querySelector('[data-action="upload"]');
    if (trigger !== null) {
      renderRefusal(trigger, message === ''
        ? {messageKey: 'one.error.requestFailed', reason: DENY.SERVER, source: 'server'}
        : {message, reason: DENY.SERVER, source: 'server'});
    }
    toast(message === '' ? t('one.error.requestFailed') : message);
    return;
  }

  toast(t('one.toast.attachmentsAdded', {count: added}));
}

/* ------------------------------------------------------------------ *
 * 11. Registration
 * ------------------------------------------------------------------ */

registerView('task', {render, mount});
