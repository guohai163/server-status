package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/guohai/server-status/internal/store"
)

const testAdminToken = "admin-token-with-at-least-thirty-two-characters"

type fakeStore struct {
	auth       store.NodeAuth
	ingested   bool
	registered bool
	nodes      []store.NodeSummary
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
func (fake *fakeStore) GetNode(context.Context, string) (store.NodeDetail, error) {
	return store.NodeDetail{}, store.ErrNotFound
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
		MemoryTotalBytes: 8 << 30, DiskTotalBytes: 100 << 30,
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

func TestHistoryRangeValidation(t *testing.T) {
	api, _ := testAPI()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/10000000-0000-4000-8000-000000000001/history?range=forever", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid range to return 400, got %d", response.Code)
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
