package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/guohai/server-status/internal/store"
	appversion "github.com/guohai/server-status/internal/version"
)

type API struct {
	store              dataStore
	logger             *slog.Logger
	adminDigest        [32]byte
	releases           *releaseCache
	historyResponses   *historyResponseCache
	latestAgentVersion string
	mux                *http.ServeMux
}

type dataStore interface {
	Ready(context.Context) error
	AuthenticateToken(context.Context, string) (store.NodeAuth, error)
	Ingest(context.Context, store.NodeAuth, report.Report) error
	RegisterNode(context.Context, store.RegisterNodeInput) (store.NodeCredentials, error)
	ListNodes(context.Context) ([]store.NodeSummary, error)
	GetNode(context.Context, string) (store.NodeDetail, error)
	SetPrimaryNetworkInterface(context.Context, string, string) error
	UpdateNodeTags(context.Context, string, []string) error
	GetNodeHistory(context.Context, string, string) (store.NodeHistory, error)
}

func NewAPI(database dataStore, adminToken string, logger *slog.Logger, releaseCacheDir string) *API {
	latestAgentVersion, _ := appversion.Normalize(Version)
	api := &API{
		store:              database,
		logger:             logger,
		adminDigest:        sha256.Sum256([]byte(adminToken)),
		releases:           newReleaseCache(releaseCacheDir, logger),
		historyResponses:   newHistoryResponseCache(30*time.Second, 256),
		latestAgentVersion: latestAgentVersion,
		mux:                http.NewServeMux(),
	}
	api.mux.HandleFunc("GET /healthz", api.health)
	api.mux.HandleFunc("GET /readyz", api.ready)
	api.mux.HandleFunc("POST /api/v1/reports", api.report)
	api.mux.HandleFunc("GET /api/v1/nodes", api.listNodes)
	api.mux.HandleFunc("GET /api/v1/nodes/{nodeID}", api.getNode)
	api.mux.HandleFunc("GET /api/v1/nodes/{nodeID}/history", api.getNodeHistory)
	api.mux.HandleFunc("POST /api/v1/admin/nodes", api.requireAdmin(api.registerNode))
	api.mux.HandleFunc("GET /api/v1/admin/nodes", api.requireAdmin(api.listNodes))
	api.mux.HandleFunc("GET /api/v1/admin/nodes/export", api.requireAdmin(api.exportNodes))
	api.mux.HandleFunc("GET /api/v1/admin/nodes/{nodeID}", api.requireAdmin(api.getNode))
	api.mux.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/primary-network-interface", api.requireAdmin(api.setPrimaryNetworkInterface))
	api.mux.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/tags", api.requireAdmin(api.updateNodeTags))
	api.mux.HandleFunc("GET /agent/releases/{version}/{asset}", api.releaseAsset)
	api.registerWebRoutes()
	return api
}

func (api *API) Handler() http.Handler {
	return api.securityHeaders(api.logging(api.mux))
}

func (api *API) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) ready(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 3*time.Second)
	defer cancel()
	if err := api.store.Ready(ctx); err != nil {
		api.logger.Error("readiness check failed", "error", err)
		writeError(response, http.StatusServiceUnavailable, "database is not ready")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
}

func (api *API) report(response http.ResponseWriter, request *http.Request) {
	rawToken, ok := bearerToken(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "missing bearer token")
		return
	}
	ctx, cancel := contextWithTimeout(request, 25*time.Second)
	defer cancel()
	auth, err := api.store.AuthenticateToken(ctx, rawToken)
	if errors.Is(err, store.ErrUnauthorized) {
		writeError(response, http.StatusUnauthorized, "invalid node token")
		return
	}
	if err != nil {
		api.logger.Error("node authentication failed", "error", err)
		writeError(response, http.StatusInternalServerError, "authentication failed")
		return
	}
	var payload report.Report
	if err := decodeJSON(response, request, &payload); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Agent.ID != auth.AgentID {
		writeError(response, http.StatusForbidden, "agent id does not match token")
		return
	}
	if err := payload.Validate(time.Now().UTC()); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := api.store.Ingest(ctx, auth, payload); err != nil {
		if errors.Is(err, store.ErrInvalidResource) {
			writeError(response, http.StatusUnprocessableEntity, err.Error())
			return
		}
		api.logger.Error("report ingestion failed", "node_id", auth.NodeID, "error", err)
		writeError(response, http.StatusInternalServerError, "report ingestion failed")
		return
	}
	receipt := report.ReportReceipt{
		Status:   "accepted",
		BucketAt: payload.CollectedAt.UTC().Truncate(time.Minute),
	}
	if comparison, ok := appversion.Compare(payload.Agent.AgentVersion, api.latestAgentVersion); ok && comparison < 0 {
		receipt.AgentUpdate = &report.AgentUpdate{Version: api.latestAgentVersion}
	}
	writeJSON(response, http.StatusAccepted, receipt)
}

func (api *API) registerNode(response http.ResponseWriter, request *http.Request) {
	var input store.RegisterNodeInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.AgentID != "" && !report.ValidUUID(input.AgentID) {
		writeError(response, http.StatusBadRequest, "agent_id must be an RFC 4122 UUID")
		return
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	credentials, err := api.store.RegisterNode(ctx, input)
	if err != nil {
		api.logger.Error("node registration failed", "error", err)
		writeError(response, http.StatusInternalServerError, "node registration failed")
		return
	}
	writeJSON(response, http.StatusCreated, credentials)
}

func (api *API) listNodes(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	nodes, err := api.store.ListNodes(ctx)
	if err != nil {
		api.logger.Error("list nodes failed", "error", err)
		writeError(response, http.StatusInternalServerError, "cannot list nodes")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"nodes": nodes})
}

func (api *API) exportNodes(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 30*time.Second)
	defer cancel()
	nodes, err := api.store.ListNodes(ctx)
	if err != nil {
		api.logger.Error("list nodes for export failed", "error", err)
		writeError(response, http.StatusInternalServerError, "cannot list nodes for export")
		return
	}
	details := make([]store.NodeDetail, 0, len(nodes))
	for _, node := range nodes {
		detail, err := api.store.GetNode(ctx, node.NodeID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			api.logger.Error("get node for export failed", "node_id", node.NodeID, "error", err)
			writeError(response, http.StatusInternalServerError, "cannot export node information")
			return
		}
		details = append(details, detail)
	}
	workbook, err := buildNodeExportWorkbook(details)
	if err != nil {
		api.logger.Error("build node export failed", "error", err)
		writeError(response, http.StatusInternalServerError, "cannot build Excel export")
		return
	}
	now := time.Now().UTC()
	response.Header().Set("Content-Type", spreadsheetContentType)
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="server-status-nodes-%s.xlsx"`, now.Format("20060102-150405")))
	response.WriteHeader(http.StatusOK)
	if _, err := response.Write(workbook); err != nil {
		api.logger.Error("write node export failed", "error", err)
	}
}

func (api *API) getNode(response http.ResponseWriter, request *http.Request) {
	nodeID := request.PathValue("nodeID")
	if !report.ValidUUID(nodeID) {
		writeError(response, http.StatusBadRequest, "invalid node id")
		return
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	detail, err := api.store.GetNode(ctx, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		api.logger.Error("get node failed", "node_id", nodeID, "error", err)
		writeError(response, http.StatusInternalServerError, "cannot get node")
		return
	}
	writeJSON(response, http.StatusOK, detail)
}

type primaryNetworkInterfaceInput struct {
	InterfaceID string `json:"interface_id"`
}

type nodeTagsInput struct {
	Tags []string `json:"tags"`
}

func normalizeNodeTags(tags []string) ([]string, error) {
	if len(tags) > 5 {
		return nil, errors.New("a node can have at most 5 tags")
	}
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, errors.New("tags cannot be empty")
		}
		if len([]rune(tag)) > 32 {
			return nil, errors.New("each tag must be at most 32 characters")
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			return nil, errors.New("tags must be unique")
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized, nil
}

func (api *API) updateNodeTags(response http.ResponseWriter, request *http.Request) {
	nodeID := request.PathValue("nodeID")
	if !report.ValidUUID(nodeID) {
		writeError(response, http.StatusBadRequest, "invalid node id")
		return
	}
	var input nodeTagsInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	tags, err := normalizeNodeTags(input.Tags)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	if err := api.store.UpdateNodeTags(ctx, nodeID, tags); errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "node not found")
		return
	} else if err != nil {
		api.logger.Error("update node tags failed", "node_id", nodeID, "error", err)
		writeError(response, http.StatusInternalServerError, "cannot update node tags")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"tags": tags})
}

// setPrimaryNetworkInterface changes the interface used for the node card IP and IP ordering.
func (api *API) setPrimaryNetworkInterface(response http.ResponseWriter, request *http.Request) {
	nodeID := request.PathValue("nodeID")
	if !report.ValidUUID(nodeID) {
		writeError(response, http.StatusBadRequest, "invalid node id")
		return
	}
	var input primaryNetworkInterfaceInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if !report.ValidUUID(input.InterfaceID) {
		writeError(response, http.StatusBadRequest, "interface_id must be an RFC 4122 UUID")
		return
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	if err := api.store.SetPrimaryNetworkInterface(ctx, nodeID, input.InterfaceID); errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "active network interface not found for node")
		return
	} else if err != nil {
		api.logger.Error("set primary network interface failed", "node_id", nodeID, "interface_id", input.InterfaceID, "error", err)
		writeError(response, http.StatusInternalServerError, "cannot set primary network interface")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"interface_id": input.InterfaceID})
}

func (api *API) getNodeHistory(response http.ResponseWriter, request *http.Request) {
	nodeID := request.PathValue("nodeID")
	if !report.ValidUUID(nodeID) {
		writeError(response, http.StatusBadRequest, "invalid node id")
		return
	}
	window := request.URL.Query().Get("range")
	if window == "" {
		window = "24h"
	}
	if !store.ValidHistoryRange(window) {
		writeError(response, http.StatusBadRequest, "range must be one of 1h, 6h, 24h, 7d, 30d, or 90d")
		return
	}
	ctx, cancel := contextWithTimeout(request, 15*time.Second)
	defer cancel()
	cached, cacheHit, err := api.historyResponses.get(ctx, nodeID+":"+window, func(loadCtx context.Context) (store.NodeHistory, error) {
		return api.store.GetNodeHistory(loadCtx, nodeID, window)
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		api.logger.Error("get node history failed", "node_id", nodeID, "range", window, "error", err)
		writeError(response, http.StatusInternalServerError, "cannot get node history")
		return
	}
	writeHistoryResponse(response, request, cached, cacheHit)
}

func (api *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		token, ok := bearerToken(request)
		if !ok {
			writeError(response, http.StatusUnauthorized, "missing admin bearer token")
			return
		}
		digest := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(digest[:], api.adminDigest[:]) != 1 {
			writeError(response, http.StatusUnauthorized, "invalid admin bearer token")
			return
		}
		next(response, request)
	}
}

func (api *API) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		writer := &statusWriter{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(writer, request)
		api.logger.Info("http request", "method", request.Method, "path", request.URL.Path,
			"status", writer.status, "duration", time.Since(started), "remote", request.RemoteAddr)
	})
}

func (api *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *statusWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 4<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func bearerToken(request *http.Request) (string, bool) {
	value := strings.TrimSpace(request.Header.Get("Authorization"))
	prefix, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(prefix, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func contextWithTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}
