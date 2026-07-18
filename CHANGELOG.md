# Changelog

## v0.1.0 - 2026-07-18

### Added
- Add a Linux agent that collects CPU, memory, block-device, filesystem, network traffic, and IP address data every minute.
- Add an authenticated central HTTP service with transactional PostgreSQL ingestion, current-state queries, hourly rollups, and partition maintenance.
- Add the initial PostgreSQL monitoring schema, retention functions, validation SQL, Docker deployment, and least-privilege runtime roles.
- Add one-command Agent deployment that registers or rotates credentials, atomically installs the binary, configures watchdog startup, and verifies the first report.
- Add unit and race-tested report validation, collector parsing, HTTP authentication, retry behavior, and deployment documentation with a complete data-flow diagram.
