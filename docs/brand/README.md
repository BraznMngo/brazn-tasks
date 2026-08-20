# Brand source artwork

`one-wordmark-source.png` is the master the shipped notification-email logo is derived
from: the ONE mark, exported from Sebastian's vector source and trimmed to its own
content. It replaces the interim Percy wordmark this ticket (BRA-1374) removed.

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

## It has a real alpha channel, unlike the wordmark it replaced

The interim Percy wordmark (removed by BRA-1374) was PNG color type 2 — truecolour, no
alpha channel, no `tRNS` chunk — so nothing could be keyed out, and `mailTemplateHTML`
had to put a matching solid band behind it to keep it from rendering as a box.

This mark carries a genuine alpha channel, and `.github/workflows/brand-assets.yml`
preserves it through trim, resize and quantization (`png:color-type=6`, checked at the
end of the derive step — the job fails rather than ship a flattened logo). `mailTemplateHTML`
places it directly on the mail's own background, light or dark, with no band behind it.
