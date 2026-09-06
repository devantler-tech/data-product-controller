package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type source struct {
	mode atomic.Int32
}

func (s *source) export(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mode := s.mode.Load()
	if mode == 1 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	token := "fixture-token-a"
	if mode == 2 {
		token = "fixture-token-b"
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, `{"fixture":"source"}`)
}

func (s *source) control(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/control/healthy":
		s.mode.Store(0)
	case "/control/down":
		s.mode.Store(1)
	case "/control/rotated":
		s.mode.Store(2)
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func control(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("control expects healthy, down, or rotated")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://127.0.0.1:9000/control/healthy", nil,
	)
	if err != nil {
		return errors.New("could not create control request")
	}
	switch args[0] {
	case "healthy":
	case "down":
		request.URL.Path = "/control/down"
	case "rotated":
		request.URL.Path = "/control/rotated"
	default:
		return errors.New("control expects healthy, down, or rotated")
	}
	client := newClient(5 * time.Second)
	defer client.CloseIdleConnections()
	// #nosec G704 -- Synthetic fixture control always targets literal 127.0.0.1:9000;
	// only fixed mode paths vary. The client has a timeout and disables redirects and proxies.
	response, err := client.Do(request)
	if err != nil {
		return errors.New("control request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("control request rejected")
	}
	fmt.Println("control passed")
	return nil
}
