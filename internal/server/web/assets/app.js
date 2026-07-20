(function () {
  "use strict";

  const app = document.getElementById("app");
  const refreshButton = document.getElementById("refresh-button");
  const updatedAt = document.getElementById("updated-at");
  const addNodeButton = document.getElementById("add-node-button");
  const addNodeDialog = document.getElementById("add-node-dialog");
  const addNodeForm = document.getElementById("add-node-form");
  const addNodeError = document.getElementById("add-node-error");
  const createNodeButton = document.getElementById("create-node-button");
  const installCommandResult = document.getElementById("install-command-result");
  const installCommand = document.getElementById("install-command");
  const copyInstallCommand = document.getElementById("copy-install-command");
  const ranges = ["1h", "6h", "24h", "7d", "30d", "90d"];
  let nodes = [];
  let selectedRange = "24h";
  let currentDetail = null;
  let currentHistoryPoints = [];
  let refreshTimer = null;
  let resizeFrame = null;

  const chartColorVariables = {
    cpu: "--chart-cpu", memory: "--chart-memory", disk: "--chart-disk",
    read: "--chart-read", write: "--chart-write", rx: "--chart-rx", tx: "--chart-tx"
  };

  function themeColor(variable) {
    return getComputedStyle(document.documentElement).getPropertyValue(variable).trim();
  }

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>'"]/g, (character) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;"
    })[character]);
  }

  function clamp(value, min, max) { return Math.min(max, Math.max(min, Number(value) || 0)); }
  function formatPercent(value) { return `${clamp(value, 0, 100).toFixed(1)}%`; }
  function formatBytes(value) {
    let amount = Number(value) || 0;
    const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    let unit = 0;
    while (Math.abs(amount) >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
    const digits = unit === 0 ? 0 : amount >= 100 ? 0 : amount >= 10 ? 1 : 2;
    return `${amount.toFixed(digits)} ${units[unit]}`;
  }
  function formatRate(value) { return `${formatBytes(value)}/s`; }
  function formatDuration(seconds) {
    const value = Number(seconds) || 0;
    const days = Math.floor(value / 86400);
    const hours = Math.floor((value % 86400) / 3600);
    return days > 0 ? `${days} 天 ${hours} 小时` : `${hours} 小时`;
  }
  function formatLoad(value) { return (Number(value) || 0).toFixed(2); }
  function formatOS(node) {
    const name = String(node.os_name || "").trim();
    const version = String(node.os_version || "").trim();
    if (!name || name === "unknown") return "系统待采集";
    return version ? `${name}: ${version}` : name;
  }
  function formatTime(value, includeSeconds) {
    if (!value) return "--";
    return new Intl.DateTimeFormat("zh-CN", {
      month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
      second: includeSeconds ? "2-digit" : undefined, hour12: false
    }).format(new Date(value));
  }
  function displayName(node) { return node.display_name || node.hostname; }
  function usageClass(value) { return value >= 90 ? "danger" : value >= 75 ? "warning" : ""; }
  function statusText(status) { return ({ online: "在线", offline: "离线", pending: "待安装", disabled: "已禁用" })[status] || status; }
  function isPhysicalNode(node) {
    const machineType = String(node.machine_type || "").toLowerCase();
    if (machineType) return machineType === "physical";
    const packages = Number(node.cpu_package_count) || 0;
    return packages > 1 && (Number(node.cpu_logical_thread_count) || 0) > packages;
  }
  function cpuLabel(node) {
    const packageThreads = (node.cpu_threads_per_package || []).map(Number).filter((threads) => threads > 0);
    const totalThreads = Number(node.cpu_logical_thread_count) || packageThreads.reduce((sum, threads) => sum + threads, 0);
    if (!totalThreads) return "CPU";
    const threads = isPhysicalNode(node) && packageThreads.length > 1 ? packageThreads.join("+") : totalThreads;
    return `CPU · ${threads} 线程`;
  }
  function parseIPv4(value) {
    const address = String(value || "").trim().split("/", 1)[0];
    const parts = address.split(".");
    if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part))) return null;
    const octets = parts.map(Number);
    return octets.every((octet) => octet >= 0 && octet <= 255) ? octets : null;
  }
  function compareOctets(left, right) {
    for (let index = 0; index < left.length; index += 1) {
      if (left[index] !== right[index]) return left[index] - right[index];
    }
    return 0;
  }
  function compareNodeNames(left, right) {
    return String(displayName(left) || "").localeCompare(String(displayName(right) || ""), "zh-CN", { numeric: true });
  }
  function groupNodesBySubnet(items) {
    const subnets = new Map();
    const unassigned = [];
    items.forEach((node) => {
      const octets = parseIPv4(node.primary_ip);
      if (!octets) {
        unassigned.push(node);
        return;
      }
      const subnetOctets = octets.slice(0, 3);
      const key = subnetOctets.join(".");
      if (!subnets.has(key)) subnets.set(key, { label: `${key}.0/24`, octets: subnetOctets, nodes: [] });
      subnets.get(key).nodes.push(node);
    });

    const groups = Array.from(subnets.values()).sort((left, right) => compareOctets(left.octets, right.octets));
    groups.forEach((group) => group.nodes.sort((left, right) => {
      const addressOrder = compareOctets(parseIPv4(left.primary_ip), parseIPv4(right.primary_ip));
      return addressOrder || compareNodeNames(left, right);
    }));
    if (unassigned.length) {
      unassigned.sort(compareNodeNames);
      groups.push({ label: "未分配 IPv4", nodes: unassigned });
    }
    return groups;
  }
  function progress(label, value) {
    const percent = clamp(value, 0, 100);
    return `<div class="metric-row"><div class="metric-row-head"><span>${label}</span><strong>${formatPercent(percent)}</strong></div><div class="progress"><i class="${usageClass(percent)}" style="width:${percent}%"></i></div></div>`;
  }

  function nodeCard(node) {
    const memoryPercent = node.memory_usage_percent || 0;
    const addressAndOS = `${node.primary_ip || "未获取 IP"} - ${formatOS(node)}`;
    const physicalIcon = isPhysicalNode(node) ? '<img class="physical-server-icon" src="/assets/cloud-server.svg" alt="" title="物理服务器">' : "";
    return `<button class="node-card" type="button" data-node-id="${escapeHTML(node.node_id)}">
      <div class="card-head">
        <div class="card-title"><strong class="card-name">${physicalIcon}<span>${escapeHTML(displayName(node))}</span></strong><span class="card-meta">${escapeHTML(addressAndOS)}</span></div>
        <span class="status-label"><i class="status-dot ${escapeHTML(node.status)}"></i>${escapeHTML(statusText(node.status))}</span>
      </div>
      <div class="metric-list">
        ${progress(cpuLabel(node), node.cpu_usage_percent)}
        ${progress(`内存 · ${formatBytes(node.memory_total_bytes)}`, memoryPercent)}
        ${progress(`磁盘 · ${formatBytes(node.disk_total_bytes)}`, node.disk_usage_percent)}
      </div>
      <div class="io-strip">
        <div class="io-item"><span>磁盘读取</span><strong>↓ ${formatRate(node.disk_read_bytes_per_second)}</strong></div>
        <div class="io-item"><span>磁盘写入</span><strong>↑ ${formatRate(node.disk_write_bytes_per_second)}</strong></div>
        <div class="io-item load-item"><span>Load Average (1/5/15)</span><strong>${formatLoad(node.load_1)} / ${formatLoad(node.load_5)} / ${formatLoad(node.load_15)}</strong></div>
      </div>
    </button>`;
  }

  async function apiFetch(path) {
    const response = await fetch(path, { headers: { Accept: "application/json" } });
    if (!response.ok) {
      let message = `请求失败 (${response.status})`;
      try { message = (await response.json()).error || message; } catch (_) { /* response is not JSON */ }
      throw new Error(message);
    }
    return response.json();
  }

  function setLoading(active) {
    refreshButton.classList.toggle("is-loading", active);
    refreshButton.disabled = active;
  }

  async function loadNodes(silent) {
    setLoading(true);
    if (!silent) app.innerHTML = '<div class="loading-state"><span class="spinner"></span><span>正在读取节点状态</span></div>';
    try {
      const data = await apiFetch("/api/v1/nodes");
      nodes = data.nodes || [];
      renderList();
      updatedAt.textContent = `更新于 ${formatTime(new Date(), true)}`;
    } catch (error) {
      if (!silent) renderError(error);
    } finally {
      setLoading(false);
    }
  }

  function renderList() {
    currentDetail = null;
    const counts = nodes.reduce((result, node) => {
      result[node.status] = (result[node.status] || 0) + 1;
      return result;
    }, {});
    const groups = groupNodesBySubnet(nodes).map((group, index) => `<section class="node-group" aria-labelledby="node-group-${index}">
      <div class="node-group-head">
        <h2 id="node-group-${index}"><code>${escapeHTML(group.label)}</code></h2>
        <span>${group.nodes.length} 台</span>
      </div>
      <div class="node-grid">${group.nodes.map(nodeCard).join("")}</div>
    </section>`).join("");
    app.innerHTML = `<div class="page-heading">
      <div><h1>节点状态</h1><p>${nodes.length} 台服务器的最新一分钟快照</p></div>
      <div class="status-counts">
        <span class="status-count"><i class="status-dot online"></i>在线 <strong>${counts.online || 0}</strong></span>
        <span class="status-count"><i class="status-dot offline"></i>离线 <strong>${counts.offline || 0}</strong></span>
        ${counts.pending ? `<span class="status-count"><i class="status-dot pending"></i>待安装 <strong>${counts.pending}</strong></span>` : ""}
        ${counts.disabled ? `<span class="status-count"><i class="status-dot disabled"></i>禁用 <strong>${counts.disabled}</strong></span>` : ""}
      </div>
    </div>${nodes.length ? `<div class="node-groups">${groups}</div>` : '<div class="empty-state"><strong>暂无节点</strong><span>尚未创建监控节点</span></div>'}`;
    app.querySelectorAll("[data-node-id]").forEach((card) => card.addEventListener("click", () => {
      location.hash = `node/${card.dataset.nodeId}`;
    }));
  }

  async function loadDetail(nodeID) {
    setLoading(true);
    app.innerHTML = '<div class="loading-state"><span class="spinner"></span><span>正在读取节点详情</span></div>';
    try {
      const [detail, history] = await Promise.all([
        apiFetch(`/api/v1/nodes/${encodeURIComponent(nodeID)}`),
        apiFetch(`/api/v1/nodes/${encodeURIComponent(nodeID)}/history?range=${selectedRange}`)
      ]);
      currentDetail = detail;
      renderDetail(detail, history);
      updatedAt.textContent = `更新于 ${formatTime(new Date(), true)}`;
    } catch (error) {
      renderError(error, true);
    } finally {
      setLoading(false);
    }
  }

  function metricTile(label, value, subtext) {
    return `<div class="metric-tile"><span>${label}</span><strong>${value}</strong><small>${subtext || "&nbsp;"}</small></div>`;
  }

  function renderDetail(detail, history) {
    const node = detail.node;
    const alias = node.display_name && node.hostname !== "pending-registration" && node.hostname !== node.display_name && node.hostname !== node.primary_ip
      ? `<span>${escapeHTML(node.hostname)}</span>` : "";
    const lastReport = node.status === "pending" ? "等待首次上报" : `最近上报 ${formatTime(node.last_seen_at, true)}`;
    const rangeButtons = ranges.map((range) => `<button class="range-button ${range === selectedRange ? "active" : ""}" type="button" data-range="${range}">${range}</button>`).join("");
    app.innerHTML = `<div class="detail-head">
      <button class="back-button" type="button" title="返回节点列表" aria-label="返回节点列表">←</button>
      <div class="detail-title"><h1>${escapeHTML(displayName(node))}</h1><div class="detail-meta">
        <span class="status-label"><i class="status-dot ${escapeHTML(node.status)}"></i>${escapeHTML(statusText(node.status))}</span>
        <code>${escapeHTML(node.primary_ip || "未获取 IP")}</code>${alias}
        <span>${lastReport}</span>
      </div></div>
    </div>
    <div class="metric-strip">
      ${metricTile("CPU", formatPercent(node.cpu_usage_percent), `${node.cpu_physical_core_count} 核 / ${node.cpu_logical_thread_count} 线程`)}
      ${metricTile("内存", formatPercent(node.memory_usage_percent), `${formatBytes(node.memory_used_bytes)} / ${formatBytes(node.memory_total_bytes)}`)}
      ${metricTile("磁盘", formatPercent(node.disk_usage_percent), `${node.disk_count} 块 / ${formatBytes(node.disk_total_bytes)}`)}
      ${metricTile("磁盘 I/O", `↓ ${formatRate(node.disk_read_bytes_per_second)}`, `↑ ${formatRate(node.disk_write_bytes_per_second)}`)}
      ${metricTile("运行时间", formatDuration(node.uptime_seconds), `${escapeHTML(node.os_name)} ${escapeHTML(node.os_version)}`)}
    </div>
    <section class="section"><div class="section-head"><div><h2>历史数据</h2><p>${history.resolution === "hour" ? "小时汇总" : "分钟原始指标"}</p></div><div class="segmented" aria-label="历史时间范围">${rangeButtons}</div></div>
      <div class="chart-grid">
        ${chartPanel("resource-chart", "资源使用率", [["CPU", chartColorVariables.cpu], ["内存", chartColorVariables.memory], ["磁盘", chartColorVariables.disk]])}
        ${chartPanel("io-chart", "吞吐速率", [["磁盘读", chartColorVariables.read], ["磁盘写", chartColorVariables.write], ["网络收", chartColorVariables.rx], ["网络发", chartColorVariables.tx]])}
      </div>
    </section>
    <section class="section"><div class="section-head"><div><h2>硬件信息</h2><p>${escapeHTML(node.architecture)} · Agent ${escapeHTML(node.agent_version)}</p></div></div>
      <div class="info-grid">
        <div class="info-block"><h3>CPU</h3>${cpuTable(detail.cpu_packages)}</div>
        <div class="info-block"><h3>内存</h3>${memoryTable(detail.memory_modules)}</div>
        <div class="info-block"><h3>块设备</h3>${blockTable(detail.block_devices)}</div>
        <div class="info-block"><h3>文件系统</h3>${filesystemTable(detail.filesystems)}</div>
      </div>
    </section>
    <section class="section"><div class="section-head"><div><h2>网络接口</h2><p>地址、链路状态与当前吞吐</p></div></div>${networkTable(detail.network)}</section>`;
    app.querySelector(".back-button").addEventListener("click", () => { location.hash = ""; });
    app.querySelectorAll("[data-range]").forEach((button) => button.addEventListener("click", () => changeRange(node.node_id, button.dataset.range)));
    renderCharts(history.points || []);
  }

  function chartPanel(id, title, items) {
    const legend = items.map(([name, color]) => `<span class="legend-item" style="--color:var(${color})"><i></i>${name}</span>`).join("");
    return `<div class="chart-panel"><h3>${title}</h3><div class="legend">${legend}</div><div class="chart-wrap"><canvas id="${id}"></canvas></div></div>`;
  }

  async function changeRange(nodeID, range) {
    if (range === selectedRange) return;
    selectedRange = range;
    app.querySelectorAll("[data-range]").forEach((button) => button.classList.toggle("active", button.dataset.range === range));
    try {
      const history = await apiFetch(`/api/v1/nodes/${encodeURIComponent(nodeID)}/history?range=${range}`);
      const subtitle = app.querySelector(".section-head p");
      if (subtitle) subtitle.textContent = history.resolution === "hour" ? "小时汇总" : "分钟原始指标";
      renderCharts(history.points || []);
    } catch (error) { renderError(error, true); }
  }

  function renderCharts(points) {
    currentHistoryPoints = points;
    drawLineChart(document.getElementById("resource-chart"), points, [
      { label: "CPU", key: "cpu_usage_percent", color: themeColor(chartColorVariables.cpu) },
      { label: "内存", key: "memory_usage_percent", color: themeColor(chartColorVariables.memory) },
      { label: "磁盘", key: "disk_usage_percent", color: themeColor(chartColorVariables.disk) }
    ], true);
    drawLineChart(document.getElementById("io-chart"), points, [
      { label: "磁盘读", key: "disk_read_bytes_per_second", color: themeColor(chartColorVariables.read) },
      { label: "磁盘写", key: "disk_write_bytes_per_second", color: themeColor(chartColorVariables.write) },
      { label: "网络收", key: "network_rx_bytes_per_second", color: themeColor(chartColorVariables.rx) },
      { label: "网络发", key: "network_tx_bytes_per_second", color: themeColor(chartColorVariables.tx) }
    ], false);
  }

  function drawLineChart(canvas, points, series, percent) {
    if (!canvas) return;
    const wrap = canvas.parentElement;
    const ratio = window.devicePixelRatio || 1;
    const width = Math.max(280, Math.floor(wrap.clientWidth));
    const height = Math.max(200, Math.floor(wrap.clientHeight));
    canvas.width = width * ratio;
    canvas.height = height * ratio;
    const context = canvas.getContext("2d");
    context.scale(ratio, ratio);
    const padding = { top: 12, right: 10, bottom: 28, left: 48 };
    const plotWidth = width - padding.left - padding.right;
    const plotHeight = height - padding.top - padding.bottom;
    const maxValue = percent ? 100 : Math.max(1, ...points.flatMap((point) => series.map((item) => Number(point[item.key]) || 0))) * 1.12;
    const gridColor = themeColor("--chart-grid");
    const labelColor = themeColor("--chart-label");
    const hoverColor = themeColor("--chart-hover");

    function paint(hoverIndex) {
      context.clearRect(0, 0, width, height);
      context.font = "10px system-ui, sans-serif";
      context.textBaseline = "middle";
      context.strokeStyle = gridColor;
      context.fillStyle = labelColor;
      context.lineWidth = 1;
      for (let step = 0; step <= 4; step += 1) {
        const y = padding.top + plotHeight * step / 4;
        const value = maxValue * (1 - step / 4);
        context.beginPath(); context.moveTo(padding.left, y); context.lineTo(width - padding.right, y); context.stroke();
        const label = percent ? `${value.toFixed(0)}%` : compactRate(value);
        context.textAlign = "right"; context.fillText(label, padding.left - 7, y);
      }
      if (!points.length) {
        context.textAlign = "center"; context.fillStyle = labelColor;
        context.fillText("所选范围暂无历史数据", padding.left + plotWidth / 2, padding.top + plotHeight / 2);
        return;
      }
      series.forEach((item) => {
        context.beginPath(); context.strokeStyle = item.color; context.lineWidth = 1.7; context.lineJoin = "round";
        points.forEach((point, index) => {
          const x = padding.left + (points.length === 1 ? plotWidth / 2 : plotWidth * index / (points.length - 1));
          const y = padding.top + plotHeight * (1 - clamp(point[item.key], 0, maxValue) / maxValue);
          if (index === 0) context.moveTo(x, y); else context.lineTo(x, y);
        });
        context.stroke();
      });
      context.fillStyle = labelColor; context.textAlign = "left"; context.fillText(formatTime(points[0].bucket_at), padding.left, height - 10);
      context.textAlign = "right"; context.fillText(formatTime(points[points.length - 1].bucket_at), width - padding.right, height - 10);
      if (hoverIndex != null) {
        const x = padding.left + (points.length === 1 ? plotWidth / 2 : plotWidth * hoverIndex / (points.length - 1));
        context.strokeStyle = hoverColor; context.lineWidth = 1; context.beginPath(); context.moveTo(x, padding.top); context.lineTo(x, padding.top + plotHeight); context.stroke();
      }
    }
    paint(null);

    let tooltip = wrap.querySelector(".chart-tooltip");
    if (tooltip) tooltip.remove();
    tooltip = document.createElement("div");
    tooltip.className = "chart-tooltip";
    tooltip.hidden = true;
    wrap.appendChild(tooltip);
    canvas.onpointermove = (event) => {
      if (!points.length) return;
      const rect = canvas.getBoundingClientRect();
      const localX = clamp(event.clientX - rect.left - padding.left, 0, plotWidth);
      const index = points.length === 1 ? 0 : Math.round(localX / plotWidth * (points.length - 1));
      const point = points[index];
      paint(index);
      tooltip.innerHTML = `<strong>${formatTime(point.bucket_at, true)}</strong><br>${series.map((item) => `${item.label}: ${percent ? formatPercent(point[item.key]) : formatRate(point[item.key])}`).join("<br>")}`;
      tooltip.hidden = false;
      const x = padding.left + (points.length === 1 ? plotWidth / 2 : plotWidth * index / (points.length - 1));
      tooltip.style.left = `${x > width * .58 ? Math.max(4, x - 154) : Math.min(width - 150, x + 8)}px`;
    };
    canvas.onpointerleave = () => { tooltip.hidden = true; paint(null); };
  }

  function compactRate(value) {
    const amount = Number(value) || 0;
    if (amount >= 1024 ** 3) return `${(amount / 1024 ** 3).toFixed(1)}G/s`;
    if (amount >= 1024 ** 2) return `${(amount / 1024 ** 2).toFixed(1)}M/s`;
    if (amount >= 1024) return `${(amount / 1024).toFixed(0)}K/s`;
    return `${amount.toFixed(0)}B/s`;
  }

  function table(headers, rows) {
    if (!rows.length) return '<div class="table-wrap"><div class="empty-inline">暂无数据</div></div>';
    return `<div class="table-wrap"><table class="data-table"><thead><tr>${headers.map((header) => `<th>${header}</th>`).join("")}</tr></thead><tbody>${rows.join("")}</tbody></table></div>`;
  }
  function cpuTable(items) {
    return table(["型号", "核心 / 线程", "最高频率"], (items || []).map((item) => `<tr><td>${escapeHTML([item.vendor, item.model_name].filter(Boolean).join(" "))}</td><td>${item.physical_cores} / ${item.logical_threads}</td><td>${item.max_frequency_mhz ? `${item.max_frequency_mhz.toFixed(0)} MHz` : "--"}</td></tr>`));
  }
  function memoryTable(items) {
    return table(["插槽", "型号", "容量", "类型 / 速率"], (items || []).map((item) => `<tr><td>${escapeHTML(item.slot_name || "--")}</td><td>${escapeHTML(item.model_name || item.part_number || item.manufacturer || "--")}</td><td>${formatBytes(item.size_bytes)}</td><td>${escapeHTML(item.memory_type || "--")}${item.speed_mts ? ` / ${item.speed_mts} MT/s` : ""}</td></tr>`));
  }
  function blockTable(items) {
    return table(["设备", "型号", "类型", "容量"], (items || []).map((item) => `<tr><td><code>${escapeHTML(item.device_name)}</code></td><td>${escapeHTML(item.model_name || item.vendor || "--")}</td><td>${escapeHTML(item.device_kind)}</td><td>${formatBytes(item.size_bytes)}</td></tr>`));
  }
  function filesystemTable(items) {
    return table(["挂载点", "设备 / 类型", "使用", "容量"], (items || []).map((item) => `<tr><td><code>${escapeHTML(item.mount_point)}</code></td><td>${escapeHTML(item.device_name)} / ${escapeHTML(item.filesystem_type)}</td><td>${formatPercent(item.used_percent)}</td><td>${formatBytes(item.used_bytes)} / ${formatBytes(item.total_bytes)}</td></tr>`));
  }
  function networkTable(items) {
    return table(["接口", "地址", "链路", "接收", "发送"], (items || []).map((item) => `<tr><td><code>${escapeHTML(item.name)}</code><br>${escapeHTML(item.mac_address || "")}</td><td>${(item.addresses || []).map((address) => `<code>${escapeHTML(address)}</code>`).join("<br>") || "--"}</td><td>${item.link_up ? "已连接" : "未连接"}${item.link_speed_mbps ? ` / ${item.link_speed_mbps} Mbps` : ""}</td><td>${formatRate((item.rx_bits_per_second || 0) / 8)}</td><td>${formatRate((item.tx_bits_per_second || 0) / 8)}</td></tr>`));
  }

  function renderError(error, withBack) {
    app.innerHTML = `<div class="error-state"><strong>数据读取失败</strong><span>${escapeHTML(error.message)}</span>${withBack ? '<button class="back-button" type="button" title="返回节点列表" aria-label="返回节点列表">←</button>' : ""}</div>`;
    const back = app.querySelector(".back-button");
    if (back) back.addEventListener("click", () => { location.hash = ""; });
  }

  function shellQuote(value) {
    return `'${String(value).replace(/'/g, `'\\''`)}'`;
  }

  function buildInstallCommand(credentials, environment, version) {
    const origin = location.origin;
    const variables = [
      `SERVER_STATUS_URL=${shellQuote(origin)}`,
      `SERVER_STATUS_AGENT_ID=${shellQuote(credentials.agent_id)}`,
      `SERVER_STATUS_TOKEN=${shellQuote(credentials.token)}`
    ];
    if (environment) variables.push(`SERVER_STATUS_AGENT_ENVIRONMENT=${shellQuote(environment)}`);
    if (version) variables.push(`SERVER_STATUS_AGENT_VERSION=${shellQuote(version)}`);
    return `curl -fsSL ${shellQuote(`${origin}/install-agent.sh`)} | sudo env ${variables.join(" ")} sh`;
  }

  function resetAddNodeDialog() {
    addNodeForm.reset();
    document.getElementById("node-environment").value = "production";
    addNodeForm.hidden = false;
    installCommandResult.hidden = true;
    installCommand.textContent = "";
    addNodeError.hidden = true;
    addNodeError.textContent = "";
    createNodeButton.disabled = false;
    createNodeButton.textContent = "创建节点";
    copyInstallCommand.textContent = "复制命令";
  }

  function closeAddNodeDialog() {
    addNodeDialog.close();
  }

  async function registerNode(event) {
    event.preventDefault();
    if (!addNodeForm.reportValidity()) return;

    const adminToken = document.getElementById("admin-token").value.trim();
    const displayNameValue = document.getElementById("node-display-name").value.trim();
    const environment = document.getElementById("node-environment").value.trim();
    const version = document.getElementById("agent-version").value.trim();
    if (!adminToken || !displayNameValue) {
      addNodeError.textContent = "Admin Token 和显示名称不能为空";
      addNodeError.hidden = false;
      return;
    }
    const payload = { display_name: displayNameValue };
    if (environment) payload.labels = { environment };

    addNodeError.hidden = true;
    createNodeButton.disabled = true;
    createNodeButton.textContent = "正在创建";
    try {
      const response = await fetch("/api/v1/admin/nodes", {
        method: "POST",
        headers: {
          Accept: "application/json",
          Authorization: `Bearer ${adminToken}`,
          "Content-Type": "application/json"
        },
        body: JSON.stringify(payload)
      });
      if (!response.ok) {
        let message = `创建失败 (${response.status})`;
        try { message = (await response.json()).error || message; } catch (_) { /* response is not JSON */ }
        throw new Error(message);
      }
      const credentials = await response.json();
      document.getElementById("admin-token").value = "";
      installCommand.textContent = buildInstallCommand(credentials, environment, version);
      addNodeForm.hidden = true;
      installCommandResult.hidden = false;
      await loadNodes(true);
    } catch (error) {
      addNodeError.textContent = error.message;
      addNodeError.hidden = false;
    } finally {
      createNodeButton.disabled = false;
      createNodeButton.textContent = "创建节点";
    }
  }

  async function copyCommand() {
    const command = installCommand.textContent;
    try {
      await navigator.clipboard.writeText(command);
    } catch (_) {
      const textarea = document.createElement("textarea");
      textarea.value = command;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      textarea.remove();
    }
    copyInstallCommand.textContent = "已复制";
  }

  function route() {
    clearInterval(refreshTimer);
    const match = location.hash.match(/^#node\/([0-9a-f-]+)$/i);
    if (match) {
      loadDetail(match[1]);
      refreshTimer = setInterval(() => loadDetail(match[1]), 60000);
    } else {
      loadNodes(false);
      refreshTimer = setInterval(() => loadNodes(true), 30000);
    }
  }

  refreshButton.addEventListener("click", () => {
    const match = location.hash.match(/^#node\/([0-9a-f-]+)$/i);
    if (match) loadDetail(match[1]); else loadNodes(false);
  });
  addNodeButton.addEventListener("click", () => {
    resetAddNodeDialog();
    addNodeDialog.showModal();
    document.getElementById("admin-token").focus();
  });
  document.getElementById("close-node-dialog").addEventListener("click", closeAddNodeDialog);
  addNodeDialog.querySelectorAll("[data-close-dialog]").forEach((button) => button.addEventListener("click", closeAddNodeDialog));
  addNodeDialog.addEventListener("close", resetAddNodeDialog);
  addNodeForm.addEventListener("submit", registerNode);
  copyInstallCommand.addEventListener("click", copyCommand);
  window.addEventListener("hashchange", route);
  window.addEventListener("resize", () => {
    if (!currentDetail) return;
    if (resizeFrame) cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => renderCharts(currentHistoryPoints));
  });
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (currentDetail) renderCharts(currentHistoryPoints);
  });
  route();
})();
