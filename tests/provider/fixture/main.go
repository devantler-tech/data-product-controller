// This independent data-plane fixture never runs in the controller process.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const databaseName = "catalog"

// trust loads only the supplied CA bundle and preserves certificate verification.
func trust(path string) (*tls.Config, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("trust bundle unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid trust bundle")
	}
	return &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
}

// connect reads current projected credentials for the fixed TLS replica set.
func connect(passwordPath string) (*mongo.Client, error) {
	password, err := os.ReadFile(passwordPath)
	if err != nil || len(password) == 0 {
		return nil, errors.New("password projection unavailable")
	}
	tlsConfig, err := trust("/database-tls/ca.crt")
	if err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(
		"/database-client-tls/tls.crt",
		"/database-client-tls/tls.key",
	)
	if err != nil {
		return nil, errors.New("client certificate unavailable")
	}
	tlsConfig.Certificates = []tls.Certificate{certificate}
	return mongo.Connect(options.Client().
		SetHosts([]string{"documents-rs0.products.svc.cluster.local:27017"}).
		SetReplicaSet("rs0").SetTLSConfig(tlsConfig).
		SetAuth(options.Credential{AuthSource: "admin", Username: os.Getenv("DOCUMENT_USER"), Password: string(password)}).
		SetConnectTimeout(3 * time.Second).SetServerSelectionTimeout(3 * time.Second).
		SetMaxPoolSize(2).SetRetryWrites(false))
}

// closeClient bounds disconnection so cleanup cannot outlive an assertion.
func closeClient(client *mongo.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = client.Disconnect(ctx)
}

// readDocuments performs the fixed lookup with a newly projected reader password.
func readDocuments(ctx context.Context) ([]document, error) {
	// A new client reads the projected password for every request, including rotation.
	client, err := connect("/password/password")
	if err != nil {
		return nil, err
	}
	defer closeClient(client)
	var record document
	err = client.Database(databaseName).
		Collection("documents").
		FindOne(ctx, bson.D{{Key: "_id", Value: "retained"}}).
		Decode(&record)
	if err != nil {
		return nil, err
	}
	return []document{record}, nil
}

// databaseAssertion seeds through the writer or checks actual database denial codes.
func databaseAssertion(mode string) error {
	passwordPath := "/password/password"
	if mode == "stale-password" {
		passwordPath = "/stale-password/password"
	}
	client, err := connect(passwordPath)
	if err != nil {
		return err
	}
	defer closeClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := client.Database(databaseName)
	collection := db.Collection("documents")
	switch mode {
	case "seed":
		_, err = collection.ReplaceOne(
			ctx,
			bson.D{{Key: "_id", Value: "retained"}},
			document{ID: "retained", Value: "persistent-document"},
			options.Replace().SetUpsert(true),
		)
		return err
	case "privileges":
		attempts := []func() error{
			func() error { _, e := collection.InsertOne(ctx, document{ID: "forbidden", Value: "denied"}); return e },
			func() error {
				_, e := collection.UpdateOne(
					ctx,
					bson.D{{Key: "_id", Value: "retained"}},
					bson.D{{Key: "$set", Value: bson.D{{Key: "value", Value: "changed"}}}},
				)
				return e
			},
			func() error { _, e := collection.DeleteOne(ctx, bson.D{{Key: "_id", Value: "retained"}}); return e },
			func() error {
				return db.RunCommand(ctx, bson.D{{Key: "createUser", Value: "forbidden"}, {Key: "pwd", Value: "synthetic-denied"}, {Key: "roles", Value: bson.A{}}}).
					Err()
			},
		}
		for i, attempt := range attempts {
			if !unauthorized(attempt()) {
				return fmt.Errorf("privilege assertion %d lacked an authorization denial", i+1)
			}
		}
		var record document
		if err = collection.FindOne(ctx, bson.D{{Key: "_id", Value: "retained"}}).
			Decode(&record); err != nil ||
			record.Value != "persistent-document" {
			return errors.New("reader did not retain the unchanged record")
		}
		return nil
	case "stale-password":
		// The old projection must be rejected by actual authentication, not a network outage.
		err = collection.FindOne(ctx, bson.D{{Key: "_id", Value: "retained"}}).Err()
		if authenticationFailure(err) {
			return nil
		}
		return errors.New("stale password lacked an authentication denial")
	default:
		return errors.New("unknown database assertion")
	}
}

// probe verifies the fixed HTTPS response, contract, outage or TCP isolation evidence.
func probe() error {
	config, err := trust("/query-tls/ca.crt")
	if err != nil {
		return err
	}
	transport := probeTransport(config)
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	path := "/api/documents"
	host := "document-query"
	if os.Getenv("PROVIDER_ENGINE") == "graph" {
		path, host = "/api/lineage", "graph-query"
	}
	if len(os.Args) > 2 && os.Args[2] == "contract" {
		path = "/openapi.json"
	}
	response, err := client.Get("https://" + host + ".products.svc.cluster.local:8443" + path)
	if err != nil {
		if len(os.Args) > 2 && os.Args[2] == "denied" && networkDenial(err) {
			return nil
		}
		return errors.New("published query connection failed")
	}
	defer response.Body.Close()
	if len(os.Args) > 2 && os.Args[2] == "denied" {
		return errors.New("network denial returned an HTTP response")
	}
	if len(os.Args) > 2 && os.Args[2] == "outage" {
		if response.StatusCode != http.StatusServiceUnavailable {
			return errors.New("query outage was not reported")
		}
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("published query unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(body) > 16384 {
		return errors.New("invalid query response size")
	}
	if path == "/openapi.json" {
		var contract struct {
			OpenAPI string                     `json:"openapi"`
			Paths   map[string]json.RawMessage `json:"paths"`
		}
		queryPath := "/api/documents"
		if os.Getenv("PROVIDER_ENGINE") == "graph" {
			queryPath = "/api/lineage"
		}
		if json.Unmarshal(body, &contract) != nil || contract.OpenAPI != "3.1.0" ||
			contract.Paths[queryPath] == nil {
			return errors.New("query contract unavailable")
		}
		return nil
	}
	if os.Getenv("PROVIDER_ENGINE") == "graph" {
		var result struct {
			Lineage []lineageNode `json:"lineage"`
		}
		if json.Unmarshal(body, &result) != nil || len(result.Lineage) != 2 ||
			result.Lineage[0] != (lineageNode{ID: "persistent-lineage-middle", Depth: 1}) || result.Lineage[1] != (lineageNode{ID: "persistent-lineage-target", Depth: 2}) {
			return errors.New("query did not return persistent two-hop lineage")
		}
		return nil
	}
	var records struct {
		Documents []document `json:"documents"`
	}
	if json.Unmarshal(body, &records) != nil || len(records.Documents) != 1 ||
		records.Documents[0] != (document{ID: "retained", Value: "persistent-document"}) {
		return errors.New("query did not return the persistent document")
	}
	return nil
}

// run selects one bounded fixture operation or serves the fixed read-only API.
func run() error {
	if len(os.Args) < 2 {
		return errors.New("fixture mode required")
	}
	switch os.Args[1] {
	case "serve":
		handler := queryHandler(readDocuments)
		if os.Getenv("PROVIDER_ENGINE") == "graph" {
			handler = graphQueryHandler(readLineage)
		}
		server := &http.Server{
			Addr:              ":8443",
			Handler:           handler,
			ReadHeaderTimeout: 3 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      8 * time.Second,
			IdleTimeout:       10 * time.Second,
			MaxHeaderBytes:    8192,
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		}()
		err := server.ListenAndServeTLS("/query-tls/tls.crt", "/query-tls/tls.key")
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case "probe":
		return probe()
	case "bootstrap", "rotate", "seed", "writer-check", "privileges", "stale-password":
		if os.Getenv("PROVIDER_ENGINE") == "graph" {
			return graphAssertion(os.Args[1])
		}
		return databaseAssertion(os.Args[1])
	default:
		return errors.New("unknown fixture mode")
	}
}

// main reports only a sanitized failure, keeping driver errors and records private.
func main() {
	if err := run(); err != nil {
		var graphError *graphAPIError
		if errors.As(err, &graphError) {
			fmt.Fprintf(
				os.Stderr,
				"graph API denial: HTTP=%d code=%d\n",
				graphError.status,
				graphError.code,
			)
		}
		// Never print a driver's URI, password, server error or document payload.
		fmt.Fprintln(os.Stderr, "provider fixture assertion failed")
		os.Exit(1)
	}
}
