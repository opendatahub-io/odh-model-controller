package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Config holds the server configuration loaded from environment variables.
type Config struct {
	ListenAddr           string
	TLSCertDir           string // Directory containing tls.crt and tls.key
	LogLevel             string
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	GatewayLabelSelector map[string]string
	MetricsAddr          string  // HTTPS address for Prometheus /metrics endpoint
	OTLPEndpoint         string  // Optional OTLP collector endpoint; enables trace export when set
	KubeQPS              float32 // K8s client QPS throttle (default: 50)
	KubeBurst            int     // K8s client burst above QPS (default: 100)
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:   envOrDefault("LISTEN_ADDR", ":8443"),
		TLSCertDir:   os.Getenv("TLS_CERT_DIR"),
		LogLevel:     envOrDefault("LOG_LEVEL", "info"),
		MetricsAddr:  envOrDefault("METRICS_ADDR", ":9090"),
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
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

	cfg.KubeQPS, err = parseFloat32Env("KUBE_QPS", 50)
	if err != nil {
		return Config{}, fmt.Errorf("invalid KUBE_QPS: %w", err)
	}

	cfg.KubeBurst, err = parseIntEnv("KUBE_BURST", 100)
	if err != nil {
		return Config{}, fmt.Errorf("invalid KUBE_BURST: %w", err)
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
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return d, nil
}

func parseFloat32Env(key string, defaultValue float32) (float32, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue, nil
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return 0, err
	}
	if f < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return float32(f), nil
}

func parseIntEnv(key string, defaultValue int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return n, nil
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
