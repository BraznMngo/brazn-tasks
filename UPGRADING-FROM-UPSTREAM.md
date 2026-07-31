# Upgrading Brazn Tasks from upstream Vikunja

This fork is pinned to the upstream **v2.4.0** tag. Pinning is only half the answer: the
recurring cost of a fork is the upgrade, and this file is the procedure for taking a 2.4.x
(or later) upstream release.

Written before the first patches diverged the tree, on purpose. Improvising this after
divergence is far more expensive than following it.

## The failure this exists to prevent

An upstream merge can silently restore behaviour we deliberately removed, and nothing tells
us. The concrete example, found while baselining the fork (BRA-774):

Upstream's `ci.yml` calls `release.yml` as a **reusable workflow** whenever the branch is
`main` or a `v*` tag is pushed. That job logs in to `ghcr.io` as
`${{ github.repository_owner }}` with the built-in `GITHUB_TOKEN` and pushes container
images — with **no** `github.repository == 'go-vikunja/vikunja'` guard anywhere in the file.

A reusable workflow reached through `uses:` runs inside the *caller's* run, so **disabling
`Release` at repository level does not stop it.** BRA-774 removed the `release` job from
`ci.yml` on `brazn/main`. Merging `upstream/main` will reintroduce it, and our organization
starts publishing images — no error, no failing check.

That is the shape of the whole problem. A rule that depends on someone remembering is the
rule that already failed. The guards below matter more than this prose.

## Automated guards

These run in CI on every pull request and every push to `brazn/main`. They are what actually
stop a regression; treat a failure as a blocking finding, never as noise to silence.

| Guard | Where | Fails when |
|---|---|---|
| Release job absent | `.github/workflows/fork-guards.yml` | `ci.yml` calls `release.yml` again, or a `release` job reappears |
| Inherited workflows still disabled | `.github/workflows/fork-guards.yml` | `Release` or `Preview` is active at repository level |
| Every mutating route classified | `pkg/routes/route_classification_test.go` (BRA-925) | an upstream change adds a mutating route absent from `pkg/routes/route-classification.json` |

The third guard is the reason the route-classification harness was built against a stock
endpoint set rather than a diverged one. Built then it was an inventory; built later it would
have been archaeology.

## Procedure

### 1. Evaluate before merging anything

Read the upstream release notes and the diff `v2.4.0..<new tag>` with these questions:

- Does it touch any of the **five sanctioned patch surfaces** (CLAUDE.md §3): branding and
  edition UX, backend managed-mode enforcement, branded email templates, entitlement
  synchronization, trusted topology provisioning?
- Does it touch **OAuth redirect validation**? We patch that to admit `percy://`. It is
  precisely the kind of code an upstream security fix will rewrite, and a security fix is
  the case where we most want the upstream change and least want to lose our patch.
- Does it change the **route set**? New endpoints must be classified before managed mode can
  enforce them.
- Does it change `.github/workflows/`? Assume yes, and assume it re-arms something.

Record the answers in the upgrade ticket. This is the step that decides how much work the
upgrade is; skipping it does not make the work smaller.

### 2. Merge deliberately

Never automatically, never on a timer, never through a bot. The `upstream` remote exists with
its push URL disabled for exactly this reason.

```
git fetch upstream --tags
git checkout -b brazn/upgrade-<version> brazn/main
git merge v<version>
```

Resolve conflicts in favour of **upstream's logic plus our patch**, not one or the other. A
conflict in a file we patch is the expected outcome, not a surprise.

### 3. Re-apply and verify every patch

Each patch needs a **check that proves it survived**, not a memory that it once existed. Work
through the five patch surfaces and confirm each has a test, a guard, or an explicit
verification step. A patch with no check is the next silent regression — add the check as
part of the upgrade rather than deferring it.

Specifically confirm:

- `percy://` is still accepted as an OAuth redirect scheme, and PKCE, code lifetime, token
  lifetime and rotation are still exactly as upstream ships them.
- Managed-mode enforcement still covers every route classified `protected-topology` or
  `access-expanding`.
- De-branding still holds in served API docs, backend output and the login page.

### 4. Assert the removals held

The guards in the table above cover the release job and the disabled workflows. Additionally
confirm by hand, once, that the **required status-check names still exist and still report** —
a renamed check does not fail, it simply never reports, and branch protection then blocks
every merge on a check that can never arrive. If a check is renamed, update branch protection
in the same change.

### 5. Re-run the route-classification harness

An upstream mutation endpoint our managed mode does not classify must fail CI. If it does not
fail, the harness is broken and that is the first thing to fix — a guard that passes because
it stopped looking is worse than no guard.

### 6. Release

Build, deploy the digest to development, promote the **tested digest** — never a floating tag —
and know the rollback: redeploy the previous digest. Images are built and promoted by the
Percy container pipeline, not by this repository's CI.

## Host-execution safety

No Go, node, npm, mage or Docker on the Windows host. Every step above that executes anything
happens in this repository's CI or in the Percy container pipeline. See CLAUDE.md §1.

## Proving the procedure works

Do a dry run against a real upstream commit **before** the procedure is needed in anger — a
merge of a later 2.4.x onto a throwaway branch, carried through steps 1–5, discarded at the
end. An untested runbook is a draft, and discovering that during a security upgrade is the
worst possible time.
