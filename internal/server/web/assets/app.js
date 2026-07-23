(function () {
  "use strict";

  const app = document.getElementById("app");
  const refreshButton = document.getElementById("refresh-button");
  const updatedAt = document.getElementById("updated-at");
  const exportButton = document.getElementById("export-button");
  const addNodeButton = document.getElementById("add-node-button");
  const addNodeDialog = document.getElementById("add-node-dialog");
  const addNodeForm = document.getElementById("add-node-form");
  const addNodeError = document.getElementById("add-node-error");
  const createNodeButton = document.getElementById("create-node-button");
  const installCommandResult = document.getElementById("install-command-result");
  const installCommand = document.getElementById("install-command");
  const copyInstallCommand = document.getElementById("copy-install-command");
  const exportDialog = document.getElementById("export-dialog");
  const exportForm = document.getElementById("export-form");
  const exportAdminToken = document.getElementById("export-admin-token");
  const exportError = document.getElementById("export-error");
  const confirmExport = document.getElementById("confirm-export");
  const networkPreferenceDialog = document.getElementById("network-preference-dialog");
  const networkPreferenceForm = document.getElementById("network-preference-form");
  const networkPreferenceContext = document.getElementById("network-preference-context");
  const networkPreferenceToken = document.getElementById("network-preference-token");
  const networkPreferenceError = document.getElementById("network-preference-error");
  const saveNetworkPreference = document.getElementById("save-network-preference");
  const tagDialog = document.getElementById("tag-dialog");
  const tagForm = document.getElementById("tag-form");
  const tagInputs = Array.from(tagDialog.querySelectorAll("[data-tag-input]"));
  const tagAdminToken = document.getElementById("tag-admin-token");
  const tagError = document.getElementById("tag-error");
  const saveTags = document.getElementById("save-tags");
  const ranges = ["1h", "6h", "24h", "7d", "30d", "90d"];
  const adminTokenStorageKey = "server-status.admin-token";
  const adminTokenLifetimeMs = 30 * 24 * 60 * 60 * 1000;
  const storedAdminToken = loadStoredAdminToken();
  let nodes = [];
  let selectedRange = "24h";
  let currentDetail = null;
  let currentHistory = { points: [], gpus: [] };
  let currentGPUDevices = [];
  let refreshTimer = null;
  let resizeFrame = null;
  let cachedAdminToken = storedAdminToken.token;
  let cachedAdminTokenExpiresAt = storedAdminToken.expiresAt;
  let pendingNetworkPreference = null;
  let pendingTagNodeID = null;
  let selectedOS = "";
  let selectedTag = "";

  const chartColorVariables = {
    cpu: "--chart-cpu", memory: "--chart-memory", disk: "--chart-disk",
    read: "--chart-read", write: "--chart-write", rx: "--chart-rx", tx: "--chart-tx",
    gpu: "--chart-cpu", gpuMemory: "--chart-memory"
  };

  function themeColor(variable) {
    return getComputedStyle(document.documentElement).getPropertyValue(variable).trim();
  }

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>'"]/g, (character) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;"
    })[character]);
  }

  function loadStoredAdminToken() {
    try {
      const stored = JSON.parse(localStorage.getItem(adminTokenStorageKey) || "null");
      if (stored && typeof stored.token === "string" && stored.token && Number(stored.expires_at) > Date.now()) {
        return { token: stored.token, expiresAt: Number(stored.expires_at) };
      }
      localStorage.removeItem(adminTokenStorageKey);
    } catch (_) {
      try { localStorage.removeItem(adminTokenStorageKey); } catch (_) { /* storage is unavailable */ }
    }
    return { token: "", expiresAt: 0 };
  }

  function rememberAdminToken(token) {
    cachedAdminToken = token;
    cachedAdminTokenExpiresAt = Date.now() + adminTokenLifetimeMs;
    try {
      localStorage.setItem(adminTokenStorageKey, JSON.stringify({
        token,
        expires_at: cachedAdminTokenExpiresAt
      }));
    } catch (_) { /* private browsing or storage policy may block persistence */ }
  }

  function clearStoredAdminToken() {
    cachedAdminToken = "";
    cachedAdminTokenExpiresAt = 0;
    try { localStorage.removeItem(adminTokenStorageKey); } catch (_) { /* storage is unavailable */ }
  }

  function currentAdminToken() {
    if (cachedAdminTokenExpiresAt <= Date.now()) clearStoredAdminToken();
    return cachedAdminToken;
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
    if (!name || name.toLowerCase() === "unknown") return "系统待采集";
    return version ? `${name}: ${version}` : name;
  }
  function osFilterValue(node) {
    const name = String(node.os_name || "").trim();
    return !name || name.toLowerCase() === "unknown" ? "__unknown__" : name.toLowerCase();
  }
  function osFilterLabel(node) {
    return osFilterValue(node) === "__unknown__" ? "待采集" : String(node.os_name).trim();
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

  function tagBadges(tags, className) {
    const values = (tags || []).slice(0, 5);
    return `<span class="${className}">${values.map((tag) => `<span class="tag-badge">${escapeHTML(tag)}</span>`).join("")}</span>`;
  }

  function nodeCard(node) {
    const memoryPercent = node.memory_usage_percent || 0;
    const addressAndOS = `${node.primary_ip || "未获取 IP"} - ${formatOS(node)}`;
    const agentVersion = String(node.agent_version || "--").trim() || "--";
    const status = statusText(node.status);
    const physicalIcon = isPhysicalNode(node) ? '<img class="physical-server-icon" src="/assets/cloud-server.svg" alt="" title="物理服务器">' : "";
    const nvidiaIcon = node.has_nvidia_gpu ? '<img class="nvidia-icon" src="/assets/nvidia.svg" alt="" title="NVIDIA GPU">' : "";
    return `<button class="node-card" type="button" data-node-id="${escapeHTML(node.node_id)}">
      <div class="card-head">
        <div class="card-title"><strong class="card-name">${physicalIcon}${nvidiaIcon}<span>${escapeHTML(displayName(node))}</span></strong><span class="card-meta">${escapeHTML(addressAndOS)}</span>${tagBadges(node.tags, "card-tags")}</div>
        <span class="status-label" aria-label="${escapeHTML(`${status}，Agent ${agentVersion}`)}" title="${escapeHTML(`${status} · Agent ${agentVersion}`)}"><i class="status-dot ${escapeHTML(node.status)}" aria-hidden="true"></i><span class="agent-version">${escapeHTML(agentVersion)}</span></span>
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
    const osLabels = new Map();
    const tagLabels = new Map();
    nodes.forEach((node) => {
      const key = osFilterValue(node);
      if (!osLabels.has(key)) osLabels.set(key, osFilterLabel(node));
    });
    nodes.flatMap((node) => node.tags || []).forEach((tag) => {
      const key = tag.toLowerCase();
      if (!tagLabels.has(key)) tagLabels.set(key, tag);
    });
    const availableOS = Array.from(osLabels, ([key, label]) => ({ key, label })).sort((left, right) => left.label.localeCompare(right.label, "zh-CN", { sensitivity: "base" }));
    const availableTags = Array.from(tagLabels, ([key, label]) => ({ key, label })).sort((left, right) => left.label.localeCompare(right.label, "zh-CN", { sensitivity: "base" }));
    if (selectedOS && !osLabels.has(selectedOS)) selectedOS = "";
    if (selectedTag && !tagLabels.has(selectedTag)) selectedTag = "";
    const visibleNodes = nodes.filter((node) => {
      const matchesOS = !selectedOS || osFilterValue(node) === selectedOS;
      const matchesTag = !selectedTag || (node.tags || []).some((tag) => tag.toLowerCase() === selectedTag);
      return matchesOS && matchesTag;
    });
    const counts = visibleNodes.reduce((result, node) => {
      result[node.status] = (result[node.status] || 0) + 1;
      return result;
    }, {});
    const groups = groupNodesBySubnet(visibleNodes).map((group, index) => `<section class="node-group" aria-labelledby="node-group-${index}">
      <div class="node-group-head">
        <h2 id="node-group-${index}"><code>${escapeHTML(group.label)}</code></h2>
        <span>${group.nodes.length} 台</span>
      </div>
      <div class="node-grid">${group.nodes.map(nodeCard).join("")}</div>
    </section>`).join("");
    const osOptions = availableOS.map((os) => `<option value="${escapeHTML(os.key)}"${os.key === selectedOS ? " selected" : ""}>${escapeHTML(os.label)}</option>`).join("");
    const tagOptions = availableTags.map((tag) => `<option value="${escapeHTML(tag.key)}"${tag.key === selectedTag ? " selected" : ""}>${escapeHTML(tag.label)}</option>`).join("");
    const hasActiveFilter = selectedOS || selectedTag;
    app.innerHTML = `<div class="page-heading">
      <div><h1>节点状态</h1><p>${hasActiveFilter ? `${visibleNodes.length} / ${nodes.length}` : nodes.length} 台服务器的最新一分钟快照</p></div>
      <div class="list-tools">
        <label class="list-filter"><span>操作系统</span><select id="os-filter"${availableOS.length ? "" : " disabled"}><option value="">全部</option>${osOptions}</select></label>
        <label class="list-filter"><span>Tag</span><select id="tag-filter"${availableTags.length ? "" : " disabled"}><option value="">全部</option>${tagOptions}</select></label>
        <div class="status-counts">
        <span class="status-count"><i class="status-dot online"></i>在线 <strong>${counts.online || 0}</strong></span>
        <span class="status-count"><i class="status-dot offline"></i>离线 <strong>${counts.offline || 0}</strong></span>
        ${counts.pending ? `<span class="status-count"><i class="status-dot pending"></i>待安装 <strong>${counts.pending}</strong></span>` : ""}
        ${counts.disabled ? `<span class="status-count"><i class="status-dot disabled"></i>禁用 <strong>${counts.disabled}</strong></span>` : ""}
        </div>
      </div>
    </div>${visibleNodes.length ? `<div class="node-groups">${groups}</div>` : `<div class="empty-state"><strong>${nodes.length ? "没有匹配的节点" : "暂无节点"}</strong></div>`}`;
    document.getElementById("os-filter").addEventListener("change", (event) => {
      selectedOS = event.target.value;
      renderList();
    });
    document.getElementById("tag-filter").addEventListener("change", (event) => {
      selectedTag = event.target.value;
      renderList();
    });
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
    currentGPUDevices = detail.gpus || [];
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
      </div><div class="detail-tags">${tagBadges(node.tags, "detail-tag-list")}<button class="tag-edit-button" type="button" data-edit-tags title="编辑 Tag" aria-label="编辑 Tag">+</button></div></div>
    </div>
    <div class="metric-strip">
      ${metricTile("CPU", formatPercent(node.cpu_usage_percent), `${node.cpu_physical_core_count} 核 / ${node.cpu_logical_thread_count} 线程`)}
      ${metricTile("内存", formatPercent(node.memory_usage_percent), `${formatBytes(node.memory_used_bytes)} / ${formatBytes(node.memory_total_bytes)}`)}
      ${metricTile("磁盘", formatPercent(node.disk_usage_percent), `${node.disk_count} 块 / ${formatBytes(node.disk_total_bytes)}`)}
      ${metricTile("磁盘 I/O", `↓ ${formatRate(node.disk_read_bytes_per_second)}`, `↑ ${formatRate(node.disk_write_bytes_per_second)}`)}
      ${metricTile("运行时间", formatDuration(node.uptime_seconds), `${escapeHTML(node.os_name)} ${escapeHTML(node.os_version)}`)}
    </div>
    ${gpuSection(detail.gpus)}
    <section class="section"><div class="section-head"><div><h2>历史数据</h2><p data-history-subtitle>${history.resolution === "hour" ? "小时汇总" : "分钟原始指标"}</p></div><div class="segmented" aria-label="历史时间范围">${rangeButtons}</div></div>
      <div class="chart-grid">
        ${chartPanel("resource-chart", "资源使用率", [["CPU", chartColorVariables.cpu], ["内存", chartColorVariables.memory], ["磁盘", chartColorVariables.disk]])}
        ${chartPanel("io-chart", "吞吐速率", [["磁盘读", chartColorVariables.read], ["磁盘写", chartColorVariables.write], ["网络收", chartColorVariables.rx], ["网络发", chartColorVariables.tx]])}
      </div>
      <div class="chart-grid gpu-history-grid" id="gpu-history-grid"></div>
    </section>
    <section class="section"><div class="section-head"><div><h2>硬件信息</h2><p>${node.system_model ? `服务器型号：${escapeHTML(node.system_model)} · ` : ""}${escapeHTML(node.architecture)} · Agent ${escapeHTML(node.agent_version)}</p></div></div>
      <div class="info-grid">
        <div class="info-block"><h3>CPU</h3>${cpuTable(detail.cpu_packages)}</div>
        <div class="info-block"><h3>内存</h3>${memoryTable(detail.memory_modules)}</div>
        <div class="info-block"><h3>块设备</h3>${blockTable(detail.block_devices)}</div>
        <div class="info-block"><h3>文件系统</h3>${filesystemTable(detail.filesystems)}</div>
      </div>
    </section>
    <section class="section"><div class="section-head"><div><h2>网络接口</h2><p>地址、链路状态与当前吞吐</p></div></div>${networkTable(detail.network)}</section>`;
    app.querySelector(".back-button").addEventListener("click", () => { location.hash = ""; });
    app.querySelector("[data-edit-tags]").addEventListener("click", () => openTagDialog(node));
    app.querySelectorAll("[data-range]").forEach((button) => button.addEventListener("click", () => changeRange(node.node_id, button.dataset.range)));
    app.querySelectorAll("[data-primary-interface-id]").forEach((button) => button.addEventListener("click", () => {
      selectPrimaryNetworkInterface(node.node_id, button.dataset.primaryInterfaceId, button.dataset.interfaceName, button);
    }));
    renderCharts(history);
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
      const subtitle = app.querySelector("[data-history-subtitle]");
      if (subtitle) subtitle.textContent = history.resolution === "hour" ? "小时汇总" : "分钟原始指标";
      renderCharts(history);
    } catch (error) { renderError(error, true); }
  }

  function renderCharts(history) {
    currentHistory = history || { points: [], gpus: [] };
    const points = currentHistory.points || [];
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
    renderGPUHistoryCharts(currentHistory.gpus || []);
  }

  function renderGPUHistoryCharts(historySeries) {
    const grid = document.getElementById("gpu-history-grid");
    if (!grid) return;
    const devices = [];
    const positions = new Map();
    currentGPUDevices.forEach((gpu) => {
      positions.set(gpu.gpu_device_id, devices.length);
      devices.push({ ...gpu, points: [] });
    });
    historySeries.forEach((gpu) => {
      const position = positions.get(gpu.gpu_device_id);
      if (position == null) {
        positions.set(gpu.gpu_device_id, devices.length);
        devices.push(gpu);
      } else {
        devices[position] = { ...devices[position], ...gpu };
      }
    });
    grid.innerHTML = devices.map((gpu, index) => chartPanel(
      `gpu-history-chart-${index}`,
      `GPU ${escapeHTML(gpu.index)} · ${escapeHTML(gpu.model_name)}`,
      [["GPU 使用率", chartColorVariables.gpu], ["显存使用率", chartColorVariables.gpuMemory]]
    )).join("");
    devices.forEach((gpu, index) => drawLineChart(
      document.getElementById(`gpu-history-chart-${index}`),
      gpu.points || [],
      [
        { label: "GPU 使用率", key: "utilization_percent", color: themeColor(chartColorVariables.gpu) },
        { label: "显存使用率", key: "memory_usage_percent", color: themeColor(chartColorVariables.gpuMemory) }
      ],
      true
    ));
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
    return table(["型号", "核心拓扑", "最高频率"], (items || []).map((item) => {
      const classes = item.performance_cores || item.efficiency_cores
        ? ` · ${item.performance_cores || 0}P + ${item.efficiency_cores || 0}E`
        : "";
      return `<tr><td>${escapeHTML([item.vendor, item.model_name].filter(Boolean).join(" "))}</td><td>${item.physical_cores} 核 / ${item.logical_threads} 线程${classes}</td><td>${item.max_frequency_mhz ? `${item.max_frequency_mhz.toFixed(0)} MHz` : "--"}</td></tr>`;
    }));
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
  function gpuMeter(value) {
    const percent = clamp(value, 0, 100);
    return `<div class="gpu-meter"><strong>${formatPercent(percent)}</strong><div class="progress"><i class="${usageClass(percent)}" style="width:${percent}%"></i></div></div>`;
  }
  function gpuSection(items) {
    if (!items || !items.length) return "";
    const rows = items.map((item) => `<tr><td><span class="gpu-model"><img class="nvidia-icon" src="/assets/nvidia.svg" alt=""><span><strong>GPU ${item.index}</strong><small>${escapeHTML(item.model_name)}</small></span></span></td><td><span class="gpu-cell-label">GPU 使用率</span>${gpuMeter(item.utilization_percent)}</td><td><span class="gpu-cell-label">显存使用率</span>${gpuMeter(item.memory_usage_percent)}</td><td><span class="gpu-cell-label">显存</span>${formatBytes(item.memory_used_bytes)} / ${formatBytes(item.memory_total_bytes)}</td></tr>`);
    return `<section class="section gpu-section"><div class="section-head"><div><h2>GPU</h2><p>${items.length} 张 NVIDIA GPU 的当前负载</p></div></div>${table(["设备", "GPU 使用率", "显存使用率", "显存"], rows)}</section>`;
  }
  function networkTable(items) {
    return table(["接口", "地址", "链路", "接收", "发送", "首页 IP"], (items || []).map((item) => {
      const hasAddress = (item.addresses || []).length > 0;
      const buttonText = item.is_primary ? '<span aria-hidden="true">✓</span> 已选择' : "选择";
      const disabled = item.is_primary || !hasAddress;
      const title = !hasAddress ? ' title="接口暂无 IP 地址"' : "";
      return `<tr class="${item.is_primary ? "selected-network-row" : ""}"><td><code>${escapeHTML(item.name)}</code><br>${escapeHTML(item.mac_address || "")}</td><td>${(item.addresses || []).map((address) => `<code>${escapeHTML(address)}</code>`).join("<br>") || "--"}</td><td>${item.link_up ? "已连接" : "未连接"}${item.link_speed_mbps ? ` / ${item.link_speed_mbps} Mbps` : ""}</td><td>${formatRate((item.rx_bits_per_second || 0) / 8)}</td><td>${formatRate((item.tx_bits_per_second || 0) / 8)}</td><td><button class="network-select-button ${item.is_primary ? "active" : ""}" type="button" data-primary-interface-id="${escapeHTML(item.interface_id)}" data-interface-name="${escapeHTML(item.name)}"${disabled ? " disabled" : ""}${title}>${buttonText}</button></td></tr>`;
    }));
  }

  function openNetworkPreferenceDialog(nodeID, interfaceID, interfaceName, errorMessage) {
    pendingNetworkPreference = { nodeID, interfaceID, interfaceName };
    networkPreferenceContext.textContent = `${interfaceName} 将作为首页卡片的 IP 来源`;
    networkPreferenceError.textContent = errorMessage || "";
    networkPreferenceError.hidden = !errorMessage;
    networkPreferenceToken.value = currentAdminToken();
    networkPreferenceDialog.showModal();
    networkPreferenceToken.focus();
  }

  function closeNetworkPreferenceDialog() {
    networkPreferenceDialog.close();
  }

  async function updatePrimaryNetworkInterface(nodeID, interfaceID, token) {
    const response = await fetch(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/primary-network-interface`, {
      method: "PUT",
      headers: {
        Accept: "application/json",
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json"
      },
      body: JSON.stringify({ interface_id: interfaceID })
    });
    if (!response.ok) {
      let message = `保存失败 (${response.status})`;
      try { message = (await response.json()).error || message; } catch (_) { /* response is not JSON */ }
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
  }

  async function selectPrimaryNetworkInterface(nodeID, interfaceID, interfaceName, button) {
    const adminToken = currentAdminToken();
    if (!adminToken) {
      openNetworkPreferenceDialog(nodeID, interfaceID, interfaceName);
      return;
    }
    button.disabled = true;
    button.textContent = "保存中";
    try {
      await updatePrimaryNetworkInterface(nodeID, interfaceID, adminToken);
      await loadDetail(nodeID);
    } catch (error) {
      if (error.status === 401) clearStoredAdminToken();
      openNetworkPreferenceDialog(nodeID, interfaceID, interfaceName, error.status === 401 ? "Admin Token 无效" : error.message);
    }
  }

  async function submitNetworkPreference(event) {
    event.preventDefault();
    if (!networkPreferenceForm.reportValidity() || !pendingNetworkPreference) return;
    const token = networkPreferenceToken.value.trim();
    networkPreferenceError.hidden = true;
    saveNetworkPreference.disabled = true;
    saveNetworkPreference.textContent = "正在保存";
    try {
      await updatePrimaryNetworkInterface(pendingNetworkPreference.nodeID, pendingNetworkPreference.interfaceID, token);
      rememberAdminToken(token);
      const nodeID = pendingNetworkPreference.nodeID;
      closeNetworkPreferenceDialog();
      await loadDetail(nodeID);
    } catch (error) {
      if (error.status === 401) clearStoredAdminToken();
      networkPreferenceError.textContent = error.status === 401 ? "Admin Token 无效" : error.message;
      networkPreferenceError.hidden = false;
    } finally {
      saveNetworkPreference.disabled = false;
      saveNetworkPreference.textContent = "确认选择";
    }
  }

  function openTagDialog(node) {
    pendingTagNodeID = node.node_id;
    tagInputs.forEach((input, index) => { input.value = (node.tags || [])[index] || ""; });
    tagAdminToken.value = currentAdminToken();
    tagError.hidden = true;
    tagError.textContent = "";
    tagDialog.showModal();
    tagInputs[0].focus();
  }

  function closeTagDialog() {
    tagDialog.close();
  }

  async function updateNodeTags(nodeID, tags, token) {
    const response = await fetch(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/tags`, {
      method: "PUT",
      headers: {
        Accept: "application/json",
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json"
      },
      body: JSON.stringify({ tags })
    });
    if (!response.ok) {
      let message = `保存失败 (${response.status})`;
      try { message = (await response.json()).error || message; } catch (_) { /* response is not JSON */ }
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    return response.json();
  }

  async function submitTags(event) {
    event.preventDefault();
    if (!tagForm.reportValidity() || !pendingTagNodeID) return;
    const tags = tagInputs.map((input) => input.value.trim()).filter(Boolean);
    const token = tagAdminToken.value.trim();
    tagError.hidden = true;
    saveTags.disabled = true;
    saveTags.textContent = "正在保存";
    try {
      await updateNodeTags(pendingTagNodeID, tags, token);
      rememberAdminToken(token);
      const nodeID = pendingTagNodeID;
      closeTagDialog();
      await loadDetail(nodeID);
    } catch (error) {
      if (error.status === 401) clearStoredAdminToken();
      tagError.textContent = error.status === 401 ? "Admin Token 无效" : error.message;
      tagError.hidden = false;
    } finally {
      saveTags.disabled = false;
      saveTags.textContent = "保存";
    }
  }

  function renderError(error, withBack) {
    app.innerHTML = `<div class="error-state"><strong>数据读取失败</strong><span>${escapeHTML(error.message)}</span>${withBack ? '<button class="back-button" type="button" title="返回节点列表" aria-label="返回节点列表">←</button>' : ""}</div>`;
    const back = app.querySelector(".back-button");
    if (back) back.addEventListener("click", () => { location.hash = ""; });
  }

  function shellQuote(value) {
    return `'${String(value).replace(/'/g, `'\\''`)}'`;
  }

  function buildInstallCommand(credentials, environment, version, platform) {
    const origin = location.origin;
    if (platform.startsWith("windows-")) {
      const architecture = platform === "windows-386" ? "386" : "amd64";
      const release = version ? `v${version.replace(/^v/, "")}` : "latest";
      const asset = `server-status-agent-windows-${architecture}.exe`;
      const downloadURL = `${origin}/agent/releases/${release}/${asset}`;
      const installArguments = [
        "install",
        `--server "${origin}"`,
        `--id "${credentials.agent_id}"`,
        `--token "${credentials.token}"`
      ];
      if (environment) installArguments.push(`--environment "${environment}"`);
      return [
        `cmd.exe /d /c bitsadmin /transfer ServerStatusAgent /download /priority normal "${downloadURL}" "%CD%\\server-status-agent.exe"`,
        `.\\server-status-agent.exe ${installArguments.join(" ")}`
      ].join("\r\n");
    }
    if (platform === "macos") {
      const variables = [
        `SERVER_STATUS_URL=${shellQuote(origin)}`,
        `SERVER_STATUS_AGENT_ID=${shellQuote(credentials.agent_id)}`,
        `SERVER_STATUS_TOKEN=${shellQuote(credentials.token)}`
      ];
      if (environment) variables.push(`SERVER_STATUS_AGENT_ENVIRONMENT=${shellQuote(environment)}`);
      if (version) variables.push(`SERVER_STATUS_AGENT_VERSION=${shellQuote(version)}`);
      return `curl -fsSL ${shellQuote(`${origin}/install-agent-macos.sh`)} | sudo env ${variables.join(" ")} sh`;
    }
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
    const platform = document.getElementById("node-platform").value;
    if (!adminToken || !displayNameValue) {
      addNodeError.textContent = "Admin Token 和显示名称不能为空";
      addNodeError.hidden = false;
      return;
    }
    const payload = { display_name: displayNameValue };
    if (environment) payload.labels = { environment };
    if (platform.startsWith("windows-")) {
      payload.os_name = "Windows";
      payload.architecture = platform === "windows-386" ? "386" : "amd64";
      payload.agent_version = "windows-legacy-pending";
    } else if (platform === "macos") {
      payload.os_name = "macOS";
      payload.architecture = "universal";
      payload.agent_version = "macos-script-pending";
    }

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
        if (response.status === 401) clearStoredAdminToken();
        throw new Error(message);
      }
      const credentials = await response.json();
      rememberAdminToken(adminToken);
      document.getElementById("admin-token").value = "";
      installCommand.textContent = buildInstallCommand(credentials, environment, version, platform);
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

  async function downloadNodeExport(token) {
    exportButton.disabled = true;
    exportButton.textContent = "正在导出";
    try {
      const response = await fetch("/api/v1/admin/nodes/export", {
        headers: { Authorization: `Bearer ${token}` }
      });
      if (!response.ok) {
        let message = `导出失败 (${response.status})`;
        try { message = (await response.json()).error || message; } catch (_) { /* response is not JSON */ }
        if (response.status === 401) clearStoredAdminToken();
        throw new Error(message);
      }
      const workbook = await response.blob();
      const url = URL.createObjectURL(workbook);
      const link = document.createElement("a");
      link.href = url;
      link.download = `server-status-nodes-${new Date().toISOString().slice(0, 19).replace(/[-:T]/g, "")}.xlsx`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      rememberAdminToken(token);
    } finally {
      exportButton.disabled = false;
      exportButton.textContent = "导出 Excel";
    }
  }

  function closeExportDialog() {
    exportDialog.close();
  }

  async function submitExport(event) {
    event.preventDefault();
    if (!exportForm.reportValidity()) return;
    const token = exportAdminToken.value.trim();
    exportError.hidden = true;
    confirmExport.disabled = true;
    confirmExport.textContent = "正在导出";
    try {
      await downloadNodeExport(token);
      closeExportDialog();
    } catch (error) {
      exportError.textContent = error.message;
      exportError.hidden = false;
    } finally {
      confirmExport.disabled = false;
      confirmExport.textContent = "下载 Excel";
    }
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
    document.getElementById("admin-token").value = currentAdminToken();
    addNodeDialog.showModal();
    document.getElementById("admin-token").focus();
  });
  exportButton.addEventListener("click", async () => {
    const token = currentAdminToken();
    if (!token) {
      exportAdminToken.value = "";
      exportError.hidden = true;
      exportError.textContent = "";
      exportDialog.showModal();
      exportAdminToken.focus();
      return;
    }
    try {
      await downloadNodeExport(token);
    } catch (error) {
      exportError.textContent = error.message;
      exportError.hidden = false;
      exportDialog.showModal();
      exportAdminToken.value = "";
      exportAdminToken.focus();
    }
  });
  document.getElementById("close-export-dialog").addEventListener("click", closeExportDialog);
  exportDialog.querySelector("[data-close-export]").addEventListener("click", closeExportDialog);
  exportDialog.addEventListener("close", () => {
    exportAdminToken.value = "";
    exportError.hidden = true;
    exportError.textContent = "";
  });
  exportForm.addEventListener("submit", submitExport);
  document.getElementById("close-node-dialog").addEventListener("click", closeAddNodeDialog);
  addNodeDialog.querySelectorAll("[data-close-dialog]").forEach((button) => button.addEventListener("click", closeAddNodeDialog));
  addNodeDialog.addEventListener("close", resetAddNodeDialog);
  addNodeForm.addEventListener("submit", registerNode);
  copyInstallCommand.addEventListener("click", copyCommand);
  document.getElementById("close-network-preference-dialog").addEventListener("click", closeNetworkPreferenceDialog);
  networkPreferenceDialog.querySelector("[data-close-network-preference]").addEventListener("click", closeNetworkPreferenceDialog);
  networkPreferenceDialog.addEventListener("close", () => {
    pendingNetworkPreference = null;
    networkPreferenceToken.value = "";
    networkPreferenceError.hidden = true;
    networkPreferenceError.textContent = "";
  });
  networkPreferenceForm.addEventListener("submit", submitNetworkPreference);
  document.getElementById("close-tag-dialog").addEventListener("click", closeTagDialog);
  tagDialog.querySelector("[data-close-tag-dialog]").addEventListener("click", closeTagDialog);
  tagDialog.addEventListener("close", () => {
    pendingTagNodeID = null;
    tagInputs.forEach((input) => { input.value = ""; });
    tagAdminToken.value = "";
    tagError.hidden = true;
    tagError.textContent = "";
  });
  tagForm.addEventListener("submit", submitTags);
  window.addEventListener("hashchange", route);
  window.addEventListener("resize", () => {
    if (!currentDetail) return;
    if (resizeFrame) cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => renderCharts(currentHistory));
  });
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (currentDetail) renderCharts(currentHistory);
  });
  route();
})();
