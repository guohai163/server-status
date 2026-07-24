package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohai/server-status/internal/store"
	"golang.org/x/sync/singleflight"
)

type cachedHistoryResponse struct {
	body           []byte
	gzipBody       []byte
	etag           string
	expiresAt      time.Time
	queryDuration  time.Duration
	encodeDuration time.Duration
}

type historyResponseCache struct {
	mu         sync.RWMutex
	entries    map[string]cachedHistoryResponse
	requests   singleflight.Group
	ttl        time.Duration
	maxEntries int
}

func newHistoryResponseCache(ttl time.Duration, maxEntries int) *historyResponseCache {
	return &historyResponseCache{
		entries:    make(map[string]cachedHistoryResponse),
		ttl:        ttl,
		maxEntries: maxEntries,
	}
}

func (cache *historyResponseCache) get(
	ctx context.Context,
	key string,
	load func(context.Context) (store.NodeHistory, error),
) (cachedHistoryResponse, bool, error) {
	if response, ok := cache.cached(key, time.Now()); ok {
		return response, true, nil
	}

	result := cache.requests.DoChan(key, func() (any, error) {
		now := time.Now()
		if response, ok := cache.cached(key, now); ok {
			return response, nil
		}
		// A shared load survives the initiating request's cancellation but keeps its deadline.
		loadCtx := context.WithoutCancel(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			loadCtx, cancel = context.WithDeadline(loadCtx, deadline)
			defer cancel()
		}
		queryStarted := time.Now()
		history, err := load(loadCtx)
		if err != nil {
			return cachedHistoryResponse{}, err
		}
		queryDuration := time.Since(queryStarted)

		encodeStarted := time.Now()
		body, err := json.Marshal(history)
		if err != nil {
			return cachedHistoryResponse{}, fmt.Errorf("encode node history: %w", err)
		}
		var compressed bytes.Buffer
		compressor := gzip.NewWriter(&compressed)
		if _, err := compressor.Write(body); err != nil {
			return cachedHistoryResponse{}, fmt.Errorf("compress node history: %w", err)
		}
		if err := compressor.Close(); err != nil {
			return cachedHistoryResponse{}, fmt.Errorf("finish node history compression: %w", err)
		}
		digest := sha256.Sum256(body)
		completedAt := time.Now()
		response := cachedHistoryResponse{
			body:           body,
			gzipBody:       compressed.Bytes(),
			etag:           fmt.Sprintf(`W/"%x"`, digest),
			expiresAt:      completedAt.Add(cache.ttl),
			queryDuration:  queryDuration,
			encodeDuration: time.Since(encodeStarted),
		}
		cache.store(key, response, completedAt)
		return response, nil
	})

	select {
	case <-ctx.Done():
		return cachedHistoryResponse{}, false, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return cachedHistoryResponse{}, false, result.Err
		}
		response, ok := result.Val.(cachedHistoryResponse)
		if !ok {
			return cachedHistoryResponse{}, false, errInvalidHistoryCacheType
		}
		return response, false, nil
	}
}

var errInvalidHistoryCacheType = errors.New("history cache returned an invalid response type")

func (cache *historyResponseCache) cached(key string, now time.Time) (cachedHistoryResponse, bool) {
	cache.mu.RLock()
	response, ok := cache.entries[key]
	cache.mu.RUnlock()
	return response, ok && now.Before(response.expiresAt)
}

func (cache *historyResponseCache) store(key string, response cachedHistoryResponse, now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	for cachedKey, cached := range cache.entries {
		if !now.Before(cached.expiresAt) {
			delete(cache.entries, cachedKey)
		}
	}
	if cache.maxEntries > 0 && len(cache.entries) >= cache.maxEntries {
		var oldestKey string
		var oldestExpiry time.Time
		for cachedKey, cached := range cache.entries {
			if oldestKey == "" || cached.expiresAt.Before(oldestExpiry) {
				oldestKey = cachedKey
				oldestExpiry = cached.expiresAt
			}
		}
		delete(cache.entries, oldestKey)
	}
	cache.entries[key] = response
}

func writeHistoryResponse(response http.ResponseWriter, request *http.Request, cached cachedHistoryResponse, cacheHit bool) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=30")
	response.Header().Set("ETag", cached.etag)
	response.Header().Set("Vary", "Accept-Encoding")
	if cacheHit {
		response.Header().Set("X-Server-Status-Cache", "HIT")
		response.Header().Set("Server-Timing", `history-cache;desc="hit"`)
	} else {
		response.Header().Set("X-Server-Status-Cache", "MISS")
		response.Header().Set("Server-Timing", fmt.Sprintf("history-db;dur=%.1f, history-encode;dur=%.1f", durationMilliseconds(cached.queryDuration), durationMilliseconds(cached.encodeDuration)))
	}
	if etagMatches(request.Header.Get("If-None-Match"), cached.etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	body := cached.body
	if acceptsGzip(request.Header.Get("Accept-Encoding")) {
		response.Header().Set("Content-Encoding", "gzip")
		body = cached.gzipBody
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err == nil && quality <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
