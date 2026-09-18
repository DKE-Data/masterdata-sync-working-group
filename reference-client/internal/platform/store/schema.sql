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
-- The shape follows the identifier mapping. agrirouter keys the mapping by the
-- participant rather than by the endpoint, so the same local_id sent through two
-- of the product's endpoints resolves to one canonical object — which is only
-- coherent if it is one record here too. Partitioning the tables by tenant would
-- put two rows behind one canonical object and leave the platform unable to say
-- which of them agrirouter is talking about.
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
-- has its own endpoint, which is what acts on a request. A delivery is not
-- addressed to it: the stream is the application's, and the tenant is read off
-- the envelope. It is not what the identifier mapping is keyed by either.
CREATE TABLE IF NOT EXISTS tenant (
    tenant_id            TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    -- The endpoint this tenant is onboarded as, as agrirouter knows it. A record
    -- synchronized through this tenant is sent by that endpoint, and bound in
    -- the application's namespace rather than in that endpoint's.
    external_endpoint_id TEXT
);

CREATE TABLE IF NOT EXISTS organization (
    local_id                    TEXT PRIMARY KEY,
    name                        TEXT NOT NULL,
    commercial_registry_number  TEXT,
    city                        TEXT,
    country                     TEXT,
    -- archived is the platform's own word for what the protocol calls inactive.
    -- The two are mapped at the edge rather than conflated: deactivation is a
    -- lifecycle transition of the canonical object, and how a product expresses
    -- it locally is its own business.
    archived                    INTEGER NOT NULL DEFAULT 0,
    -- unmodelled holds the attributes this platform does not understand, as
    -- JSON. The specification requires a participant to preserve what it does
    -- not model and relay it unchanged, so dropping them would corrupt the
    -- exchange for every other participant that does model them.
    unmodelled                  TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS person (
    local_id    TEXT PRIMARY KEY,
    last_name   TEXT NOT NULL,
    first_name  TEXT,
    title       TEXT,
    archived    INTEGER NOT NULL DEFAULT 0,
    unmodelled  TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS farm (
    local_id        TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    -- The owner is stored as this platform's own identifier for the party, plus
    -- which kind of party it is. That is how a reference is held locally: the
    -- canonical identifier is sync bookkeeping and lives in agmasync_object.
    owner_type      TEXT,
    owner_local_id  TEXT,
    city            TEXT,
    archived        INTEGER NOT NULL DEFAULT 0,
    unmodelled      TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS field (
    local_id       TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    area           REAL,
    farm_local_id  TEXT,
    archived       INTEGER NOT NULL DEFAULT 0,
    unmodelled     TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS field_boundary (
    local_id         TEXT PRIMARY KEY,
    boundary_type    TEXT,
    creation_method  TEXT,
    boundary         TEXT,
    archived         INTEGER NOT NULL DEFAULT 0,
    unmodelled       TEXT NOT NULL DEFAULT '{}'
);

-- tenant_entity says which of the product's tenants hold a record. It is the
-- only place the tenant appears in the first half.
--
-- A record may be held by more than one, which is the ordinary case this table
-- exists for: the same farm appearing in two of a user's tenancies is one record
-- here and one canonical object in agrirouter, listed twice. Under a schema that
-- put tenant_id in the record's primary key it would have been two rows fighting
-- over one binding.
--
-- Membership is the platform's own bookkeeping and is exchanged with nobody. It
-- lines up with agrirouter's `tenantId` because each tenancy is onboarded as its
-- own endpoint in its own tenant, which is what lets a delivery be routed to one
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
-- It is keyed by (entity_type, local_id) and by nothing else, because that is
-- what agrirouter keys it by: the mapping belongs to the participant, so one
-- pair covers the record however many of the product's tenants hold it and
-- whichever of its endpoints sends it. A tenant_id in this key would be a
-- second, finer namespace the far end does not have, and every tenant after the
-- first would bind an identifier agrirouter has already given to the first.
CREATE TABLE IF NOT EXISTS agmasync_object (
    entity_type   TEXT NOT NULL,
    local_id      TEXT NOT NULL,
    agrirouter_id TEXT,
    -- revision is the last revision of the canonical object this platform has
    -- durably applied. It guards every apply: an object whose revision is lower
    -- than this one must not be applied over it, which is what keeps a write
    -- response processed after a later stream frame from undoing it.
    revision      INTEGER,
    -- unbound records that this platform once held the object and has told
    -- agrirouter it no longer does. The pair itself is gone, so it cannot be
    -- read out of agrirouter_id: what remains is a record the platform must not
    -- send. An unbound send does not resolve against the mapping and would mint
    -- a second canonical object for an entity that already has one, so the row
    -- outlives the pair for exactly as long as the record does.
    unbound       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (entity_type, local_id)
);

-- One canonical object is known by at most one local identifier, and the other
-- way round. The constraint is agrirouter's `409`, decided per participant, so
-- it is enforced here at the same scope: a mistaken binding then fails locally
-- rather than becoming a `409` nobody expected.
CREATE UNIQUE INDEX IF NOT EXISTS agmasync_object_canonical
    ON agmasync_object (entity_type, agrirouter_id)
    WHERE agrirouter_id IS NOT NULL;

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
