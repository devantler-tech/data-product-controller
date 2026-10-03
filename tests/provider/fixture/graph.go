package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	graphEndpoint = "https://lineage.products.svc.cluster.local:8529"
	traversal     = `FOR vertex, edge, path IN 1..2 OUTBOUND "products/persistent-lineage-source" GRAPH "lineage" SORT LENGTH(path.edges) RETURN {id: vertex._key, depth: LENGTH(path.edges)}`
)

type lineageNode struct {
	ID    string `json:"id"`
	Depth int    `json:"depth"`
}

type graphCursor struct {
	Result  []lineageNode `json:"result"`
	HasMore bool          `json:"hasMore"`
}

type graphClient struct {
	endpoint       string
	http           *http.Client
	user, password string
}

type graphAPIError struct{ status, code int }

// Error omits server detail and credentials from the public failure text.
func (e *graphAPIError) Error() string { return "graph API rejected request" }

// request confines authentication to the selected HTTPS origin, bounds responses,
// and retains numeric error evidence without database error text.
func (c *graphClient) request(ctx context.Context, method, path string, input, output any) error {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		!strings.HasPrefix(path, "/_db/") || c.user == "" || c.password == "" {
		return errors.New("invalid graph request configuration")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid graph request")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid graph request")
	}
	request.SetBasicAuth(c.user, c.password)
	request.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errors.New("graph API connection failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return errors.New("invalid graph response size")
	}
	var envelope struct {
		Error bool `json:"error"`
		Code  int  `json:"errorNum"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return errors.New("invalid graph response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Error {
		return &graphAPIError{status: response.StatusCode, code: envelope.Code}
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return errors.New("invalid graph result")
	}
	return nil
}

// graphWriteDenied accepts only ArangoDB's effective authorization denial.
func graphWriteDenied(err error) bool {
	var rejected *graphAPIError
	return errors.As(err, &rejected) && rejected.status == http.StatusForbidden &&
		(rejected.code == 11 || rejected.code == 1004)
}

// graphServerWritable rejects global read-only mode before a per-reader denial is accepted.
func graphServerWritable(ctx context.Context, client *graphClient) error {
	var state struct {
		Mode string `json:"mode"`
	}
	if err := client.request(
		ctx,
		http.MethodGet,
		"/_db/catalog/_admin/server/mode",
		nil,
		&state,
	); err != nil {
		return err
	}
	if state.Mode != "default" {
		return errors.New("server is not in its default writable mode")
	}
	return nil
}

// connectGraph rereads the independent password and verifies the fixed HTTPS database origin.
func connectGraph(user, passwordPath string) (*graphClient, func(), error) {
	password, err := os.ReadFile(passwordPath)
	if err != nil || len(password) == 0 {
		return nil, nil, errors.New("password projection unavailable")
	}
	config, err := trust("/database-tls/ca.crt")
	if err != nil {
		return nil, nil, err
	}
	transport := probeTransport(config)
	return &graphClient{
		endpoint: graphEndpoint, user: user, password: string(password),
		http: &http.Client{Transport: transport, Timeout: 8 * time.Second},
	}, transport.CloseIdleConnections, nil
}

// readLineage requires the two independently seeded hops with no unconsumed cursor.
func readLineage(ctx context.Context) ([]lineageNode, error) {
	client, closeTransport, err := connectGraph("catalog-reader", "/password/password")
	if err != nil {
		return nil, err
	}
	defer closeTransport()
	var result graphCursor
	if err = client.request(ctx, http.MethodPost, "/_db/catalog/_api/cursor",
		map[string]any{"query": traversal, "batchSize": 8, "ttl": 10}, &result); err != nil {
		return nil, err
	}
	if result.HasMore || len(result.Result) != 2 ||
		result.Result[0] != (lineageNode{ID: "persistent-lineage-middle", Depth: 1}) ||
		result.Result[1] != (lineageNode{ID: "persistent-lineage-target", Depth: 2}) {
		return nil, errors.New("unexpected traversal result")
	}
	return result.Result, nil
}

// graphQueryHandler exposes a fixed bounded read, health and contract without interpreting input as AQL.
func graphQueryHandler(read func(context.Context) ([]lineageNode, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" {
			http.Error(w, "query parameters are unsupported", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/openapi.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(
				[]byte(
					`{"openapi":"3.1.0","info":{"title":"Product lineage","version":"1.0.0"},"paths":{"/api/lineage":{"get":{"responses":{"200":{"description":"Two-hop product lineage","content":{"application/json":{"schema":{"type":"object","required":["lineage"],"properties":{"lineage":{"type":"array","maxItems":2,"items":{"type":"object","required":["id","depth"],"properties":{"id":{"type":"string"},"depth":{"type":"integer","minimum":1,"maximum":2}}}}}}}}},"503":{"description":"Source unavailable"}}}}}}`,
				),
			)
		case "/api/lineage":
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			nodes, err := read(ctx)
			if err != nil {
				http.Error(w, "source unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(struct {
				Lineage []lineageNode `json:"lineage"`
			}{nodes})
		default:
			http.NotFound(w, r)
		}
	})
}
