# Silvercare Coordination

Silvercare Coordination is a production-oriented backend for district teams that coordinate welfare support for older residents. It is not a medical appointment system or a generic booking tool. Its business boundary starts with resident consent and eligibility, continues through support-plan approval, funded service authorization, provider allocation and visit evidence, and ends with subsidy review, support-resource custody, urgent escalation, and immutable audit.

## Business flows

The API supports these related paths:

1. A coordinator enrolls an older resident, records time-limited household consent, creates an evidence-backed eligibility assessment, and activates a support plan.
2. The district reserves budget for an authorization, matches an accredited provider, schedules a non-conflicting visit, records provider ownership and completion evidence, and reviews the resulting subsidy claim.
3. Coordinators assign reusable support resources to consenting residents and preserve custody through return and quarantine.
4. Provider staff can open urgent assistance escalations. Severity determines a deterministic acknowledgement deadline; coordinators own acknowledgement and resolution.
5. Every material transaction writes an audit event and an outbox event in the same database transaction. Background workers lease jobs and events, retry with bounded exponential backoff, and preserve permanent failures.

## Architecture

The repository follows dependency-oriented packages:

- `cmd/server`: configuration, signal lifecycle, HTTP server, service wiring, and workers.
- `internal/domain`: entity invariants and state machines without HTTP or SQL dependencies.
- `internal/auth`: password verification, opaque session issuance, expiry, revocation, and role principals.
- `internal/resident`, `eligibility`, `plan`, `benefit`, `provider`, `visit`, `resource`, `escalation`: business use cases and cross-entity transactions.
- `internal/store/sqlite`: migrations, real SQL repositories, optimistic writes, transaction ownership, leases, and restart recovery.
- `internal/httpapi`: typed routing, request IDs, JSON contracts, auth middleware, panic recovery, and stable errors.
- `internal/worker`: cancellable job and outbox loops with leases, retry, backoff, and dead states.
- `migrations`: ordered embedded SQL migrations.

The production data store is SQLite through `modernc.org/sqlite`. Foreign keys, WAL mode, and a busy timeout are enabled for every application connection. Main workflows never substitute an in-memory map for persistence.

## Data model

The initial migration creates these related business tables:

- `districts`: budget ownership and optimistic version.
- `users`, `sessions`: district-scoped identities and revocable server-side sessions.
- `residents`, `assessments`, `support_plans`: consent, eligibility evidence, and plan state.
- `providers`, `authorizations`, `visits`, `claims`: accreditation, funded entitlement, service delivery, and subsidy review.
- `resources`: reusable support-resource custody.
- `escalations`: deadline-bound urgent assistance ownership.
- `audit_events`: immutable actor, request, object, action, result, and detail records.
- `idempotency_records`: actor and operation-scoped command replay state.
- `outbox_events`, `worker_jobs`: restart-safe asynchronous delivery and work.

Migrations run in transactions and are recorded in `schema_migrations`. Startup validates historical budget, authorization, visit, resource, and session invariants. Conflicting historical rows block startup rather than being deleted or silently changed.

## Identity and roles

The server uses bcrypt password hashes and opaque random bearer tokens. Only token hashes are persisted. Sessions have a configured expiry, can be revoked at logout, and can be revoked in bulk for an account. Deactivated users cannot continue using an existing session.

Roles have distinct business permissions:

- `coordinator`: resident consent, assessments, plans, provider accreditation, authorizations, visit scheduling, resources, and escalation ownership.
- `provider`: visit acceptance and evidence, claim submission, resource returns, and escalation creation.
- `auditor`: claim review and immutable audit inspection.

The first coordinator can be created at startup through the bootstrap environment variables. Production deployments should set a unique password and disable bootstrap after provisioning.

## API

Health endpoints:

- `GET /health/live`
- `GET /health/ready`

Session endpoints:

- `POST /v1/sessions`
- `DELETE /v1/sessions/current`
- `GET /v1/me`

Core workflow endpoints:

- `POST|GET /v1/residents`
- `GET /v1/residents/{id}`
- `POST|DELETE /v1/residents/{id}/consent`
- `POST /v1/residents/{id}/assessments`
- `POST /v1/assessments/{id}/evidence|submit|approve`
- `POST /v1/residents/{id}/plans`
- `POST /v1/plans/{id}/goals|submit|activate`
- `POST /v1/providers` and `POST /v1/providers/{id}/activate`
- `POST /v1/plans/{id}/authorizations`
- `POST /v1/authorizations/{id}/activate`
- `POST /v1/visits` and visit ownership/lifecycle actions
- `POST /v1/visits/{id}/claims` and `POST /v1/claims/{id}/review`
- `POST /v1/resources` and resource assignment/return actions
- `POST /v1/escalations` and acknowledgement/resolution actions
- `GET /v1/audit/{type}/{id}`

Every response includes `X-Request-ID`. Errors use a stable JSON object:

```json
{
  "error": {
    "code": "conflict",
    "message": "the record changed or no longer exists",
    "request_id": "request_00000001"
  }
}
```

State-changing requests carry `expected_version` where optimistic ownership matters. Times use RFC3339; resident birth dates use `YYYY-MM-DD`.

## Local development

Requirements:

- Go matching the `go.mod` declaration, with `GOTOOLCHAIN=local`.
- Docker for the container gate.

Run the server:

```bash
cp .env.example .env
set -a
. ./.env
set +a
GOTOOLCHAIN=local go run ./cmd/server
```

The default development bootstrap account is `coordinator@example.test` with password `change-me-now` in district `district_demo`. Override it through environment variables outside local development.

Example login:

```bash
curl -sS http://127.0.0.1:8080/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{"district_id":"district_demo","email":"coordinator@example.test","password":"change-me-now"}'
```

## Verification

Run the complete native gate:

```bash
GOTOOLCHAIN=local go test ./... -count=1
GOTOOLCHAIN=local go test -race ./... -count=1
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...
```

Tests cover domain state machines, consent and evidence time boundaries, repository isolation, real migrations, foreign keys, transaction rollback, optimistic conflicts, concurrent budget reservations, database restart recovery, session expiry/revocation, outbox and worker leases, retry/cancellation, HTTP request IDs and error mapping, and an end-to-end HTTP/service/SQLite transaction.

Build and run the container:

```bash
docker build -t silvercare-coordination:local .
docker run --rm -p 8080:8080 -e SILVERCARE_BOOTSTRAP_PASSWORD=local-change-me silvercare-coordination:local
curl -fsS http://127.0.0.1:8080/health/ready
```

The final image contains the Go-built server and CA certificates, runs as an unprivileged user, persists its database under `/app/data`, and does not copy a host binary.
