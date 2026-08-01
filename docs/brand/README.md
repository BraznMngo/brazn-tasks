# Brand source artwork

`percy-wordmark-source.png` is the master the shipped notification-email logo is derived
from. It is 1536 x 1024, 1.1 MB, opaque, SHA-256 `aa2a0614…07ffcea`, and it is the same
file as `docs/brand/percy-wordmark-source.png` in the private Percy repository, which is
where it is maintained. Supplied by Sebastian, 2026-08-01.

**Nothing in this directory ships.** `.github/workflows/brand-assets.yml` derives
`pkg/notifications/logo.png` from the file here, so a shipped asset can always be
regenerated rather than reverse-engineered from whatever happened to be committed
somewhere else. Do not edit the derived file by hand — the workflow's path filter
covers it, so an edit only triggers a regeneration over the top.

## Why it is committed to a public repository

This fork is public because the AGPL requires it, so committing the artwork here
publishes it. That is the intended outcome and not an accident of the mechanism: the
same artwork is attached to every non-conversational notification mail the product
sends, which is a wider and less controlled distribution than a public Git repository.

## Why it is the Percy wordmark and not a Brazn Tasks one

Sebastian's decision, 2026-08-01: *"logo yes wordmark scaled down i will later replace
with a brazn task one."* Until that mark exists, the mail carries the Percy wordmark with
`alt="Brazn Tasks"` beside it. Replacing this file and letting the workflow re-derive is
the whole of that future change.

The point it does settle is the one with a legal edge: `pkg/notifications/logo.png` was
byte-identical to upstream Vikunja's, and the AGPL does not license the Vikunja
trademark.

## It has no transparency, and cannot be given any

PNG color type 2 — truecolour, no alpha channel, and no `tRNS` chunk either. The `P`
ribbon's glow fades continuously into the white background rather than ending at an
edge, so there is no colour that can be keyed out and no alpha matte to recover: a
luminance key would take the glow and the ribbon's own white highlights with it.

The derived logo is therefore opaque, and `mailTemplateHTML` in
`pkg/notifications/mail_render.go` puts a matching white band behind it so it does not
render as a box on the `#f3f4f6` mail body. Both are reversible in one commit each if a
wordmark with real transparency is ever exported.
