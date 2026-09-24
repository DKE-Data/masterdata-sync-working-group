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
	if err := addAgrirouterTenantIDColumn(db); err != nil {
		return nil, err
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

// addAgrirouterTenantIDColumn brings a database made before agmasync_object had
// an agrirouter_tenant_id column up to date. Its existing pairs are left without
// one until the object is next applied, and a reset does not discard them
// before then.
func addAgrirouterTenantIDColumn(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`
		SELECT count(*) FROM pragma_table_info('agmasync_object') WHERE name = 'agrirouter_tenant_id'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("reading schema: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE agmasync_object ADD COLUMN agrirouter_tenant_id TEXT`); err != nil {
		return fmt.Errorf("adding agrirouter_tenant_id column: %w", err)
	}
	return nil
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
// records themselves or the identifier mapping — both are the whole platform's,
// which is the scope agrirouter keys the mapping at. See schema.sql.
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

// SyncRow is what the platform knows about one record's place in the exchange.
// There is one row per record for the whole platform, however many of its
// tenants hold that record.
type SyncRow struct {
	EntityType   agmasync.EntityType
	LocalID      string
	AgrirouterID *uuid.UUID
	Revision     *int

	// TenantID is the agrirouter tenant the canonical object belongs to, taken
	// from its `tenantId`. It is written, not read back: a masterdata reset
	// discards by it. Nil leaves what the row holds.
	TenantID *uuid.UUID

	// Unbound is true where the platform told agrirouter it no longer holds the
	// object. It is not the same as never having been bound: this record must
	// not be sent, because the send would not resolve and would create a second
	// canonical object for the same entity.
	Unbound bool
}

// Bound reports whether the record is bound to a canonical object. An unbound
// record has never been sent, or has been unbound, and must not be sent under
// its own identifier until it is bound — an unbound send does not resolve and
// mints a duplicate.
func (r SyncRow) Bound() bool { return r.AgrirouterID != nil }

// SyncRow reads the bookkeeping for one of the platform's records.
func (t *Tx) SyncRow(typ agmasync.EntityType, localID string) (SyncRow, error) {
	row := t.tx.QueryRow(`
		SELECT agrirouter_id, revision, unbound
		  FROM agmasync_object
		 WHERE entity_type = ? AND local_id = ?`,
		string(typ), localID)

	var rawID sql.NullString
	var revision sql.NullInt64
	var unbound int
	if err := row.Scan(&rawID, &revision, &unbound); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncRow{}, ErrNotFound
		}
		return SyncRow{}, fmt.Errorf("reading sync row: %w", err)
	}
	return toSyncRow(typ, localID, rawID, revision, unbound)
}

// SyncRowByAgrirouterID reads the bookkeeping by canonical identifier, which is
// how a delivered object is matched when it carries no localId of ours.
//
// Platform-wide, like the mapping it reads. An object one tenant has already
// bound is found here for every other tenant too, which is the point: the
// second is told about a record the platform already holds and joins it rather
// than creating a duplicate agrirouter would then refuse to bind.
func (t *Tx) SyncRowByAgrirouterID(
	typ agmasync.EntityType, agrirouterID uuid.UUID,
) (SyncRow, error) {
	row := t.tx.QueryRow(`
		SELECT local_id, agrirouter_id, revision, unbound
		  FROM agmasync_object
		 WHERE entity_type = ? AND agrirouter_id = ?`,
		string(typ), agrirouterID.String())

	var localID string
	var rawID sql.NullString
	var revision sql.NullInt64
	var unbound int
	if err := row.Scan(&localID, &rawID, &revision, &unbound); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncRow{}, ErrNotFound
		}
		return SyncRow{}, fmt.Errorf("reading sync row: %w", err)
	}
	return toSyncRow(typ, localID, rawID, revision, unbound)
}

func toSyncRow(
	typ agmasync.EntityType, localID string, rawID sql.NullString,
	revision sql.NullInt64, unbound int,
) (SyncRow, error) {
	out := SyncRow{EntityType: typ, LocalID: localID, Unbound: unbound != 0}
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
	var rawID any
	if r.AgrirouterID != nil {
		rawID = r.AgrirouterID.String()
	}
	var tenantID any
	if r.TenantID != nil {
		tenantID = r.TenantID.String()
	}
	var revision any
	if r.Revision != nil {
		revision = *r.Revision
	}

	// Writing a pair clears the unbound marker: the platform holds the object
	// again, under this identifier, whether that is a rebinding of the same one
	// or a fresh record.
	_, err := t.tx.Exec(`
		INSERT INTO agmasync_object (entity_type, local_id, agrirouter_id, revision, agrirouter_tenant_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (entity_type, local_id) DO UPDATE SET
			agrirouter_id = excluded.agrirouter_id,
			revision      = excluded.revision,
			unbound       = 0,
			agrirouter_tenant_id = COALESCE(excluded.agrirouter_tenant_id, agmasync_object.agrirouter_tenant_id)`,
		string(r.EntityType), r.LocalID, rawID, revision, tenantID)
	if err != nil {
		return fmt.Errorf("writing sync row: %w", err)
	}
	return nil
}

// Unbind drops the correspondence while leaving the record itself alone.
//
// It is the local half of unbinding: the platform stops claiming to hold the
// canonical object. The record stays, because the platform may still want it —
// what has gone is the claim that it is the same thing agrirouter holds.
//
// The row is marked rather than deleted. A record with no row at all is one that
// has never been sent, which is sendable; this one is not, and nothing else
// would say so once the pair is gone.
func (t *Tx) Unbind(typ agmasync.EntityType, localID string) error {
	_, err := t.tx.Exec(`
		UPDATE agmasync_object
		   SET agrirouter_id = NULL, revision = NULL, unbound = 1
		 WHERE entity_type = ? AND local_id = ?`,
		string(typ), localID)
	if err != nil {
		return fmt.Errorf("unbinding: %w", err)
	}
	return nil
}

// DiscardTenant drops every pair whose canonical object belongs to the given
// agrirouter tenant, which is what a masterdata reset of it requires, and
// reports how many it dropped.
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

// Bindings lists every correspondence the platform holds, which is what a
// reconciliation confirmation carries in bulk.
//
// Platform-wide, because the mapping is: a pair another tenant produced is one
// agrirouter already holds for this participant, so carrying it on this
// endpoint's confirmation claims nothing new and is recorded idempotently.
func (t *Tx) Bindings() ([]SyncRow, error) {
	rows, err := t.tx.Query(`
		SELECT entity_type, local_id, agrirouter_id, revision
		  FROM agmasync_object
		 WHERE agrirouter_id IS NOT NULL
		 ORDER BY entity_type, local_id`)
	if err != nil {
		return nil, fmt.Errorf("listing bindings: %w", err)
	}
	defer rows.Close()

	var out []SyncRow
	for rows.Next() {
		var typ, localID string
		var rawID sql.NullString
		var revision sql.NullInt64
		if err := rows.Scan(&typ, &localID, &rawID, &revision); err != nil {
			return nil, fmt.Errorf("scanning binding: %w", err)
		}
		// A bound row is by definition not unbound.
		row, err := toSyncRow(agmasync.EntityType(typ), localID, rawID, revision, 0)
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
	var list string
	err := t.tx.QueryRow(`
		SELECT entity_types FROM agmasync_route WHERE endpoint_id = ?`,
		endpointID.String()).Scan(&list)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading routing: %w", err)
	}

	var out []agmasync.EntityType
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, agmasync.EntityType(name))
		}
	}
	return out, nil
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
