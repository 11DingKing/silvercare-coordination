CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS districts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    timezone TEXT NOT NULL,
    monthly_budget_cents INTEGER NOT NULL CHECK (monthly_budget_cents >= 0),
    reserved_budget_cents INTEGER NOT NULL DEFAULT 0 CHECK (reserved_budget_cents >= 0),
    settled_budget_cents INTEGER NOT NULL DEFAULT 0 CHECK (settled_budget_cents >= 0),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (reserved_budget_cents + settled_budget_cents <= monthly_budget_cents)
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('coordinator','provider','auditor')),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (district_id, email)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, expires_at);

CREATE TABLE IF NOT EXISTS residents (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    external_ref TEXT NOT NULL,
    full_name TEXT NOT NULL,
    birth_date TEXT NOT NULL,
    household_id TEXT NOT NULL,
    consent_status TEXT NOT NULL CHECK (consent_status IN ('pending','granted','withdrawn')),
    consent_expires_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (district_id, external_ref)
);
CREATE INDEX IF NOT EXISTS idx_residents_household ON residents(district_id, household_id);

CREATE TABLE IF NOT EXISTS assessments (
    id TEXT PRIMARY KEY,
    resident_id TEXT NOT NULL REFERENCES residents(id),
    assessor_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL CHECK (status IN ('draft','submitted','approved','expired','superseded')),
    support_level INTEGER NOT NULL CHECK (support_level BETWEEN 1 AND 5),
    evidence_json TEXT NOT NULL,
    valid_until TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_assessments_resident ON assessments(resident_id, status, valid_until);

CREATE TABLE IF NOT EXISTS support_plans (
    id TEXT PRIMARY KEY,
    resident_id TEXT NOT NULL REFERENCES residents(id),
    assessment_id TEXT NOT NULL REFERENCES assessments(id),
    coordinator_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL CHECK (status IN ('draft','review','active','suspended','completed','cancelled')),
    starts_at TEXT NOT NULL,
    ends_at TEXT NOT NULL,
    goals_json TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS idx_plans_resident ON support_plans(resident_id, status);

CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    name TEXT NOT NULL,
    accreditation_status TEXT NOT NULL CHECK (accreditation_status IN ('pending','active','suspended','expired')),
    capabilities_json TEXT NOT NULL,
    capacity_per_day INTEGER NOT NULL CHECK (capacity_per_day > 0),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (district_id, name)
);

CREATE TABLE IF NOT EXISTS authorizations (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES support_plans(id),
    district_id TEXT NOT NULL REFERENCES districts(id),
    service_code TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('reserved','active','exhausted','cancelled','expired')),
    units_authorized INTEGER NOT NULL CHECK (units_authorized > 0),
    units_consumed INTEGER NOT NULL DEFAULT 0 CHECK (units_consumed >= 0),
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents > 0),
    reserved_cents INTEGER NOT NULL CHECK (reserved_cents >= 0),
    starts_at TEXT NOT NULL,
    ends_at TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (units_consumed <= units_authorized),
    CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS idx_authorizations_plan ON authorizations(plan_id, status);

CREATE TABLE IF NOT EXISTS visits (
    id TEXT PRIMARY KEY,
    authorization_id TEXT NOT NULL REFERENCES authorizations(id),
    provider_id TEXT NOT NULL REFERENCES providers(id),
    assigned_user_id TEXT REFERENCES users(id),
    resident_id TEXT NOT NULL REFERENCES residents(id),
    status TEXT NOT NULL CHECK (status IN ('scheduled','accepted','enroute','checked_in','completed','cancelled','disputed')),
    scheduled_start TEXT NOT NULL,
    scheduled_end TEXT NOT NULL,
    checked_in_at TEXT,
    completed_at TEXT,
    evidence_json TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (scheduled_end > scheduled_start)
);
CREATE INDEX IF NOT EXISTS idx_visits_provider_window ON visits(provider_id, scheduled_start, scheduled_end, status);
CREATE INDEX IF NOT EXISTS idx_visits_resident ON visits(resident_id, scheduled_start);

CREATE TABLE IF NOT EXISTS claims (
    id TEXT PRIMARY KEY,
    visit_id TEXT NOT NULL UNIQUE REFERENCES visits(id),
    district_id TEXT NOT NULL REFERENCES districts(id),
    status TEXT NOT NULL CHECK (status IN ('draft','submitted','approved','rejected','paid','held')),
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    reviewer_id TEXT REFERENCES users(id),
    rejection_reason TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_claims_district_status ON claims(district_id, status, created_at);

CREATE TABLE IF NOT EXISTS resources (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    resource_type TEXT NOT NULL,
    serial_number TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('available','assigned','maintenance','quarantine','retired')),
    assigned_resident_id TEXT REFERENCES residents(id),
    assigned_at TEXT,
    due_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (district_id, serial_number)
);
CREATE INDEX IF NOT EXISTS idx_resources_available ON resources(district_id, resource_type, status);

CREATE TABLE IF NOT EXISTS escalations (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    resident_id TEXT NOT NULL REFERENCES residents(id),
    visit_id TEXT REFERENCES visits(id),
    severity TEXT NOT NULL CHECK (severity IN ('standard','urgent','critical')),
    status TEXT NOT NULL CHECK (status IN ('open','acknowledged','resolved','expired')),
    summary TEXT NOT NULL,
    acknowledgement_due_at TEXT NOT NULL,
    acknowledged_by TEXT REFERENCES users(id),
    acknowledged_at TEXT,
    resolved_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_escalations_due ON escalations(status, acknowledgement_due_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    district_id TEXT NOT NULL REFERENCES districts(id),
    actor_id TEXT,
    request_id TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    detail_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_object ON audit_events(district_id, object_type, object_id, created_at);

CREATE TABLE IF NOT EXISTS idempotency_records (
    district_id TEXT NOT NULL REFERENCES districts(id),
    actor_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_code INTEGER,
    response_json TEXT,
    state TEXT NOT NULL CHECK (state IN ('started','completed','failed')),
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (district_id, actor_id, operation, idempotency_key)
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    topic TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','processing','delivered','dead')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TEXT NOT NULL,
    locked_by TEXT,
    locked_until TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_outbox_available ON outbox_events(state, available_at, locked_until);

CREATE TABLE IF NOT EXISTS worker_jobs (
    id TEXT PRIMARY KEY,
    district_id TEXT NOT NULL REFERENCES districts(id),
    kind TEXT NOT NULL,
    object_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','running','succeeded','retry','dead')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    available_at TEXT NOT NULL,
    locked_by TEXT,
    locked_until TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_worker_jobs_available ON worker_jobs(state, available_at, locked_until);
