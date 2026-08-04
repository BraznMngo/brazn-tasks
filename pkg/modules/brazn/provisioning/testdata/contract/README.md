# Provisioning channel conformance fixtures — a vendored copy

These twenty-six files are **copies**. The canonical set lives in the Percy repository,
and those directories — not this one — are where they are defined, documented and
changed. The schemas they satisfy live beside them there, and `cloud/contracts/README.md`
describes the channel as a whole.

| Operation | Canonical directory |
| --- | --- |
| `create_user`, `create_personal_inbox`, `create_team_roots` | `cloud/contracts/v1/provisioning/examples/` |
| `resolve_user` | `cloud/contracts/v1/user/examples/` |

One directory here for payloads defined in two there, because what makes them one set is
the **channel** they arrive on, not the schema file they were written in: they are
decoded by one package, dispatched by one switch and refused by one flat 400.

They exist for the same reason `pkg/modules/brazn/entitlement/testdata/golden/` does,
and the argument is the one `CLAUDE.md` makes: an interop value that both a producer
and its test double take from one definition is checked by neither. The provisioning
channel had no definition at all until BRA-1108 — the payloads were inline strings in
Percy's `cloud/service/src/fork.ts` and Go structs here, and each side was checked
only against its own constants.

**That has already cost two defects, and both were invisible from inside either
repository.** BRA-1050: the two topology operations had no half here at all, so
BRA-1026's caller got a 400 for a wave. BRA-1106: Percy bound the signup token to an
id *it* had minted while this fork reported the id *it* had minted, so every
redemption was refused — every purchase and every trial failed. Both sides were
internally correct. Both were separately reviewed.

## What each file is for

`contract_test.go` runs the **request** fixtures through the production decoders —
`DecodeCreateUser`, `DecodeCreatePersonalInbox`, `DecodeCreateTeamRoots`,
`DecodeResolveUser` — and never composes a payload of its own.
`pkg/routes/api/v1/brazn_provisioning_contract_test.go` marshals the production **reply**
structs and compares them against the response fixtures. Between the two, a renamed JSON
tag or a changed Go type on either side of this seam turns a test red instead of a
customer's signup.

`pkg/webtests/brazn_user_resolution_test.go` additionally puts the `resolve_user`
fixtures through the **whole production path** — the signature, `Verify`, the decoder and
the switch — as their exact bytes, and compares what goes on the wire. It reaches this
directory by relative path rather than holding a third copy, for the reason the routes
package gives beside its own constants: a frozen artifact that exists twice in one
repository is one that can drift inside it.

| File | Obligation |
| --- | --- |
| `create-user-request.valid.conformance.json` | **Must decode**, with `email` exactly as sent. |
| `create-user-request.invalid.numeric-contract-version.json` | **Must be refused.** `1` rather than `"1"` — `ContractVersion` is a Go string compared with `!=`. |
| `create-user-request.invalid.mailbox-without-an-at.json` | **Must be refused.** `user.CreateUser` validates nothing about the shape, so whatever arrives becomes a real user's address. |
| `create-personal-inbox-request.valid.conformance.json` | **Must decode**, with both identifiers intact. |
| `create-personal-inbox-request.invalid.carries-a-team-id.json` | **Must be refused.** `decodeExactly` calls `DisallowUnknownFields()`, which is what stops a `create_personal_inbox` carrying a `team_id` from being carried out as if the team were not there. |
| `create-personal-inbox-request.invalid.opaque-user-id.json` | ⚠ **Must be ACCEPTED here** — see below. |
| `create-team-roots-request.valid.conformance.json` | **Must decode**, all three identifiers intact. |
| `create-team-roots-request.invalid.missing-team-id.json` | **Must be refused.** An absent `team_id` is `""`, which `commercialID` rejects — and the team id is what a later call is matched against, so a wrong one provisions a second set of roots permanently. |
| `create-user-response.valid.conformance-created.json` | `provisionedUserReply` must marshal to exactly this on the create path. |
| `create-user-response.valid.conformance-resolved.json` | The same on the resolve path. |
| `create-team-roots-response.valid.conformance.json` | `teamRootsReply` must marshal to exactly this. |
| `provisioning-acknowledgement.valid.conformance.json` | `nothingToReport` must marshal to `{}` — **not** an empty body, which Percy's transport cannot tell from a truncated one. |
| `user-resolution-request.valid.conformance-by-email.json` | **Must decode**, with `email` exactly as sent — the recognition form. |
| `user-resolution-request.valid.conformance-by-user-id.json` | **Must decode** — the verification form, the one `requireVerifiedAccount` asks by. |
| `user-resolution-request.invalid.both-identifiers.json` | **Must be refused.** The presence rule, and the only refusal on this operation nothing else catches — every other check passes on it. Both is refused rather than resolved by a precedence the receiver invented. |
| `user-resolution-request.invalid.neither-identifier.json` | **Must be refused** — but note it is refused twice over: with the presence check deleted, an empty `user_id` still fails `commercialID`. |
| `user-resolution-request.invalid.numeric-user-id.json` | **Must be refused.** `9001` rather than `"9001"` — the Go type is the guard, because a `pattern` check coerces. |
| `user-resolution-request.invalid.create-user-operation.json` | **Must be refused.** It is a valid `create_user` payload: `resolve_user`'s recognition form is that payload plus an optional id, so the operation member is the whole of the difference between asking about an address and provisioning one. |
| `user-resolution-request.invalid.provisional-account-key.json` | ⚠ **Must be ACCEPTED here** — see below. |
| `user-resolution-response.valid.conformance-resolved.json` | `resolvedUser` must marshal to exactly this. |
| `user-resolution-response.valid.conformance-unresolvable.json` | The bytes this build emits for **every** absence — erased, never minted, alike. |
| `user-resolution-response.invalid.resolved-without-user-id.json` | **Must not be emitted.** A resolution with no id makes a signup converge on `undefined`. |
| `user-resolution-response.invalid.resolved-without-verification.json` | **Must not be emitted.** This is what `omitempty` on a bool produces for an unconfirmed customer, which is why the tag does not carry one. |
| `user-resolution-response.invalid.unresolvable-with-user-id.json` | **Must not be emitted.** The oracle boundary: an absence carries nothing, and `unresolvableUser` has no field it could be written into. |
| `user-resolution-response.invalid.address-in-the-answer.json` | **Must not be emitted.** Nothing in a user resolution is an address; that is what keeps it off `resolve_mailbox`. |
| `user-resolution-response.invalid.distinguishes-erasure.json` | **Must not be emitted.** Two outcomes and not three — a vocabulary that could say "erased" is one an implementation would eventually be asked to populate. |

## ⚠ Two fixtures are named `.invalid.` and this build must accept them

`create-personal-inbox-request.invalid.opaque-user-id.json` carries
`"user_id": "acct_3d77e0c15a84"`. **Percy refuses to send it and this build accepts
it, and both are correct.** The contract states `^[1-9][0-9]{0,18}$` for `user_id`
because the value is this instance's own `users.id`; `commercialID` here admits
`^[A-Za-z0-9_-]{1,64}$` for every identifier on the channel. Percy's contract set
runs on "strict producers, tolerant consumers", so the narrower rule is the
producer's and this build is deliberately not the place it is enforced.

The test asserts the acceptance rather than ignoring the file, because the gap is
not harmless: for a value inside it, `erase_subject` answers the flat 400
(`ErrProvisioningSubjectUnknown`) while `resolve_mailbox` answers 200
`unresolvable` — two functions in this repository giving opposite answers to the
same malformed id on an identical payload. If somebody later tightens
`commercialID`, that test goes red and the decision gets made deliberately instead
of discovered.

`user-resolution-request.invalid.provisional-account-key.json` is the same case with
one difference worth stating: **nothing is lost by admitting it.** An account still
under its provisional `acct_…` key has no fork user, so Percy never asks about one —
and if it did, `models.ResolveUserBySubject` cannot parse the value as an id and
answers `unresolvable`, which is a legitimate answer to a question about a subject
this instance does not have. `resolve_user` therefore sits on `resolve_mailbox`'s side
of the disagreement above rather than `erase_subject`'s.

## Two operations are still absent from this directory

`erase_subject` has canonical fixtures in Percy (`v1/provisioning/examples/`) and none
here. `resolve_mailbox` has its three in `pkg/webtests/testdata/mailbox/` instead of
this directory, which is a second vendored location rather than the one this file
argues for. Neither is `resolve_user`'s to move: consolidating them is a change to two
other tickets' tests, and doing it in passing is how a frozen artifact gets edited by
somebody who is not looking at what asserts against it. **BRA-827's public contract
host is the named exit for all of it**, at which point every copy here is deleted.

## Do not edit, reformat or regenerate these files

A change to any file here means the wire format changed, and needs the
justification a wire-format change needs — **in Percy first**. Regenerating one to
make a failing test pass is precisely the failure they exist to prevent: a failure
here means this build and Percy disagree about what a message on this channel
means, which is the condition that produced BRA-1050 and BRA-1106.

They were copied byte-for-byte and verified with `git hash-object`:

```
9a1135502c02693646bb4efb9095923408520b19   create-user-request.valid.conformance.json
d94ca932ed184905cd92127eeddfe52e47795021   create-user-request.invalid.numeric-contract-version.json
0340acc326ef067d5bf146d1ce3055523bd664a0   create-user-request.invalid.mailbox-without-an-at.json
b16ffa9fcd107551a0f9b0dbb148512c334e6794   create-personal-inbox-request.valid.conformance.json
9c687fc2cce2c3d7bc7e3aa24316fcbdc3bbe4f8   create-personal-inbox-request.invalid.carries-a-team-id.json
3c005209d99e7ab0154c4b363d71cb06f559a581   create-personal-inbox-request.invalid.opaque-user-id.json
e9b52050515529c55b7265b9213d8005042a9806   create-team-roots-request.valid.conformance.json
4782da3c2d32ea158e7a2d18ea08f70a16d1e490   create-team-roots-request.invalid.missing-team-id.json
b1394695199a9cefb60c2beb3a1be28e0c166227   create-user-response.valid.conformance-created.json
6b10883b59c027fe1873a972396213f89775c63b   create-user-response.valid.conformance-resolved.json
baee7d4c10a49351a9579fad92a3af652eddbe81   create-team-roots-response.valid.conformance.json
0967ef424bce6791893e9a57bb952f80fd536e93   provisioning-acknowledgement.valid.conformance.json
1b0ba148aed5ea40a922d4f524741696db92d6f1   user-resolution-request.valid.conformance-by-email.json
1eb985255b8e427d6ca5824d4a627cf773b6bab6   user-resolution-request.valid.conformance-by-user-id.json
9e18729f298bc56691f4da96cc0f5c5a33042bbe   user-resolution-request.invalid.both-identifiers.json
ff0a59bbfba58624d944e4625d2cdfd7657904c3   user-resolution-request.invalid.neither-identifier.json
c3c4d97d723e6331dae6b5ca6e09cd17849c15aa   user-resolution-request.invalid.numeric-user-id.json
2a80b7dc99481d3d854af44fb6e93da3722eb8b9   user-resolution-request.invalid.create-user-operation.json
ccc0a4ebab848f4b35493e18afb62e0e361a8fca   user-resolution-request.invalid.provisional-account-key.json
d42e35d018661f5e896b2e4f0d8d7551c7a7be0d   user-resolution-response.valid.conformance-resolved.json
ad1be82c4f20bc71e79335d608742143ab8c8b6e   user-resolution-response.valid.conformance-unresolvable.json
41dafde3ea2466be15a10b5db61c567d81fe92f7   user-resolution-response.invalid.resolved-without-user-id.json
78add23989804592fc2bf918ef2a185a40b61a43   user-resolution-response.invalid.resolved-without-verification.json
a96b8a41c71ceb28299694eff3576fa39398eab5   user-resolution-response.invalid.unresolvable-with-user-id.json
bb5571e103644cd18013582694927c2422eb7de0   user-resolution-response.invalid.address-in-the-answer.json
79fb6cfcbcf16457b16f3bb3a70778456825bd07   user-resolution-response.invalid.distinguishes-erasure.json
```

Unlike the entitlement golden envelopes, **these files do end with a trailing
newline**, because they are payload fixtures rather than signed octets — nothing
verifies a signature over them. The `.gitattributes` beside them still switches
end-of-line conversion off, so a checkout on a host with `core.autocrlf=true` gets
the committed bytes rather than a CRLF rendering of them.

## The drift check lives in Percy's CI, not ours

Asserting that the two copies are byte-identical is **Percy's** job, and it has to
be: a private repository can fetch a public one unauthenticated, and the reverse is
impossible without putting a Percy token in a public workflow. If the two copies
ever differ, **this copy is the wrong one** — unless the wire format genuinely
changed, which is a reviewed change in Percy first.

This duplication is interim and has the same named exit as the entitlement golden
set: **BRA-827** provides a public contract host, and when it lands both sides fetch
these artifacts from there and the vendored copy is deleted.
