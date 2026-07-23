package server

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/store"
	"github.com/xuri/excelize/v2"
)

const spreadsheetContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

type nodeExportWorkbook struct {
	file        *excelize.File
	headerStyle int
	dateStyle   int
}

func buildNodeExportWorkbook(nodes []store.NodeDetail) ([]byte, error) {
	workbook := excelize.NewFile()
	defer workbook.Close()

	headerStyle, err := workbook.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"1F4E78"}},
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	if err != nil {
		return nil, fmt.Errorf("create export header style: %w", err)
	}
	dateFormat := "yyyy-mm-dd hh:mm:ss"
	dateStyle, err := workbook.NewStyle(&excelize.Style{CustomNumFmt: &dateFormat})
	if err != nil {
		return nil, fmt.Errorf("create export date style: %w", err)
	}
	export := nodeExportWorkbook{file: workbook, headerStyle: headerStyle, dateStyle: dateStyle}
	if err := export.writeSummary(nodes); err != nil {
		return nil, err
	}
	if err := export.writeCPU(nodes); err != nil {
		return nil, err
	}
	if err := export.writeMemory(nodes); err != nil {
		return nil, err
	}
	if err := export.writeDisks(nodes); err != nil {
		return nil, err
	}
	if err := export.writeFilesystems(nodes); err != nil {
		return nil, err
	}
	if err := export.writeNetwork(nodes); err != nil {
		return nil, err
	}
	if err := export.writeGPUs(nodes); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	if err := workbook.Write(&output); err != nil {
		return nil, fmt.Errorf("write export workbook: %w", err)
	}
	return output.Bytes(), nil
}

func (export nodeExportWorkbook) writeSummary(nodes []store.NodeDetail) error {
	const sheet = "机器概览"
	headers := []string{
		"节点 ID", "Agent ID", "显示名称", "主机名", "IP 地址", "状态", "机器类型", "服务器型号", "操作系统", "架构", "Tag", "自定义 Labels", "最近上报", "Agent 版本",
		"CPU 型号", "CPU 封装数", "CPU 物理核心", "CPU 逻辑线程", "内存总量 (Bytes)", "内存条数", "磁盘总量 (Bytes)", "磁盘数量", "GPU 数量",
		"CPU 使用率 (%)", "内存使用率 (%)", "磁盘使用率 (%)", "Load 1", "Load 5", "Load 15", "运行时间 (秒)", "网络接收 (Bytes/s)", "网络发送 (Bytes/s)",
	}
	rows := make([][]any, 0, len(nodes))
	for _, detail := range nodes {
		node := detail.Node
		rows = append(rows, []any{
			excelText(node.NodeID), excelText(node.AgentID), excelText(nodeDisplayName(node)), excelText(node.Hostname), excelText(node.PrimaryIP), excelText(node.Status), excelText(node.MachineType), excelText(node.SystemModel),
			excelText(strings.TrimSpace(strings.TrimSpace(node.OSName + " " + node.OSVersion))), excelText(node.Architecture), excelText(strings.Join(node.Tags, ", ")), excelText(formatLabels(node.Labels)), node.LastSeenAt,
			excelText(node.AgentVersion), excelText(strings.Join(node.CPUModels, "; ")), node.CPUPackageCount, node.CPUPhysicalCoreCount, node.CPULogicalThreadCount,
			node.MemoryTotalBytes, node.MemoryModuleCount, node.DiskTotalBytes, node.DiskCount, len(detail.GPUs), node.CPUUsagePercent, node.MemoryUsagePercent,
			node.DiskUsagePercent, node.Load1, node.Load5, node.Load15, node.UptimeSeconds, node.NetworkRXBytesPerSec, node.NetworkTXBytesPerSec,
		})
	}
	return export.writeSheet(sheet, headers, rows, 20, map[int]float64{1: 38, 2: 38, 3: 18, 4: 18, 5: 16, 8: 26, 9: 24, 12: 40, 13: 20, 15: 40})
}

func (export nodeExportWorkbook) writeCPU(nodes []store.NodeDetail) error {
	const sheet = "CPU"
	headers := []string{"显示名称", "主机名", "IP 地址", "封装序号", "厂商", "型号", "物理核心", "性能核", "能效核", "逻辑线程", "最高频率 (MHz)"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, cpu := range detail.CPUPackages {
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), cpu.PackageIndex, excelText(cpu.Vendor), excelText(cpu.ModelName), cpu.PhysicalCores, cpu.PerformanceCores, cpu.EfficiencyCores, cpu.LogicalThreads, cpu.MaxFrequencyMHz})
		}
	}
	return export.writeSheet(sheet, headers, rows, 20, map[int]float64{1: 18, 2: 18, 3: 16, 6: 44})
}

func (export nodeExportWorkbook) writeMemory(nodes []store.NodeDetail) error {
	const sheet = "内存"
	headers := []string{"显示名称", "主机名", "IP 地址", "插槽", "厂商", "型号", "料号", "序列号", "类型", "容量 (Bytes)", "速率 (MT/s)"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, memory := range detail.MemoryModules {
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), excelText(memory.SlotName), excelText(memory.Manufacturer), excelText(memory.ModelName), excelText(memory.PartNumber), excelText(memory.SerialNumber), excelText(memory.MemoryType), memory.SizeBytes, memory.SpeedMTs})
		}
	}
	return export.writeSheet(sheet, headers, rows, 18, map[int]float64{1: 18, 2: 18, 3: 16, 6: 28, 7: 20, 8: 20})
}

func (export nodeExportWorkbook) writeDisks(nodes []store.NodeDetail) error {
	const sheet = "磁盘"
	headers := []string{"显示名称", "主机名", "IP 地址", "设备", "类型", "厂商", "型号", "序列号", "WWN", "容量 (Bytes)", "机械盘"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, disk := range detail.BlockDevices {
			rotational := ""
			if disk.Rotational != nil {
				rotational = map[bool]string{true: "是", false: "否"}[*disk.Rotational]
			}
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), excelText(disk.DeviceName), excelText(disk.DeviceKind), excelText(disk.Vendor), excelText(disk.ModelName), excelText(disk.SerialNumber), excelText(disk.WWN), disk.SizeBytes, rotational})
		}
	}
	return export.writeSheet(sheet, headers, rows, 18, map[int]float64{1: 18, 2: 18, 3: 16, 4: 22, 7: 32, 8: 22, 9: 24})
}

func (export nodeExportWorkbook) writeFilesystems(nodes []store.NodeDetail) error {
	const sheet = "文件系统"
	headers := []string{"显示名称", "主机名", "IP 地址", "设备", "文件系统", "挂载点", "采集时间", "总量 (Bytes)", "已用 (Bytes)", "可用 (Bytes)", "使用率 (%)"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, filesystem := range detail.Filesystems {
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), excelText(filesystem.DeviceName), excelText(filesystem.FilesystemType), excelText(filesystem.MountPoint), timeValue(filesystem.BucketAt), filesystem.TotalBytes, filesystem.UsedBytes, filesystem.AvailableBytes, filesystem.UsedPercent})
		}
	}
	return export.writeSheet(sheet, headers, rows, 18, map[int]float64{1: 18, 2: 18, 3: 16, 4: 22, 6: 28, 7: 20})
}

func (export nodeExportWorkbook) writeNetwork(nodes []store.NodeDetail) error {
	const sheet = "网卡"
	headers := []string{"显示名称", "主机名", "IP 地址", "网卡", "首页 IP 网卡", "MAC 地址", "MTU", "链路速率 (Mbps)", "地址", "链路状态", "采集时间", "接收累计 (Bytes)", "发送累计 (Bytes)", "接收 (bits/s)", "发送 (bits/s)"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, network := range detail.Network {
			linkState := "断开"
			if network.LinkUp {
				linkState = "已连接"
			}
			primary := "否"
			if network.IsPrimary {
				primary = "是"
			}
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), excelText(network.Name), primary, excelText(network.MACAddress), network.MTU, network.LinkSpeedMbps, excelText(strings.Join(network.Addresses, ", ")), linkState, timeValue(network.BucketAt), network.RXBytesTotal, network.TXBytesTotal, network.RXBitsPerSecond, network.TXBitsPerSecond})
		}
	}
	return export.writeSheet(sheet, headers, rows, 18, map[int]float64{1: 18, 2: 18, 3: 16, 4: 16, 6: 20, 9: 36, 11: 20})
}

func (export nodeExportWorkbook) writeGPUs(nodes []store.NodeDetail) error {
	const sheet = "GPU"
	headers := []string{"显示名称", "主机名", "IP 地址", "索引", "UUID", "型号", "采集时间", "核心使用率 (%)", "显存总量 (Bytes)", "显存已用 (Bytes)", "显存使用率 (%)"}
	rows := make([][]any, 0)
	for _, detail := range nodes {
		for _, gpu := range detail.GPUs {
			rows = append(rows, []any{excelText(nodeDisplayName(detail.Node)), excelText(detail.Node.Hostname), excelText(detail.Node.PrimaryIP), gpu.Index, excelText(gpu.UUID), excelText(gpu.ModelName), timeValue(gpu.BucketAt), gpu.UtilizationPercent, gpu.MemoryTotalBytes, gpu.MemoryUsedBytes, gpu.MemoryUsagePercent})
		}
	}
	return export.writeSheet(sheet, headers, rows, 18, map[int]float64{1: 18, 2: 18, 3: 16, 5: 40, 6: 32, 7: 20})
}

func (export nodeExportWorkbook) writeSheet(name string, headers []string, rows [][]any, defaultWidth float64, widths map[int]float64) error {
	if name != "机器概览" {
		if _, err := export.file.NewSheet(name); err != nil {
			return fmt.Errorf("create %s worksheet: %w", name, err)
		}
	} else if err := export.file.SetSheetName("Sheet1", name); err != nil {
		return fmt.Errorf("name summary worksheet: %w", err)
	}
	for column, header := range headers {
		if err := export.setCell(name, column+1, 1, header); err != nil {
			return err
		}
		width := defaultWidth
		if configured, ok := widths[column+1]; ok {
			width = configured
		}
		columnName, _ := excelize.ColumnNumberToName(column + 1)
		if err := export.file.SetColWidth(name, columnName, columnName, width); err != nil {
			return fmt.Errorf("set %s column width: %w", name, err)
		}
	}
	lastColumn, _ := excelize.ColumnNumberToName(len(headers))
	if err := export.file.SetCellStyle(name, "A1", lastColumn+"1", export.headerStyle); err != nil {
		return fmt.Errorf("style %s header: %w", name, err)
	}
	if err := export.file.SetRowHeight(name, 1, 32); err != nil {
		return fmt.Errorf("set %s header height: %w", name, err)
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			if err := export.setCell(name, columnIndex+1, rowIndex+2, value); err != nil {
				return err
			}
		}
	}
	lastRow := len(rows) + 1
	if err := export.file.AutoFilter(name, fmt.Sprintf("A1:%s%d", lastColumn, lastRow), nil); err != nil {
		return fmt.Errorf("set %s filters: %w", name, err)
	}
	if err := export.file.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return fmt.Errorf("freeze %s header: %w", name, err)
	}
	return nil
}

func (export nodeExportWorkbook) setCell(sheet string, column, row int, value any) error {
	cell, err := excelize.CoordinatesToCellName(column, row)
	if err != nil {
		return err
	}
	if err := export.file.SetCellValue(sheet, cell, value); err != nil {
		return fmt.Errorf("set %s!%s: %w", sheet, cell, err)
	}
	if _, ok := value.(time.Time); ok {
		if err := export.file.SetCellStyle(sheet, cell, cell, export.dateStyle); err != nil {
			return fmt.Errorf("style %s!%s date: %w", sheet, cell, err)
		}
	}
	return nil
}

func nodeDisplayName(node store.NodeSummary) string {
	if node.DisplayName != "" {
		return node.DisplayName
	}
	return node.Hostname
}

func timeValue(value *time.Time) any {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC()
}

func excelText(value string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value[:1], "=+-@") {
		return "'" + value
	}
	return value
}

func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+labels[key])
	}
	return strings.Join(values, "; ")
}
