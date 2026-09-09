package agmasync_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

func TestDependencyClosureExpandsToWhatReferencesResolveTo(t *testing.T) {
	// Opt-in must be dependency-closed, because a receiving endpoint has to be
	// able to resolve every reference on the objects it is sent. Fields pull in
	// the whole graph; a field boundary references nothing and pulls in nothing.
	tests := map[string]struct {
		in   []agmasync.EntityType
		want []agmasync.EntityType
	}{
		"fields reach everything": {
			in: []agmasync.EntityType{agmasync.TypeField},
			want: []agmasync.EntityType{
				agmasync.TypeOrganization, agmasync.TypePerson,
				agmasync.TypeFarm, agmasync.TypeField, agmasync.TypeFieldBoundary,
			},
		},
		"farms reach their owning and partner parties": {
			in: []agmasync.EntityType{agmasync.TypeFarm},
			want: []agmasync.EntityType{
				agmasync.TypeOrganization, agmasync.TypePerson, agmasync.TypeFarm,
			},
		},
		"persons reach the organizations they belong to": {
			in:   []agmasync.EntityType{agmasync.TypePerson},
			want: []agmasync.EntityType{agmasync.TypeOrganization, agmasync.TypePerson},
		},
		"field boundaries reference nothing": {
			in:   []agmasync.EntityType{agmasync.TypeFieldBoundary},
			want: []agmasync.EntityType{agmasync.TypeFieldBoundary},
		},
		"organizations reference nothing": {
			in:   []agmasync.EntityType{agmasync.TypeOrganization},
			want: []agmasync.EntityType{agmasync.TypeOrganization},
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

func TestOptedInMatchesCollectionNames(t *testing.T) {
	// The toggles name collections, not entity types: `field-boundaries`, not
	// `fieldBoundary`. Comparing against the entity type finds nothing, and an
	// endpoint that gets this wrong concludes it is opted into nothing.
	cfg := oapi.MasterdataConfig{Toggles: []oapi.EntityTypeToggle{
		{EntityType: "farms"},
		{EntityType: "field-boundaries"},
	}}

	for _, tc := range []struct {
		in   agmasync.EntityType
		want bool
	}{
		{agmasync.TypeFarm, true},
		{agmasync.TypeFieldBoundary, true},
		{agmasync.TypeField, false},
	} {
		if got := agmasync.OptedIn(cfg, tc.in); got != tc.want {
			t.Errorf("OptedIn(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCollectionRoundTrip(t *testing.T) {
	for _, want := range agmasync.EntityTypes {
		got, ok := agmasync.ParseCollection(want.Collection())
		if !ok || got != want {
			t.Errorf("ParseCollection(%q) = %q, %v; want %q, true",
				want.Collection(), got, ok, want)
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
		{agmasync.TypeOrganization, func() (oapi.Entity, error) {
			return agmasync.FromOrganization(oapi.Organization{Name: "Acme"})
		}},
		{agmasync.TypePerson, func() (oapi.Entity, error) {
			return agmasync.FromPerson(oapi.Person{LastName: "Schmidt"})
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

func TestLocalPartyRefRequiresAParty(t *testing.T) {
	// A party reference carries a discriminator because a receiver that does
	// not hold the target has to request it, and requests are per entity type.
	if _, err := agmasync.LocalPartyRef(agmasync.TypeOrganization, "ORG-1"); err != nil {
		t.Errorf("organization should be a valid party: %v", err)
	}
	if _, err := agmasync.LocalPartyRef(agmasync.TypePerson, "PSN-1"); err != nil {
		t.Errorf("person should be a valid party: %v", err)
	}
	if _, err := agmasync.LocalPartyRef(agmasync.TypeField, "PFD-1"); !errors.Is(
		err, agmasync.ErrUnknownEntityType) {
		t.Errorf("field is not a party; error = %v, want ErrUnknownEntityType", err)
	}
}
