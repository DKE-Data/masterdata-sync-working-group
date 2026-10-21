package testrouter

import (
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// wireID is an `Id` as it travels: an object's own `content.id`, or the `id`
// of a reference. See "Identity" in specification.md.
type wireID struct {
	ReferenceID *string        `json:"referenceId,omitempty"`
	UniqueIDs   []wireUniqueID `json:"uniqueIds,omitempty"`
}

type wireUniqueID struct {
	IDText     string  `json:"idText"`
	IDTypeCode string  `json:"idTypeCode"`
	IDSource   *string `json:"idSource,omitempty"`
}

func (u wireUniqueID) isAgrirouter() bool {
	return u.IDSource != nil && *u.IDSource == agmasync.AgrirouterIDSource
}

// localEntry is what a participant's latest write of an object said about its
// own identifier besides the identifier itself. agrirouter keeps it with the
// binding and relays it back to that participant only, in the object's `id`
// and in every reference to it. The zero value is what a write that sent no
// local entry leaves: `STRING`, and no source.
type localEntry struct {
	typeCode string
	source   *string
}

// read is what agrirouter takes from an `Id` on send: the agrirouter entry, at
// most one, the `referenceId`, and the local entry whose `idText` equals it, at
// most one. Any other entry is rejected.
func (id wireID) read() (agrirouterID *uuid.UUID, local *localEntry, err error) {
	for _, entry := range id.UniqueIDs {
		if id.ReferenceID != nil && entry.IDText == *id.ReferenceID {
			if entry.isAgrirouter() {
				return nil, nil, fmt.Errorf("the local entry %q cannot have idSource %s, which is reserved",
					entry.IDText, agmasync.AgrirouterIDSource)
			}
			if local != nil {
				return nil, nil, errors.New("an id carries at most one local entry")
			}
			local = &localEntry{typeCode: entry.IDTypeCode, source: entry.IDSource}
			continue
		}
		if !entry.isAgrirouter() {
			return nil, nil, fmt.Errorf("the entry %q is neither the local entry nor the agrirouter entry",
				entry.IDText)
		}
		if agrirouterID != nil {
			return nil, nil, errors.New("an id carries at most one agrirouter entry")
		}
		parsed, err := uuid.Parse(entry.IDText)
		if err != nil {
			return nil, nil, fmt.Errorf("the agrirouter entry %q is not a UUID", entry.IDText)
		}
		agrirouterID = &parsed
	}
	return agrirouterID, local, nil
}

// canonicalID is how the SSOT holds a reference's target: the agrirouter entry
// alone. Whose local identifier goes with it depends on who it is delivered to.
func canonicalID(objID uuid.UUID) wireID {
	source := agmasync.AgrirouterIDSource
	return wireID{UniqueIDs: []wireUniqueID{{
		IDText: objID.String(), IDTypeCode: agmasync.IDTypeUUID, IDSource: &source,
	}}}
}

// renderID is an object's `Id` as one application sees it: the agrirouter
// entry, then the application's own local entry and `referenceId` where it has
// bound the object, and neither where it has not. An absent local entry states
// that agrirouter does not believe this application holds the object, which is
// what makes an unbound object recognisable as one to create locally and bind.
func (s *store) renderID(appID string, objID uuid.UUID) wireID {
	out := canonicalID(objID)
	localID, ok := s.canonical[canonicalKey{appID, objID}]
	if !ok {
		return out
	}
	entry := s.localEntries[canonicalKey{appID, objID}]
	if entry.typeCode == "" {
		entry.typeCode = agmasync.IDTypeString
	}
	out.ReferenceID = &localID
	out.UniqueIDs = append(out.UniqueIDs, wireUniqueID{
		IDText: localID, IDTypeCode: entry.typeCode, IDSource: entry.source,
	})
	return out
}

// recordLocalEntry keeps what a write said about the writer's local entry,
// replacing what an earlier write said: nil, a write that sent none, resets it.
func (s *store) recordLocalEntry(appID string, objID uuid.UUID, entry *localEntry) {
	key := canonicalKey{appID, objID}
	if entry == nil {
		delete(s.localEntries, key)
		return
	}
	s.localEntries[key] = *entry
}
