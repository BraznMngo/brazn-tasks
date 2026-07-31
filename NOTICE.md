# Modification notice and source offer

## What this software is

**Brazn Tasks is a modified version of [Vikunja](https://vikunja.io).**

- Upstream project: Vikunja — <https://vikunja.io>
- Upstream source: <https://github.com/go-vikunja/vikunja>
- Upstream version this fork is based on: **v2.4.0**
- Corresponding source for this modified version: <https://github.com/BraznMngo/brazn-tasks>

Brazn Tasks is published by Brazn Mngo. It is **not** affiliated with, endorsed
by, sponsored by, or a product of the Vikunja project or its maintainers. Do not
report Brazn Tasks problems to the Vikunja project as though they were Vikunja
problems.

## Copyright and licence

Most of this repository is licensed under **AGPL-3.0-or-later**; see
[`LICENSE`](LICENSE). The contents of [`desktop/`](desktop/) are licensed under
**GPL-3.0-or-later**; see [`desktop/LICENSE`](desktop/LICENSE).

Copyright in the overwhelming majority of this code is held by the upstream
Vikunja authors ("Copyright 2018-present Vikunja and contributors"). This fork:

- keeps every upstream copyright header exactly as upstream ships it;
- keeps `LICENSE` and `desktop/LICENSE` unmodified;
- does not rewrite history, and does not remove or reassign author attribution.

Modifications made by Brazn Mngo are likewise released under the licence of the
file they modify.

## Source offer (AGPL-3.0 section 13)

If you interact with a Brazn Tasks instance over a network, you are entitled to
the corresponding source for the version you are interacting with. That source
is published at:

**<https://github.com/BraznMngo/brazn-tasks>**

The running application links to it: the footer link in the sidebar navigation
and on public link-share pages points at this repository, so the offer is
reachable without leaving the app. Released builds also carry it in their
package metadata — the container image's `org.opencontainers.image.source`
label and the OS package `homepage` field.

## Trademarks

The AGPL licenses the *code*. It does not license names, logos or wordmarks.

"Vikunja", the Vikunja wordmark and the Vikunja llama logo are marks of the
Vikunja project. They are not licensed to this fork, and Brazn Tasks does not
use them to identify itself. Where "Vikunja" appears in this repository it is
either:

- an upstream copyright notice or licence text, which must not be altered;
- a factual, descriptive reference to the upstream project (for example, this
  notice, or an import filter that reads a Vikunja data export); or
- an internal identifier — a Go import path, database table, configuration key,
  URI scheme or HTTP header — that is not shown to users and that is
  deliberately left unchanged so this fork can keep taking upstream updates.

Nothing in this repository is intended to suggest that Brazn Tasks is Vikunja,
or that the Vikunja project stands behind it.

## Summary of user-visible changes from upstream v2.4.0

- Product name, window and page titles, PWA manifest, and visible interface
  strings say "Brazn Tasks".
- The Vikunja logo, wordmark and llama mascot artwork have been removed from the
  interface and replaced with placeholder marks.
- Outgoing email identifies the sender as Brazn Tasks; TOTP enrolment uses
  "Brazn Tasks" as the authenticator issuer.
- The desktop client no longer offers the upstream-operated Vikunja Cloud and
  try.vikunja.io sign-in shortcuts; any server can still be reached through the
  custom-server option.
- Package, container and service metadata identify Brazn Tasks and point at this
  repository.

Internal identifiers, the `/api/v1` route namespace, configuration keys and the
database schema are unchanged, so the API and existing deployments behave
exactly as upstream v2.4.0 does.
