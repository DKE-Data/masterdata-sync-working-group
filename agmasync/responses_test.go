package agmasync_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// answer serves every request with one status, media type, and body.
func answer(t *testing.T, status int, contentType, body string) *oapi.ClientWithResponses {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return api
}

// deactivate deactivates as an arbitrary endpoint of an arbitrary tenant.
func deactivate(api *oapi.ClientWithResponses, t agmasync.EntityType, localID string) (agmasync.Object, error) {
	return agmasync.Deactivate(context.Background(), api, uuid.New(), uuid.New(), t, localID, nil)
}

func TestDeactivateDecodesByTheRequestedType(t *testing.T) {
	t.Run("body without type", func(t *testing.T) {
		api := answer(t, http.StatusOK, "application/json", `{"content":{"id":{"referenceId":"a-1"},"name":"Acker"},"active":false}`)
		obj, err := deactivate(api, agmasync.TypeField, "field-1")
		if err != nil {
			t.Fatalf("Deactivate: %v", err)
		}
		if obj.Envelope.Type != agmasync.TypeField {
			t.Errorf("Type = %q, want %q", obj.Envelope.Type, agmasync.TypeField)
		}
		if obj.Field == nil || obj.Field.Content.Name != "Acker" {
			t.Errorf("Field = %+v, want name Acker", obj.Field)
		}
	})

	t.Run("body of another type", func(t *testing.T) {
		api := answer(t, http.StatusOK, "application/json", `{"type":"farm","content":{"id":{"referenceId":"h-1"},"name":"Hof"},"active":false}`)
		if _, err := deactivate(api, agmasync.TypeField, "field-1"); !errors.Is(err, agmasync.ErrEntityTypeMismatch) {
			t.Errorf("Deactivate error = %v, want ErrEntityTypeMismatch", err)
		}
	})
}

func TestErrorsCarryTheServersMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("stream refused", func(t *testing.T) {
		api := answer(t, http.StatusForbidden, "application/json", `{"message":"endpoint not opted into fieldBoundary"}`)
		_, err := agmasync.Events(ctx, api, "")
		var apiErr *agmasync.APIError
		if !errors.As(err, &apiErr) || !errors.Is(err, agmasync.ErrForbidden) {
			t.Fatalf("Events error = %v, want an APIError matching ErrForbidden", err)
		}
		if apiErr.Message != "endpoint not opted into fieldBoundary" {
			t.Errorf("Message = %q", apiErr.Message)
		}
	})

	t.Run("400 on an operation that declares none", func(t *testing.T) {
		api := answer(t, http.StatusBadRequest, "application/json", `{"message":"invalid base revision"}`)
		_, err := deactivate(api, agmasync.TypeFarm, "farm-1")
		var apiErr *agmasync.APIError
		if !errors.As(err, &apiErr) || !errors.Is(err, agmasync.ErrValidation) {
			t.Fatalf("Deactivate error = %v, want an APIError matching ErrValidation", err)
		}
		if apiErr.Message != "invalid base revision" {
			t.Errorf("Message = %q", apiErr.Message)
		}
	})
}

func TestStreamRejectsAResponseThatIsNotAnEventStream(t *testing.T) {
	api := answer(t, http.StatusOK, "application/json", `{"hello":"proxy"}`)
	if _, err := agmasync.Events(context.Background(), api, ""); !errors.Is(err, agmasync.ErrNotEventStream) {
		t.Errorf("Events error = %v, want ErrNotEventStream", err)
	}
}

func frame(name string) string {
	return "id: pos-1\nevent: MASTERDATA_CHANGED\n" +
		`data: {"type":"farm","revision":2,"content":{"id":{"uniqueIds":[{"idText":` +
		`"33333333-3333-3333-3333-333333333333","idTypeCode":"UUID","idSource":"https://agrirouter.com"}]},` +
		`"name":"` + name + `"}}` + "\n\n"
}

func TestStreamReadsFramesLargerThan64KB(t *testing.T) {
	// A field boundary's geometry alone can exceed go-sse's 64KB default; a
	// frame over the limit would fail every resume from before it.
	name := strings.Repeat("x", 1<<20)
	api := answer(t, http.StatusOK, "text/event-stream", frame(name))
	stream, err := agmasync.Events(context.Background(), api, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var got int
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		farm := ev.Farm
		if farm == nil {
			t.Fatalf("frame carries no farm: %+v", ev.Envelope)
		}
		if len(farm.Content.Name) != len(name) {
			t.Errorf("name length = %d, want %d", len(farm.Content.Name), len(name))
		}
		got++
	}
	if got != 1 {
		t.Errorf("frames = %d, want 1", got)
	}
}

func TestStreamMaxEventSizeBoundsFrames(t *testing.T) {
	api := answer(t, http.StatusOK, "text/event-stream", frame(strings.Repeat("x", 2048)))
	stream, err := agmasync.Events(context.Background(), api, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.MaxEventSize = 1024

	var sawErr bool
	for _, err := range stream.Events() {
		if err == nil {
			t.Fatal("a frame over the configured bound must not be delivered")
		}
		sawErr = true
	}
	if !sawErr {
		t.Error("iteration ended without an error")
	}
}

// TestBaseRevisionRequiredCarriesTheCurrentRevision pins that a 428 hands back
// the revision the body names, so a participant whose create landed but whose
// response was lost can rebase its resend without a round trip.
func TestBaseRevisionRequiredCarriesTheCurrentRevision(t *testing.T) {
	api := answer(t, http.StatusPreconditionRequired, "application/json",
		`{"message":"base revision required","current_revision":4}`)
	_, err := deactivate(api, agmasync.TypeFarm, "farm-1")
	var conflict *agmasync.RevisionConflict
	if !errors.As(err, &conflict) || !errors.Is(err, agmasync.ErrBaseRevisionRequired) {
		t.Fatalf("error = %v, want a RevisionConflict matching ErrBaseRevisionRequired", err)
	}
	if conflict.CurrentRevision != 4 {
		t.Errorf("CurrentRevision = %d, want 4", conflict.CurrentRevision)
	}
	if errors.Is(err, agmasync.ErrRevisionConflict) {
		t.Errorf("a 428 matches ErrRevisionConflict")
	}
}

func TestCorrectMappingsSendsNullAndDecodesTheConflict(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sent = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"taken","rejection":{"local_id":"north","agrirouter_id":"` + north.String() +
			`","reason":"AGRIROUTER_ID_ALREADY_BOUND","existing_mapping":{"local_id":"south","agrirouter_id":"` + north.String() + `"}}}`))
	}))
	t.Cleanup(srv.Close)
	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	err = agmasync.CorrectMappings(context.Background(), api, uuid.New(), uuid.New(), agmasync.TypeField,
		[]oapi.IdMappingCorrectionEntry{
			agmasync.Correction("north", &north),
			agmasync.Correction("stray", nil),
		})

	// null is "none", which an omitted agrirouter_id would not say: the
	// attribute is required.
	want := `{"id_mappings":[{"agrirouter_id":"` + north.String() + `","local_id":"north"},{"agrirouter_id":null,"local_id":"stray"}]}`
	if sent != want {
		t.Errorf("body = %s, want %s", sent, want)
	}
	var mc *agmasync.MappingConflict
	if !errors.As(err, &mc) || !errors.Is(err, agmasync.ErrMappingConflict) {
		t.Fatalf("CorrectMappings error = %v, want a MappingConflict", err)
	}
	if mc.Rejection.ExistingMapping == nil || mc.Rejection.ExistingMapping.LocalId != "south" {
		t.Errorf("ExistingMapping = %+v, want the unlisted south holding the target", mc.Rejection.ExistingMapping)
	}
}

var north = uuid.MustParse("7d3e2f10-5a6b-4c8d-9e0f-1a2b3c4d5e6f")
