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
)

type API struct {
	store       dataStore
	logger      *slog.Logger
	adminDigest [32]byte
	mux         *http.ServeMux
}

type dataStore interface {
	Ready(context.Context) error
	AuthenticateToken(context.Context, string) (store.NodeAuth, error)
	Ingest(context.Context, store.NodeAuth, report.Report) error
	RegisterNode(context.Context, store.RegisterNodeInput) (store.NodeCredentials, error)
	ListNodes(context.Context) ([]store.NodeSummary, error)
	GetNode(context.Context, string) (store.NodeDetail, error)
}

func NewAPI(database dataStore, adminToken string, logger *slog.Logger) *API {
	api := &API{
		store:       database,
		logger:      logger,
		adminDigest: sha256.Sum256([]byte(adminToken)),
		mux:         http.NewServeMux(),
	}
	api.mux.HandleFunc("GET /healthz", api.health)
	api.mux.HandleFunc("GET /readyz", api.ready)
	api.mux.HandleFunc("POST /api/v1/reports", api.report)
	api.mux.HandleFunc("POST /api/v1/admin/nodes", api.requireAdmin(api.registerNode))
	api.mux.HandleFunc("GET /api/v1/admin/nodes", api.requireAdmin(api.listNodes))
	api.mux.HandleFunc("GET /api/v1/admin/nodes/{nodeID}", api.requireAdmin(api.getNode))
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
	writeJSON(response, http.StatusAccepted, map[string]any{
		"status":    "accepted",
		"bucket_at": payload.CollectedAt.UTC().Truncate(time.Minute),
	})
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
