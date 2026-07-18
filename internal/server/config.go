package server

import (
	"errors"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddress string
	DatabaseURL   string
	AdminToken    string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		ListenAddress: strings.TrimSpace(os.Getenv("SERVER_STATUS_LISTEN_ADDR")),
		DatabaseURL:   strings.TrimSpace(os.Getenv("SERVER_STATUS_DATABASE_URL")),
		AdminToken:    strings.TrimSpace(os.Getenv("SERVER_STATUS_ADMIN_TOKEN")),
		ReadTimeout:   15 * time.Second,
		WriteTimeout:  30 * time.Second,
	}
	if config.ListenAddress == "" {
		config.ListenAddress = ":8080"
	}
	if config.DatabaseURL == "" || config.AdminToken == "" {
		return Config{}, errors.New("SERVER_STATUS_DATABASE_URL and SERVER_STATUS_ADMIN_TOKEN are required")
	}
	if len(config.AdminToken) < 32 {
		return Config{}, errors.New("SERVER_STATUS_ADMIN_TOKEN must contain at least 32 characters")
	}
	return config, nil
}
