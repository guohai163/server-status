package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	nodes                       []store.NodeSummary
	nodeDetails                 map[string]store.NodeDetail
	primaryNetworkNodeID        string
	primaryNetworkInterfaceID   string
	primaryNetworkPreferenceErr error
	tagNodeID                   string
	tags                        []string
	tagUpdateErr                error
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

func (fake *fakeStore) GetNode(_ context.Context, nodeID string) (store.NodeDetail, error) {
	if detail, ok := fake.nodeDetails[nodeID]; ok {
		return detail, nil
	}
	return store.NodeDetail{}, store.ErrNotFound
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
func (fake *fakeStore) GetNodeHistory(context.Context, string, string) (store.NodeHistory, error) {
	return store.NodeHistory{}, store.ErrNotFound
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
		"central release cache", "/opt/server-agent", "# server-status-agent-managed",
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

func TestWebUIIncludesWindowsAgentUpgradeCommand(t *testing.T) {
	api, _ := testAPI()
	for path, expected := range map[string][]string{
		"/": {"agent-update-dialog", "copy-agent-update-command"},
		"/assets/app.js": {
			"data-agent-upgrade", "server-status-agent-upgrade.exe", ".\\\\${filename} upgrade",
			`if exist "%CD%\\${filename}" del /q`,
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewAPI(database, testAdminToken, logger, ""), database
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
