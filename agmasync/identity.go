package agmasync

import (
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// The agrirouter entry of an `Id`: the canonical identifier, a UUID from this
// source. The source is reserved, and is never a local entry's. See "Identity"
// in specification.md.
const (
	AgrirouterIDSource = "https://agrirouter.com"
	IDTypeUUID         = "UUID"
)

// IDTypeString is the `idTypeCode` agrirouter delivers on a local entry when
// the application sent none.
const IDTypeString = "STRING"

// Identity is what an `Id` says, as the two identifiers this protocol reads
// from it: the canonical one and the near end's local one. Either may be
// absent.
type Identity struct {
	AgrirouterID *uuid.UUID
	LocalID      *string
}

// IdentityOf reads an `Id`, an object's own or a reference's.
//
// The local identifier is `referenceId`, which on delivery is present exactly
// when the local entry is. The canonical one is the `uniqueIds` entry whose
// `idSource` is [AgrirouterIDSource]. A delivered `Id` holds no other entry
// besides the local one.
func IdentityOf(id oapi.Id) (Identity, error) {
	var out Identity
	if id.ReferenceId != nil {
		local := *id.ReferenceId
		out.LocalID = &local
	}
	if id.UniqueIds == nil {
		return out, nil
	}
	for _, entry := range *id.UniqueIds {
		if entry.IdSource == nil || *entry.IdSource != AgrirouterIDSource {
			continue
		}
		parsed, err := uuid.Parse(entry.IdText)
		if err != nil {
			return Identity{}, fmt.Errorf("agmasync: reading agrirouter identifier %q: %w", entry.IdText, err)
		}
		out.AgrirouterID = &parsed
		break
	}
	return out, nil
}

// LocalID builds an `Id` from the near end's own identifier alone, which is
// all a send needs: agrirouter takes the local identifier from `referenceId`.
// It is what a participant puts in `content.id` of an object it writes.
func LocalID(localID string) oapi.Id {
	return oapi.Id{ReferenceId: &localID}
}

// LocalRef builds a reference to another entity from the referencing
// endpoint's own identifier for the target.
//
// See "References" in specification.md. On send a participant may use either
// identifier, and using its own is what keeps the agrirouter identifier off the
// write path: references can be built out of a participant's own keys, without
// first capturing and correlating canonical ones. agrirouter resolves the
// `referenceId` against the sending endpoint's own mapping and rejects the
// write if that endpoint has not sent or bound the target yet, so the target
// must be sent or bound through the same endpoint before the first reference to
// it.
func LocalRef(localID string) oapi.Ref {
	return oapi.Ref{Id: LocalID(localID)}
}

// CanonicalRef builds a reference to another entity from its canonical
// identifier, for a target the referencing participant holds no local
// identifier for.
func CanonicalRef(agrirouterID uuid.UUID) oapi.Ref {
	source := AgrirouterIDSource
	return oapi.Ref{Id: oapi.Id{UniqueIds: &[]oapi.UniqueId{{
		IdText: agrirouterID.String(), IdTypeCode: IDTypeUUID, IdSource: &source,
	}}}}
}
