(() => {
  "use strict";

  const state = {
    view: "open",
    alerts: [],
    nextCursor: "",
    loading: false,
    requestSequence: 0,
  };

  const elements = {
    updatedAt: document.getElementById("alerts-updated-at"),
    headingCopy: document.getElementById("alerts-heading-copy"),
    summaryCritical: document.getElementById("summary-critical"),
    summaryWarning: document.getElementById("summary-warning"),
    summaryAffected: document.getElementById("summary-affected"),
    summaryTotal: document.getElementById("summary-total"),
    category: document.getElementById("alert-category-filter"),
    severity: document.getElementById("alert-severity-filter"),
    node: document.getElementById("alert-node-filter"),
    range: document.getElementById("alert-range-filter"),
    rangeField: document.getElementById("alert-range-field"),
    status: document.getElementById("alerts-status"),
    results: document.querySelector(".alert-results"),
    tableWrap: document.getElementById("alerts-table-wrap"),
    tableBody: document.getElementById("alerts-table-body"),
    loadMore: document.getElementById("alerts-load-more"),
    refresh: document.getElementById("alerts-refresh-button"),
  };

  const categoryLabels = {
    offline: "离线",
    filesystem: "文件系统容量",
    load: "系统负载",
    smart: "SMART",
    raid: "RAID",
    temperature: "硬件温度",
  };

  const severityLabels = { critical: "严重", warning: "警告" };

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>'"]/g, (character) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;",
    })[character]);
  }

  function displayNode(alert) {
    return alert.display_name || alert.hostname || alert.primary_ip || "未命名节点";
  }

  function resourceName(alert) {
    return alert.details?.resource_name || alert.resource_key;
  }

  function alertValue(alert) {
    const details = alert.details || {};
    switch (alert.category) {
      case "offline":
        return "超过 3 分钟未上报";
      case "filesystem":
        return Number.isFinite(Number(details.used_percent)) ? `${Number(details.used_percent).toFixed(1)}% 已用` : "容量不足";
      case "load":
        return Number.isFinite(Number(details.normalized_load)) ? `${Number(details.normalized_load).toFixed(2)} / 线程` : "负载过高";
      case "smart": {
        const reasons = Array.isArray(details.risk_reasons) ? details.risk_reasons.join("、") : "";
        return reasons || details.smart_status || "SMART 异常";
      }
      case "raid":
        return details.state || "RAID 异常";
      case "temperature":
        return Number.isFinite(Number(details.temperature_celsius)) ? `${Number(details.temperature_celsius).toFixed(1)} °C` : "温度过高";
      default:
        return alert.title;
    }
  }

  function formatDate(value) {
    if (!value) return "--";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "--";
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
    }).format(date).replaceAll("/", "-");
  }

  function formatDuration(seconds) {
    let remaining = Math.max(0, Number(seconds) || 0);
    const days = Math.floor(remaining / 86400);
    remaining %= 86400;
    const hours = Math.floor(remaining / 3600);
    remaining %= 3600;
    const minutes = Math.floor(remaining / 60);
    if (days > 0) return `${days} 天 ${hours} 小时`;
    if (hours > 0) return `${hours} 小时 ${minutes} 分钟`;
    return `${minutes} 分钟`;
  }

  function buildURL(cursor = "") {
    const params = new URLSearchParams({ state: state.view, limit: state.view === "open" ? "200" : "100" });
    if (elements.category.value) params.set("category", elements.category.value);
    if (elements.severity.value) params.set("severity", elements.severity.value);
    if (elements.node.value) params.set("node_id", elements.node.value);
    if (state.view === "resolved") params.set("range", elements.range.value);
    if (cursor) params.set("cursor", cursor);
    return `/api/v1/alerts?${params.toString()}`;
  }

  async function readJSON(response) {
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || `请求失败（${response.status}）`);
    return payload;
  }

  function renderSummary(summary = {}) {
    elements.summaryCritical.textContent = Number(summary.critical || 0).toLocaleString("zh-CN");
    elements.summaryWarning.textContent = Number(summary.warning || 0).toLocaleString("zh-CN");
    elements.summaryAffected.textContent = Number(summary.affected_nodes || 0).toLocaleString("zh-CN");
    elements.summaryTotal.textContent = Number(summary.open_total || 0).toLocaleString("zh-CN");
  }

  function alertRow(alert) {
    const nodeName = escapeHTML(displayNode(alert));
    const ip = alert.primary_ip ? `<small>${escapeHTML(alert.primary_ip)}</small>` : "";
    const resource = escapeHTML(resourceName(alert));
    const category = escapeHTML(categoryLabels[alert.category] || alert.category);
    const severity = escapeHTML(severityLabels[alert.severity] || alert.severity);
    const restored = alert.resolved_at ? formatDate(alert.resolved_at) : "--";
    return `<tr>
      <td data-label="级别"><span class="alert-severity ${escapeHTML(alert.severity)}"><i></i>${severity}</span></td>
      <td data-label="节点"><a class="alert-node-link" href="/#node/${encodeURIComponent(alert.node_id)}" target="_blank" rel="noopener">${nodeName}</a>${ip}</td>
      <td data-label="分类 / 资源"><strong>${category}</strong><small>${resource}</small></td>
      <td data-label="当前值 / 状态"><span class="alert-current-value">${escapeHTML(alertValue(alert))}</span></td>
      <td data-label="开始时间"><time datetime="${escapeHTML(alert.opened_at)}">${formatDate(alert.opened_at)}</time></td>
      <td data-label="持续时间">${formatDuration(alert.duration_seconds)}</td>
      <td data-label="恢复时间">${restored}</td>
    </tr>`;
  }

  function renderAlerts() {
    if (state.alerts.length === 0) {
      elements.tableWrap.hidden = true;
      elements.status.hidden = false;
      elements.status.className = "alerts-feedback empty";
      elements.status.innerHTML = state.view === "open"
        ? "<strong>当前没有告警</strong><span>所有已启用节点均处于正常状态</span>"
        : "<strong>所选范围内没有历史记录</strong><span>可调整筛选条件或时间范围</span>";
    } else {
      elements.status.hidden = true;
      elements.tableBody.innerHTML = state.alerts.map(alertRow).join("");
      elements.tableWrap.hidden = false;
    }
    elements.loadMore.hidden = state.view !== "resolved" || !state.nextCursor;
    elements.results.setAttribute("aria-busy", "false");
    elements.headingCopy.textContent = state.view === "open"
      ? `当前显示 ${state.alerts.length} 条未恢复告警`
      : `已加载 ${state.alerts.length} 条历史记录`;
  }

  function showLoading(more) {
    if (!more) {
      elements.tableWrap.hidden = true;
      elements.status.hidden = false;
      elements.status.className = "alerts-feedback";
      elements.status.innerHTML = '<span class="spinner"></span><span>正在读取告警</span>';
    }
    elements.results.setAttribute("aria-busy", "true");
    elements.loadMore.disabled = true;
    elements.refresh.disabled = true;
  }

  function showError(error, more) {
    if (!more) {
      elements.tableWrap.hidden = true;
      elements.status.hidden = false;
      elements.status.className = "alerts-feedback error";
      elements.status.innerHTML = `<strong>告警读取失败</strong><span>${escapeHTML(error.message)}</span><button class="secondary-button" type="button" data-alert-retry>重试</button>`;
      elements.status.querySelector("[data-alert-retry]").addEventListener("click", () => loadAlerts(false));
    } else {
      elements.headingCopy.textContent = `加载更多失败：${error.message}`;
    }
    elements.results.setAttribute("aria-busy", "false");
  }

  async function loadAlerts(more = false) {
    state.loading = true;
    const sequence = ++state.requestSequence;
    showLoading(more);
    try {
      const payload = await readJSON(await fetch(buildURL(more ? state.nextCursor : ""), { cache: "no-store" }));
      if (sequence !== state.requestSequence) return;
      state.alerts = more ? state.alerts.concat(payload.alerts || []) : (payload.alerts || []);
      state.nextCursor = payload.next_cursor || "";
      renderSummary(payload.summary);
      renderAlerts();
      elements.updatedAt.textContent = `更新于 ${new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(new Date())}`;
    } catch (error) {
      if (sequence === state.requestSequence) showError(error, more);
    } finally {
      if (sequence === state.requestSequence) {
        state.loading = false;
        elements.loadMore.disabled = false;
        elements.refresh.disabled = false;
      }
    }
  }

  async function loadNodes() {
    try {
      const payload = await readJSON(await fetch("/api/v1/nodes", { cache: "no-store" }));
      const nodes = (payload.nodes || []).slice().sort((a, b) => displayNode(a).localeCompare(displayNode(b), "zh-CN"));
      const options = nodes.map((node) => `<option value="${escapeHTML(node.node_id)}">${escapeHTML(displayNode(node))}${node.primary_ip ? ` · ${escapeHTML(node.primary_ip)}` : ""}</option>`);
      elements.node.insertAdjacentHTML("beforeend", options.join(""));
    } catch (_) {
      elements.node.disabled = true;
    }
  }

  function changeView(view) {
    if (state.view === view) return;
    state.view = view;
    state.alerts = [];
    state.nextCursor = "";
    document.querySelectorAll("[data-alert-state]").forEach((button) => {
      const selected = button.dataset.alertState === view;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-selected", String(selected));
    });
    elements.rangeField.hidden = view !== "resolved";
    loadAlerts(false);
  }

  document.querySelectorAll("[data-alert-state]").forEach((button) => button.addEventListener("click", () => changeView(button.dataset.alertState)));
  [elements.category, elements.severity, elements.node, elements.range].forEach((control) => control.addEventListener("change", () => loadAlerts(false)));
  elements.loadMore.addEventListener("click", () => loadAlerts(true));
  elements.refresh.addEventListener("click", () => loadAlerts(false));
  window.setInterval(() => {
    if (state.view === "open" && !document.hidden) loadAlerts(false);
  }, 30000);

  loadNodes();
  loadAlerts(false);
})();
