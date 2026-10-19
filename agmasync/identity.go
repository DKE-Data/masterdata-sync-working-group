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
// `idSource` is [AgrirouterIDSource]; every other entry is ignored, as
// agrirouter ignores them on send.
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

// LocalId builds an `Id` from the near end's own identifier alone, which is
// all a send needs: agrirouter takes the local identifier from `referenceId`.
// It is what a participant puts in `content.id` of an object it writes.
func LocalId(localID string) oapi.Id {
	return oapi.Id{ReferenceId: &localID}
}

// LocalRef builds a reference to another entity from the referencing
// endpoint's own identifier for the target.
//
// See "References" in specification.md. On send a participant may use either
// identifier, and using its own is what keeps the agrirouter identifier off the
// write path: references can be built out of a participant's own keys, without
// first capturing and correlating canonical ones. agrirouter resolves the
// `referenceId` against the sending application's mapping and rejects the
// write if the target has not been sent yet, so the target must be sent before
// the first reference to it.
func LocalRef(localID string) oapi.Ref {
	return oapi.Ref{Id: LocalId(localID)}
}

// AgrirouterRef builds a reference to another entity from its canonical
// identifier, for a target the referencing participant holds no local
// identifier for.
func AgrirouterRef(agrirouterID uuid.UUID) oapi.Ref {
	source := AgrirouterIDSource
	return oapi.Ref{Id: oapi.Id{UniqueIds: &[]oapi.UniqueId{{
		IdText: agrirouterID.String(), IdTypeCode: IDTypeUUID, IdSource: &source,
	}}}}
}
