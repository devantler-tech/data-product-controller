package main

import (
	"net/http"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// TestRegistryServerDoesNotWaitForLeadership protects the Service's non-leader endpoints.
func TestRegistryServerDoesNotWaitForLeadership(t *testing.T) {
	server := newRegistryServer(&http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second})
	independent, ok := server.(manager.LeaderElectionRunnable)
	if !ok || independent.NeedLeaderElection() {
		t.Fatal("every registry replica must serve before acquiring controller leadership")
	}
}
