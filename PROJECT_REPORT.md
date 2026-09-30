# SellingBot Repository Report

## 1. Executive Summary

SellingBot is a Go-based automotive sales and WhatsApp automation platform. The application runs as a single backend service with a PostgreSQL database, an embedded web UI, and a WhatsApp worker that manages conversational flows, test-drive reminders, follow-ups, and lead/customer lifecycle automation.

From the startup flow and runtime configuration, the project is designed as a self-contained monolith: one Go binary, one Postgres data store, a persisted local data directory, and a Railway-friendly deployment model. The design emphasizes operational simplicity, persistence, and CRM-driven process automation rather than a microservice decomposition.

## 2. High-Level Architecture

### Application shape
- Language: Go
- Main entrypoint: [cmd/server/main.go](cmd/server/main.go)
- Module root: [internal](internal)
- DB: PostgreSQL via pgxpool
- Frontend: embedded static web assets served by the Go server
- Data persistence: local data directory on disk and database tables for core state
- Messaging automation: WhatsApp via whatsmeow
- Scheduling: background follow-up loop with Postgres-backed state

### Core runtime components
- [internal/config/config.go](internal/config/config.go): environment-driven config, default secret generation, and startup defaults
- [internal/db/db.go](internal/db/db.go): DB connect, migration execution, admin seeding
- [internal/auth/auth.go](internal/auth/auth.go): JWT generation/verification, login flow, role enforcement, account limiter
- [internal/api/router.go](internal/api/router.go): API server, middleware, health/metrics endpoints, route mounting
- [internal/whatsapp/worker.go](internal/whatsapp/worker.go): WhatsApp client lifecycle, session recovery, QR flow, state tracking
- [internal/scheduler/followups.go](internal/scheduler/followups.go): periodic reminder/follow-up processing
- [internal/images/store.go](internal/images/store.go): image upload validation and dedupe using file magic bytes + SHA-256

## 3. Startup and Boot Flow

The runtime flow is concentrated in [cmd/server/main.go](cmd/server/main.go) and is the best entry point for understanding how the app boots.

### Observed startup sequence
1. Load environment configuration and logger setup.
2. Resolve runtime directories and secrets.
3. Connect to PostgreSQL.
4. Retry DB connectivity if needed.
5. Run SQL migrations from [migrations](migrations).
6. Seed the default admin account if absent.
7. Initialize timezone and file storage.
8. Initialize image store and Web UI assets.
9. Start WhatsApp worker.
10. Start follow-up scheduler.
11. Start the HTTP server and attach API routes.
12. Wait for graceful shutdown via signal handling.

This shows a conventional “single-process orchestration” pattern: the main binary is the central coordinator for all subsystems.

## 4. Configuration and Security Defaults

The configuration layer in [internal/config/config.go](internal/config/config.go) is important because it defines the app’s default runtime assumptions.

### Configuration characteristics
- Reads environment variables instead of static config files.
- Generates a JWT secret and persists it under the data directory if not already set.
- Creates a random admin password if one is not supplied.
- Defaults the admin email to admin@autokart.local.
- Uses a default port and timezone, including Asia/Kuala_Lumpur behavior.
- Persists data in a DATA_DIR location that is reused by images, session storage, and other runtime artifacts.

### Security posture
- JWT-based auth using HS256-style signing logic.
- Password hashing via bcrypt.
- Login brute-force limiter to mitigate repeated credential guessing.
- Role-aware middleware and access checks for user-level segmentation.

## 5. Database Layer and Migration Model

The DB layer is centered in [internal/db/db.go](internal/db/db.go).

### Database responsibilities
- Creates the PostgreSQL pool with bounded concurrency and lifecycle settings.
- Executes schema migrations in order.
- Ensures the bootstrap admin user exists.
- Stores core operational state for customers, leads, vehicles, follow-ups, test drives, and WhatsApp session metadata.

### Migration structure
The app uses a sequential migration chain under [migrations](migrations):
- 01_init.sql
- 02_fullflow.sql
- 03_deepqa.sql
- 04_dedup.sql
- 05_settings.sql
- 06_sdas_stock.sql
- 07_payments_inspections_exchange.sql
- 08_vehicles_acquired_via.sql

This is a strong indicator that the app evolved through multiple phases and uses DB migrations as the formal upgrade path.

## 6. API Layer and HTTP Behavior

The API router in [internal/api/router.go](internal/api/router.go) is the central HTTP boundary. It wires middleware, health checks, metrics, and many protected CRUD handlers.

### API concerns
- JSON request/response handling
- User/session identification from JWT claims
- Access control for sales/admin flows
- Request validation and UUID/time parsing helpers
- Health endpoint checks for DB, disk, timezone, uptime, and WhatsApp status
- Metrics endpoint for runtime visibility and monitoring

### Key middleware behaviors
- Security headers
- Panic recovery
- IP-based request limiting
- Access logging
- Exemption of health/metrics from rate limiting to avoid interrupting Railway health checks

### Domain logic in API handlers
Large portions of domain behaviors are implemented in [internal/api/crud.go](internal/api/crud.go), including:
- customer creation + listing
- lead listing with sales-only filtering
- vehicle inventory listing and filtering
- vehicle lifecycle state transitions
- image attachment lookup for vehicles
- vehicle creation/patching and state validation

This confirms the app is not just a thin controller but an operational CRM/internal admin system with real workflow logic embedded in the API layer.

## 7. Authentication and Authorization Model

The auth package in [internal/auth/auth.go](internal/auth/auth.go) is the security boundary for the app.

### Observed auth flow
- Login requests are rate-limited by IP.
- User lookup and bcrypt password verification.
- JWT signed using server secret.
- Claims include user ID, email, and role.
- Auth middleware checks Authorization header and token validity.
- Role-based checks allow access restrictions like sales-only views.
- A Me endpoint returns the authenticated account for the frontend.

### Role pattern
The app is built around admin and sales-type roles, with logic that hides or scopes data by role. This is especially visible in lead listings and assignment rules.

## 8. WhatsApp Worker and Automation

The WhatsApp integration is in [internal/whatsapp/worker.go](internal/whatsapp/worker.go). This is one of the project’s most important operational subsystems.

### WhatsApp system responsibilities
- Maintains a WhatsApp client lifecycle with states such as disabled, qr, connecting, connected, logged_out, and expired
- Uses Postgres-backed state plus SQLite session storage stored in the local data directory
- Detects failure accumulation and flap cycles to avoid endless reconnect loops
- Handles QR code generation for login
- Serializes per-phone send operations to avoid overlapping messaging
- Provides sending cooldowns and dedupe safety controls
- Supports message processing logic and chat/session flow tracking

### Session design notes
The code explicitly mentions:
- “Session (device keys) lives in SQLite file on Railway volume.”
- Temporary ban logic for repeated failures
- Session expiry windows to avoid stale chat state
- Recovery paths for reconnection and worker resets

This strongly suggests the WhatsApp system is a production-grade CRM messaging layer, not a lightweight script.

## 9. Scheduler and Follow-Up Automation

The scheduler in [internal/scheduler/followups.go](internal/scheduler/followups.go) is responsible for recurring CRM automation.

### Scheduled flows
- Generic follow-up sending logic
- Test-drive reminder logic with 24h and 2h reminders
- Post-test-drive classification prompts
- Inspection reminders
- Outbox flush behavior

### Important design decisions
- Follow-ups are stored in Postgres rather than in-memory state.
- Claims are done atomically using SKIP LOCKED to prevent duplicate sends across overlapping ticks or worker restarts.
- Lost or stale sending states are recovered automatically.
- Messages are deduplicated by a unique follow-up type + message marker pattern.

This architecture is resilient and operationally safe: the app can restart without losing its scheduled reminder state.

## 10. Media and Asset Handling

The image storage logic in [internal/images/store.go](internal/images/store.go) is minimal but robust.

### Image handling behavior
- Creates a data directory for images under the main data folder
- Validates file type using magic bytes
- Accepts JPEG, PNG, and WEBP
- Deduplicates files by SHA-256 hash
- Reuses a stable filename format based on content hash to avoid duplicate uploads

This is aligned with a vehicle-listing / product-media use case where image content is more important than original filename.

## 11. Deployment Model and Operational Assumptions

The project’s deployment story is visible in [README.md](README.md), [railway.json](railway.json), and [railway.toml](railway.toml).

### Deployment profile
- Railway-friendly app deployment
- PostgreSQL-backed runtime
- Data volume mounted at a persistent directory
- Static frontend served from the Go backend
- Health checks and app startup assumptions designed for containerized hosting

### Operational assumptions
- App can run as a single service with DB + local persistence
- Admin and JWT state are persisted between restarts
- WhatsApp session state must survive restart in the data directory
- Worker/service restarts should not lose follow-up or CRM schedule state because those are persisted in Postgres

## 12. Notable Strengths

- Clear boot orchestration via [cmd/server/main.go](cmd/server/main.go)
- Strong use of migrations rather than ad-hoc schema updates
- Database-backed event and follow-up state reduces in-memory loss during restarts
- Auth layer includes basic brute-force protection and role enforcement
- WhatsApp integration is built with recovery and failure-state handling in mind
- Image storage and deduplication avoid noisy duplicate uploads

## 13. Risks and Observations

- The project is a single-service monolith, so service failures are more coupled than in a microservice design.
- The app relies heavily on one Postgres database plus a local data directory, so backup and restore discipline matters.
- WhatsApp sessions and worker state are operationally critical; any outage or stale session can directly affect messaging flows.
- The repo is broad and contains many business workflows beyond the core boot path, so a full narrative review requires systematic file-by-file traversal rather than a single pass.

## 14. Conclusion

SellingBot is best understood as a Go-based CRM + WhatsApp workflow engine for automotive sales. It combines a Postgres-backed business model, JWT/authenticated admin access, a WhatsApp messaging worker, reminder scheduling, and embedded web UI into a single deployable service.

The project’s architecture is coherent and intentional: the main binary orchestrates all runtime subsystems, the database stores durable state, and the background workers manage messaging automation with recovery logic built in. The most important runtime files are concentrated in the startup path, auth, DB migration, router, and WhatsApp worker layer.

## 15. Priority Files for Continued Review

If deeper review is needed, these files are the best next targets:
- [cmd/server/main.go](cmd/server/main.go)
- [internal/db/db.go](internal/db/db.go)
- [internal/api/router.go](internal/api/router.go)
- [internal/auth/auth.go](internal/auth/auth.go)
- [internal/whatsapp/worker.go](internal/whatsapp/worker.go)
- [internal/scheduler/followups.go](internal/scheduler/followups.go)
- [internal/api/crud.go](internal/api/crud.go)
- [README.md](README.md)
