# Control Plane

Production-grade orchestration layer for Studojo: auth, job lifecycle, async coordination with backend workers (e.g. assignment-gen). Frontend talks only to the control plane.

## Overview

- **`GET /health`** — Liveness (no dependencies).
- **`GET /ready`** — Readiness (PostgreSQL + RabbitMQ reachable).
- **`POST /v1/jobs`** — Submit job (assignment-gen). Requires `Authorization: Bearer <JWT>`. Optional `Idempotency-Key`. Returns `202` + `job_id` or `200` on replay.
- **`GET /v1/jobs/:id`** — Poll job status. Requires JWT. Returns `job_id`, `status`, `result` / `error`.

Job states: `CREATED` → `QUEUED` → `RUNNING` → `COMPLETED` | `FAILED`. Results arrive via RabbitMQ; workers consume from `assignment-gen.jobs` and publish to `cp.results`.

## Requirements

- Go 1.25+
- PostgreSQL (schema `cp`, migrations applied on startup)
- RabbitMQ
- Frontend better-auth JWKS at `JWKS_URL` (e.g. `https://frontend-host/api/auth/jwks`)

## Environment Variables

| Variable        | Description                                      |
|-----------------|--------------------------------------------------|
| `DATABASE_URL`  | Postgres DSN (default: `postgresql://...`)       |
| `RABBITMQ_URL`  | AMQP URL (default: `amqp://guest:guest@...`)     |
| `JWKS_URL`      | Better-auth JWKS (default: `http://localhost:3000/api/auth/jwks`) |
| `HTTP_PORT`     | HTTP port (default: `8080`)                      |
| `CORS_ORIGINS`  | Comma-separated origins (default: `http://localhost:3000`) |

## Build and Run

```bash
go mod tidy
go build -o server ./cmd/server
./server
```

## Docker

From the service directory:

```bash
cd services/control-plane
docker build -t control-plane .
docker run -p 8080:8080 \
  -e DATABASE_URL=postgresql://studojo:studojo@host.docker.internal:5432/postgres \
  -e RABBITMQ_URL=amqp://guest:guest@host.docker.internal:5672/ \
  -e JWKS_URL=http://localhost:3000/api/auth/jwks \
  control-plane
```

## Example

Submit (after obtaining JWT from better-auth):

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: my-key-1" \
  -d '{"type":"assignment-gen","payload":{"assignment_type":"essay","description":"...","length_words":1500,"format_style":"APA"}}'
```

Poll:

```bash
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/v1/jobs/<job_id>
```

## Architecture

- **API** — HTTP, CORS, correlation ID, logging.
- **Auth** — JWT validation via JWKS; `user_id` in context.
- **Workflow** — Submit (idempotency, create job, enqueue), GetJob, HandleResult (from RabbitMQ consumer).
- **Store** — Jobs, state transitions, idempotency keys (PostgreSQL, schema `cp`).
- **Messaging** — Publish to `cp.jobs` (routing `job.<type>`), consume `control-plane.results` (`result.#`).

Workers consume `assignment-gen.jobs`, run assignment-gen, then publish result events to `cp.results`.
