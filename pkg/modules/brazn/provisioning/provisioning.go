// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-present Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Package provisioning decodes the signed requests Brazn's commercial service
// uses to provision on this instance.
//
// IT IS A CHANNEL, NOT AN ENDPOINT'S HELPER. Creating a user is its first
// operation and deliberately not its only shape: BRA-1026 adds an
// organization's primary team and its roots through the same door. Everything
// that would otherwise have to be decided twice is decided here once - the
// authentication scheme, the trust store, the signing domain, the route and
// its classification - and an operation is a value in the signed payload
// rather than a second endpoint.
//
// Like the entitlement ingest, nothing here reaches the network or the
// database. It verifies and decodes; models does the writing.
package provisioning

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
)

// SigningDomain is the domain-separation prefix a provisioning signature
// covers, terminated by 0x0A exactly as the entitlement contract's is.
//
// IT IS THE ONLY THING SEPARATING THIS CHANNEL FROM THE ENTITLEMENT ONE. Both
// verify against the same key list (brazn.entitlementkeys), so without a
// distinct prefix every entitlement projection Percy has ever signed would be
// a candidate provisioning request and the reverse would hold too. Adding a
// third channel means adding a third prefix here and nothing else.
const SigningDomain = "percy.provisioning.v1\n"

// ContractVersion is the only provisioning contract version this build
// accepts. A request from a newer contract is refused rather than guessed at,
// for the reason the strict decode below gives: a field this build cannot see
// is a field it would silently ignore.
const ContractVersion = "1"

// The operations this channel carries. One today; the switch that reads this
// is the extension point, and a value it does not know is refused rather than
// treated as the default.
const OperationCreateUser = "create_user"

// maxMailboxLength is users.email's column width. An address past it is
// refused rather than stored, because the database would truncate it into a
// DIFFERENT mailbox - which is another person's account, silently. This is the
// same reasoning the entitlement contract's opaqueId length bound is written
// for.
const maxMailboxLength = 250

// ErrInvalidRequest means a provisioning request could not be acted on: its
// envelope did not verify, or what it carried was not something this build
// accepts. Callers must not distinguish the two - see the endpoint on why the
// reply is flat.
var ErrInvalidRequest = errors.New("provisioning request is not valid")

// CreateUser is the whole signed payload of a create_user operation, including
// the two members every operation carries.
//
// Each operation declares its own complete payload type rather than embedding
// a shared header, and that is what keeps the strict decode below exact: a
// member that belongs to another operation is undeclared here and refused,
// instead of being accepted and ignored because some sibling operation happens
// to declare it.
type CreateUser struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// Email is the mailbox to provision, treated as an OPAQUE KEY: it is
	// matched byte for byte and never normalised. Case folding, plus-address
	// stripping and the rest belong to the commercial layer, which owns the
	// mailbox and knows which two spellings it considers one customer. A fork
	// that folded them would merge two accounts the sender believes are
	// separate, and nothing downstream could tell that it had.
	Email string `json:"email"`
}

// operation is the lenient first read of a signed payload: enough to route it,
// and deliberately nothing else. It ignores unknown members because at this
// point every member of every operation is unknown to it.
type operation struct {
	Operation string `json:"operation"`
}

// Verify authenticates a provisioning envelope and returns the operation it
// names together with the signed payload, for the caller to decode into that
// operation's own type.
//
// It is two steps rather than one because the payload's shape depends on the
// operation, and the operation is inside the signed bytes - so it cannot be
// read before the signature has been checked, and the payload cannot be
// decoded before the operation is known.
func Verify(raw []byte) (string, json.RawMessage, error) {
	payload, err := entitlement.VerifyEnvelope(SigningDomain, raw)
	if err != nil {
		return "", nil, err
	}

	// EVERYTHING BELOW READS THE MESSAGE, so everything below is after the
	// signature, exactly as on the entitlement channel.
	var op operation
	if err := json.Unmarshal(payload, &op); err != nil {
		return "", nil, ErrInvalidRequest
	}
	return op.Operation, payload, nil
}

// DecodeCreateUser reads a verified payload as a create_user request and
// checks the one field it carries.
func DecodeCreateUser(payload json.RawMessage) (*CreateUser, error) {
	request := &CreateUser{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	// A mailbox with no "@" is not one, and user.CreateUser will not say so:
	// it requires the field to be non-empty and validates nothing about its
	// shape, so whatever arrives becomes a real user's address.
	if len(request.Email) > maxMailboxLength || !strings.Contains(request.Email, "@") {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// decodeExactly reads a payload into an operation's own type and refuses
// anything the type does not declare, or anything after it.
//
// THE STRICTNESS IS FORWARD COMPATIBILITY, not tidiness. A member this build
// cannot see is a member it would silently drop, and the sender would have no
// way to learn that its request had been carried out differently from how it
// was written - a create_user that grows an organization it must belong to is
// exactly that, and it is filed (BRA-1026). Refusing turns a silent
// half-application into a visible failure.
//
// json.Unmarshal runs first because it rejects trailing content, which
// json.Decoder does not; the decoder then runs for the unknown-member check
// alone.
func decodeExactly(payload []byte, into interface{}) error {
	if err := json.Unmarshal(payload, into); err != nil {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return ErrInvalidRequest
	}
	return nil
}
