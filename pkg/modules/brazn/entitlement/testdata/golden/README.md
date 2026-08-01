# Frozen golden artifacts — a vendored copy

These five files are **copies**. The canonical set lives in the Percy repository at
`cloud/contracts/v2/entitlements/golden/`, and that directory — not this one — is
where they are defined, documented and changed.

They exist because the entitlement signing rule crosses a language boundary: a
TypeScript producer in Percy's `cloud/service/`, a Go consumer here. It has already
been misread twice **in this package**, and both times every test on both sides was
green, because each side was checking itself against the format it had itself
implemented. `Verify` shipped without the domain-separation prefix, and then decoding
the signature as padded base64 rather than unpadded base64url. Either alone meant no
conforming projection could be accepted at all.

These are bytes **neither side produces at test time**, so neither side is testing its
own assumption. `golden_test.go` checks them through the production `Verify` and
`SigningInput`; it never rebuilds the signed octets from a literal of its own.

| File | Obligation |
| --- | --- |
| `key-id.txt` | The `signature.key_id` these envelopes are signed under. In its own file so a test never learns which key to trust from the message it is checking. |
| `signing-key.pub.pem` | Ed25519 public key, SPKI PEM. `pem.Decode`, then `x509.ParsePKIXPublicKey`. |
| `projection.verifies.json` | **Must verify.** |
| `projection.rejects.unprefixed-signature.json` | **Must fail.** A valid signature over the signed member with no domain prefix — shipped bug one. |
| `projection.rejects.padded-signature.json` | **Must fail.** The same, correct signature octets in the forbidden padded encoding — shipped bug two. |

Each negative differs from the positive **only** in `signature.value`, so a failure is
attributable to the rule under test and to nothing else.

## Do not edit, reformat or regenerate these files

The set is frozen, and structurally rather than by convention: the keypair was
generated inside one CI process, only the public half was published, and the private
half was deliberately not retained. Nobody can quietly re-sign these envelopes. A
change to any file here means the wire format changed, and needs the justification a
wire-format change needs — **in Percy first**.

Regenerating them to make a failing test pass is precisely the failure they exist to
prevent. A failure here means this build cannot accept a real projection.

**The three JSON files carry no trailing newline.** The file is exactly the wire
message, and a receiver slices `signed` out of it rather than reformatting it.
`key-id.txt` and the PEM do end in a newline. Any editor, formatter or hook that
normalises whitespace here corrupts the artifacts — which is why the `.gitattributes`
beside them switches end-of-line conversion off rather than trusting every checkout to
be configured harmlessly. They were copied byte-for-byte and verified with
`git hash-object`:

```
cec13b1002c4d6b3ebfe5056868331f8e59ee53a    34   key-id.txt
036c37d23df607cfd2bfa7a9b29f1e1934d272f6   113   signing-key.pub.pem
14a2804c79ba8e3d5ee7ec6640e6588fc435d2f9   437   projection.verifies.json
20b250c6c842c908c57e52f910c00a99705d4bfa   437   projection.rejects.unprefixed-signature.json
6ba2ef46e649561054205880670093b2c82e6aaa   439   projection.rejects.padded-signature.json
```

## The drift check lives in Percy's CI, not ours

Asserting that the two copies are byte-identical is **Percy's** job, and it has to be:
a private repository can fetch a public one unauthenticated, and the reverse is
impossible without putting a Percy token in a public workflow, which must never
happen. If the two copies ever differ, **this copy is the wrong one** — unless the wire
format genuinely changed, which is a reviewed change in Percy first.

## This duplication is interim, and it has a named exit

It is not intentional architecture. It exists only because Percy is private and this
fork is public, so public CI cannot read the canonical directory.

These schemas' `$id`s already read `https://contracts.percy.works/v2/entitlements/...`.
A public contract host is the intended end state and **BRA-827** provides it. When it
lands, both sides fetch these artifacts from there, and this vendored copy and Percy's
drift check are deleted together.
