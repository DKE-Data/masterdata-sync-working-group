// Package store is the sample platform's own database.
//
// It is one plausible farm management system, not something the specification
// requires. What is worth reading is schema.sql, and in particular the split it
// draws: the platform's own tables, which know nothing about AgmaSync, and the
// agmasync_ tables beside them holding the correspondence and the delivery
// position — the two things a synchronizing participant has to keep and usually
// has nowhere to put.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"

	// modernc.org/sqlite is the pure-Go driver, so the sample builds and runs
	// with no cgo toolchain.
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned where the platform holds no such record.
var ErrNotFound = errors.New("not found")

// Store is the platform's database.
type Store struct {
	db *sql.DB

	// ro serves reads that must not queue behind the writer. It is the same
	// handle as db for an in-memory database, where a second connection would be
	// a second, empty database rather than another view of this one.
	ro *sql.DB
}

// Open opens the database at path and applies the schema.
//
// Use ":memory:" for a test. A file path is what makes the point of the
// bookkeeping tables visible: stop the process, start it again, and the
// platform still knows what agrirouter calls its records and where its stream
// left off.
//
// A file-backed database is opened in WAL mode with a second, read-only pool
// beside the writer. Neither is required by the specification and neither
// changes what is stored: they exist because a reconciliation that waits for a
// person holds its transaction open for as long as the person takes, and
// something has to be able to read the platform's tables meanwhile — the screen
// asking the question, for one. In the journal mode SQLite starts in, that read
// would wait for the answer it is trying to collect.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// One connection keeps the sample's transactions simple and side-steps
	// SQLite's writer lock. A real platform would size its pool properly.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	s := &Store{db: db, ro: db}
	if inMemory(path) {
		return s, nil
	}

	// WAL is a property of the file rather than of the connection, so this is
	// set once and outlives the process that set it.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("enabling WAL: %w", err)
	}
	ro, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening database for reading: %w", err)
	}
	s.ro = ro
	return s, nil
}

// dsn adds a busy timeout, which is per connection and so cannot be set once
// for a pool by executing it.
func dsn(path string) string {
	if inMemory(path) {
		return path
	}
	return "file:" + path + "?_pragma=busy_timeout(10000)"
}

func inMemory(path string) bool {
	return path == ":memory:" || strings.Contains(path, "mode=memory")
}

// Close releases the database.
func (s *Store) Close() error {
	if s.ro != s.db {
		_ = s.ro.Close()
	}
	return s.db.Close()
}

// Tx runs fn in a transaction, on behalf of one of the product's tenants,
// rolling back on error.
//
// Every apply goes through here, because the entity, its revision, and the
// delivery position have to move together. Advancing the position outside the
// transaction that wrote the object would break the rule that a position is
// derived from what has been durably applied.
//
// The tenant is fixed for the transaction rather than passed to each call: a
// unit of work is done on behalf of one tenancy, and it decides whose holdings
// a write joins and whose holdings a listing walks. It does not scope the
// records themselves, which are the whole platform's, nor the identifier
// mapping, which is scoped by the endpoint each call names — the scope
// agrirouter keys the mapping at. See schema.sql.
func (s *Store) Tx(tenant string, fn func(*Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&Tx{tx: tx, tenant: tenant}); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadTx runs fn against the read pool, for work that only looks.
//
// It is not a weaker Tx and it is not an optimisation: it exists so that
// reading cannot be blocked by a write that is waiting for a person. Writing
// through it is a mistake the database will refuse under concurrency rather
// than one this signature prevents, so the rule is the caller's to keep.
func (s *Store) ReadTx(tenant string, fn func(*Tx) error) error {
	tx, err := s.ro.Begin()
	if err != nil {
		return fmt.Errorf("beginning read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	return fn(&Tx{tx: tx, tenant: tenant})
}

// Tx is a transaction over the platform's database, on behalf of one tenant.
type Tx struct {
	tx     *sql.Tx
	tenant string
}

// Tenant is the tenancy this transaction runs in.
func (t *Tx) Tenant() string { return t.tenant }

// Endpoint names the namespace a pair lives in: one of the product's endpoints,
// by the external id it gave it, within the agrirouter tenant the endpoint is
// in. It is what agrirouter keys the identifier mapping by.
type Endpoint struct {
	TenantID   uuid.UUID
	ExternalID string
}

// SyncRow is what the platform knows about one record's place in the exchange
// through one endpoint. There is one row per record and endpoint that has sent
// or bound it; two endpoints in one agrirouter tenant hold a row each, both
// naming one canonical object.
type SyncRow struct {
	// Endpoint is whose pair this is. Its TenantID is the agrirouter tenant the
	// canonical object belongs to.
	Endpoint     Endpoint
	EntityType   agmasync.EntityType
	LocalID      string
	AgrirouterID *uuid.UUID
	Revision     *int

	// Unbound is true where agrirouter refused the binding the platform
	// offered for the record. It is not the same as never having been bound:
	// this record must not be sent, because the send would not resolve and
	// would create a second canonical object for the same entity.
	Unbound bool
}

// Bound reports whether the record is bound to a canonical object. An unbound
// record has never been sent, or had its binding refused, and must not be sent under
// its own identifier until it is bound — an unbound send does not resolve and
// mints a duplicate.
func (r SyncRow) Bound() bool { return r.AgrirouterID != nil }

const syncColumns = `agrirouter_tenant_id, external_endpoint_id, entity_type, local_id,
	agrirouter_id, revision, unbound`

// SyncRow reads one endpoint's bookkeeping for one of the platform's records.
func (t *Tx) SyncRow(ep Endpoint, typ agmasync.EntityType, localID string) (SyncRow, error) {
	return scanSyncRow(t.tx.QueryRow(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id = ?
		   AND entity_type = ? AND local_id = ?`,
		ep.TenantID.String(), ep.ExternalID, string(typ), localID))
}

// SyncRowByAgrirouterID reads one endpoint's bookkeeping by canonical
// identifier, which is how a delivered object is matched when it carries no
// localId of ours.
func (t *Tx) SyncRowByAgrirouterID(
	ep Endpoint, typ agmasync.EntityType, agrirouterID uuid.UUID,
) (SyncRow, error) {
	return scanSyncRow(t.tx.QueryRow(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id = ?
		   AND entity_type = ? AND agrirouter_id = ?`,
		ep.TenantID.String(), ep.ExternalID, string(typ), agrirouterID.String()))
}

// SiblingRow reads another endpoint's pair for a canonical object: which of the
// platform's records it is, as a sibling endpoint bound it.
//
// The store is one behind every endpoint, so a record a sibling bound is a
// record this endpoint holds too, and an endpoint that finds one binds the
// same local_id for itself rather than creating a second record — or, on
// send, a second canonical object. An agrirouterId belongs to one tenant, so
// every row answering it is a sibling in that tenant.
func (t *Tx) SiblingRow(
	ep Endpoint, typ agmasync.EntityType, agrirouterID uuid.UUID,
) (SyncRow, error) {
	return scanSyncRow(t.tx.QueryRow(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE entity_type = ? AND agrirouter_id = ?
		   AND NOT (agrirouter_tenant_id = ? AND external_endpoint_id = ?)
		 ORDER BY external_endpoint_id
		 LIMIT 1`,
		string(typ), agrirouterID.String(), ep.TenantID.String(), ep.ExternalID))
}

// SiblingRowByLocalID reads another endpoint's bound pair for one of the
// platform's records in the same agrirouter tenant. An endpoint about to send
// a record it has never bound uses it to bind first: the record is already a
// canonical object there, and sending it unbound would mint a second.
func (t *Tx) SiblingRowByLocalID(
	ep Endpoint, typ agmasync.EntityType, localID string,
) (SyncRow, error) {
	return scanSyncRow(t.tx.QueryRow(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id <> ?
		   AND entity_type = ? AND local_id = ? AND agrirouter_id IS NOT NULL
		 ORDER BY external_endpoint_id
		 LIMIT 1`,
		ep.TenantID.String(), ep.ExternalID, string(typ), localID))
}

// recordOf reads which of the platform's records a canonical object is,
// whichever endpoint bound it. Records are the whole platform's, so any pair
// answers.
func (t *Tx) recordOf(typ agmasync.EntityType, agrirouterID uuid.UUID) (SyncRow, error) {
	return scanSyncRow(t.tx.QueryRow(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE entity_type = ? AND agrirouter_id = ?
		 ORDER BY external_endpoint_id
		 LIMIT 1`,
		string(typ), agrirouterID.String()))
}

type scanner interface{ Scan(dest ...any) error }

func scanSyncRow(row scanner) (SyncRow, error) {
	var tenantID, externalID, typ, localID string
	var rawID sql.NullString
	var revision sql.NullInt64
	var unbound int
	if err := row.Scan(&tenantID, &externalID, &typ, &localID, &rawID, &revision, &unbound); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncRow{}, ErrNotFound
		}
		return SyncRow{}, fmt.Errorf("reading sync row: %w", err)
	}
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return SyncRow{}, fmt.Errorf("stored agrirouter tenant is not a uuid: %w", err)
	}
	out := SyncRow{
		Endpoint:   Endpoint{TenantID: tenant, ExternalID: externalID},
		EntityType: agmasync.EntityType(typ), LocalID: localID, Unbound: unbound != 0,
	}
	if rawID.Valid {
		parsed, err := uuid.Parse(rawID.String)
		if err != nil {
			return SyncRow{}, fmt.Errorf("stored agrirouterId is not a uuid: %w", err)
		}
		out.AgrirouterID = &parsed
	}
	if revision.Valid {
		rev := int(revision.Int64)
		out.Revision = &rev
	}
	return out, nil
}

// PutSyncRow records the correspondence and the revision for one record.
func (t *Tx) PutSyncRow(r SyncRow) error {
	if r.Endpoint.TenantID == uuid.Nil || r.Endpoint.ExternalID == "" {
		return fmt.Errorf("writing sync row: no endpoint for %s %q", r.EntityType, r.LocalID)
	}
	var rawID any
	if r.AgrirouterID != nil {
		rawID = r.AgrirouterID.String()
	}
	var revision any
	if r.Revision != nil {
		revision = *r.Revision
	}

	// Writing a pair clears the unbound marker: the platform holds the object
	// again, under this identifier, whether that is a rebinding of the same one
	// or a fresh record.
	_, err := t.tx.Exec(`
		INSERT INTO agmasync_object
			(agrirouter_tenant_id, external_endpoint_id, entity_type, local_id, agrirouter_id, revision)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (agrirouter_tenant_id, external_endpoint_id, entity_type, local_id) DO UPDATE SET
			agrirouter_id = excluded.agrirouter_id,
			revision      = excluded.revision,
			unbound       = 0`,
		r.Endpoint.TenantID.String(), r.Endpoint.ExternalID, string(r.EntityType), r.LocalID,
		rawID, revision)
	if err != nil {
		return fmt.Errorf("writing sync row: %w", err)
	}
	return nil
}

// Unbind drops the correspondence while leaving the record itself alone, for a
// binding agrirouter refused to record.
//
// The record stays, because the platform still holds it — what has gone is the
// claim that it is the same thing agrirouter holds, and until a person settles
// what it is, it must not be sent.
//
// The row is marked rather than deleted. A record with no row at all is one that
// has never been sent, which is sendable; this one is not, and nothing else
// would say so once the pair is gone.
//
// It is per endpoint, as the pair is: the record stays bound through any other
// endpoint that holds it.
func (t *Tx) Unbind(ep Endpoint, typ agmasync.EntityType, localID string) error {
	_, err := t.tx.Exec(`
		UPDATE agmasync_object
		   SET agrirouter_id = NULL, revision = NULL, unbound = 1
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id = ?
		   AND entity_type = ? AND local_id = ?`,
		ep.TenantID.String(), ep.ExternalID, string(typ), localID)
	if err != nil {
		return fmt.Errorf("unbinding: %w", err)
	}
	return nil
}

// DropSyncRow forgets one endpoint's bookkeeping for one record, leaving it a
// record that endpoint has never sent. A correction to none does this: the
// record matched nothing, and is sent as a create.
func (t *Tx) DropSyncRow(ep Endpoint, typ agmasync.EntityType, localID string) error {
	_, err := t.tx.Exec(`
		DELETE FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id = ?
		   AND entity_type = ? AND local_id = ?`,
		ep.TenantID.String(), ep.ExternalID, string(typ), localID)
	if err != nil {
		return fmt.Errorf("dropping sync row: %w", err)
	}
	return nil
}

// DiscardTenant drops every pair whose canonical object belongs to the given
// agrirouter tenant, through whichever endpoint, which is what a masterdata
// reset of it requires, and reports how many it dropped. That includes the
// pairs kept for an endpoint agrirouter has since removed.
//
// The rows are deleted rather than marked unbound. Unbound says the record must
// not be sent because a canonical object for it exists; after a reset none
// does, so the record is simply one the platform has never sent. The records
// themselves stay: they are the user's data, and the reset ended the
// correspondence, not the data.
func (t *Tx) DiscardTenant(tenantID uuid.UUID) (int, error) {
	res, err := t.tx.Exec(`DELETE FROM agmasync_object WHERE agrirouter_tenant_id = ?`, tenantID.String())
	if err != nil {
		return 0, fmt.Errorf("discarding bindings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("discarding bindings: %w", err)
	}
	return int(n), nil
}

// Bindings lists every correspondence one endpoint holds, which is what its
// reconciliation confirmation carries in bulk.
//
// Only this endpoint's: a sibling's pairs are in the sibling's namespace, and
// carrying them here would bind them for this endpoint without its having
// recognised anything.
func (t *Tx) Bindings(ep Endpoint) ([]SyncRow, error) {
	rows, err := t.tx.Query(`
		SELECT `+syncColumns+`
		  FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND external_endpoint_id = ? AND agrirouter_id IS NOT NULL
		 ORDER BY entity_type, local_id`, ep.TenantID.String(), ep.ExternalID)
	if err != nil {
		return nil, fmt.Errorf("listing bindings: %w", err)
	}
	defer rows.Close()

	var out []SyncRow
	for rows.Next() {
		row, err := scanSyncRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Position reads the delivery position, empty if the platform has never applied
// anything. An empty position asks agrirouter for everything.
//
// Not per tenant, unlike everything above it: one stream serves the whole
// application, carrying every tenant it holds, so there is one position for all
// of them.
func (t *Tx) Position() (string, error) {
	var id string
	err := t.tx.QueryRow(`SELECT last_event_id FROM agmasync_stream WHERE id = 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading position: %w", err)
	}
	return id, nil
}

// SetPosition records the delivery position.
//
// Call it in the same transaction as the apply it covers. The identifier is
// opaque and is stored exactly as agrirouter issued it: nothing here parses,
// compares, or constructs one.
func (t *Tx) SetPosition(lastEventID string) error {
	if lastEventID == "" {
		return nil
	}
	_, err := t.tx.Exec(`
		INSERT INTO agmasync_stream (id, last_event_id) VALUES (1, ?)
		ON CONFLICT (id) DO UPDATE SET last_event_id = excluded.last_event_id`, lastEventID)
	if err != nil {
		return fmt.Errorf("writing position: %w", err)
	}
	return nil
}

// Route reads what one endpoint is routed to exchange, as the last
// ROUTE_CHANGED frame stated it. An endpoint that has never been routed has no
// row and no types, which is not an error: it is an endpoint that exchanges
// nothing.
func (t *Tx) Route(endpointID uuid.UUID) ([]agmasync.EntityType, error) {
	types, _, err := t.Routing(endpointID)
	return types, err
}

// Routing is [Tx.Route] that also reports whether any ROUTE_CHANGED frame was
// recorded for the endpoint, which an empty list alone does not say.
func (t *Tx) Routing(endpointID uuid.UUID) ([]agmasync.EntityType, bool, error) {
	var list string
	err := t.tx.QueryRow(`
		SELECT entity_types FROM agmasync_route WHERE endpoint_id = ?`,
		endpointID.String()).Scan(&list)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading routing: %w", err)
	}

	var out []agmasync.EntityType
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, agmasync.EntityType(name))
		}
	}
	return out, true, nil
}

// SetRoute records what an endpoint is routed to exchange.
//
// Call it in the transaction the frame's position is written in. The frame
// states the whole routing, so this replaces what was held rather than merging
// with it — an empty list is the statement that the endpoint exchanges nothing,
// and it is the one that must survive, since it is how a withdrawal arrives.
func (t *Tx) SetRoute(endpointID uuid.UUID, types []agmasync.EntityType) error {
	names := make([]string, 0, len(types))
	for _, typ := range types {
		names = append(names, string(typ))
	}
	_, err := t.tx.Exec(`
		INSERT INTO agmasync_route (endpoint_id, entity_types) VALUES (?, ?)
		ON CONFLICT (endpoint_id) DO UPDATE SET entity_types = excluded.entity_types`,
		endpointID.String(), strings.Join(names, ","))
	if err != nil {
		return fmt.Errorf("writing routing: %w", err)
	}
	return nil
}

// Position reads the delivery position outside a transaction. The tenant is
// immaterial — the position is the application's — so any will do.
func (s *Store) Position() (string, error) {
	var out string
	err := s.Tx("", func(tx *Tx) error {
		var err error
		out, err = tx.Position()
		return err
	})
	return out, err
}

// BoundInTenant reports whether a record is bound to a canonical object of an
// agrirouter tenant through any of the platform's endpoints there.
func (t *Tx) BoundInTenant(tenantID uuid.UUID, typ agmasync.EntityType, localID string) (bool, error) {
	return t.anyRowInTenant(tenantID, typ, localID, `agrirouter_id IS NOT NULL`)
}

// ClaimedInTenant reports whether a record is bound in an agrirouter tenant, or
// had its binding there refused, through any of the platform's endpoints.
// Either way it answers to a canonical object of the tenant already.
func (t *Tx) ClaimedInTenant(tenantID uuid.UUID, typ agmasync.EntityType, localID string) (bool, error) {
	return t.anyRowInTenant(tenantID, typ, localID, `(agrirouter_id IS NOT NULL OR unbound = 1)`)
}

func (t *Tx) anyRowInTenant(
	tenantID uuid.UUID, typ agmasync.EntityType, localID, condition string,
) (bool, error) {
	var one int
	err := t.tx.QueryRow(`
		SELECT 1 FROM agmasync_object
		 WHERE agrirouter_tenant_id = ? AND entity_type = ? AND local_id = ?
		   AND `+condition+`
		 LIMIT 1`, tenantID.String(), string(typ), localID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking bindings of %s %q: %w", typ, localID, err)
	}
	return true, nil
}
