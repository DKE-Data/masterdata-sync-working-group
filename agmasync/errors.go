package agmasync

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
)

// Sentinel errors for conditions a participant branches on. Compare with
// [errors.Is]; the concrete types below carry the detail.
var (
	// ErrRevisionConflict is a 412. The write was made from a base revision
	// that cannot be reconciled with the current one, or from a base
	// agrirouter never issued, or against a localId that resolves to nothing.
	// See [RevisionConflict] and "Concurrency control" in specification.md.
	ErrRevisionConflict = errors.New("revision conflict")

	// ErrBaseRevisionRequired is a 428: an update was sent with no base
	// revision. Omitting the header would opt the participant out of
	// concurrency control, so agrirouter refuses rather than assuming.
	ErrBaseRevisionRequired = errors.New("base revision required")

	// ErrMappingConflict is a 409: an identifier in the request is already
	// bound to something else. See [MappingConflict] and "Asymmetric and
	// non-unique mappings" in specification.md.
	ErrMappingConflict = errors.New("identifier mapping conflict")

	// ErrValidation is a 400. Payloads are rejected rather than repaired: see
	// "Hard validation" in specification.md.
	ErrValidation = errors.New("validation failed")

	// ErrForbidden is a 403 — the acting endpoint is not entitled to the
	// object, or is not opted into its entity type.
	ErrForbidden = errors.New("forbidden")

	// ErrNotFound is a 404.
	ErrNotFound = errors.New("not found")

	// ErrInitialLoadConflict is a 409 on the initial-load status resource: a
	// transition out of order. Repeating the state the endpoint is already in
	// is not this — that succeeds. See "Initial load" in specification.md.
	ErrInitialLoadConflict = errors.New("initial load transition out of order")

	// ErrLocalIDRequired is raised locally: an entity is sent under its
	// local_id, so a write without one cannot be addressed.
	ErrLocalIDRequired = errors.New("local_id is required on send")

	// ErrEmptyResponse is a success status that carried no body to read.
	ErrEmptyResponse = errors.New("empty response")

	// ErrUnknownEntityType is raised locally, not by agrirouter, when an
	// entity carries a type this version does not define.
	ErrUnknownEntityType = errors.New("unknown entity type")

	// ErrEntityTypeMismatch is an answer carrying an entity of another type
	// than the operation asked for.
	ErrEntityTypeMismatch = errors.New("entity type mismatch")

	// ErrNotEventStream is a stream answering 200 in a media type other than
	// text/event-stream — a proxy or an error page answering in agrirouter's
	// place.
	ErrNotEventStream = errors.New("not an event stream")
)

// RevisionConflict reports a rejected write and the revision that stands.
//
// The specification requires a participant to take the revision from the
// response rather than assume base + 1, on failure as much as on success: the
// way out of a conflict is to obtain the current object, rebase the change onto
// it, and write again.
type RevisionConflict struct {
	// CurrentRevision is the canonical object's revision now. It is the
	// revision to rebase onto, not necessarily base + 1 — other participants
	// may have written more than once since.
	CurrentRevision int
	Message         string
}

func (e *RevisionConflict) Error() string {
	return fmt.Sprintf("agmasync: revision conflict, current revision is %d: %s",
		e.CurrentRevision, e.Message)
}

// Is makes [errors.Is] match [ErrRevisionConflict].
func (e *RevisionConflict) Is(target error) bool { return errors.Is(target, ErrRevisionConflict) }

// MappingConflict reports a binding that could not be recorded.
//
// The specification requires the cause to be machine-readable, because a
// participant's handling differs by cause: some of these belong in front of a
// user and some must never reach one. [MappingConflict.Rejection] carries both
// which identifier is taken and, where one exists, the mapping standing in the
// way — and both ends of that mapping belong to the rejected participant
// itself, so naming it discloses nothing it does not already hold.
type MappingConflict struct {
	Rejection oapi.IdMappingRejection
	Message   string
}

func (e *MappingConflict) Error() string {
	return fmt.Sprintf("agmasync: mapping conflict (%s) for localId %q / agrirouterId %s: %s",
		e.Rejection.Reason, e.Rejection.LocalId, e.Rejection.AgrirouterId, e.Message)
}

// Is makes [errors.Is] match [ErrMappingConflict].
func (e *MappingConflict) Is(target error) bool { return errors.Is(target, ErrMappingConflict) }

// Rejection reasons. The set is an extensible enumeration, so a participant
// MUST tolerate a value it does not know rather than treat it as a failure to
// parse — hence plain string constants and a default branch, not a closed Go
// enum. See "Extensible enumerations" in specification.md.
const (
	// ReasonLocalIDAlreadyBound means this application already knows a
	// different canonical object in the tenant by that localId. Resolving it
	// needs a user: two of their records are being claimed to be one. What the
	// application has bound in another tenant never causes this: the mapping is
	// keyed by the application and the tenant.
	ReasonLocalIDAlreadyBound = "LOCAL_ID_ALREADY_BOUND"

	// ReasonAgrirouterIDAlreadyBound means this application already knows that
	// canonical object under a different localId. Often resolvable without a
	// user — the participant is usually holding the answer already.
	ReasonAgrirouterIDAlreadyBound = "AGRIROUTER_ID_ALREADY_BOUND"

	// ReasonUnknownObject means no such canonical object, or the endpoint is
	// not entitled to it.
	ReasonUnknownObject = "UNKNOWN_OBJECT"

	// ReasonDuplicateInRequest means one bulk confirmation named the same
	// identifier in more than one pair. Every pair involved is rejected and
	// none applied, since the outcome would otherwise depend on ordering.
	ReasonDuplicateInRequest = "DUPLICATE_IN_REQUEST"
)

// NeedsUser reports whether a rejection is one only a person can settle.
//
// A localId already bound to a different canonical object is a genuine
// granularity disagreement between two systems — the n:1 case the protocol
// deliberately does not merge automatically. The others are the participant's
// own to fix: it already holds the object under another identifier, or it sent
// a malformed batch.
func NeedsUser(r oapi.IdMappingRejection) bool {
	return r.Reason == ReasonLocalIDAlreadyBound
}

// APIError is any other non-success response.
type APIError struct {
	StatusCode int
	Message    string
	sentinel   error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("agmasync: %s (HTTP %d)", e.Message, e.StatusCode)
}

// Is matches the sentinel for the status code, where there is one.
func (e *APIError) Is(target error) bool {
	return e.sentinel != nil && errors.Is(target, e.sentinel)
}

// writeResult is what every write operation reduces to before it is turned into
// a result or an error. The generated response types are one per operation and
// per entity type — twenty of them, structurally identical — so each operation
// wrapper fills this in and shares the handling below.
type writeResult struct {
	statusCode int
	validation *oapi.Error
	forbidden  *oapi.Error
	notFound   *oapi.Error
	conflict   *oapi.MappingConflictError
	precond    *oapi.RevisionConflictError
	// required is the 428 body, which is shaped like the 412 body: agrirouter
	// answers a missing base revision with the revision the participant should
	// have sent, sparing it a round trip to find out.
	required *oapi.RevisionConflictError
	body     []byte
}

// err converts a response into the typed error the participant branches on, or
// nil where the response was a success.
func (r writeResult) err() error {
	switch r.statusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusBadRequest:
		return &APIError{r.statusCode, r.message(r.validation, "validation failed"), ErrValidation}
	case http.StatusForbidden:
		return &APIError{r.statusCode, r.message(r.forbidden, "forbidden"), ErrForbidden}
	case http.StatusNotFound:
		return &APIError{r.statusCode, r.message(r.notFound, "not found"), ErrNotFound}
	case http.StatusConflict:
		if r.conflict != nil {
			return &MappingConflict{Rejection: r.conflict.Rejection, Message: r.conflict.Message}
		}
		return &APIError{r.statusCode, "conflict", ErrMappingConflict}
	case http.StatusPreconditionFailed:
		if r.precond != nil {
			return &RevisionConflict{
				CurrentRevision: r.precond.CurrentRevision,
				Message:         r.precond.Message,
			}
		}
		return &APIError{r.statusCode, "precondition failed", ErrRevisionConflict}
	case http.StatusPreconditionRequired:
		msg := "base revision required"
		if r.required != nil && r.required.Message != "" {
			msg = r.required.Message
		}
		return &APIError{r.statusCode, msg, ErrBaseRevisionRequired}
	default:
		return &APIError{r.statusCode, unexpected(r.body), nil}
	}
}

// message is the error message agrirouter sent, or fallback where it sent none.
//
// e is the body as the generated client decoded it. It is nil where the
// operation declares no body for the status — no operation but a put declares
// a 400, though agrirouter answers one to any malformed request — and for the
// streams, which are read without the generated decoding. The raw body is read
// as the same error schema then.
func (r writeResult) message(e *oapi.Error, fallback string) string {
	if e == nil {
		var raw oapi.Error
		if json.Unmarshal(r.body, &raw) == nil {
			e = &raw
		}
	}
	if e == nil || e.Message == "" {
		return fallback
	}
	return e.Message
}

func unexpected(body []byte) string {
	if len(body) == 0 {
		return "unexpected response"
	}
	const maxLen = 200
	if len(body) > maxLen {
		body = body[:maxLen]
	}
	return "unexpected response: " + string(body)
}

// mappingResult is the same reduction for the binding operations, which answer
// 204 and carry no entity.
type mappingResult struct {
	statusCode int
	forbidden  *oapi.Error
	notFound   *oapi.Error
	conflict   *oapi.MappingConflictError
	body       []byte
}

func (r mappingResult) err() error {
	return writeResult{
		statusCode: r.statusCode,
		forbidden:  r.forbidden,
		notFound:   r.notFound,
		conflict:   r.conflict,
		body:       r.body,
	}.err()
}
