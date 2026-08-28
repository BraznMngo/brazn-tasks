/**
 * Types for `view-task.js`, the task page's view module.
 *
 * THE MODULE'S WHOLE PUBLIC SURFACE IS THE TWO FUNCTIONS `app.js` CALLS. Everything else it does
 * — every modal, every field write, every print of a project name — is reached by clicking a
 * control, because `app.js` owns the one delegated click listener on the page and dispatches to
 * handlers this module registered at import time. That is why this file declares two functions
 * and not twenty: a test that wants a modal opens it the way a person does.
 *
 * `TEST-INVENTORY.md` recorded the absence of this file as the reason the two view modules had no
 * render tests at all. BRA-1414 needed those tests — a project name is printed in five places and
 * only one of them was going through the naming helper — so the file exists now.
 */

// `./app.js` rather than `./app.d.ts`: TypeScript resolves the runtime specifier to the
// hand-written declarations beside it, which is the same resolution every test in
// `src/brazn/one/` relies on and is why none of them needs an `@ts-expect-error`.
import type {ViewContext} from './app.js'

/**
 * The HTML for `#app`. Emits every gated node and lets `applyGates` decide its fate (ruling C4).
 */
export function render(ctx: ViewContext): string

/**
 * Runs after insertion and BEFORE gates. Installs the `change` / `input` / `keydown` listeners
 * this view needs, and starts the task read unless the state already holds that task.
 */
export function mount(root: Element, ctx: ViewContext): void
