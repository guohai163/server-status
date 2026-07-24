package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guohai/server-status/internal/store"
)

func TestHistoryResponseCacheCoalescesConcurrentLoads(t *testing.T) {
	cache := newHistoryResponseCache(time.Minute, 16)
	var loads atomic.Int32
	load := func(context.Context) (store.NodeHistory, error) {
		loads.Add(1)
		time.Sleep(25 * time.Millisecond)
		return store.NodeHistory{NodeID: "node", Range: "24h", Resolution: "5minute"}, nil
	}

	const requestCount = 8
	var wait sync.WaitGroup
	errors := make(chan error, requestCount)
	for range requestCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := cache.get(context.Background(), "node:24h", load)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count := loads.Load(); count != 1 {
		t.Fatalf("expected one coalesced history load, got %d", count)
	}
}

func TestAcceptsGzipHonorsDisabledQuality(t *testing.T) {
	if acceptsGzip("br, gzip;q=0.000") {
		t.Fatal("gzip with zero quality must not be accepted")
	}
	if !acceptsGzip("br, gzip;q=0.5") {
		t.Fatal("gzip with positive quality should be accepted")
	}
}
