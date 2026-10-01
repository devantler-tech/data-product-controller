package main

import (
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// newRegistryServer registers the read-only descriptor server with manager lifecycle.
func newRegistryServer(server *http.Server) manager.Runnable {
	shutdownTimeout := 10 * time.Second
	return &manager.Server{
		Name:                "descriptor registry",
		Server:              server,
		OnlyServeWhenLeader: false,
		ShutdownTimeout:     &shutdownTimeout,
	}
}
