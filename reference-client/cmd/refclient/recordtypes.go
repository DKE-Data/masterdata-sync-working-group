package main

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// recordType is one of this platform's own record types, as its screens offer
// them.
//
// It is not the protocol's entity type. Like most farm management systems this
// platform keeps organizations and persons apart; the protocol has one party
// entity and says which it is in `details`. The screens speak the platform's
// language, and the mapping happens where a record is turned into an entity.
type recordType string

const (
	recordOrganization recordType = "organization"
	recordPerson       recordType = "person"
)

// recordTypes lists what can be created, in dependency order.
var recordTypes = []recordType{
	recordOrganization, recordPerson,
	recordType(agmasync.TypeFarm), recordType(agmasync.TypeField), recordType(agmasync.TypeFieldBoundary),
}

func parseRecordType(name string) (recordType, error) {
	typ := recordType(name)
	if !slices.Contains(recordTypes, typ) {
		return "", fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, name)
	}
	return typ, nil
}

// entityType is the protocol entity type a record of this type travels as.
func (t recordType) entityType() agmasync.EntityType {
	if t.partyType() != "" {
		return agmasync.TypeParty
	}
	return agmasync.EntityType(t)
}

// partyType is the party type a record of this type states, "" for one that
// is not a party.
func (t recordType) partyType() string {
	switch t {
	case recordOrganization:
		return agmasync.PartyTypeOrganization
	case recordPerson:
		return agmasync.PartyTypePerson
	default:
		return ""
	}
}

// recordTypeOf reads which of the platform's types a record is. A party goes by
// the party type its details state, and one stating none is an organization,
// which is where the store files it.
func recordTypeOf(r store.Record) recordType {
	if r.EntityType != agmasync.TypeParty {
		return recordType(r.EntityType)
	}
	var details struct {
		PartyType string `json:"party_type"`
	}
	if raw := r.Modelled["details"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &details)
	}
	if details.PartyType == agmasync.PartyTypePerson {
		return recordPerson
	}
	return recordOrganization
}
