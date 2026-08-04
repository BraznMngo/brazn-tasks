# Signup contract fixtures — a vendored copy

These three files are **copies**. The canonical set lives in the Percy repository at
`cloud/contracts/v1/signup/examples/`, and that directory — not this one — is where
they are defined, documented and changed (BRA-1080).

They exist for the reason `../../entitlement/testdata/golden/` exists: the signup
redemption crosses a language boundary — a TypeScript service in Percy's
`cloud/service/`, a Go caller here — and each side checking itself against the format
it has itself implemented is how BRA-1050 happened, a caller in one repository
against an operation the other never defined.

The one this set is really pinning is `user_id`. It is a **decimal string** on the
wire, and a Go `int64` field marshals to `42` rather than `"42"`. Both look correct in
isolation; the difference only appears at the entitlement projection, where a subject
spelled differently does not exist — and a projection for a subject that does not
exist is answered **204 and stored nowhere** (`docs/Identity-and-Access-Rules.md`
§3.3). That failure is silent on both sides: the signup succeeds, and the customer has
no entitlement.

| File | Obligation |
| --- | --- |
| `signup-token-redemption-request.valid.conformance.json` | The request this build must produce: three members, every value a JSON string. `signup_test.go` compares the request it actually sends against this **member by member, including the JSON type of each value**. |
| `signup-token-redemption-response.valid.conformance.json` | The only answer that may be committed on. Fed back through `Redeem` unchanged. |
| `signup-token-redemption-error.valid.conformance.json` | A refusal, which must roll the registration back. Fed back through `Redeem` unchanged. |

The token value inside the request fixture is a 43-character example and not a secret.
It is quoted in tests rather than invented, so the length this contract requires is
never accidentally satisfied by a value somebody made up.

## Do not edit, reformat or regenerate these files

A change to any file here means the wire format changed, and needs the justification a
wire-format change needs — **in Percy first**. Regenerating them to make a failing test
pass is precisely the failure they exist to prevent.

**They carry no trailing newline.** The file is exactly the wire message. The
`.gitattributes` beside them switches end-of-line conversion off rather than trusting
every checkout to be configured harmlessly.

They were copied byte-for-byte from `agent/phase-1.3-bra-1080` and verified with
`git hash-object`:

```
2367af102b74336a945abe3cfd119f65362f568b   112   signup-token-redemption-request.valid.conformance.json
46d6852a60094a2a0c4d0f5687f84eb1b7d5acdc    27   signup-token-redemption-response.valid.conformance.json
27926b4c3f181e45716d339eb8d292fd30684d3c    32   signup-token-redemption-error.valid.conformance.json
```

## The drift check lives in Percy's CI, not ours

Asserting that the two copies are byte-identical is **Percy's** job, for the reason the
golden set's README gives: a private repository can fetch a public one unauthenticated,
and the reverse is not true.
