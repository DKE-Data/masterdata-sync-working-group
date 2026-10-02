package agmasync_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
)

func TestDependencyClosureExpandsToWhatReferencesResolveTo(t *testing.T) {
	// Opt-in must be dependency-closed, because a receiving endpoint has to be
	// able to resolve every reference on the objects it is sent. Field
	// boundaries pull in the whole graph; a party references nothing outside
	// its own type and pulls in nothing.
	tests := map[string]struct {
		in   []agmasync.EntityType
		want []agmasync.EntityType
	}{
		"field boundaries reach everything": {
			in: []agmasync.EntityType{agmasync.TypeFieldBoundary},
			want: []agmasync.EntityType{
				agmasync.TypeParty, agmasync.TypeFarm, agmasync.TypeField, agmasync.TypeFieldBoundary,
			},
		},
		"fields reach their farms and owning parties": {
			in: []agmasync.EntityType{agmasync.TypeField},
			want: []agmasync.EntityType{
				agmasync.TypeParty, agmasync.TypeFarm, agmasync.TypeField,
			},
		},
		"farms reach their owning and partner parties": {
			in:   []agmasync.EntityType{agmasync.TypeFarm},
			want: []agmasync.EntityType{agmasync.TypeParty, agmasync.TypeFarm},
		},
		"parties reference nothing outside their type": {
			in:   []agmasync.EntityType{agmasync.TypeParty},
			want: []agmasync.EntityType{agmasync.TypeParty},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := agmasync.DependencyClosure(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Errorf("DependencyClosure(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDeclaredMatchesTypeNames(t *testing.T) {
	cfg := oapi.MasterdataConfig{Capabilities: []oapi.EntityTypeToggle{
		{EntityType: "farm"},
		{EntityType: "fieldBoundary"},
	}}

	for _, tc := range []struct {
		in   agmasync.EntityType
		want bool
	}{
		{agmasync.TypeFarm, true},
		{agmasync.TypeFieldBoundary, true},
		{agmasync.TypeField, false},
	} {
		if got := agmasync.Declared(cfg, tc.in); got != tc.want {
			t.Errorf("Declared(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSelectedTypesReadsTheSelectionInDependencyOrder(t *testing.T) {
	// What the ROUTE_CHANGED frame carries, read into entity types. Dependency
	// order matters: it is the order the set is walked in, so parents precede
	// what references them.
	selection := oapi.RouteChangedEventData{
		ExternalId: "ep-a",
		EntityTypes: []oapi.EntityTypeToggle{
			{EntityType: "field"},
			{EntityType: "party"},
			{EntityType: "farm"},
		},
	}

	got := agmasync.SelectedTypes(selection)
	want := []agmasync.EntityType{
		agmasync.TypeParty, agmasync.TypeFarm, agmasync.TypeField,
	}
	if len(got) != len(want) {
		t.Fatalf("SelectedTypes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SelectedTypes = %v, want %v", got, want)
		}
	}
}

func TestEnvelopeOfReadsCommonFieldsWhicheverTypeItIs(t *testing.T) {
	// A receiver reads type, revision, and localId off a frame before it knows
	// which concrete schema the object is. This is that step.
	arID := uuid.New()
	revision := 7
	localID := "PFD-00042"

	ent, err := agmasync.FromField(oapi.Field{
		AgrirouterId: &arID,
		LocalId:      &localID,
		Revision:     &revision,
		Name:         "North 40",
	})
	if err != nil {
		t.Fatalf("FromField: %v", err)
	}

	env, err := agmasync.EnvelopeOf(ent)
	if err != nil {
		t.Fatalf("EnvelopeOf: %v", err)
	}
	if env.Type != agmasync.TypeField {
		t.Errorf("Type = %q, want %q", env.Type, agmasync.TypeField)
	}
	if env.Revision == nil || *env.Revision != revision {
		t.Errorf("Revision = %v, want %d", env.Revision, revision)
	}
	if env.LocalId == nil || *env.LocalId != localID {
		t.Errorf("LocalId = %v, want %q", env.LocalId, localID)
	}
	if env.AgrirouterId == nil || *env.AgrirouterId != arID {
		t.Errorf("AgrirouterId = %v, want %s", env.AgrirouterId, arID)
	}
}

func TestEnvelopeOfSetsTheDiscriminatorForEveryType(t *testing.T) {
	// The union writes the discriminator; nothing else does. If a wrapper ever
	// stopped setting it, objects would go out untyped and arrive unreadable.
	cases := []struct {
		want agmasync.EntityType
		make func() (oapi.Entity, error)
	}{
		{agmasync.TypeParty, func() (oapi.Entity, error) {
			return agmasync.FromParty(oapi.Party{Name: "Acme"})
		}},
		{agmasync.TypeFarm, func() (oapi.Entity, error) {
			return agmasync.FromFarm(oapi.Farm{Name: "Hof Nord"})
		}},
		{agmasync.TypeField, func() (oapi.Entity, error) {
			return agmasync.FromField(oapi.Field{Name: "North 40"})
		}},
		{agmasync.TypeFieldBoundary, func() (oapi.Entity, error) {
			return agmasync.FromFieldBoundary(oapi.FieldBoundary{})
		}},
	}

	for _, tc := range cases {
		t.Run(string(tc.want), func(t *testing.T) {
			ent, err := tc.make()
			if err != nil {
				t.Fatalf("building entity: %v", err)
			}
			env, err := agmasync.EnvelopeOf(ent)
			if err != nil {
				t.Fatalf("EnvelopeOf: %v", err)
			}
			if env.Type != tc.want {
				t.Errorf("Type = %q, want %q", env.Type, tc.want)
			}
		})
	}
}

func TestEnvelopeOfRejectsAnUnknownEntityType(t *testing.T) {
	var ent oapi.Entity
	if err := ent.UnmarshalJSON([]byte(`{"type":"guidanceLine","localId":"GL-1"}`)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if _, err := agmasync.EnvelopeOf(ent); !errors.Is(err, agmasync.ErrUnknownEntityType) {
		t.Errorf("EnvelopeOf error = %v, want ErrUnknownEntityType", err)
	}
}

func TestNeedsUserSeparatesTheTwoMappingRejections(t *testing.T) {
	// The two conflicts are different problems in the participant's own store
	// and are deliberately not interchangeable. One local identifier claimed by
	// two canonical objects is a granularity disagreement only a person can
	// settle; the reverse is something the participant already holds the answer
	// to and must not put in front of anyone.
	if !agmasync.NeedsUser(oapi.IdMappingRejection{
		Reason: agmasync.ReasonLocalIDAlreadyBound,
	}) {
		t.Error("LOCAL_ID_ALREADY_BOUND should need a user")
	}
	for _, reason := range []string{
		agmasync.ReasonAgrirouterIDAlreadyBound,
		agmasync.ReasonUnknownObject,
		agmasync.ReasonDuplicateInRequest,
		"SOMETHING_ADDED_AFTER_THIS_WAS_WRITTEN",
	} {
		if agmasync.NeedsUser(oapi.IdMappingRejection{Reason: reason}) {
			t.Errorf("%s should not need a user", reason)
		}
	}
}

func TestIsRepeatLoad(t *testing.T) {
	// A participant that cannot tell a repeat load from a first one creates
	// local duplicates of everything it already holds.
	if agmasync.IsRepeatLoad(oapi.InitialLoadStatus{State: agmasync.StateLoadingFromAgrirouter}) {
		t.Error("a status with no previousLoadCompletedAt is a first load")
	}

	completed := time.Date(2026, 7, 14, 9, 20, 0, 0, time.UTC)
	if !agmasync.IsRepeatLoad(oapi.InitialLoadStatus{
		State:                   agmasync.StateLoadingFromAgrirouter,
		PreviousLoadCompletedAt: &completed,
	}) {
		t.Error("a status carrying previousLoadCompletedAt is a repeat load")
	}
}

func TestPartyTierOrdersPersonsAfterOrganizations(t *testing.T) {
	// A membership names an organization from a person, so persons go one tier
	// below every other party. The tier is read from the object, not its type.
	person := func() oapi.Party {
		var d oapi.PartyDetails
		if err := d.FromPersonDetails(oapi.PersonDetails{PartyType: agmasync.PartyTypePerson}); err != nil {
			t.Fatal(err)
		}
		return oapi.Party{Name: "Anna Schmidt", Details: nullable.NewNullableWithValue(d)}
	}
	organization := func() oapi.Party {
		var d oapi.PartyDetails
		if err := d.FromOrganizationDetails(oapi.OrganizationDetails{PartyType: agmasync.PartyTypeOrganization}); err != nil {
			t.Fatal(err)
		}
		return oapi.Party{Name: "Acme", Details: nullable.NewNullableWithValue(d)}
	}

	for _, tc := range []struct {
		name      string
		party     oapi.Party
		partyType string
		tier      int
	}{
		{"unknown party type", oapi.Party{Name: "Hof Nord"}, "", 0},
		{"null details", oapi.Party{Name: "Hof Nord", Details: nullable.NewNullNullable[oapi.PartyDetails]()}, "", 0},
		{"organization", organization(), agmasync.PartyTypeOrganization, 0},
		{"person", person(), agmasync.PartyTypePerson, 1},
	} {
		partyType, err := agmasync.PartyTypeOf(tc.party)
		if err != nil || partyType != tc.partyType {
			t.Errorf("%s: PartyTypeOf = %q, %v; want %q", tc.name, partyType, err, tc.partyType)
		}
		tier, err := agmasync.PartyTier(tc.party)
		if err != nil || tier != tc.tier {
			t.Errorf("%s: PartyTier = %d, %v; want %d", tc.name, tier, err, tc.tier)
		}
	}
}
