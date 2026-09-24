package testrouter

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// sentIdentity is what a write says about the fields agrirouter assigns that
// identify the object. A participant may send back the object it was delivered,
// every field of it, so these are accepted when they agree with the write and
// refused, as a real agrirouter refuses them, when they name another object.
// The rest of what agrirouter assigns — revision, modified_at,
// source_endpoint_id — goes stale with every write and is ignored.
type sentIdentity struct {
	Type         *string    `json:"type"`
	TenantID     *uuid.UUID `json:"tenant_id"`
	AgrirouterID *uuid.UUID `json:"agrirouter_id"`
}

func decodeSentIdentity(raw []byte) (sentIdentity, error) {
	var sent sentIdentity
	if err := json.Unmarshal(raw, &sent); err != nil {
		return sentIdentity{}, fmt.Errorf("type, tenant_id, or agrirouter_id is malformed: %w", err)
	}
	return sent, nil
}

// checkRequest refuses a type or tenant_id the request itself contradicts.
//
// The body arrives re-marshalled from the generated server's typed value, where
// `type` is a plain required string: a client that left it out is
// indistinguishable from one that sent "", so the empty string counts as
// absent.
func (s sentIdentity) checkRequest(typ agmasync.EntityType, tenantID uuid.UUID) error {
	if s.Type != nil && *s.Type != "" && *s.Type != string(typ) {
		return fmt.Errorf("type %q does not match the path, which writes a %s", *s.Type, typ)
	}
	if s.TenantID != nil && *s.TenantID != tenantID {
		return fmt.Errorf("tenant_id does not match the tenant of the acting endpoint")
	}
	return nil
}

// checkBinding refuses an agrirouter_id other than the object localID is bound
// to. On an unbound localID any agrirouter_id is refused: the write would create
// a second object rather than update the one named, the duplicate binding
// exists to prevent.
func (s sentIdentity) checkBinding(localID string, bound uuid.UUID, known bool) error {
	switch {
	case s.AgrirouterID == nil:
		return nil
	case !known:
		return fmt.Errorf("agrirouter_id is not bound to local_id %q: bind it before writing under that local_id", localID)
	case *s.AgrirouterID != bound:
		return fmt.Errorf("agrirouter_id does not match the object local_id %q is bound to", localID)
	}
	return nil
}
