package testrouter

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// sentIdentity is what a write says about the fields agrirouter assigns that
// identify the object, and about the writer's own identifier for it. A
// participant may send back the object it was delivered, every field of it, so
// the assigned ones are accepted when they agree with the write and refused, as
// a real agrirouter refuses them, when they name another object. The rest of
// what agrirouter assigns — revision, modified_at, source_endpoint_id — goes
// stale with every write and is ignored.
type sentIdentity struct {
	Type     *string    `json:"type"`
	TenantID *uuid.UUID `json:"tenant_id"`

	// AgrirouterID is the agrirouter entry of `content.id`, and local the
	// local entry, nil where the write sent none.
	AgrirouterID *uuid.UUID
	local        *localEntry
}

// decodeSentIdentity reads a write's identity. `content.id.referenceId` is the
// local identifier, and must be the one the path writes under.
func decodeSentIdentity(raw []byte, localID string) (sentIdentity, error) {
	var sent sentIdentity
	if err := json.Unmarshal(raw, &sent); err != nil {
		return sentIdentity{}, fmt.Errorf("type or tenant_id is malformed: %w", err)
	}
	var body struct {
		Content *struct {
			ID *wireID `json:"id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return sentIdentity{}, fmt.Errorf("content.id is malformed: %w", err)
	}
	if body.Content == nil || body.Content.ID == nil {
		return sentIdentity{}, errors.New("content.id is required")
	}
	id := *body.Content.ID
	if id.ReferenceID == nil || *id.ReferenceID != localID {
		return sentIdentity{}, fmt.Errorf("content.id.referenceId must be the local_id %q the path writes", localID)
	}
	agrirouterID, local, err := id.read()
	if err != nil {
		return sentIdentity{}, err
	}
	sent.AgrirouterID, sent.local = agrirouterID, local
	return sent, nil
}

// checkRequest refuses a type or tenant_id the request itself contradicts.
//
// Clients generated with `type` as a plain required string send "" when they
// never set it, so the empty string counts as absent.
func (s sentIdentity) checkRequest(typ agmasync.EntityType, tenantID uuid.UUID) error {
	if s.Type != nil && *s.Type != "" && *s.Type != string(typ) {
		return fmt.Errorf("type %q does not match the path, which writes a %s", *s.Type, typ)
	}
	if s.TenantID != nil && *s.TenantID != tenantID {
		return fmt.Errorf("tenant_id does not match the tenant of the acting endpoint")
	}
	return nil
}

// checkBinding refuses an agrirouter entry other than the object localID is
// bound to. On an unbound localID any agrirouter entry is refused: the write
// would create a second object rather than update the one named, the duplicate
// binding exists to prevent.
func (s sentIdentity) checkBinding(localID string, bound uuid.UUID, known bool) error {
	switch {
	case s.AgrirouterID == nil:
		return nil
	case !known:
		return fmt.Errorf("the agrirouter entry is not bound to local_id %q: bind it before writing under that local_id", localID)
	case *s.AgrirouterID != bound:
		return fmt.Errorf("the agrirouter entry does not match the object local_id %q is bound to", localID)
	}
	return nil
}
