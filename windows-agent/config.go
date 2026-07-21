package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/url"
	"regexp"
	"strings"
)

type Config struct {
	ServerURL       string            `json:"server_url"`
	AgentID         string            `json:"agent_id"`
	Token           string            `json:"token"`
	IntervalSeconds int               `json:"interval_seconds"`
	Labels          map[string]string `json:"labels,omitempty"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func loadConfig(path string) (Config, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, err
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) applyDefaults() {
	config.ServerURL = strings.TrimRight(strings.TrimSpace(config.ServerURL), "/")
	config.AgentID = strings.TrimSpace(config.AgentID)
	config.Token = strings.TrimSpace(config.Token)
	if config.IntervalSeconds == 0 {
		config.IntervalSeconds = 60
	}
	if config.Labels == nil {
		config.Labels = make(map[string]string)
	}
}

func (config *Config) validate() error {
	config.applyDefaults()
	parsed, err := url.Parse(config.ServerURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("server_url must be an absolute HTTP or HTTPS URL")
	}
	if !uuidPattern.MatchString(config.AgentID) {
		return errors.New("agent_id must be an RFC 4122 UUID")
	}
	if config.Token == "" {
		return errors.New("token is required")
	}
	if config.IntervalSeconds < 10 || config.IntervalSeconds > 3600 {
		return errors.New("interval_seconds must be between 10 and 3600")
	}
	return nil
}

func writeConfig(path string, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\r', '\n')
	return ioutil.WriteFile(path, content, 0600)
}
