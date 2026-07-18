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
	return []store.NodeSummary{}, nil
}
func (fake *fakeStore) GetNode(context.Context, string) (store.NodeDetail, error) {
	return store.NodeDetail{}, store.ErrNotFound
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

func testAPI() (*API, *fakeStore) {
	database := &fakeStore{auth: store.NodeAuth{
		TokenID: "30000000-0000-4000-8000-000000000001",
		NodeID:  "10000000-0000-4000-8000-000000000001",
		AgentID: "20000000-0000-4000-8000-000000000001",
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewAPI(database, testAdminToken, logger), database
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
