package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/guohai/server-status/internal/store"
)

const testAdminToken = "admin-token-with-at-least-thirty-two-characters"

type fakeStore struct {
	auth                        store.NodeAuth
	ingested                    bool
	registered                  bool
	homepageSummary             store.HomepageSummary
	homepageSummaryErr          error
	nodes                       []store.NodeSummary
	nodeDetails                 map[string]store.NodeDetail
	deletedNodeID               string
	deleteNodeErr               error
	primaryNetworkNodeID        string
	primaryNetworkInterfaceID   string
	primaryNetworkPreferenceErr error
	tagNodeID                   string
	tags                        []string
	tagUpdateErr                error
	history                     store.NodeHistory
	historyErr                  error
	historyCalls                atomic.Int32
	historyDelay                time.Duration
	alerts                      store.AlertList
	alertsErr                   error
	alertOptions                store.AlertListOptions
}

type controlledDeleteStore struct {
	*fakeStore
	started chan string
	release chan struct{}
	result  error
	calls   atomic.Int32
}

func (store *controlledDeleteStore) DeleteNode(ctx context.Context, nodeID string) error {
	store.calls.Add(1)
	store.started <- nodeID
	if store.release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-store.release:
		}
	}
	return store.result
}

func (fake *fakeStore) Ready(context.Context) error { return nil }
func (fake *fakeStore) AuthenticateToken(context.Context, string) (store.NodeAuth, error) {
	return fake.auth, nil
}
func (fake *fakeStore) Ingest(context.Context, store.NodeAuth, report.Report) error {
	fake.ingested = true
	return nil
}
func (fake *fakeStore) RegisterNode(context.Context, store.RegisterNodeInput) (store.NodeCredentials, error) {
	fake.registered = true
	return store.NodeCredentials{NodeID: "10000000-0000-4000-8000-000000000001", AgentID: fake.auth.AgentID, Token: "node-token"}, nil
}
func (fake *fakeStore) ListNodes(context.Context) ([]store.NodeSummary, error) {
	return fake.nodes, nil
}
func (fake *fakeStore) GetHomepageSummary(context.Context) (store.HomepageSummary, error) {
	return fake.homepageSummary, fake.homepageSummaryErr
}

func (fake *fakeStore) GetNode(_ context.Context, nodeID string) (store.NodeDetail, error) {
	if detail, ok := fake.nodeDetails[nodeID]; ok {
		return detail, nil
	}
	return store.NodeDetail{}, store.ErrNotFound
}
func (fake *fakeStore) DeleteNode(_ context.Context, nodeID string) error {
	fake.deletedNodeID = nodeID
	return fake.deleteNodeErr
}
func (fake *fakeStore) SetPrimaryNetworkInterface(_ context.Context, nodeID, interfaceID string) error {
	fake.primaryNetworkNodeID = nodeID
	fake.primaryNetworkInterfaceID = interfaceID
	return fake.primaryNetworkPreferenceErr
}
func (fake *fakeStore) UpdateNodeTags(_ context.Context, nodeID string, tags []string) error {
	fake.tagNodeID = nodeID
	fake.tags = append([]string(nil), tags...)
	return fake.tagUpdateErr
}
func (fake *fakeStore) GetNodeHistory(ctx context.Context, _, _ string) (store.NodeHistory, error) {
	fake.historyCalls.Add(1)
	if fake.historyDelay > 0 {
		select {
		case <-ctx.Done():
			return store.NodeHistory{}, ctx.Err()
		case <-time.After(fake.historyDelay):
		}
	}
	if fake.historyErr != nil {
		return store.NodeHistory{}, fake.historyErr
	}
	if fake.history.NodeID == "" {
		return store.NodeHistory{}, store.ErrNotFound
	}
	return fake.history, nil
}

func (fake *fakeStore) ListAlerts(_ context.Context, options store.AlertListOptions) (store.AlertList, error) {
	fake.alertOptions = options
	return fake.alerts, fake.alertsErr
}

func TestHealth(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestReportRequiresToken(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader([]byte(`{}`)))
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestValidReportIsIngested(t *testing.T) {
	api, database := testAPI()
	payload := validHTTPTestReport(t)
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer node-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	if !database.ingested {
		t.Fatal("valid report did not reach the store")
	}
}

func TestOlderAgentReceivesCentralReleaseUpdate(t *testing.T) {
	api, _ := testAPI()
	api.latestAgentVersion = "1.2.0"
	payload := validHTTPTestReport(t)
	payload.Agent.AgentVersion = "1.1.0"
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer node-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	var receipt report.ReportReceipt
	if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.AgentUpdate == nil || receipt.AgentUpdate.Version != "1.2.0" {
		t.Fatalf("unexpected update directive: %+v", receipt.AgentUpdate)
	}
}

func TestCurrentOrNewerAgentDoesNotReceiveUpdate(t *testing.T) {
	for _, agentVersion := range []string{"1.2.0", "1.3.0", "dev"} {
		api, _ := testAPI()
		api.latestAgentVersion = "1.2.0"
		payload := validHTTPTestReport(t)
		payload.Agent.AgentVersion = agentVersion
		body, _ := json.Marshal(payload)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer node-token")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("version %s returned %d: %s", agentVersion, response.Code, response.Body.String())
		}
		var receipt report.ReportReceipt
		if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.AgentUpdate != nil {
			t.Errorf("version %s unexpectedly received update %+v", agentVersion, receipt.AgentUpdate)
		}
	}
}

func TestAdminRegistrationRequiresAdminToken(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/nodes", bytes.NewReader([]byte(`{"hostname":"node"}`)))
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || database.registered {
		t.Fatalf("unauthorized registration returned %d, registered=%v", response.Code, database.registered)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/nodes", bytes.NewReader([]byte(`{"hostname":"node"}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response = httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !database.registered {
		t.Fatalf("authorized registration returned %d, registered=%v", response.Code, database.registered)
	}
}

func TestPublicNodeListDoesNotRequireAuthentication(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected public list to return 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestHomepageSummaryIsPublicAndReturnsCounts(t *testing.T) {
	api, database := testAPI()
	database.homepageSummary = store.HomepageSummary{Normal: 8, Offline: 2, Alerts: 1}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/homepage", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload store.HomepageSummary
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != database.homepageSummary {
		t.Fatalf("unexpected Homepage summary: %+v", payload)
	}
}

func TestPublicNodeListIncludesLoadAveragesAndCapacities(t *testing.T) {
	api, database := testAPI()
	database.nodes = []store.NodeSummary{{
		NodeID: "10000000-0000-4000-8000-000000000001",
		Load1:  1.25, Load5: 0.75, Load15: 0.5,
		MemoryTotalBytes: 8 << 30, DiskTotalBytes: 100 << 30, HasNVIDIAGPU: true,
		Tags: []string{"production", "gpu"},
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Nodes []store.NodeSummary `json:"nodes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Nodes) != 1 || payload.Nodes[0].Load1 != 1.25 || payload.Nodes[0].Load5 != 0.75 || payload.Nodes[0].Load15 != 0.5 {
		t.Fatalf("unexpected load averages: %+v", payload.Nodes)
	}
	if payload.Nodes[0].MemoryTotalBytes != 8<<30 || payload.Nodes[0].DiskTotalBytes != 100<<30 {
		t.Fatalf("unexpected capacities: %+v", payload.Nodes[0])
	}
	if !payload.Nodes[0].HasNVIDIAGPU {
		t.Fatal("NVIDIA GPU capability was omitted from the node list")
	}
	if len(payload.Nodes[0].Tags) != 2 || payload.Nodes[0].Tags[1] != "gpu" {
		t.Fatalf("unexpected node tags: %+v", payload.Nodes[0].Tags)
	}
}

func TestNodeExportRequiresAdminToken(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes/export", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
}

func TestNodeExportReturnsExcelWorkbook(t *testing.T) {
	api, database := testAPI()
	nodeID := "10000000-0000-4000-8000-000000000001"
	summary := store.NodeSummary{NodeID: nodeID, AgentID: "20000000-0000-4000-8000-000000000001", DisplayName: "生产节点", Hostname: "server-01", SystemModel: "ThinkSystem SR630 -[7X02CTO1WW]-"}
	database.nodes = []store.NodeSummary{summary}
	database.nodeDetails = map[string]store.NodeDetail{nodeID: {Node: summary}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes/export", nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != spreadsheetContentType {
		t.Fatalf("unexpected content type: %q", contentType)
	}
	if !strings.Contains(response.Header().Get("Content-Disposition"), "server-status-nodes-") {
		t.Fatalf("unexpected content disposition: %q", response.Header().Get("Content-Disposition"))
	}
	if response.Body.Len() == 0 {
		t.Fatal("expected workbook response body")
	}
}

func TestWebDashboardIsServed(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dashboard to return 200, got %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("unexpected dashboard content type: %s", contentType)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`/assets/favicon.ico`)) {
		t.Fatal("dashboard does not reference the favicon")
	}
	for _, fragment := range []string{`id="alerts-button"`, `href="/alerts"`, `target="_blank"`, `rel="noopener"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(fragment)) {
			t.Errorf("dashboard alert link does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{`id="delete-node-dialog"`, `id="delete-node-admin-token"`, `id="confirm-delete-node"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(fragment)) {
			t.Errorf("dashboard node deletion dialog does not contain %q", fragment)
		}
	}
}

func TestWebDashboardDeletesNodesThroughAdminAPI(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dashboard script to return 200, got %d", response.Code)
	}
	for _, fragment := range []string{
		`data-delete-node`,
		`method: "DELETE"`,
		`Authorization: ` + "`Bearer ${token}`",
		`response.headers.get("Location")`,
		`status.status !== "completed"`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("dashboard node deletion script does not contain %q", fragment)
		}
	}
}

func TestAlertPageAndScriptAreServed(t *testing.T) {
	api, _ := testAPI()
	for path, expected := range map[string][]string{
		"/alerts":           {"告警日志", "alert-category-filter", "/assets/alerts.js"},
		"/alerts/":          {"告警日志", "alerts-table-body"},
		"/assets/alerts.js": {"/api/v1/alerts", "next_cursor", "30000", "data-alert-state"},
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", path, response.Code)
		}
		for _, fragment := range expected {
			if !strings.Contains(response.Body.String(), fragment) {
				t.Errorf("GET %s does not contain %q", path, fragment)
			}
		}
	}
}

func TestListAlertsDefaultsAndIsPublic(t *testing.T) {
	api, database := testAPI()
	database.alerts = store.AlertList{
		Alerts:  []store.AlertEvent{{AlertID: "40000000-0000-4000-8000-000000000001", Severity: "critical"}},
		Summary: store.AlertSummary{OpenTotal: 1, Critical: 1, AffectedNodes: 1},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if database.alertOptions.State != "open" || database.alertOptions.Since != nil || database.alertOptions.Limit != 0 {
		t.Fatalf("unexpected default options: %#v", database.alertOptions)
	}
	if !strings.Contains(response.Body.String(), `"open_total":1`) {
		t.Fatalf("response does not contain alert summary: %s", response.Body.String())
	}
}

func TestListAlertsPassesFiltersAndHistoryRange(t *testing.T) {
	api, database := testAPI()
	before := time.Now().UTC().Add(-30 * 24 * time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?state=resolved&category=smart&severity=warning&node_id=10000000-0000-4000-8000-000000000001&range=30d&limit=25&cursor=next", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	options := database.alertOptions
	if options.State != "resolved" || options.Category != "smart" || options.Severity != "warning" || options.Limit != 25 || options.Cursor != "next" {
		t.Fatalf("filters were not passed to store: %#v", options)
	}
	if options.Since == nil || options.Since.Before(before) || options.Since.After(time.Now().UTC().Add(-30*24*time.Hour+time.Second)) {
		t.Fatalf("unexpected history start time: %v", options.Since)
	}
}

func TestListAlertsRejectsInvalidParameters(t *testing.T) {
	tests := []string{
		"state=active", "category=memory", "severity=info", "node_id=bad", "state=resolved&range=all", "limit=0", "limit=201", "limit=many",
	}
	for _, query := range tests {
		api, _ := testAPI()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?"+query, nil)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q: expected 400, got %d", query, response.Code)
		}
	}
}

func TestListAlertsRejectsInvalidCursorFromStore(t *testing.T) {
	api, database := testAPI()
	database.alertsErr = store.ErrInvalidAlertQuery
	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?cursor=bad", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestFaviconIsServed(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/favicon.ico", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected favicon to return 200, got %d", response.Code)
	}
	if !bytes.HasPrefix(response.Body.Bytes(), []byte{0, 0, 1, 0, 3, 0}) {
		t.Fatal("favicon does not contain the expected three-image ICO header")
	}
}

func TestNVIDIAIconIsServed(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/nvidia.svg", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected NVIDIA icon to return 200, got %d", response.Code)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("#76B900")) {
		t.Fatal("NVIDIA icon does not contain the supplied brand color")
	}
}

func TestAgentInstallerIsServedWithoutCaching(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/install-agent.sh", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected installer to return 200, got %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/x-shellscript; charset=utf-8" {
		t.Fatalf("unexpected installer content type: %s", contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("unexpected installer cache policy: %s", cacheControl)
	}
	for _, expected := range []string{
		"x86_64|amd64", "aarch64|arm64",
		"/agent/releases/latest", "/agent/releases/v$version",
		"server-status-agent-linux-$architecture", "checksums.txt",
		"central release cache", "/opt/server-agent", "exec setsid /opt/server-agent/server-status-agent",
		"# server-status-agent-managed",
	} {
		if !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("installer response does not contain %q", expected)
		}
	}
	if bytes.Contains(response.Body.Bytes(), []byte("github.com")) {
		t.Fatal("installer must not require target nodes to access GitHub")
	}
}

func TestMacOSAgentInstallerIsServedWithoutCaching(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/install-agent-macos.sh", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected macOS installer to return 200, got %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/x-shellscript; charset=utf-8" {
		t.Fatalf("unexpected installer content type: %s", contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("unexpected installer cache policy: %s", cacheControl)
	}
	for _, expected := range []string{
		"Darwin", "/agent/releases/latest", "/agent/releases/v$version",
		"server-status-agent-macos", "shasum -a 256", "LaunchDaemons",
		"launchctl bootstrap system", "/Library/Application Support/ServerStatus",
		`installed_version=$("$AGENT" --version`,
	} {
		if !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("macOS installer response does not contain %q", expected)
		}
	}
	if bytes.Contains(response.Body.Bytes(), []byte("github.com")) {
		t.Fatal("macOS installer must not require target nodes to access GitHub")
	}
}

func TestWebUIRunsMacOSInstallerInsideForegroundSudo(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	body := response.Body.String()
	start := strings.Index(body, `if (platform === "macos") {`)
	if start < 0 {
		t.Fatal("macOS install command branch is missing")
	}
	macOSBranch := body[start:]
	end := strings.Index(macOSBranch, "\n    }\n")
	if end < 0 {
		t.Fatal("macOS install command branch is incomplete")
	}
	macOSBranch = macOSBranch[:end]
	for _, expected := range []string{
		`sudo env ${variables.join(" ")}`,
		`sh -c "curl -fsSL`,
		`/install-agent-macos.sh`,
		`| sh"`,
	} {
		if !strings.Contains(macOSBranch, expected) {
			t.Errorf("macOS install command does not contain %q", expected)
		}
	}
	if strings.Contains(macOSBranch, "| sudo env") {
		t.Error("macOS install command must not place sudo on the right side of a pipeline")
	}
}

func TestWebUIIncludesWindowsAgentUpgradeCommand(t *testing.T) {
	api, _ := testAPI()
	for path, expected := range map[string][]string{
		"/": {"agent-update-dialog", "copy-agent-update-command"},
		"/assets/app.js": {
			"data-agent-upgrade", "server-status-agent-upgrade.exe", "Get-Variable -Name LASTEXITCODE",
			"System.IO.File]::Delete", "Windows Agent upgrade failed",
			"powershell.exe -NoLogo -NoProfile -NonInteractive", "System.Net.WebClient",
			"System.Environment]::CurrentDirectory",
		},
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", path, response.Code)
		}
		for _, value := range expected {
			if !strings.Contains(response.Body.String(), value) {
				t.Errorf("GET %s does not contain %q", path, value)
			}
		}
		if path == "/assets/app.js" && strings.Contains(strings.ToLower(response.Body.String()), "bitsadmin") {
			t.Error("Windows commands must not use deprecated BITSAdmin")
		}
	}
}

func TestWebUIBuildsAtomicWindowsCommands(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}

	script := response.Body.String()
	blocks := map[string][2]string{
		"install": {`if (platform.startsWith("windows-")) {`, `if (platform === "macos") {`},
		"upgrade": {"function buildWindowsUpgradeCommand()", "function openAgentUpdateDialog(node)"},
	}
	for name, markers := range blocks {
		start := strings.Index(script, markers[0])
		if start < 0 {
			t.Fatalf("Windows %s command start was not found", name)
		}
		end := strings.Index(script[start:], markers[1])
		if end < 0 {
			t.Fatalf("Windows %s command end was not found", name)
		}
		block := script[start : start+end]
		if strings.Contains(block, `.join("\r\n")`) {
			t.Errorf("Windows %s command must not contain multiple pasted lines", name)
		}
		if !strings.Contains(block, "buildWindowsPowerShellCommand(") {
			t.Errorf("Windows %s command must use one PowerShell invocation", name)
		}
	}
}

func TestWebUILoadsNodeDetailBeforeHistory(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	script := response.Body.String()
	for _, fragment := range []string{
		`resolution: "loading"`, "historyResolutionLabel", `if (resolution === "5minute")`,
		"updateHistoryCharts", "historyRequestSequence",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("progressive history loading does not contain %q", fragment)
		}
	}
}

func TestWebUIPrioritizesReportedSMARTStatus(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	script := response.Body.String()
	statusCheck := strings.Index(script, `if (item.smart_status === "passed" || item.smart_status === "failed")`)
	unavailableCheck := strings.Index(script, `if (!item.smart_available) return "SMART 不可用"`)
	if statusCheck < 0 || unavailableCheck < 0 || statusCheck > unavailableCheck {
		t.Fatal("SMART pass/fail status must take precedence over a missing availability flag")
	}
	for _, fragment := range []string{`item.raid_passthrough ? "（RAID 透传）" : ""`, `return "SMART 状态未知"`} {
		if !strings.Contains(script, fragment) {
			t.Errorf("SMART status messaging does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{
		`scsi_grown_defects`, `scsi_ecc_corrected_delta`, `scsi_non_medium_errors_delta`,
		`本周期 SAS ECC 纠错增加`, `本周期 SAS 非介质错误增加`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("SAS health UI does not contain %q", fragment)
		}
	}
}

func TestWebUIShowsKVMVirtualMachineInventory(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	for _, fragment := range []string{
		"detail.kvm_virtual_machines", "function kvmSection(items)", "KVM 虚拟机",
		"名称 / UUID", "item.vcpus", "item.memory_bytes", "item.disk_bytes", "item.os_name",
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("KVM inventory UI does not contain %q", fragment)
		}
	}
}

func TestWebUIShowsRAIDControllerTopologyAndRebuildProgress(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	script := response.Body.String()
	for _, fragment := range []string{
		"raidSection(detail.raid_controllers)", "RAID 控制器", "机箱:槽位",
		"item.interface", "item.media_type", "item.rebuild_progress", "estimated_time_left",
		`dgrd: "降级"`, `jbod: "JBOD"`, `onln: "在线"`, `rbld: "重建中"`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("RAID controller UI does not contain %q", fragment)
		}
	}
}

func TestWebUIShowsHardwareSensorsOnlyForPhysicalNodes(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js returned %d", response.Code)
	}
	script := response.Body.String()
	for _, fragment := range []string{
		`isPhysicalNode(node) ? hardwareSensorSection(detail.hardware_sensors) : ""`,
		"风扇转速", "电压", "电流", "功率", "item.alarm", "item.fault",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("hardware sensor UI does not contain %q", fragment)
		}
	}
}

func TestHistoryRangeValidation(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/10000000-0000-4000-8000-000000000001/history?range=forever", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid range to return 400, got %d", response.Code)
	}
}

func TestNodeHistoryResponseIsCompressedCachedAndRevalidated(t *testing.T) {
	api, database := testAPI()
	nodeID := "10000000-0000-4000-8000-000000000001"
	database.history = store.NodeHistory{
		NodeID: nodeID, Range: "24h", Resolution: "5minute",
		Points: []store.HistoryPoint{{BucketAt: time.Now().UTC(), CPUUsagePercent: 25}},
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/"+nodeID+"/history?range=24h", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected history response 200, got %d", response.Code)
	}
	if response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("history response was not compressed")
	}
	if response.Header().Get("X-Server-Status-Cache") != "MISS" {
		t.Fatal("first history response was not marked as a cache miss")
	}
	if !strings.Contains(response.Header().Get("Server-Timing"), "history-db") {
		t.Fatal("history response does not expose database timing")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	_ = compressed.Close()
	var decoded store.NodeHistory
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Resolution != "5minute" {
		t.Fatalf("unexpected history resolution %q", decoded.Resolution)
	}

	etag := response.Header().Get("ETag")
	request = httptest.NewRequest(http.MethodGet, "/api/v1/nodes/"+nodeID+"/history?range=24h", nil)
	request.Header.Set("If-None-Match", etag)
	response = httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("expected cached history response 304, got %d", response.Code)
	}
	if response.Header().Get("X-Server-Status-Cache") != "HIT" {
		t.Fatal("second history response was not marked as a cache hit")
	}
	if calls := database.historyCalls.Load(); calls != 1 {
		t.Fatalf("expected one history store call, got %d", calls)
	}
}

func TestSetPrimaryNetworkInterfaceRequiresAdminToken(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/primary-network-interface", bytes.NewReader([]byte(`{"interface_id":"80000000-0000-4000-8000-000000000001"}`)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	if database.primaryNetworkInterfaceID != "" {
		t.Fatal("unauthorized preference change reached the store")
	}
}

func TestDeleteNodeRequiresAdminToken(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	if database.deletedNodeID != "" {
		t.Fatal("unauthorized deletion reached the store")
	}
}

func TestDeleteNodeEnqueuesRequestedNode(t *testing.T) {
	database := &controlledDeleteStore{
		fakeStore: &fakeStore{},
		started:   make(chan string, 1),
		release:   make(chan struct{}),
	}
	api := testAPIWithStore(database)
	defer api.Close()
	nodeID := "10000000-0000-4000-8000-000000000001"
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/"+nodeID, nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	statusURL := "/api/v1/admin/node-deletions/" + nodeID
	if location := response.Header().Get("Location"); location != statusURL {
		t.Fatalf("deletion location %q, want %q", location, statusURL)
	}
	var accepted nodeDeletionTask
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted deletion: %v", err)
	}
	if accepted.NodeID != nodeID || accepted.Status != "queued" || accepted.StatusURL != statusURL {
		t.Fatalf("unexpected accepted deletion: %+v", accepted)
	}

	select {
	case deletedNodeID := <-database.started:
		if deletedNodeID != nodeID {
			t.Fatalf("deleted node %q, want %q", deletedNodeID, nodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("background deletion did not start")
	}

	statusRequest := httptest.NewRequest(http.MethodGet, statusURL, nil)
	statusRequest.Header.Set("Authorization", "Bearer "+testAdminToken)
	statusResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("expected deletion status 200, got %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	var running nodeDeletionTask
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &running); err != nil {
		t.Fatalf("decode running deletion: %v", err)
	}
	if running.Status != "running" || running.StartedAt == nil {
		t.Fatalf("unexpected running deletion: %+v", running)
	}

	close(database.release)
	completed := waitForNodeDeletionStatus(t, api, nodeID, "completed")
	if completed.FinishedAt == nil || completed.Error != "" {
		t.Fatalf("unexpected completed deletion: %+v", completed)
	}
}

func TestDeleteNodeDeduplicatesActiveTask(t *testing.T) {
	database := &controlledDeleteStore{
		fakeStore: &fakeStore{},
		started:   make(chan string, 2),
		release:   make(chan struct{}),
	}
	api := testAPIWithStore(database)
	defer api.Close()
	nodeID := "10000000-0000-4000-8000-000000000001"

	for range 2 {
		request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/"+nodeID, nil)
		request.Header.Set("Authorization", "Bearer "+testAdminToken)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
		}
	}

	select {
	case <-database.started:
	case <-time.After(time.Second):
		t.Fatal("background deletion did not start")
	}
	if calls := database.calls.Load(); calls != 1 {
		t.Fatalf("expected one background deletion, got %d", calls)
	}
	close(database.release)
	waitForNodeDeletionStatus(t, api, nodeID, "completed")
}

func TestDeleteNodeRecordsBackgroundFailure(t *testing.T) {
	database := &controlledDeleteStore{
		fakeStore: &fakeStore{},
		started:   make(chan string, 1),
		result:    errors.New("database unavailable"),
	}
	api := testAPIWithStore(database)
	defer api.Close()
	nodeID := "10000000-0000-4000-8000-000000000001"
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/"+nodeID, nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}

	failed := waitForNodeDeletionStatus(t, api, nodeID, "failed")
	if failed.Error != "cannot delete node" || failed.FinishedAt == nil {
		t.Fatalf("unexpected failed deletion: %+v", failed)
	}
}

func TestDeleteNodeTreatsAlreadyAbsentNodeAsCompleted(t *testing.T) {
	database := &controlledDeleteStore{
		fakeStore: &fakeStore{},
		started:   make(chan string, 1),
		result:    store.ErrNotFound,
	}
	api := testAPIWithStore(database)
	defer api.Close()
	nodeID := "10000000-0000-4000-8000-000000000001"
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/"+nodeID, nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	waitForNodeDeletionStatus(t, api, nodeID, "completed")
}

func TestDeleteNodeRejectsInvalidNodeID(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/not-a-uuid", nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if database.deletedNodeID != "" {
		t.Fatal("invalid node id reached the store")
	}
}

func TestSetPrimaryNetworkInterfaceUpdatesActiveInterface(t *testing.T) {
	api, database := testAPI()
	nodeID := "10000000-0000-4000-8000-000000000001"
	interfaceID := "80000000-0000-4000-8000-000000000001"
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/"+nodeID+"/primary-network-interface", bytes.NewReader([]byte(`{"interface_id":"`+interfaceID+`"}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if database.primaryNetworkNodeID != nodeID || database.primaryNetworkInterfaceID != interfaceID {
		t.Fatalf("unexpected preference update: node=%q interface=%q", database.primaryNetworkNodeID, database.primaryNetworkInterfaceID)
	}
}

func TestSetPrimaryNetworkInterfaceRejectsInvalidInterfaceID(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/primary-network-interface", bytes.NewReader([]byte(`{"interface_id":"eth0"}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if database.primaryNetworkInterfaceID != "" {
		t.Fatal("invalid interface id reached the store")
	}
}

func TestSetPrimaryNetworkInterfaceReturnsNotFoundForInactiveInterface(t *testing.T) {
	api, database := testAPI()
	database.primaryNetworkPreferenceErr = store.ErrNotFound
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/primary-network-interface", bytes.NewReader([]byte(`{"interface_id":"80000000-0000-4000-8000-000000000001"}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateNodeTagsRequiresAdminToken(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/tags", bytes.NewReader([]byte(`{"tags":["production"]}`)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	if database.tagNodeID != "" {
		t.Fatal("unauthorized tag update reached the store")
	}
}

func TestUpdateNodeTagsNormalizesValues(t *testing.T) {
	api, database := testAPI()
	nodeID := "10000000-0000-4000-8000-000000000001"
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/"+nodeID+"/tags", bytes.NewReader([]byte(`{"tags":[" production ","GPU"]}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if database.tagNodeID != nodeID || len(database.tags) != 2 || database.tags[0] != "production" || database.tags[1] != "GPU" {
		t.Fatalf("unexpected tag update: node=%q tags=%+v", database.tagNodeID, database.tags)
	}
}

func TestUpdateNodeTagsCanClearValues(t *testing.T) {
	api, database := testAPI()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/tags", bytes.NewReader([]byte(`{"tags":[]}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(database.tags) != 0 {
		t.Fatalf("expected tags to be cleared, status=%d tags=%+v", response.Code, database.tags)
	}
}

func TestUpdateNodeTagsReturnsNotFound(t *testing.T) {
	api, database := testAPI()
	database.tagUpdateErr = store.ErrNotFound
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/tags", bytes.NewReader([]byte(`{"tags":["production"]}`)))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateNodeTagsRejectsInvalidValues(t *testing.T) {
	tests := []string{
		`{"tags":["1","2","3","4","5","6"]}`,
		`{"tags":["GPU","gpu"]}`,
		`{"tags":[""]}`,
	}
	for _, body := range tests {
		api, database := testAPI()
		request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nodes/10000000-0000-4000-8000-000000000001/tags", bytes.NewReader([]byte(body)))
		request.Header.Set("Authorization", "Bearer "+testAdminToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %s: expected 400, got %d: %s", body, response.Code, response.Body.String())
		}
		if database.tagNodeID != "" {
			t.Errorf("body %s: invalid tags reached the store", body)
		}
	}
}

func testAPI() (*API, *fakeStore) {
	database := &fakeStore{auth: store.NodeAuth{
		TokenID: "30000000-0000-4000-8000-000000000001",
		NodeID:  "10000000-0000-4000-8000-000000000001",
		AgentID: "20000000-0000-4000-8000-000000000001",
	}}
	return testAPIWithStore(database), database
}

func testAPIWithStore(database dataStore) *API {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewAPI(database, testAdminToken, logger, "")
}

func waitForNodeDeletionStatus(t *testing.T, api *API, nodeID, want string) nodeDeletionTask {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		task, ok := api.nodeDeletions.Status(nodeID)
		if ok && task.Status == want {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := api.nodeDeletions.Status(nodeID)
	t.Fatalf("deletion status %q, want %q", task.Status, want)
	return nodeDeletionTask{}
}

func validHTTPTestReport(t *testing.T) report.Report {
	t.Helper()
	inventory := report.Inventory{
		CPUPackages:       []report.CPUPackage{{Key: "cpu-0", ModelName: "Test CPU", PhysicalCores: 4, LogicalThreads: 8}},
		Filesystems:       []report.Filesystem{{Key: "root", DeviceName: "/dev/sda1", FilesystemType: "ext4", MountPoint: "/"}},
		NetworkInterfaces: []report.NetworkInterface{{Key: "eth0", Name: "eth0", Addresses: []report.NetworkAddress{{Address: "10.0.0.1/24", Scope: "private"}}}},
	}
	fingerprint, err := report.InventoryFingerprint(inventory)
	if err != nil {
		t.Fatal(err)
	}
	return report.Report{
		Version: report.Version, CollectedAt: time.Now().UTC(), IntervalSeconds: 60,
		InventoryFingerprint: fingerprint,
		Agent:                report.AgentInfo{ID: "20000000-0000-4000-8000-000000000001", Hostname: "node", OSName: "linux", Architecture: "amd64", AgentVersion: "test"},
		Inventory:            inventory,
		Metrics: report.Metrics{
			CPU:         report.CPUMetrics{UsagePercent: 20},
			Memory:      report.MemoryMetrics{TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 400},
			Filesystems: []report.FilesystemMetrics{{FilesystemKey: "root", TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 400}},
			Network:     []report.NetworkMetrics{{InterfaceKey: "eth0", LinkUp: true}},
		},
	}
}
