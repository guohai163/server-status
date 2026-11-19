package store

import (
	"context"
	"fmt"
	"time"
)

const networkUsageDays = 30

var networkUsageLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type NetworkUsageBytes struct {
	RXBytes int64 `json:"rx_bytes"`
	TXBytes int64 `json:"tx_bytes"`
}

type NetworkUsageDay struct {
	Date  string             `json:"date"`
	Usage *NetworkUsageBytes `json:"usage"`
}

type NodeNetworkUsage struct {
	Daily         []NetworkUsageDay  `json:"daily"`
	MonthStart    string             `json:"month_start"`
	MonthToDate   *NetworkUsageBytes `json:"month_to_date"`
	MonthDaysSeen int                `json:"month_days_seen"`
}

// GetNodeNetworkUsage sums the reported deltas for an interface, or for the whole node when interfaceID is empty.
func (store *Store) GetNodeNetworkUsage(ctx context.Context, nodeID, interfaceID string) (NodeNetworkUsage, error) {
	var exists bool
	if err := store.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM monitoring.nodes WHERE id = $1::uuid AND (
				$2 = '' OR EXISTS (
					SELECT 1 FROM monitoring.network_interfaces
					 WHERE node_id = $1::uuid AND id = NULLIF($2, '')::uuid
				)
			)
		)
	`, nodeID, interfaceID).Scan(&exists); err != nil {
		return NodeNetworkUsage{}, fmt.Errorf("check network usage node and interface: %w", err)
	}
	if !exists {
		return NodeNetworkUsage{}, ErrNotFound
	}

	now := time.Now().UTC()
	localNow := now.In(networkUsageLocation)
	year, month, day := localNow.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, networkUsageLocation)
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, networkUsageLocation)
	chartStart := today.AddDate(0, 0, 1-networkUsageDays)
	start := chartStart
	if monthStart.Before(start) {
		start = monthStart
	}
	// Raw partitions cover at least seven full UTC days plus today. Use raw samples
	// throughout that period to include the current hour and any late reports.
	rawStart := now.Truncate(24*time.Hour).AddDate(0, 0, -7)
	rows, err := store.pool.Query(ctx, nodeNetworkUsageSQL, nodeID, start.UTC(), rawStart, now, interfaceID)
	if err != nil {
		return NodeNetworkUsage{}, fmt.Errorf("query network usage: %w", err)
	}
	defer rows.Close()

	byDate := make(map[string]NetworkUsageBytes, 31)
	for rows.Next() {
		var date string
		var usage NetworkUsageBytes
		if err := rows.Scan(&date, &usage.RXBytes, &usage.TXBytes); err != nil {
			return NodeNetworkUsage{}, fmt.Errorf("scan network usage: %w", err)
		}
		byDate[date] = usage
	}
	if err := rows.Err(); err != nil {
		return NodeNetworkUsage{}, fmt.Errorf("iterate network usage: %w", err)
	}
	return summarizeNetworkUsage(chartStart, monthStart, today, byDate), nil
}

func summarizeNetworkUsage(chartStart, monthStart, today time.Time, byDate map[string]NetworkUsageBytes) NodeNetworkUsage {
	result := NodeNetworkUsage{
		Daily:      make([]NetworkUsageDay, 0, networkUsageDays),
		MonthStart: monthStart.Format(time.DateOnly),
	}
	for date := chartStart; !date.After(today); date = date.AddDate(0, 0, 1) {
		item := NetworkUsageDay{Date: date.Format(time.DateOnly)}
		if usage, ok := byDate[item.Date]; ok {
			item.Usage = &usage
		}
		result.Daily = append(result.Daily, item)
	}
	var monthTotal NetworkUsageBytes
	for date := monthStart; !date.After(today); date = date.AddDate(0, 0, 1) {
		if usage, ok := byDate[date.Format(time.DateOnly)]; ok {
			monthTotal.RXBytes += usage.RXBytes
			monthTotal.TXBytes += usage.TXBytes
			result.MonthDaysSeen++
		}
	}
	if result.MonthDaysSeen > 0 {
		result.MonthToDate = &monthTotal
	}
	return result
}

const nodeNetworkUsageSQL = `
	SELECT to_char(sample_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD'),
	       sum(rx_bytes)::bigint, sum(tx_bytes)::bigint
	  FROM (
		SELECT hour_at AS sample_at, rx_bytes, tx_bytes
		  FROM monitoring.network_metric_hourly
		 WHERE node_id = $1::uuid AND hour_at >= $2 AND hour_at < $3
		   AND ($5 = '' OR interface_id = NULLIF($5, '')::uuid)
		UNION ALL
		SELECT bucket_at, rx_bytes_delta, tx_bytes_delta
		  FROM monitoring.network_metric_samples
		 WHERE node_id = $1::uuid AND bucket_at >= $3 AND bucket_at < $4
		   AND ($5 = '' OR interface_id = NULLIF($5, '')::uuid)
	  ) traffic
	 GROUP BY 1
`
