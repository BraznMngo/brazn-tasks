# Mailbox-resolution conformance fixtures — a vendored copy

These three files are **copies**. The canonical set lives in the Percy repository at
`cloud/contracts/v1/mailbox/examples/`, and that directory — not this one — is where
they are defined, documented and changed. `cloud/contracts/README.md` §`v1/mailbox/`
is the normative text; the two schemas beside them are the specification.

They exist because `resolve_mailbox` crosses a language boundary: a TypeScript caller
in Percy's `cloud/service/`, this Go implementation. Both sides run the same three
files, so neither side is checking itself against the format it implemented.

| File | Obligation |
| --- | --- |
| `mailbox-resolution-request.valid.conformance.json` | **Must be accepted**, verbatim, through the whole production path — `Verify`, `DecodeResolveMailbox`, the switch. |
| `mailbox-resolution-response.valid.conformance-resolved.json` | The bytes this build emits for a subject that has a mailbox. |
| `mailbox-resolution-response.valid.conformance-unresolvable.json` | The bytes this build emits for **every** absence — erased, never minted, alike. |

## Why they are checked the way `brazn_mailbox_resolution_test.go` checks them

The field names and code strings are asserted as **literals written into the test
source** rather than read from these files. That is not belt and braces: a value both
sides take from one definition is checked by neither, so renaming a member here and in
one implementation together would stay green while the other rejected every message.
The literals are pinned against the contract document, which is the thing neither
implementation can edit by accident.

`user_id` is asserted to be a JSON **string**. A Go implementation marshalling an
`int64` emits `42` rather than `"42"`, and a `pattern` check coerces — so only a type
assertion catches the single most likely defect on this seam.

## Do not edit, reformat or regenerate these files

They were copied byte-for-byte and verified with `git hash-object`:

```
8f0c2c4475028ca92685e23acfef328bdd0757a5   124   mailbox-resolution-request.valid.conformance.json
416de6a730d276ea7b166c40b1d7edbc2a31e5bc    59   mailbox-resolution-response.valid.conformance-resolved.json
ad1be82c4f20bc71e79335d608742143ab8c8b6e    31   mailbox-resolution-response.valid.conformance-unresolvable.json
```

Editing one to make a failing test pass is precisely the failure they exist to
prevent. A failure here means this build cannot answer a real resolution — and the
caller that asks is erasure, at step 4 of six, against a one-month statutory deadline.

## The drift check lives in Percy's CI, not ours

Asserting that the two copies are byte-identical is **Percy's** job, and it has to be:
a private repository can fetch a public one unauthenticated, and the reverse is
impossible without putting a Percy token in a public workflow. If the two copies ever
differ, **this copy is the wrong one** — unless the wire format genuinely changed,
which is a reviewed change in Percy first.

The duplication has the same named exit as `pkg/modules/brazn/entitlement/testdata/golden/`:
**BRA-827**'s public contract host, after which both sides fetch these from there and
this copy and Percy's drift check are deleted together.
