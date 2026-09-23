package testrouter

import (
	"encoding/json"
	"fmt"
)

// serverAssigned are the entity properties openapi.yaml marks `readOnly`: the
// six agrirouter owns, the same six on every entity type.
//
// A real agrirouter validates requests against the schema and refuses a write
// carrying any of them. This router refuses them too, and for the reason it
// implements anything: a participant that sends back the object it was
// delivered — every field of it, including the ones it never set — is a
// participant whose writes fail in production, and finding that out here is the
// whole point of having this.
var serverAssigned = []string{
	"agrirouter_id", "modified_at", "revision", "source_endpoint_id", "tenant_id",
}

// rejectServerAssigned refuses a write that states what agrirouter assigns.
//
// It reads the body the generated server already parsed, which is why `type` is
// treated apart from the rest. The five below are pointers and omitted when
// absent, so their presence is the client's doing. `type` is a plain required
// string that marshals whatever happens, so presence says nothing and only a
// value does: a client that left it out is indistinguishable from one that sent
// an empty string, and the empty string is not a discriminator anybody meant.
func rejectServerAssigned(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}

	for _, name := range serverAssigned {
		if _, found := fields[name]; found {
			return readOnlyErr(name)
		}
	}
	if value, found := fields["type"]; found && string(value) != `""` {
		return readOnlyErr("type")
	}
	return nil
}

// readOnlyErr is worded as a real agrirouter words it, so that a participant
// meeting it here recognises it when it arrives from the other one.
func readOnlyErr(property string) error {
	return fmt.Errorf("readOnly property %q in request", property)
}
