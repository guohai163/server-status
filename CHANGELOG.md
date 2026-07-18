# Changelog

## v0.3.2 - 2026-07-18

### Fixed
- Pass the repository explicitly to GitHub CLI release commands so post-publish verification works without a checked-out working tree.

## v0.3.1 - 2026-07-18

### Added
- Add a post-publish job that verifies all Agent release assets and both required GHCR image platforms before the release workflow succeeds.

## v0.3.0 - 2026-07-18

### Added
- Add a tag-triggered GitHub Actions release workflow that publishes versioned static Agent binaries and checksums to GitHub Releases.
- Add multi-architecture central server image publishing to GHCR with semantic tags, SBOM, and provenance attestations.
- Add a reusable Agent release builder and a `--version` command with build-time tag injection.

### Changed
- Make the Dockerfile cross-compile for the requested Buildx target while preserving dependency-layer caching.
- Make local and one-command Agent builds use the same injected version and baseline amd64 instruction set as release builds.

## v0.2.0 - 2026-07-18

### Added
- Add a public responsive Web dashboard with node cards, full hardware details, and minute/hour history charts.
- Add public latest-state, node-detail, and ranged-history APIs without changing Agent or admin authentication.
- Add aggregate disk read/write collection, current rates, raw storage, and hourly rollups.

## v0.1.0 - 2026-07-18

### Added
- Add a Linux agent that collects CPU, memory, block-device, filesystem, network traffic, and IP address data every minute.
- Add an authenticated central HTTP service with transactional PostgreSQL ingestion, current-state queries, hourly rollups, and partition maintenance.
- Add the initial PostgreSQL monitoring schema, retention functions, validation SQL, Docker deployment, and least-privilege runtime roles.
- Add one-command Agent deployment that registers or rotates credentials, atomically installs the binary, configures watchdog startup, and verifies the first report.
- Add unit and race-tested report validation, collector parsing, HTTP authentication, retry behavior, and deployment documentation with a complete data-flow diagram.
