package main

import (
	"net/url"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func TestOrganizationsAndPersonsGoOutAsParties(t *testing.T) {
	// The screens offer the platform's own two kinds of record; the protocol
	// has one party entity stating its party type in details.
	for _, tc := range []struct {
		typ  recordType
		form url.Values
		want string
	}{
		{recordOrganization,
			url.Values{"name": {"Agrar GmbH"}, "details.commercial_registry_number": {"HRB 1"}},
			`{"details":{"commercial_registry_number":"HRB 1","party_type":"ORGANIZATION"},"name":"Agrar GmbH"}`},
		{recordPerson,
			url.Values{"name": {"Anke Meyer"}, "details.last_name": {"Meyer"}},
			`{"details":{"last_name":"Meyer","party_type":"PERSON"},"name":"Anke Meyer"}`},
	} {
		if got := tc.typ.entityType(); got != agmasync.TypeParty {
			t.Errorf("%s travels as %s, want party", tc.typ, got)
		}
		body, err := attributesFromForm(tc.typ, tc.form)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if string(body) != tc.want {
			t.Errorf("%s: attributes = %s, want %s", tc.typ, body, tc.want)
		}
		record, err := recordFrom(agmasync.TypeParty, "PTY-1", body)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if got := recordTypeOf(record); got != tc.typ {
			t.Errorf("record type read back = %s, want %s", got, tc.typ)
		}
	}
}
