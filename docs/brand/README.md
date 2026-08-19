# Brand source artwork

`one-mark-source.png` is the master the shipped notification-email logo is derived from.
It is the ONE mark, 1536 x 1024, RGBA, from the shared drive at
`Brazn Mngo Share/Development/ONE (Percy)/ONE - light mode.png`. `one-mark-source.svg` is
the vector the PNG was exported from, kept because email clients do not render SVG and
the next export should come from here rather than from a screenshot.

**Nothing in this directory ships.** `.github/workflows/brand-assets.yml` derives
`pkg/notifications/logo.png` from the PNG here. Do not edit the derived file by hand: the
workflow's path filter covers it, so an edit only triggers a regeneration over the top.

## Why it is committed to a public repository

This fork is public because the AGPL requires it, so committing the artwork here
publishes it. That is intended: the same artwork is attached to every notification mail
the product sends, which is wider distribution than a public Git repository.

The point it settles is the one with a legal edge. `pkg/notifications/logo.png` was once
byte-identical to upstream Vikunja's, and the AGPL does not license the Vikunja trademark.

## It has real transparency

The mark that preceded this one was the Percy wordmark, PNG color type 2 with no alpha
and no `tRNS` chunk, so `mailTemplateHTML` had to paint a white band behind it or it
rendered as a box on the mail body. This source has an alpha channel and the band is gone.
