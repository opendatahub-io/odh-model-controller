package main

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/opendatahub-io/odh-model-controller/server/gateway"
	"github.com/opendatahub-io/odh-model-controller/server/handlers"
	"github.com/opendatahub-io/odh-model-controller/server/middleware"
)

// NewServer creates an http.Server with the full middleware chain and route registration.
func NewServer(cfg Config, discoverer gateway.Discoverer) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", handlers.Healthz)
	mux.HandleFunc("/readyz", handlers.Readyz)

	gatewayHandler := &handlers.GatewayHandler{Discoverer: discoverer}
	mux.Handle("/api/v1/gateways", middleware.Auth(gatewayHandler))

	handler := otelhttp.NewHandler(
		middleware.Recovery(middleware.SecurityHeaders(middleware.Logging(mux))),
		"model-serving-api",
	)

	return &http.Server{
		Addr:           cfg.ListenAddr,
		Handler:        handler,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		IdleTimeout:    cfg.IdleTimeout,
		MaxHeaderBytes: 8 << 10, // 8 KB
	}
}
