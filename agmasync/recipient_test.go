package agmasync_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// TestStreamReadsOneFramePerRecipientEndpoint pins the fan-out of one object
// to two endpoints of the application: each frame names its recipient and
// carries that endpoint's own localId, which may differ between the two.
func TestStreamReadsOneFramePerRecipientEndpoint(t *testing.T) {
	agrirouterID := uuid.MustParse("7c1d2e3f-4a5b-6c7d-8e9f-001122338899")
	orgA := uuid.MustParse("2b3c4d5e-6f70-8192-a3b4-c5d6e7f80912")
	orgB := uuid.MustParse("9f8e7d6c-5b4a-3210-fedc-ba9876543210")

	frameFor := func(recipient uuid.UUID, localID string) string {
		return fmt.Sprintf("event: MASTERDATA_CHANGED\nid: p1\ndata: "+
			`{"type":"farm","revision":3,"recipient_endpoint_id":%q,`+
			`"content":{"id":{"referenceId":%q,"uniqueIds":[`+
			`{"idText":%q,"idTypeCode":"UUID","idSource":%q},`+
			`{"idText":%q,"idTypeCode":"STRING"}]},"name":"Hof"}}`+"\n\n",
			recipient, localID, agrirouterID, agmasync.AgrirouterIDSource, localID)
	}
	api := answer(t, http.StatusOK, "text/event-stream",
		frameFor(orgA, "abc")+frameFor(orgB, "xyz"))

	stream, err := agmasync.Events(context.Background(), api, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	type delivery struct {
		recipient uuid.UUID
		localID   string
	}
	var got []delivery
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		env := ev.Envelope
		if env.RecipientEndpointId == nil || env.LocalID == nil {
			t.Fatalf("frame lacks recipient or localId: %+v", env)
		}
		if env.AgrirouterID == nil || *env.AgrirouterID != agrirouterID {
			t.Errorf("AgrirouterID = %v, want %s", env.AgrirouterID, agrirouterID)
		}
		got = append(got, delivery{*env.RecipientEndpointId, *env.LocalID})
	}

	want := []delivery{{orgA, "abc"}, {orgB, "xyz"}}
	if len(got) != len(want) {
		t.Fatalf("frames = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestWriteResponseNamesTheActingEndpoint pins recipient_endpoint_id on the
// object a write answers with: it is the acting endpoint, whose localIds the
// object carries.
func TestWriteResponseNamesTheActingEndpoint(t *testing.T) {
	acting := uuid.MustParse("2b3c4d5e-6f70-8192-a3b4-c5d6e7f80912")
	api := answer(t, http.StatusOK, "application/json", fmt.Sprintf(
		`{"type":"field","active":false,"recipient_endpoint_id":%q,`+
			`"content":{"id":{"referenceId":"field-1"},"name":"Acker"}}`, acting))

	obj, err := deactivate(api, agmasync.TypeField, "field-1")
	if err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if got := obj.Envelope.RecipientEndpointId; got == nil || *got != acting {
		t.Errorf("RecipientEndpointId = %v, want %s", got, acting)
	}
}
