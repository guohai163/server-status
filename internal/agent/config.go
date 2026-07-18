package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerURL string
	Token     string
	AgentID   string
	Interval  time.Duration
	Labels    map[string]string
	LockFile  string
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		ServerURL: strings.TrimRight(strings.TrimSpace(os.Getenv("SERVER_STATUS_URL")), "/"),
		Token:     strings.TrimSpace(os.Getenv("SERVER_STATUS_TOKEN")),
		AgentID:   strings.TrimSpace(os.Getenv("SERVER_STATUS_AGENT_ID")),
		Interval:  time.Minute,
		Labels:    map[string]string{},
		LockFile:  strings.TrimSpace(os.Getenv("SERVER_STATUS_LOCK_FILE")),
	}
	if value := strings.TrimSpace(os.Getenv("SERVER_STATUS_INTERVAL")); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse SERVER_STATUS_INTERVAL: %w", err)
		}
		config.Interval = interval
	}
	if value := strings.TrimSpace(os.Getenv("SERVER_STATUS_LABELS")); value != "" {
		if err := json.Unmarshal([]byte(value), &config.Labels); err != nil {
			return Config{}, fmt.Errorf("parse SERVER_STATUS_LABELS: %w", err)
		}
	}
	if config.LockFile == "" {
		config.LockFile = "/tmp/server-status-agent-" + strconv.Itoa(os.Getuid()) + ".lock"
	}
	if config.ServerURL == "" || config.Token == "" || config.AgentID == "" {
		return Config{}, errors.New("SERVER_STATUS_URL, SERVER_STATUS_TOKEN, and SERVER_STATUS_AGENT_ID are required")
	}
	if config.Interval < 10*time.Second || config.Interval > time.Hour {
		return Config{}, errors.New("SERVER_STATUS_INTERVAL must be between 10s and 1h")
	}
	return config, nil
}
