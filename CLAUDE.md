# Brazn Tasks — repository development instructions

Brazn Tasks is a **public fork of [Vikunja](https://vikunja.io)** (`go-vikunja/vikunja`),
pinned at upstream tag **v2.4.0**. It is AGPL-3.0 licensed, and it stays public because the
AGPL requires it. Upstream authorship and attribution stay accurate and visible: we are
re-branding a derivative work, not concealing where it came from.

Upstream's own codebase conventions live in **`AGENTS.md`** (build layout, code style, where
things are). That file is upstream's and is deliberately left unmodified — read it for *how
the codebase works*. This file governs *how we are allowed to work in it*, and where the two
conflict, this file wins. It is adapted from the Percy repository's `CLAUDE.md`.

> Note: upstream ships `CLAUDE.md` as a symlink to `AGENTS.md`. This fork replaces it with a
> real file. Keep both.

## 1. Mandatory host-execution safety rule

The Windows development host is **not** a build or test environment. A development build was
followed by a host blue screen on 2026-07-22 (`SYSTEM_THREAD_EXCEPTION_NOT_HANDLED`,
`dxgmms2.sys`). Causation was never proven and does not need to be: the isolation policy is
strict regardless.

On the host, agents may only:

- read files;
- edit files with patch-based tools;
- inspect Git metadata and textual diffs (`git`, `gh`);
- perform other operations explicitly documented as non-executing static inspection.

On the host, agents must **never** run this project's code or development tooling, including:

- `go`, `gofmt`, `golangci-lint`, `mage`, the `vikunja` binary, or any compiled artefact;
- `node`, `npm`, `npx`, `pnpm`, Vite, TypeScript, ESLint, Stylelint, Playwright, Cypress,
  Vitest, or any other frontend tool or test runner;
- `docker`, `docker compose`, database servers, or any container/VM runtime;
- repository scripts, generated executables, dependency installers, or packaging tools;
- the application itself, in any form, including "just to check something".

`node --check` (parse-only, executes no program) is the single approved exception — but note
that **Node is not installed on the development host at all**, so in practice there is no local
check of any kind, for any language. Do not plan on one. Verified 2026-08-01: not on `PATH`,
not in `C:\Program Files\nodejs`, not under `%LOCALAPPDATA%\Programs`, no nvm. An agent
reporting that it could not run this is telling the truth, not making an excuse.

Builds and tests run **only** on GitHub-hosted runners via the workflows in
`.github/workflows/`. Self-hosted runners, Windows Sandbox, Hyper-V, WSL, Docker, local
containers and local VMs are forbidden fallbacks. If GitHub-hosted CI is unavailable,
executable verification stops — it does not move to the host.

Before claiming anything was verified, identify the exact commit SHA and the GitHub-hosted
run that verified it. If the run is absent or uncertain, **stop** and say so.

## 2. Mandatory branch and integration rule

- The remote is **`BraznMngo/brazn-tasks`** (public), a fork of `go-vikunja/vikunja`.
- The default and integration branch is **`brazn/main`**, cut from the upstream **v2.4.0**
  tag. Never develop, commit, or push directly to `brazn/main`.
- Every ticket gets one child branch named **`brazn/<ticket>-<slug>`** (e.g.
  `brazn/bra-774-baseline`) and one agent. Ticket PRs target `brazn/main`.
- `brazn/main` is protected: pull request required before merging, required status checks,
  no force-push, no deletion. If a required check is ever renamed in `.github/workflows/`,
  branch protection must be updated **in the same change**, or every merge will block on a
  check that can never report.
- **`main` tracks upstream Vikunja and is not our line of development.** Do not build from
  it, do not treat it as a baseline, and do not push to it casually. It exists so upstream
  history stays fetchable.
- The `upstream` remote (`https://github.com/go-vikunja/vikunja`) is merged **deliberately,
  by an explicit human decision, and never on a timer.** There is no scheduled sync, no
  auto-merge, and none may be added. Its push URL is disabled in the reference clone.
- Never rewrite history on any branch. Never delete upstream's inherited branches or tags.

### Inherited CI

Upstream's project-management workflows are **disabled** at the repository level (`Release`,
`Preview`, `Crowdin Sync`, `Dependency Checks`, `Update nixpkgs`, the stale-issue bot, the
auto-labeller, the issue-closed commenter). Only `ci`, its reusable `Test` workflow, and
`Notify Percy Vikunja deploy` are active. Do not re-enable a disabled workflow without an
explicit decision recorded in the ticket.

`ci.yml` on this branch deliberately **does not** call `release.yml`: upstream's release job
pushes container images to `ghcr.io/${{ github.repository_owner }}` with no
upstream-repository guard, which on a fork means publishing under the Brazn name. Brazn
Tasks images are built and promoted by the Percy container pipeline instead. Do not
reinstate that job.

## 3. Patch-surface limits (from the governing ADR)

Every patch we carry must be re-applied on every upstream upgrade, so the patch surface is
the long-term cost of this fork. Changes are confined to **five** areas:

1. **Branding and edition UX** — names, logos, colours, product-facing copy, edition-aware
   interface behaviour.
2. **Backend managed-mode enforcement** — server-side restrictions that make the hosted
   edition behave as a managed service.
3. **Branded email templates** — transactional mail rendered under the Brazn identity.
4. **Entitlement synchronization** — projecting subscription/plan state into the app.
5. **Trusted topology provisioning** — creating and protecting the structures the product
   guarantees.

**Anything outside this list requires an explicit recorded decision before it is written.**
Prefer configuration over patching, and upstream-compatible extension points over edits to
upstream files. A patch that could have been a config value is a permanent tax.

## 4. Mandatory engineering and QA rule

Follow YAGNI. Minimize lines of code, dependencies, and public surface; prefer the smallest
correct solution over speculative abstraction. Tests must challenge observable behavior
rather than mirror the implementation. Because every line we add is re-applied on each
upstream bump, "small" here is a correctness property, not a style preference.

Four gates, in order:

1. **Gate 1 — CI.** GitHub-hosted CI green for the exact SHA. This is the only environment
   allowed to execute anything.
2. **Gate 2 — Independent review.** A *fresh* agent with no implementation context reviews
   the diff against the ticket's acceptance criteria. One complete review per subsystem;
   after corrections, re-review only the changed code and its immediate boundaries.
3. **Gate 3 — Integration review.** One agent runs the affected journeys end-to-end before
   an epic closes. Repository-wide inspection is permitted here and only here.
4. **Gate 4 — Credentialed/manual acceptance (Sebastian).** Real accounts and real
   credentials, never on the development host as development or testing.

Development and test must run **this** image. Stock Vikunja cannot exercise managed mode,
protected topology or entitlement projection, so a stock instance produces false confidence.

### A test that cannot fail is worse than no test

Because CI is the only verifier here, a test that passes for the wrong reason does not merely
miss a bug — it spends a full CI round-trip reporting that the code is fine. Five instances
landed on 2026-08-01 across this repository and Percy's. Every one read as the most valuable
assertion in its file. Four distinct shapes, in increasing order of how hard they are to see:

- **Self-referential comparison.** The expected value is produced by the code under test, so
  the test agrees with itself whatever the code does. Assert against a fixed, independently
  written expectation — the literal contract string, not a value the implementation computed.
  `pkg/modules/brazn/entitlement/entitlement_test.go` is the pattern: it writes the signing
  prefix out as a literal rather than calling `SigningInput`, so it asserts conformance to the
  contract instead of agreement with our own constant.
- **A difference asserted in the wrong place.** The setup really does construct the bad input,
  but something between the fixture and the code under test normalises it away, and the
  assertion sits before that step. `TestVerifyRejectsReformattedJSON` asserted `require.NotEqual`
  on its own intermediate buffer, which was true — then building the envelope ran `json.Marshal`
  over a `json.RawMessage`, which **compacts** it, so the indented payload reached the verifier
  byte-identical. Note the direction: Go *erased* the difference rather than preserving it, and
  reasoning from "the field is passed through verbatim" gets it exactly backwards and produces
  the same broken test again. **Assert the difference where it decides the outcome, not where
  you happen to create it.** The degenerate version is a setup that never constructs the bad
  input at all: an oversized body that is not oversized, a second organization that is not a
  second one.
- **An unrelated guard masking the bug.** Setup correct, assertion correct, still worthless —
  the refusal the test observed came from somewhere else. A Teams reparent test asserted a
  refusal, got one, and passed against genuinely buggy code, because Huma's autopatch re-entered
  the root router and a *different* guard refused the inner request. Nothing about the test
  looks wrong, which makes this the hardest of the three to catch by reading.
- **The evidence moves out of the assertion's field of view.** Setup correct, assertion correct,
  guard deleted, test green — and worse than silent, because the test then *attests the opposite*
  of the property it was written for. `TestEntitlementIngestAcceptsAnErasedSubject` asserted on
  `user_id = 987654321`; with the guard removed, `ApplyEntitlement` falls through and inserts the
  erased subject's full signed envelope at **`user_id = 0`** — legal, since the column is
  `bigint not null unique` with no foreign key. Every assertion still passed while the code
  retained exactly the organization, edition, seat status and timestamps that erasure exists to
  destroy. The assertion never moved; the evidence did. **When a mutation can relocate the
  evidence, assert over the whole store rather than keying on the identifier under test** — count
  `brazn_entitlement_projections` around the operation, so a row written under *any* id is
  caught. And add a control proving the count moves for a normal case, or a frozen counter makes
  the test vacuous in the other direction: guard the guard.

**The cheap check, and it belongs in every brief that asks for a negative test:** deleting the
production guard must make the test fail. If it still passes, the test is not testing that
guard. This is the only reliable way to catch the third and fourth shapes, where reading proves
nothing. Nobody can run that check on this host, which is exactly why it has to be stated as a
required reasoning step rather than left as an aspiration.

**And state the mutation claim in the test, because writing it down is what exposes it.** Both
times a test in this repository carried a comment saying "deleting X makes this fail", the claim
was **wrong** — once about production code the test never imported, once about a mutation that
relocated the row instead of refusing it. Neither error was visible in the test; both were
visible the moment someone traced the sentence. A mutation claim nobody checks is decoration, but
a mutation claim written down is a claim a reviewer can disprove.

The same trap catches values shared with the commercial service, and this fork has already paid
for it twice — both in `Verify`, both found only by building the other side against it. It
shipped without the domain-separation prefix, and then decoding base64url as padded base64;
either alone meant **no conforming projection could be accepted at all**. An interop constant
both sides derive from one definition is checked by neither, and an encoding both sides merely
*assume* is checked by neither either. Pin such values against the contract text by literal,
and test the **wire form** rather than a round trip through our own encoder.

## 5. Mandatory pre-push correctness rule

Local compilation is forbidden, so every push is verified only by remote CI and each wrong
guess costs a full CI round-trip. Reason a change through completely before pushing it;
never use CI as a substitute for thinking.

- Go code must be `gofmt`-clean and satisfy `golangci-lint` as configured in
  `.golangci.yml`. Match the surrounding file's existing style exactly rather than
  introducing a new one.
- Frontend code must satisfy ESLint, Stylelint and `vue-tsc` as configured. Do not reformat
  untouched lines; a large diff on an upstream file makes every future upstream merge harder.
- For a visual/CSS change described in prose, first identify **every** rule already touching
  the affected element and state before changing any of them. Layered mechanisms can each
  independently match an ambiguous description. If more than one reading is plausible, ask,
  or make the narrowest change that matches the literal request.

## 6. Mandatory agent token-efficiency rule

The dominant cost is context re-derivation, not writing code.

- **Text, labels, copy and config values are fixed by the reviewer, inline.** Never dispatch
  a development agent for a wording or config-value change.
- **Never say "read subsystem X in full" in a brief.** Name the exact files, and where
  possible the exact symbols, that the task needs.
- **Cap concurrency at 4 agents.** Every agent pays the same large fixed context cost, so
  more parallelism past this point buys wall-clock at a steeply rising token price.
- **No agent waits on CI.** Agents push and report; whoever orchestrates watches the run.
- **One review pass, not a polish loop.** Draft, review once, park what is unresolved.
- Do not recursively read or search the whole repository by default. This is a large
  upstream codebase; a repo-wide read is affordable only for a sanctioned Gate 3 review.
- Prefer structures that discover rather than enumerate. A hand-maintained registry file
  that every ticket appends to is a standing merge-conflict source — say so in the ticket
  that creates it.

## 7. AGPL-3.0 and trademark obligations

- **The source stays public.** AGPL §13 requires that users interacting with a modified
  version over a network are offered its complete corresponding source. The hosted product
  must link to this repository at the exact deployed revision.
- **Keep upstream notices intact.** `LICENSE`, copyright headers and attribution notices are
  not ours to remove. Modified files should record that they were changed.
- **Vikunja's name and logo are trademarks, and the licence does not grant them.** Shipped
  surfaces carry Brazn branding; upstream is credited in prose ("based on Vikunja"), never in
  a way that implies endorsement by, or affiliation with, the Vikunja project.
- Do not remove or weaken attribution to make the fork look original. The obligation is to
  re-brand, not to conceal.
