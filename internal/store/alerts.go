package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const alertEvaluatorLockID int64 = 0x5353414c455254

const (
	AlertCategoryOffline     = "offline"
	AlertCategoryFilesystem  = "filesystem"
	AlertCategoryLoad        = "load"
	AlertCategorySMART       = "smart"
	AlertCategoryRAID        = "raid"
	AlertCategoryTemperature = "temperature"

	AlertSeverityWarning  = "warning"
	AlertSeverityCritical = "critical"
)

var alertCategories = map[string]struct{}{
	AlertCategoryOffline: {}, AlertCategoryFilesystem: {}, AlertCategoryLoad: {},
	AlertCategorySMART: {}, AlertCategoryRAID: {}, AlertCategoryTemperature: {},
}

var ErrInvalidAlertQuery = errors.New("invalid alert query")

type AlertEvent struct {
	AlertID         string         `json:"alert_id"`
	NodeID          string         `json:"node_id"`
	Hostname        string         `json:"hostname"`
	DisplayName     string         `json:"display_name,omitempty"`
	PrimaryIP       string         `json:"primary_ip,omitempty"`
	Category        string         `json:"category"`
	ResourceKey     string         `json:"resource_key"`
	Severity        string         `json:"severity"`
	State           string         `json:"state"`
	Title           string         `json:"title"`
	Details         map[string]any `json:"details"`
	OpenedAt        time.Time      `json:"opened_at"`
	LastObservedAt  time.Time      `json:"last_observed_at"`
	ResolvedAt      *time.Time     `json:"resolved_at,omitempty"`
	DurationSeconds int64          `json:"duration_seconds"`
}

type AlertSummary struct {
	OpenTotal     int64 `json:"open_total"`
	Critical      int64 `json:"critical"`
	Warning       int64 `json:"warning"`
	AffectedNodes int64 `json:"affected_nodes"`
}

type AlertListOptions struct {
	State    string
	Category string
	Severity string
	NodeID   string
	Since    *time.Time
	Limit    int
	Cursor   string
}

type AlertList struct {
	Alerts     []AlertEvent `json:"alerts"`
	Summary    AlertSummary `json:"summary"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type HomepageSummary struct {
	Normal  int `json:"normal"`
	Offline int `json:"offline"`
	Alerts  int `json:"alerts"`
}

func (store *Store) GetHomepageSummary(ctx context.Context) (HomepageSummary, error) {
	var summary HomepageSummary
	if err := store.pool.QueryRow(ctx, homepageSummarySQL).Scan(&summary.Normal, &summary.Offline, &summary.Alerts); err != nil {
		return HomepageSummary{}, fmt.Errorf("query Homepage summary: %w", err)
	}
	return summary, nil
}

const homepageSummarySQL = `
	WITH monitored_nodes AS (
		SELECT
			status.latest_bucket_at,
			status.status,
			EXISTS (
				SELECT 1
				  FROM monitoring.alert_events alert
				 WHERE alert.node_id = status.node_id
				   AND alert.resolved_at IS NULL
				   AND alert.category <> 'offline'
			) AS alerting
		  FROM monitoring.v_node_status status
	)
	SELECT
		count(*) FILTER (WHERE latest_bucket_at IS NOT NULL AND status = 'online' AND NOT alerting)::integer,
		count(*) FILTER (WHERE latest_bucket_at IS NOT NULL AND status = 'offline')::integer,
		count(*) FILTER (WHERE latest_bucket_at IS NOT NULL AND status = 'online' AND alerting)::integer
	  FROM monitored_nodes`

type alertCursor struct {
	OpenedAt time.Time `json:"opened_at"`
	AlertID  string    `json:"alert_id"`
}

type alertSignal struct {
	NodeID      string
	Category    string
	ResourceKey string
	Severity    string
	Title       string
	Details     map[string]any
	OpenedAt    time.Time
	ObservedAt  time.Time
}

type openAlert struct {
	ID          string
	NodeID      string
	Category    string
	ResourceKey string
	Severity    string
}

type alertNodeState struct {
	NodeID         string
	Hostname       string
	Status         string
	LastSeenAt     time.Time
	LatestBucketAt *time.Time
	Load5          float64
	LogicalThreads int
}

func ValidAlertCategory(value string) bool {
	_, ok := alertCategories[value]
	return ok
}

func ValidAlertSeverity(value string) bool {
	return value == AlertSeverityWarning || value == AlertSeverityCritical
}

func (store *Store) ReconcileAlerts(ctx context.Context, evaluatedAt time.Time) error {
	evaluatedAt = evaluatedAt.UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("begin alert reconciliation: %w", err)
	}
	defer tx.Rollback(ctx)

	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, alertEvaluatorLockID).Scan(&locked); err != nil {
		return fmt.Errorf("lock alert reconciliation: %w", err)
	}
	if !locked {
		return nil
	}

	openAlerts, err := loadOpenAlerts(ctx, tx)
	if err != nil {
		return err
	}
	nodes, err := loadAlertNodeStates(ctx, tx)
	if err != nil {
		return err
	}
	signals, err := collectAlertSignals(ctx, tx, nodes, openAlerts, evaluatedAt)
	if err != nil {
		return err
	}
	desired := make(map[string]alertSignal, len(signals))
	for _, signal := range signals {
		key := alertIdentity(signal.NodeID, signal.Category, signal.ResourceKey)
		desired[key] = signal
		if err := upsertAlertSignal(ctx, tx, signal); err != nil {
			return err
		}
	}
	for key, existing := range openAlerts {
		_, active := desired[key]
		node, exists := nodes[existing.NodeID]
		if !shouldResolveAlert(active, exists, node.Status, existing.Category) {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE monitoring.alert_events
			   SET resolved_at = $2, updated_at = $2
			 WHERE id = $1::uuid AND resolved_at IS NULL
		`, existing.ID, evaluatedAt); err != nil {
			return fmt.Errorf("resolve alert %s: %w", existing.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit alert reconciliation: %w", err)
	}
	return nil
}

func loadOpenAlerts(ctx context.Context, tx pgx.Tx) (map[string]openAlert, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, node_id::text, category, resource_key, severity
		  FROM monitoring.alert_events
		 WHERE resolved_at IS NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("query open alerts: %w", err)
	}
	defer rows.Close()
	result := make(map[string]openAlert)
	for rows.Next() {
		var item openAlert
		if err := rows.Scan(&item.ID, &item.NodeID, &item.Category, &item.ResourceKey, &item.Severity); err != nil {
			return nil, fmt.Errorf("scan open alert: %w", err)
		}
		result[alertIdentity(item.NodeID, item.Category, item.ResourceKey)] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open alerts: %w", err)
	}
	return result, nil
}

func loadAlertNodeStates(ctx context.Context, tx pgx.Tx) (map[string]alertNodeState, error) {
	rows, err := tx.Query(ctx, `
		SELECT status.node_id::text, status.hostname, status.status, status.last_seen_at,
		       status.latest_bucket_at, COALESCE(status.load_5, 0)::double precision,
		       hardware.cpu_logical_thread_count
		  FROM monitoring.v_node_status status
		  JOIN monitoring.v_node_hardware_summary hardware ON hardware.node_id = status.node_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query alert node states: %w", err)
	}
	defer rows.Close()
	result := make(map[string]alertNodeState)
	for rows.Next() {
		var item alertNodeState
		if err := rows.Scan(
			&item.NodeID, &item.Hostname, &item.Status, &item.LastSeenAt,
			&item.LatestBucketAt, &item.Load5, &item.LogicalThreads,
		); err != nil {
			return nil, fmt.Errorf("scan alert node state: %w", err)
		}
		if item.LatestBucketAt == nil && item.Status != "disabled" {
			item.Status = "pending"
		}
		result[item.NodeID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alert node states: %w", err)
	}
	return result, nil
}

func collectAlertSignals(ctx context.Context, tx pgx.Tx, nodes map[string]alertNodeState, openAlerts map[string]openAlert, evaluatedAt time.Time) ([]alertSignal, error) {
	signals := make([]alertSignal, 0)
	for _, node := range nodes {
		switch node.Status {
		case "offline":
			openedAt := node.LastSeenAt.Add(3 * time.Minute)
			observedAt := evaluatedAt
			if openedAt.After(observedAt) {
				observedAt = openedAt
			}
			signals = append(signals, alertSignal{
				NodeID: node.NodeID, Category: AlertCategoryOffline, ResourceKey: "node",
				Severity: AlertSeverityCritical, Title: "节点离线", OpenedAt: openedAt, ObservedAt: observedAt,
				Details: map[string]any{"resource_name": node.Hostname, "last_seen_at": node.LastSeenAt},
			})
		case "online":
			if node.LatestBucketAt != nil && node.LogicalThreads > 0 {
				normalized := node.Load5 / float64(node.LogicalThreads)
				key := alertIdentity(node.NodeID, AlertCategoryLoad, "node")
				if severity := thresholdAlertSeverity(normalized, 1.0, 1.5, 0.8, openAlerts[key].ID != ""); severity != "" {
					signals = append(signals, alertSignal{
						NodeID: node.NodeID, Category: AlertCategoryLoad, ResourceKey: "node", Severity: severity,
						Title: "系统负载过高", OpenedAt: *node.LatestBucketAt, ObservedAt: *node.LatestBucketAt,
						Details: map[string]any{"resource_name": "系统负载", "load_5": node.Load5, "logical_threads": node.LogicalThreads, "normalized_load": normalized},
					})
				}
			}
		}
	}

	filesystemSignals, err := collectFilesystemAlertSignals(ctx, tx, nodes, openAlerts)
	if err != nil {
		return nil, err
	}
	signals = append(signals, filesystemSignals...)
	smartSignals, err := collectSMARTAlertSignals(ctx, tx, nodes)
	if err != nil {
		return nil, err
	}
	signals = append(signals, smartSignals...)
	temperatureSignals, err := collectTemperatureAlertSignals(ctx, tx, nodes, openAlerts)
	if err != nil {
		return nil, err
	}
	signals = append(signals, temperatureSignals...)
	raidSignals, err := collectRAIDAlertSignals(ctx, tx, nodes)
	if err != nil {
		return nil, err
	}
	signals = append(signals, raidSignals...)
	return signals, nil
}

func collectFilesystemAlertSignals(ctx context.Context, tx pgx.Tx, nodes map[string]alertNodeState, openAlerts map[string]openAlert) ([]alertSignal, error) {
	rows, err := tx.Query(ctx, `
		SELECT metric.node_id::text, filesystem.id::text, filesystem.device_name,
		       filesystem.mount_point, metric.bucket_at, metric.total_bytes,
		       metric.available_bytes, metric.used_percent::double precision
		  FROM monitoring.filesystem_current_metrics metric
		  JOIN monitoring.filesystems filesystem
		    ON filesystem.node_id = metric.node_id AND filesystem.id = metric.filesystem_id
		 WHERE filesystem.removed_at IS NULL
		   AND NOT ('ro' = ANY(filesystem.mount_options))
	`)
	if err != nil {
		return nil, fmt.Errorf("query filesystem alert inputs: %w", err)
	}
	defer rows.Close()
	result := make([]alertSignal, 0)
	for rows.Next() {
		var nodeID, filesystemID, deviceName, mountPoint string
		var bucketAt time.Time
		var totalBytes, availableBytes int64
		var usedPercent float64
		if err := rows.Scan(&nodeID, &filesystemID, &deviceName, &mountPoint, &bucketAt, &totalBytes, &availableBytes, &usedPercent); err != nil {
			return nil, fmt.Errorf("scan filesystem alert input: %w", err)
		}
		if nodes[nodeID].Status != "online" {
			continue
		}
		key := alertIdentity(nodeID, AlertCategoryFilesystem, filesystemID)
		severity := thresholdAlertSeverity(usedPercent, 90, 95, 88, openAlerts[key].ID != "")
		if severity == "" {
			continue
		}
		result = append(result, alertSignal{
			NodeID: nodeID, Category: AlertCategoryFilesystem, ResourceKey: filesystemID,
			Severity: severity, Title: "文件系统容量不足", OpenedAt: bucketAt, ObservedAt: bucketAt,
			Details: map[string]any{"resource_name": mountPoint, "device_name": deviceName, "used_percent": usedPercent, "total_bytes": totalBytes, "available_bytes": availableBytes},
		})
	}
	return result, rows.Err()
}

func collectSMARTAlertSignals(ctx context.Context, tx pgx.Tx, nodes map[string]alertNodeState) ([]alertSignal, error) {
	rows, err := tx.Query(ctx, `
		SELECT health.node_id::text, device.id::text, health.bucket_at, device.device_name,
		       COALESCE(NULLIF(device.model_name, ''), NULLIF(device.vendor, ''), device.device_name),
		       COALESCE(device.serial_number, ''), health.smart_status, health.risk_level,
		       health.risk_reasons
		  FROM monitoring.storage_health_current_metrics health
		  JOIN monitoring.block_devices device
		    ON device.node_id = health.node_id AND device.id = health.block_device_id
		 WHERE device.removed_at IS NULL
		   AND health.risk_level IN ('warning', 'critical')
	`)
	if err != nil {
		return nil, fmt.Errorf("query SMART alert inputs: %w", err)
	}
	defer rows.Close()
	result := make([]alertSignal, 0)
	for rows.Next() {
		var nodeID, deviceID, deviceName, modelName, serialNumber, smartStatus, severity string
		var bucketAt time.Time
		var reasons []string
		if err := rows.Scan(&nodeID, &deviceID, &bucketAt, &deviceName, &modelName, &serialNumber, &smartStatus, &severity, &reasons); err != nil {
			return nil, fmt.Errorf("scan SMART alert input: %w", err)
		}
		if nodes[nodeID].Status != "online" {
			continue
		}
		result = append(result, alertSignal{
			NodeID: nodeID, Category: AlertCategorySMART, ResourceKey: deviceID,
			Severity: severity, Title: "SMART 状态异常", OpenedAt: bucketAt, ObservedAt: bucketAt,
			Details: map[string]any{"resource_name": modelName, "device_name": deviceName, "serial_number": serialNumber, "smart_status": smartStatus, "risk_reasons": reasons},
		})
	}
	return result, rows.Err()
}

func collectTemperatureAlertSignals(ctx context.Context, tx pgx.Tx, nodes map[string]alertNodeState, openAlerts map[string]openAlert) ([]alertSignal, error) {
	rows, err := tx.Query(ctx, `
		SELECT node_id::text, sensor_key, component, label, bucket_at,
		       temperature_celsius::double precision, high_celsius::double precision,
		       critical_celsius::double precision
		  FROM monitoring.temperature_current_metrics
	`)
	if err != nil {
		return nil, fmt.Errorf("query temperature alert inputs: %w", err)
	}
	defer rows.Close()
	result := make([]alertSignal, 0)
	for rows.Next() {
		var nodeID, sensorKey, component, label string
		var bucketAt time.Time
		var value float64
		var high, critical *float64
		if err := rows.Scan(&nodeID, &sensorKey, &component, &label, &bucketAt, &value, &high, &critical); err != nil {
			return nil, fmt.Errorf("scan temperature alert input: %w", err)
		}
		if nodes[nodeID].Status != "online" {
			continue
		}
		key := alertIdentity(nodeID, AlertCategoryTemperature, sensorKey)
		severity := temperatureAlertSeverity(value, high, critical, openAlerts[key].ID != "")
		if severity == "" {
			continue
		}
		result = append(result, alertSignal{
			NodeID: nodeID, Category: AlertCategoryTemperature, ResourceKey: sensorKey,
			Severity: severity, Title: "硬件温度过高", OpenedAt: bucketAt, ObservedAt: bucketAt,
			Details: map[string]any{"resource_name": label, "component": component, "temperature_celsius": value, "high_celsius": high, "critical_celsius": critical},
		})
	}
	return result, rows.Err()
}

func collectRAIDAlertSignals(ctx context.Context, tx pgx.Tx, nodes map[string]alertNodeState) ([]alertSignal, error) {
	result := make([]alertSignal, 0)
	controllerRows, err := tx.Query(ctx, `
		SELECT controller.node_id::text, controller.controller_id, controller.model,
		       controller.state, snapshot.bucket_at
		  FROM monitoring.raid_controller_current_metrics controller
		  JOIN monitoring.raid_status_current_snapshots snapshot ON snapshot.node_id = controller.node_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query RAID controller alert inputs: %w", err)
	}
	for controllerRows.Next() {
		var nodeID, model, state string
		var controllerID int
		var bucketAt time.Time
		if err := controllerRows.Scan(&nodeID, &controllerID, &model, &state, &bucketAt); err != nil {
			controllerRows.Close()
			return nil, fmt.Errorf("scan RAID controller alert input: %w", err)
		}
		if severity := raidAlertSeverity(state); nodes[nodeID].Status == "online" && severity != "" {
			result = append(result, alertSignal{
				NodeID: nodeID, Category: AlertCategoryRAID, ResourceKey: fmt.Sprintf("controller:%d", controllerID),
				Severity: severity, Title: "RAID 控制器异常", OpenedAt: bucketAt, ObservedAt: bucketAt,
				Details: map[string]any{"resource_name": fmt.Sprintf("控制器 %d · %s", controllerID, model), "component": "controller", "state": state, "controller_id": controllerID, "model": model},
			})
		}
	}
	if err := controllerRows.Err(); err != nil {
		controllerRows.Close()
		return nil, fmt.Errorf("iterate RAID controller alert inputs: %w", err)
	}
	controllerRows.Close()

	virtualRows, err := tx.Query(ctx, `
		SELECT drive.node_id::text, drive.controller_id, drive.drive_group_id,
		       drive.virtual_drive_id, drive.raid_level, drive.state, snapshot.bucket_at,
		       COALESCE(drive.name, '')
		  FROM monitoring.raid_virtual_drive_current_metrics drive
		  JOIN monitoring.raid_status_current_snapshots snapshot ON snapshot.node_id = drive.node_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query RAID virtual-drive alert inputs: %w", err)
	}
	for virtualRows.Next() {
		var nodeID, raidLevel, state, name string
		var controllerID, driveGroupID, virtualDriveID int
		var bucketAt time.Time
		if err := virtualRows.Scan(&nodeID, &controllerID, &driveGroupID, &virtualDriveID, &raidLevel, &state, &bucketAt, &name); err != nil {
			virtualRows.Close()
			return nil, fmt.Errorf("scan RAID virtual-drive alert input: %w", err)
		}
		if severity := raidAlertSeverity(state); nodes[nodeID].Status == "online" && severity != "" {
			resourceName := fmt.Sprintf("虚拟盘 %d/%d · %s", driveGroupID, virtualDriveID, raidLevel)
			if name != "" {
				resourceName = name + " · " + resourceName
			}
			result = append(result, alertSignal{
				NodeID: nodeID, Category: AlertCategoryRAID, ResourceKey: fmt.Sprintf("virtual:%d:%d:%d", controllerID, driveGroupID, virtualDriveID),
				Severity: severity, Title: "RAID 虚拟盘异常", OpenedAt: bucketAt, ObservedAt: bucketAt,
				Details: map[string]any{"resource_name": resourceName, "component": "virtual_drive", "state": state, "controller_id": controllerID, "drive_group_id": driveGroupID, "virtual_drive_id": virtualDriveID, "raid_level": raidLevel},
			})
		}
	}
	if err := virtualRows.Err(); err != nil {
		virtualRows.Close()
		return nil, fmt.Errorf("iterate RAID virtual-drive alert inputs: %w", err)
	}
	virtualRows.Close()

	physicalRows, err := tx.Query(ctx, `
		SELECT drive.node_id::text, drive.controller_id, drive.enclosure_id, drive.slot,
		       drive.device_id, drive.state, drive.model, drive.rebuild_progress::double precision,
		       snapshot.bucket_at
		  FROM monitoring.raid_physical_drive_current_metrics drive
		  JOIN monitoring.raid_status_current_snapshots snapshot ON snapshot.node_id = drive.node_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query RAID physical-drive alert inputs: %w", err)
	}
	defer physicalRows.Close()
	for physicalRows.Next() {
		var nodeID, state, model string
		var controllerID, enclosureID, slot, deviceID int
		var rebuildProgress *float64
		var bucketAt time.Time
		if err := physicalRows.Scan(&nodeID, &controllerID, &enclosureID, &slot, &deviceID, &state, &model, &rebuildProgress, &bucketAt); err != nil {
			return nil, fmt.Errorf("scan RAID physical-drive alert input: %w", err)
		}
		if severity := raidAlertSeverity(state); nodes[nodeID].Status == "online" && severity != "" {
			location := strconv.Itoa(slot)
			if enclosureID >= 0 {
				location = fmt.Sprintf("%d:%d", enclosureID, slot)
			}
			result = append(result, alertSignal{
				NodeID: nodeID, Category: AlertCategoryRAID, ResourceKey: fmt.Sprintf("physical:%d:%d:%d", controllerID, enclosureID, slot),
				Severity: severity, Title: "RAID 物理盘异常", OpenedAt: bucketAt, ObservedAt: bucketAt,
				Details: map[string]any{"resource_name": fmt.Sprintf("槽位 %s · %s", location, model), "component": "physical_drive", "state": state, "controller_id": controllerID, "enclosure_id": enclosureID, "slot": slot, "device_id": deviceID, "rebuild_progress": rebuildProgress},
			})
		}
	}
	return result, physicalRows.Err()
}

func upsertAlertSignal(ctx context.Context, tx pgx.Tx, signal alertSignal) error {
	details, err := json.Marshal(signal.Details)
	if err != nil {
		return fmt.Errorf("encode alert details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitoring.alert_events (
			node_id, category, resource_key, severity, title, details,
			opened_at, last_observed_at
		) VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7, $8)
		ON CONFLICT (node_id, category, resource_key) WHERE resolved_at IS NULL
		DO UPDATE SET
			severity = CASE
				WHEN monitoring.alert_events.severity = 'critical' OR EXCLUDED.severity = 'critical' THEN 'critical'
				ELSE 'warning'
			END,
			title = EXCLUDED.title,
			details = EXCLUDED.details,
			last_observed_at = GREATEST(monitoring.alert_events.last_observed_at, EXCLUDED.last_observed_at),
			updated_at = CURRENT_TIMESTAMP
	`, signal.NodeID, signal.Category, signal.ResourceKey, signal.Severity, signal.Title,
		details, signal.OpenedAt, signal.ObservedAt); err != nil {
		return fmt.Errorf("upsert %s alert for node %s: %w", signal.Category, signal.NodeID, err)
	}
	return nil
}

func thresholdAlertSeverity(value, warning, critical, recovery float64, alreadyOpen bool) string {
	if value >= critical {
		return AlertSeverityCritical
	}
	if value >= warning || alreadyOpen && value >= recovery {
		return AlertSeverityWarning
	}
	return ""
}

func temperatureAlertSeverity(value float64, high, critical *float64, alreadyOpen bool) string {
	if critical != nil && value >= *critical {
		return AlertSeverityCritical
	}
	if high != nil && value >= *high {
		return AlertSeverityWarning
	}
	if !alreadyOpen {
		return ""
	}
	recovery := high
	if recovery == nil {
		recovery = critical
	}
	if recovery != nil && value >= *recovery-3 {
		return AlertSeverityWarning
	}
	return ""
}

func raidAlertSeverity(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	switch normalized {
	case "offln", "ofln", "offline", "ubad", "bad", "failed", "failure", "msng", "missing", "dhs":
		return AlertSeverityCritical
	case "dgrd", "degraded", "pdgd", "partially degraded", "rbld", "rebuild", "rebuilding", "in progress", "needs attention":
		return AlertSeverityWarning
	default:
		return ""
	}
}

func shouldResolveAlert(active, nodeExists bool, nodeStatus, category string) bool {
	if active {
		return false
	}
	return !nodeExists || nodeStatus != "offline" || category == AlertCategoryOffline
}

func alertIdentity(nodeID, category, resourceKey string) string {
	return nodeID + "\x00" + category + "\x00" + resourceKey
}

func (store *Store) PruneResolvedAlerts(ctx context.Context, referenceAt time.Time) (int64, error) {
	var deleted int64
	if err := store.pool.QueryRow(ctx, `SELECT monitoring.prune_resolved_alert_events($1)`, referenceAt.UTC()).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("prune resolved alerts: %w", err)
	}
	return deleted, nil
}

func (store *Store) ListAlerts(ctx context.Context, options AlertListOptions) (AlertList, error) {
	if options.Limit <= 0 {
		options.Limit = 100
	}
	if options.Limit > 200 {
		options.Limit = 200
	}
	var cursor alertCursor
	if options.Cursor != "" {
		decoded, err := decodeAlertCursor(options.Cursor)
		if err != nil {
			return AlertList{}, err
		}
		cursor = decoded
	}

	where := make([]string, 0, 7)
	args := make([]any, 0, 8)
	add := func(format string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(format, len(args)))
	}
	switch options.State {
	case "open":
		where = append(where, "alert.resolved_at IS NULL")
	case "resolved":
		where = append(where, "alert.resolved_at IS NOT NULL")
	case "all":
	default:
		return AlertList{}, fmt.Errorf("%w: invalid state", ErrInvalidAlertQuery)
	}
	if options.Category != "" {
		add("alert.category = $%d", options.Category)
	}
	if options.Severity != "" {
		add("alert.severity = $%d", options.Severity)
	}
	if options.NodeID != "" {
		add("alert.node_id = $%d::uuid", options.NodeID)
	}
	if options.Since != nil && options.State != "open" {
		add("alert.opened_at >= $%d", options.Since.UTC())
	}
	if !cursor.OpenedAt.IsZero() {
		args = append(args, cursor.OpenedAt.UTC(), cursor.AlertID)
		where = append(where, fmt.Sprintf("(alert.opened_at, alert.id) < ($%d, $%d::uuid)", len(args)-1, len(args)))
	}
	args = append(args, options.Limit+1)
	query := alertListSQL + " WHERE " + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY alert.opened_at DESC, alert.id DESC LIMIT $%d", len(args))
	rows, err := store.pool.Query(ctx, query, args...)
	if err != nil {
		return AlertList{}, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]AlertEvent, 0, options.Limit+1)
	now := time.Now().UTC()
	for rows.Next() {
		var item AlertEvent
		var detailsJSON []byte
		if err := rows.Scan(
			&item.AlertID, &item.NodeID, &item.Hostname, &item.DisplayName, &item.PrimaryIP,
			&item.Category, &item.ResourceKey, &item.Severity, &item.Title, &detailsJSON,
			&item.OpenedAt, &item.LastObservedAt, &item.ResolvedAt,
		); err != nil {
			return AlertList{}, fmt.Errorf("scan alert: %w", err)
		}
		if err := json.Unmarshal(detailsJSON, &item.Details); err != nil {
			return AlertList{}, fmt.Errorf("decode alert details: %w", err)
		}
		item.State = "open"
		end := now
		if item.ResolvedAt != nil {
			item.State = "resolved"
			end = *item.ResolvedAt
		}
		item.DurationSeconds = max(0, int64(end.Sub(item.OpenedAt).Seconds()))
		alerts = append(alerts, item)
	}
	if err := rows.Err(); err != nil {
		return AlertList{}, fmt.Errorf("iterate alerts: %w", err)
	}
	hasMore := len(alerts) > options.Limit
	if hasMore {
		alerts = alerts[:options.Limit]
	}
	result := AlertList{Alerts: alerts}
	if hasMore && len(alerts) > 0 {
		last := alerts[len(alerts)-1]
		result.NextCursor = encodeAlertCursor(alertCursor{OpenedAt: last.OpenedAt, AlertID: last.AlertID})
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE severity = 'critical'),
		       count(*) FILTER (WHERE severity = 'warning'),
		       count(DISTINCT node_id)
		  FROM monitoring.alert_events
		 WHERE resolved_at IS NULL
	`).Scan(&result.Summary.OpenTotal, &result.Summary.Critical, &result.Summary.Warning, &result.Summary.AffectedNodes); err != nil {
		return AlertList{}, fmt.Errorf("query alert summary: %w", err)
	}
	return result, nil
}

const alertListSQL = `
	SELECT alert.id::text, alert.node_id::text, node.hostname,
	       COALESCE(node.display_name, ''),
	       COALESCE(CASE WHEN primary_address.is_preferred THEN primary_address.address END,
	                NULLIF(node.labels->>'server_status.primary_ip', ''), primary_address.address, ''),
	       alert.category, alert.resource_key, alert.severity, alert.title, alert.details,
	       alert.opened_at, alert.last_observed_at, alert.resolved_at
	  FROM monitoring.alert_events alert
	  JOIN monitoring.nodes node ON node.id = alert.node_id
	  LEFT JOIN monitoring.node_network_preferences network_preference
	    ON network_preference.node_id = node.id
	  LEFT JOIN LATERAL (
		SELECT host(network_address.address)::text AS address,
		       network_interface.id = network_preference.interface_id AS is_preferred
		  FROM monitoring.network_addresses network_address
		  JOIN monitoring.network_interfaces network_interface
		    ON network_interface.node_id = network_address.node_id
		   AND network_interface.id = network_address.interface_id
		 WHERE network_address.node_id = node.id
		   AND network_address.removed_at IS NULL
		   AND network_interface.removed_at IS NULL
		 ORDER BY CASE WHEN network_interface.id = network_preference.interface_id THEN 0 ELSE 1 END,
		          CASE network_address.address_scope WHEN 'global' THEN 0 WHEN 'private' THEN 1 WHEN 'link' THEN 2 ELSE 3 END,
		          family(network_address.address), network_address.address
		 LIMIT 1
	  ) primary_address ON true`

func encodeAlertCursor(cursor alertCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeAlertCursor(value string) (alertCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alertCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidAlertQuery)
	}
	var cursor alertCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.OpenedAt.IsZero() || !validUUIDText(cursor.AlertID) {
		return alertCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidAlertQuery)
	}
	return cursor, nil
}

func validUUIDText(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 16, 64); err != nil {
			return false
		}
	}
	return true
}
