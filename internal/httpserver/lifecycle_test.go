package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

type observedListener struct {
	net.Listener
	closed chan struct{}
}

func (l *observedListener) Close() error {
	err := l.Listener.Close()
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return err
}

// TestShutdownWaitsForActiveResponse reproduces returning when only the listener has closed.
func TestShutdownWaitsForActiveResponse(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "deadline"}[timeout], func(t *testing.T) {
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			observed := &observedListener{Listener: listener, closed: make(chan struct{})}
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			server := &http.Server{
				ReadHeaderTimeout: time.Second,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					close(entered)
					<-release
					_, _ = io.WriteString(w, "completed")
				}),
			}
			defer func() { _ = server.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			limit := time.Second
			if timeout {
				limit = 100 * time.Millisecond
			}
			go func() { done <- Run(ctx, server, func() error { return server.Serve(observed) }, limit) }()
			response := make(chan error, 1)
			go func() {
				client := &http.Client{Timeout: 2 * time.Second}
				request, getErr := http.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					"http://"+listener.Addr().String(),
					nil,
				)
				if getErr != nil {
					response <- getErr
					return
				}
				// #nosec G704 -- A test-owned loopback listener is the only target.
				got, getErr := client.Do(request)
				if getErr == nil {
					_, getErr = io.Copy(io.Discard, got.Body)
					_ = got.Body.Close()
				}
				response <- getErr
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("handler did not start")
			}
			cancel()
			select {
			case <-observed.closed:
			case <-time.After(time.Second):
				t.Fatal("listener did not close")
			}
			if timeout {
				select {
				case err = <-done:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("shutdown did not report its deadline: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("shutdown deadline did not bound serving")
				}
				select {
				case <-response:
				case <-time.After(time.Second):
					t.Fatal("deadline did not close the active connection")
				}
			} else {
				select {
				case err = <-done:
					t.Fatalf("returned before the active response drained: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
				release <- struct{}{}
				select {
				case err = <-response:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("response did not complete")
				}
				select {
				case err = <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("shutdown did not finish after drain")
				}
			}
		})
	}
}

// TestListenerFailureReturnsWithoutWaitingForASignal preserves immediate startup failures.
func TestListenerFailureReturnsWithoutWaitingForASignal(t *testing.T) {
	want := errors.New("listener failed")
	server := &http.Server{ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if got := Run(ctx, server, func() error { return want }, time.Second); !errors.Is(got, want) {
		t.Fatal(got)
	}
}

// TestBlockedStartupHonorsShutdownDeadline covers work before Serve registers a listener.
func TestBlockedStartupHonorsShutdownDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, &http.Server{ReadHeaderTimeout: time.Second}, func() error { close(entered); <-release; return nil }, 50*time.Millisecond)
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked startup lost deadline: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked startup ignored cancellation")
	}
}
