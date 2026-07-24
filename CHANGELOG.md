# Changelog

## v0.15.1 - 2026-07-24

### Fixed
- Prefer an explicit SMART pass or fail result over a missing availability flag when displaying RAID passthrough health.

## v0.15.0 - 2026-07-24

### Added
- Collect Linux hwmon CPU, motherboard, GPU, and storage temperatures for node details.
- Collect SMART model, power-on hours, error counters, wear, and risk from direct disks and smartmontools-discovered RAID passthrough members.
- Persist current hardware health snapshots and show SMART availability instead of assuming missing data is healthy.

### Fixed
- Run Windows Agent downloads and install or upgrade actions in one PowerShell invocation so a single paste cannot stop after the silent download step.
- Read the Windows product name from the registry so modern Windows Server releases no longer fall back to a generic label.
- Preaggregate filesystem and network history instead of running correlated resource aggregates for every timeline point.

### Changed
- Replace the dashboard favicon with the server status brand icon.
- Downsample 24-hour node and GPU history to five-minute points while preserving minute points for shorter ranges and hourly rollups for longer ranges.
- Cache successful history responses for 30 seconds with request coalescing, ETag revalidation, gzip compression, and server timing headers.
- Render node details before their history request completes so slow charts no longer block current hardware and status data.

## v0.14.2 - 2026-07-23

### Fixed
- Replace deprecated BITSAdmin downloads in Windows install and update commands with a PowerShell WebClient command compatible with both Command Prompt and PowerShell.

## v0.14.1 - 2026-07-23

### Fixed
- Avoid PowerShell intercepting the Windows Agent update command's temporary-file cleanup redirection.

## v0.14.0 - 2026-07-23

### Added
- Add a Windows Agent update command to existing node details and preserve the installed node configuration during upgrades.

## v0.13.1 - 2026-07-23

### Fixed
- Quote the macOS Agent installation path when reporting the installed version.

## v0.13.0 - 2026-07-23

### Added
- Add a zero-dependency macOS 11+ `zsh` Agent with report v1 inventory fingerprints, CPU, memory, filesystem, and network collection.
- Install the macOS Agent as a root `LaunchDaemon` from the dashboard without an app bundle, code signing, or notarization.
- Publish and bundle the architecture-independent macOS Agent with the existing verified Release assets.
- Distinguish Apple Silicon Performance and Efficiency core counts in inventory, database queries, dashboard details, and Excel exports.
- Apply verified macOS script updates atomically from the central Release cache and restart through `launchd`.

## v0.12.0 - 2026-07-22

### Added
- Export all monitored machines to an Admin Token-protected Excel workbook with overview, CPU, memory, disk, filesystem, network, and GPU worksheets.
- Collect the SMBIOS product name and SKU for physical Linux servers and show the server model in node details.

### Fixed
- Deduplicate multiple mount points for the same filesystem UUID so inventory and metric reports do not contain duplicate filesystem keys.

## v0.11.0 - 2026-07-21

### Added
- Bundle matching-version Linux `amd64/arm64` and Windows `amd64` Agent binaries with unified SHA-256 checksums in every released central image.
- Verify the bundled Agent assets and executable version after publishing the multi-architecture central image.

### Changed
- Serve `latest` and the central image's fixed Agent version from verified read-only bundled assets, while retaining the persistent GitHub-backed cache for historical versions.
- Store the central image's writable Release cache under `/var/cache/server-status` instead of the size-limited `/tmp` filesystem.

### Fixed
- Prevent current-version Agent installation from returning 502 when the central container's temporary filesystem is full or GitHub is temporarily unreachable.

## v0.10.0 - 2026-07-21

### Added
- Add an operating-system filter to the node dashboard that combines with the existing Tag filter.

### Changed
- Build the Windows Agent with Go 1.20.14 for Windows Server 2008 R2 and later, using an isolated module and the official `x/sys/windows/svc` service lifecycle.
- Publish Windows Agent releases for `amd64`, matching the 64-bit-only Windows Server 2008 R2 baseline.

### Fixed
- Remove the Go 1.10 native service callback bridge that could leave the Agent indefinitely in `START_PENDING`.

## v0.8.0 - 2026-07-21

### Added
- Persist per-GPU minute metrics and hourly rollups, and show separate GPU utilization and framebuffer memory history charts in node details.
- Add up to five administrator-managed tags to every node card, with a top-level tag filter and an Admin Token-protected tag editor.

## v0.7.0 - 2026-07-21

### Added
- Collect NVIDIA GPU utilization and framebuffer memory usage through `nvidia-smi`, preserving separate current metrics for every GPU.
- Mark GPU-equipped nodes with the NVIDIA icon and show per-GPU utilization in node details.

## v0.6.0 - 2026-07-21

### Added
- Let administrators select the network interface whose IP identifies each node on dashboard cards and controls IP-based node ordering.
- Persist the selected interface with a PostgreSQL migration and expose an Admin Token-protected update endpoint.
- Remember a successfully authenticated Admin Token in the browser for 30 days, clearing it when it expires or is rejected.

### Changed
- Extend database verification to cover network preferences, cross-node constraints, and runtime role permissions.

## v0.5.1 - 2026-07-21

### Changed
- Show each node's current Agent version beside its colored availability indicator instead of repeating the textual online status.
- Reduce the physical-server icon size in dashboard card titles.

### Fixed
- Prefer an IPv4 address bound to a detected Linux bridge for node display and `/24` grouping, while preserving the existing address fallback for nodes without a bridge.

## v0.5.0 - 2026-07-20

### Added
- Let released central servers direct older Agents to download, verify, atomically install, and restart into the matching Release version without changing their Agent ID, Node Token, labels, or collection settings.
- Detect physical and virtual machines, expose CPU package thread topology, and mark physical servers in the dashboard with per-package thread counts.
- Group dashboard cards by IPv4 `/24` subnet and sort groups and nodes numerically by address.
- Follow the operating system light or dark color scheme across dashboard cards, controls, dialogs, tables, and history charts.

### Changed
- Embed the Release tag in central container images so update decisions use the exact published Agent version.

## v0.4.4 - 2026-07-20

### Added
- Proxy, verify, and persistently cache Agent Release assets on the central server so target nodes no longer need direct GitHub access.

## v0.4.2 - 2026-07-20

### Added
- Show operating system details, memory and disk capacities, and 1/5/15-minute load averages on node cards.
- Expose current load averages in the public node summary API.

## v0.4.1 - 2026-07-20

### Fixed
- Exclude read-only filesystem mounts from current and historical disk-usage aggregation so Snap squashfs images cannot force a node to 100% disk usage.

## v0.4.0 - 2026-07-20

### Added
- Add dashboard-based node creation with one-time install commands and a centrally hosted Agent installer.
- Install release binaries for Linux amd64/arm64 under `/opt/server-agent`, verify SHA-256 checksums, and configure an idempotent root crontab watchdog.

### Changed
- Mark registered nodes without reports as pending and prefer their configured display names in the dashboard.
- Make GitHub Release installation the default Agent deployment path without requiring Go, Python, a repository checkout, or SSH on the target node.
- Deploy the central service from the pinned GHCR image instead of building it from source on the target host.

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
