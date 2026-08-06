package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/guohai/server-status/internal/store"
)

const nodeDeletionTimeout = 10 * time.Minute

type nodeDeletionTask struct {
	NodeID      string     `json:"node_id"`
	Status      string     `json:"status"`
	StatusURL   string     `json:"status_url,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type nodeDeletionManager struct {
	store     dataStore
	logger    *slog.Logger
	onDeleted func(string)

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.RWMutex
	tasks  map[string]nodeDeletionTask
	closed bool
	wg     sync.WaitGroup
}

func newNodeDeletionManager(database dataStore, logger *slog.Logger, onDeleted func(string)) *nodeDeletionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &nodeDeletionManager{
		store:     database,
		logger:    logger,
		onDeleted: onDeleted,
		ctx:       ctx,
		cancel:    cancel,
		tasks:     make(map[string]nodeDeletionTask),
	}
}

func (manager *nodeDeletionManager) Enqueue(nodeID string) (nodeDeletionTask, bool) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nodeDeletionTask{}, false
	}
	if task, exists := manager.tasks[nodeID]; exists && task.Status != "failed" {
		manager.mu.Unlock()
		return task, true
	}
	task := nodeDeletionTask{
		NodeID:      nodeID,
		Status:      "queued",
		RequestedAt: time.Now().UTC(),
	}
	manager.tasks[nodeID] = task
	manager.wg.Add(1)
	manager.mu.Unlock()

	go manager.run(nodeID)
	return task, true
}

func (manager *nodeDeletionManager) Status(nodeID string) (nodeDeletionTask, bool) {
	manager.mu.RLock()
	task, ok := manager.tasks[nodeID]
	manager.mu.RUnlock()
	return task, ok
}

func (manager *nodeDeletionManager) Close() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	manager.mu.Unlock()

	manager.cancel()
	manager.wg.Wait()
}

func (manager *nodeDeletionManager) run(nodeID string) {
	defer manager.wg.Done()

	startedAt := time.Now().UTC()
	manager.mu.Lock()
	task := manager.tasks[nodeID]
	task.Status = "running"
	task.StartedAt = &startedAt
	manager.tasks[nodeID] = task
	manager.mu.Unlock()

	manager.logger.Info("node deletion started", "node_id", nodeID)
	ctx, cancel := context.WithTimeout(manager.ctx, nodeDeletionTimeout)
	err := manager.store.DeleteNode(ctx, nodeID)
	cancel()
	finishedAt := time.Now().UTC()

	manager.mu.Lock()
	task = manager.tasks[nodeID]
	task.FinishedAt = &finishedAt
	if err == nil || errors.Is(err, store.ErrNotFound) {
		task.Status = "completed"
		task.Error = ""
	} else {
		task.Status = "failed"
		task.Error = "cannot delete node"
	}
	manager.tasks[nodeID] = task
	manager.mu.Unlock()

	if task.Status == "failed" {
		manager.logger.Error("node deletion failed", "node_id", nodeID, "duration", finishedAt.Sub(startedAt), "error", err)
		return
	}
	if manager.onDeleted != nil {
		manager.onDeleted(nodeID)
	}
	manager.logger.Info("node deletion completed", "node_id", nodeID, "duration", finishedAt.Sub(startedAt), "already_absent", errors.Is(err, store.ErrNotFound))
}
