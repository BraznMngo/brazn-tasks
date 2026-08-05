---
name: bra-feature-request
description: Use when turning a raw feature idea into a scoped BRA ticket for Brazn Tasks. Checks the idea against the fork's five permitted patch areas, writes acceptance criteria, names the branch, and picks the verification gates. Drafting only — files nothing in a tracker.
user-invocable: true
---

# Feature idea → scoped BRA ticket

Turns a one-line idea into a ticket someone can pick up without re-deriving the fork's constraints. **This skill produces a draft and stops** — it does not create issues, branches, or PRs.

**Reference docs:** `CLAUDE.md` at the repo root is the governing document; this skill is the intake checklist on top of it. `AGENTS.md` is upstream's own description of how the codebase works.

## 1. Patch-surface check (first, because it can end the ticket)

Every patch we carry is re-applied on every upstream upgrade, so scope *is* the long-term cost. Place the idea in exactly one of the five permitted areas (`CLAUDE.md` §3):

1. Branding and edition UX
2. Backend managed-mode enforcement
3. Branded email templates
4. Entitlement synchronization
5. Trusted topology provisioning

Answer these in the ticket, above the description:

- **Which area, and why that one?** If it genuinely needs two, that is usually two tickets.
- **Fits none?** Stop and say so plainly. It needs an explicit recorded decision *before* code is written — the ticket's first job is to request that decision, not to assume it. Size is not the test; the five areas are.
- **Could this be configuration instead of a patch?** A patch that could have been a config value is a permanent tax. Say what you ruled out.
- **Could this use an upstream extension point instead of editing an upstream file?** Prefer the extension point. If an upstream file must change, name the file and say why nothing else works.

## 2. Acceptance criteria

Write observable behavior, not implementation steps — a fresh agent with no implementation context has to judge the diff against these at Gate 2.

- One criterion per line, each independently checkable.
- State the negative cases: what must be **refused**, and by which guard.
- For every negative criterion, name the production guard whose deletion would make the test fail. If you cannot name one, the criterion is not yet testable — that is a finding to resolve now, not a formatting problem (`CLAUDE.md` §4).
- Nothing executes on the Windows host — no `go`, no `node`, no Docker, no parse check. Every criterion must be verifiable by GitHub-hosted CI, or explicitly deferred to Gate 4. Never write "verify locally".

## 3. Branch and target

- Branch: `brazn/<ticket>-<slug>`, e.g. `brazn/bra-774-baseline`.
- Cut from, and PR into, `brazn/main`. Never `main` — that tracks upstream Vikunja and is not our line of development.
- One ticket, one branch, one agent.

## 4. Which gates apply

Name them in the ticket so nobody guesses (`CLAUDE.md` §4):

- **Gate 1 — CI.** Always. Green on GitHub-hosted CI for the exact SHA; cite the run.
- **Gate 2 — Independent review.** Always. A fresh agent, no implementation context, against §2's criteria.
- **Gate 3 — Integration review.** When an epic closes, or the change crosses subsystems.
- **Gate 4 — Manual acceptance (Sebastian).** Whenever real credentials, real mail delivery, or real subscription state is involved.

## 5. Brief the work so it costs one context, not three

- Name the **exact files and symbols** the work touches. Never write "read subsystem X in full" (`CLAUDE.md` §6).
- Wording, labels, copy and config values are fixed inline by the reviewer — never a task for a development agent.
- For a visual or CSS change, list every rule already touching the element *before* proposing a change. If the description has more than one plausible reading, ask instead of guessing (`CLAUDE.md` §5).

## Anti-patterns (these come back at review every time)

- Opening with a solution and never naming the patch area.
- Acceptance criteria that restate the implementation ("adds a `Can*` method") instead of the behavior a user can observe.
- A negative criterion with no named guard — it reliably produces a test that cannot fail.
- "Verify locally" or "run the app and check" — impossible here, so it silently drops the change to zero verification.
- Speculative scope ("while we're in there…"). Every extra line is re-applied on every upstream bump.
- Assuming an out-of-scope idea is acceptable because it is small.

## Related

- Governing rules: `CLAUDE.md` — §3 patch surface, §4 gates and test quality, §5 pre-push correctness, §6 briefing.
- Upstream codebase conventions: `AGENTS.md`.
- What a patch costs at upgrade time: `UPGRADING-FROM-UPSTREAM.md`.
