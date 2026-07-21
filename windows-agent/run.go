package main

import (
	"log"
	"time"
)

var Version = "windows-legacy-dev"

type SnapshotCollector interface {
	Collect(Config) (Report, error)
}

func runAgent(stop <-chan struct{}, config Config, collector SnapshotCollector, logger *log.Logger) error {
	client := newClient(config.ServerURL, config.Token)
	interval := time.Duration(config.IntervalSeconds) * time.Second
	for {
		started := time.Now()
		payload, err := collector.Collect(config)
		if err != nil {
			logger.Printf("collection failed: %v", err)
		} else if err := client.send(payload); err != nil {
			logger.Printf("report failed: %v", err)
		} else {
			logger.Printf("report accepted: collected_at=%s duration=%s", payload.CollectedAt.Format(time.RFC3339), time.Since(started))
		}
		delay := interval - time.Since(started)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
