package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Config holds the server configuration loaded from environment variables.
type Config struct {
	ListenAddr           string
	TLSCertFile          string
	TLSKeyFile           string
	LogLevel             string
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	GatewayLabelSelector map[string]string
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:  envOrDefault("LISTEN_ADDR", ":8443"),
		TLSCertFile: os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:  os.Getenv("TLS_KEY_FILE"),
		LogLevel:    envOrDefault("LOG_LEVEL", "info"),
	}

	var err error
	cfg.ReadTimeout, err = parseDurationEnv("READ_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("invalid READ_TIMEOUT: %w", err)
	}

	cfg.WriteTimeout, err = parseDurationEnv("WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("invalid WRITE_TIMEOUT: %w", err)
	}

	cfg.IdleTimeout, err = parseDurationEnv("IDLE_TIMEOUT", 120*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("invalid IDLE_TIMEOUT: %w", err)
	}

	labelSelector := os.Getenv("GATEWAY_LABEL_SELECTOR")
	if labelSelector != "" {
		parsed, err := parseLabelSelector(labelSelector)
		if err != nil {
			return Config{}, fmt.Errorf("invalid GATEWAY_LABEL_SELECTOR: %w", err)
		}
		cfg.GatewayLabelSelector = parsed
	}

	return cfg, nil
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func parseDurationEnv(key string, defaultValue time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue, nil
	}
	return time.ParseDuration(v)
}

// parseLabelSelector parses a comma-separated list of key=value pairs.
func parseLabelSelector(s string) (map[string]string, error) {
	labels := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid label %q: must be key=value", pair)
		}
		key, value := parts[0], parts[1]
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label key %q: %s", key, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label value %q for key %q: %s", value, key, strings.Join(errs, "; "))
		}
		labels[key] = value
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("empty label selector")
	}
	return labels, nil
}
