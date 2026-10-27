-- The sample platform's database.
--
-- It is in two halves, and the split is the point of this file.
--
-- The first half is what a farm management system would have had anyway:
-- tables of organizations, persons, farms, fields and boundaries, keyed by the
-- platform's own identifiers, holding the attributes it actually models. None
-- of it knows that AgmaSync exists.
--
-- Everything in that half is keyed by local_id alone, because the platform is
-- one store: a local identifier names the same record wherever in the product
-- it is reached from. Which of the product's *tenants* holds a record — the
-- tenancies a user works in and switches between, each onboarded to agrirouter
-- as its own endpoint — is a separate question, answered by tenant_entity below
-- rather than by a column on the record. The word is agrirouter's: a delivered
-- object names the tenant it belongs to in `tenantId`, and this is that
-- partition. It is deliberately not called an organization, which in this
-- schema is the master-data entity beside it: a tenant is who is using the
-- product, an organization is a company the master data is about.
--
-- agrirouter keys the identifier mapping by the endpoint, so a record held by
-- two tenancies is known to agrirouter once per endpoint. That is the platform's
-- choice to make: it may keep a store per endpoint, or, as this one does, one
-- store behind all of them, sending the same local_id through each. The
-- bookkeeping then has a row per endpoint, and where two tenancies onboarded
-- into one agrirouter tenant hold a record, both rows name one canonical
-- object. A record held in two agrirouter tenants is still one record here,
-- bound to a separate canonical object in each.
--
-- The second half is the sync bookkeeping, in tables prefixed agmasync_. It is
-- separate because `revision` and the delivery position have nowhere to live in
-- the first half: they are agrirouter's counters, meaningless to the platform's
-- own features, and a product with an established schema cannot usually push a
-- foreign counter into its primary tables. The specification says as much —
-- where a participant's store has nowhere to put the revision, it keeps it
-- alongside the store rather than inside it.

-- ---------------------------------------------------------------- the platform

-- tenant is one of the product's own tenancies: what a user works in, and what
-- they switch between. Each is onboarded to agrirouter separately and therefore
-- has its own endpoint, which is what acts on a request, what a delivery is
-- addressed to in `recipient_endpoint_id`, and what the identifier mapping is
-- keyed by.
CREATE TABLE IF NOT EXISTS tenant (
    tenant_id            TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    -- The endpoint this tenant is onboarded as, as agrirouter knows it. A record
    -- synchronized through this tenant is sent by that endpoint, and bound in
    -- that endpoint's namespace.
    external_endpoint_id TEXT
);

-- organization and person are the platform's own two kinds of party. The
-- protocol has one party entity and states the party type in its
-- `_agmasync_details`; the
-- codec maps between the two shapes, and a local_id is unique across both
-- tables, the mapping knowing only the one type.
CREATE TABLE IF NOT EXISTS organization (
    local_id                    TEXT PRIMARY KEY,
    name                        TEXT NOT NULL,
    commercial_registry_number  TEXT,
    -- The party's addresses, as the JSON list the protocol carries them in. A
    -- list is replaced whole on write, so a platform that kept only a city
    -- would erase everything else in every address it sent.
    addresses                   TEXT,
    -- archived is the platform's own word for what the protocol calls inactive.
    -- The two are mapped at the edge rather than conflated: deactivation is a
    -- lifecycle transition of the canonical object, and how a product expresses
    -- it locally is its own business.
    archived                    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS person (
    local_id    TEXT PRIMARY KEY,
    -- The name the person is known by, kept as sent rather than assembled from
    -- the parts: the protocol requires it on every write, and an assembled one
    -- would overwrite whatever another participant called them.
    name        TEXT NOT NULL,
    title       TEXT,
    first_name  TEXT,
    last_name   TEXT,
    archived    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS farm (
    local_id        TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    -- The grower is stored as this platform's own identifier for the party. That
    -- is how a reference is held locally: the canonical identifier is sync
    -- bookkeeping and lives in agmasync_object.
    owner_local_id  TEXT,
    -- The farm's address as JSON, held whole because it is replaced whole.
    address         TEXT,
    archived        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS field (
    local_id       TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    area           REAL,
    farm_local_id  TEXT,
    archived       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS field_boundary (
    local_id         TEXT PRIMARY KEY,
    field_local_id   TEXT,
    name             TEXT,
    boundary_type    TEXT,
    creation_method  TEXT,
    -- WKT, in the canonical form the protocol requires.
    geometry         TEXT,
    archived         INTEGER NOT NULL DEFAULT 0
);

-- tenant_entity says which of the product's tenants hold a record. It is the
-- only place the tenant appears in the first half.
--
-- A record may be held by more than one, which is the ordinary case this table
-- exists for: the same farm appearing in two of a user's tenancies is one record
-- here, listed twice. Under a schema that put tenant_id in the record's primary
-- key it would have been two records for one farm. In agrirouter it is one
-- canonical object per agrirouter tenant, bound once per endpoint, each with
-- its own row in agmasync_object.
--
-- Membership is the platform's own bookkeeping and is exchanged with nobody. It
-- lines up with agrirouter's `recipient_endpoint_id` because each tenancy is
-- onboarded as its own endpoint, which is what lets a delivery be routed to one
-- of them.
CREATE TABLE IF NOT EXISTS tenant_entity (
    tenant_id   TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    local_id    TEXT NOT NULL,
    PRIMARY KEY (tenant_id, entity_type, local_id)
);

-- The reverse lookup: which tenants hold this record. It answers whether the
-- last tenant holding a record has just let go of it, which is when the record
-- itself can be deleted.
CREATE INDEX IF NOT EXISTS tenant_entity_by_record
    ON tenant_entity (entity_type, local_id);

-- ------------------------------------------------------------ the bookkeeping

-- agmasync_object is the correspondence table: what agrirouter calls each of
-- this platform's records, and which revision of it the platform holds.
--
-- A row exists only for an object the platform has bound. agrirouter does not
-- serve the mapping back, so this table is the platform's own to keep and to
-- restore with the rest of its data — losing it means losing the ability to
-- send, since an ordinary write resolves through a localId already mapped.
--
-- It is keyed by (agrirouter_tenant_id, external_endpoint_id, entity_type,
-- local_id), because that is what agrirouter keys it by: a local_id names a
-- record in the namespace of the endpoint that sent it, and an external id
-- names an endpoint within its tenant. Each endpoint binds for itself, so two
-- tenancies onboarded into one agrirouter tenant hold a pair each for a record
-- they share, both naming one canonical object.
--
-- Rows outlive the endpoint they belong to. agrirouter discards a removed
-- endpoint's mapping; this table keeps it, so that an endpoint onboarded again
-- under the same external id offers the pairs back on its initial-load
-- confirmation and re-binds without asking its user.
CREATE TABLE IF NOT EXISTS agmasync_object (
    -- agrirouter_tenant_id is the agrirouter tenant the canonical object
    -- belongs to, as its `tenantId` said. A masterdata reset, which is per
    -- tenant, discards by it.
    agrirouter_tenant_id TEXT NOT NULL,
    -- external_endpoint_id is the endpoint whose pair this is, by the
    -- identifier this platform gave it.
    external_endpoint_id TEXT NOT NULL,
    entity_type   TEXT NOT NULL,
    local_id      TEXT NOT NULL,
    agrirouter_id TEXT,
    -- revision is the last revision of the canonical object this platform has
    -- durably applied. It guards every apply: an object whose revision is lower
    -- than this one must not be applied over it, which is what keeps a write
    -- response processed after a later stream frame from undoing it.
    revision      INTEGER,
    -- unbound records that agrirouter refused the binding this platform
    -- offered for the record. The pair itself is gone, so it cannot be read out
    -- of agrirouter_id: what remains is a record the platform must not send.
    -- An unbound send does not resolve against the mapping and would mint a
    -- second canonical object for an entity that already has one, so the row
    -- outlives the pair for exactly as long as the record does.
    unbound       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (agrirouter_tenant_id, external_endpoint_id, entity_type, local_id)
);

-- One canonical object is known by at most one local identifier, and the other
-- way round. The constraint is agrirouter's `409`, decided per endpoint, so it
-- is enforced here at the same scope: a mistaken binding then fails locally
-- rather than becoming a `409` nobody expected. Two endpoints binding the same
-- object is not a conflict.
CREATE UNIQUE INDEX IF NOT EXISTS agmasync_object_canonical
    ON agmasync_object (agrirouter_tenant_id, external_endpoint_id, entity_type, agrirouter_id)
    WHERE agrirouter_id IS NOT NULL;

-- The reverse lookup across endpoints: which of this platform's records an
-- object is, whichever endpoint bound it. It is how a sibling endpoint
-- recognises an object the store already holds.
CREATE INDEX IF NOT EXISTS agmasync_object_by_canonical
    ON agmasync_object (entity_type, agrirouter_id);

-- agmasync_stream holds the delivery position, and there is exactly one.
--
-- The position belongs to the application rather than to an endpoint or an
-- entity, which is why it is a single row rather than a column somewhere. One
-- stream carries every tenant's deliveries, and each frame names the tenant its
-- object belongs to in the envelope. It is written in the same transaction
-- as the objects it covers: the specification requires it to be derived from
-- what has been *durably applied*, so advancing it separately would lose data on
-- any crash in between.
CREATE TABLE IF NOT EXISTS agmasync_stream (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    last_event_id TEXT NOT NULL
);

-- agmasync_route holds what the user has routed each of this platform's
-- endpoints to exchange, as the last ROUTE_CHANGED frame stated it.
--
-- It has to be kept for the same reason the position does. The frame states the
-- whole routing rather than a delta, and catch-up restates it only for
-- endpoints whose routing changed above the participant's position — so a
-- participant that took the position and forgot the routing has no way to ask
-- for it again. It is also not the same thing as the endpoint's declaration,
-- which is a superset: loading against the declaration offers agrirouter back
-- types nobody asked for.
--
-- Per endpoint, unlike the position: routing is a decision made about one
-- endpoint in one tenant, while one stream carries them all.
CREATE TABLE IF NOT EXISTS agmasync_route (
    endpoint_id  TEXT PRIMARY KEY,
    -- The routed entity types, comma-separated in their wire spelling. A list
    -- in a column is the sample keeping its schema readable; nothing reads it
    -- but the platform itself.
    entity_types TEXT NOT NULL
);
