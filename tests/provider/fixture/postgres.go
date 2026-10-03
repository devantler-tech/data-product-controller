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
	"reflect"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const postgresHost = "warehouse-rw.products.svc.cluster.local"

// postgresConfig discards ambient connection settings and rereads the current password projection.
func postgresConfig(model, passwordPath string, roots *x509.CertPool) (*pgx.ConnConfig, error) {
	users := map[string]string{
		"sql":      "sql_reader",
		"document": "document_reader",
		"graph":    "graph_reader",
	}
	user, supported := users[model]
	password, err := os.ReadFile(passwordPath)
	if !supported || err != nil || len(password) == 0 || len(password) > 4096 ||
		os.Getenv("PGSERVICE") != "" || os.Getenv("PGSERVICEFILE") != "" {
		return nil, errors.New("reader configuration unavailable")
	}
	// Pin parser-only options too: TLS and password overrides happen after parsing,
	// so the parser must not first read ambient certificates, passfiles or services.
	config, err := pgx.ParseConfig(
		"host=warehouse-rw.products.svc.cluster.local port=5432 dbname=catalog user=reader password=unused-projected-password sslmode=disable sslrootcert='' sslcert='' sslkey='' sslpassword='' sslsni=1 sslnegotiation=postgres connect_timeout=3 target_session_attrs=any min_protocol_version=3.0 max_protocol_version=3.0 channel_binding=prefer require_auth=''",
	)
	if err != nil {
		return nil, errors.New("connection configuration unavailable")
	}
	config.Host, config.Port, config.Database = postgresHost, 5432, "catalog"
	config.User, config.Password = user, string(password)
	config.ConnectTimeout = 3 * time.Second
	config.Fallbacks = nil
	config.RuntimeParams = map[string]string{"application_name": "provider-fixture"}
	config.TLSConfig = &tls.Config{
		RootCAs:    roots,
		ServerName: postgresHost,
		MinVersion: tls.VersionTLS13,
	}
	return config, nil
}

// postgresConnect verifies the fixed database hostname using only the mounted CA certificate.
func postgresConnect(ctx context.Context, model, passwordPath string) (*pgx.Conn, error) {
	config, err := trust("/database-tls/ca.crt")
	if err != nil {
		return nil, err
	}
	connection, err := postgresConfig(model, passwordPath, config.RootCAs)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, connection)
}

// closePostgres bounds cleanup independently of an expired query context.
func closePostgres(connection *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = connection.Close(ctx)
}

// readPostgres executes a fixed model query; no request input is interpreted as SQL or Cypher.
func readPostgres(ctx context.Context, model string) ([]postgresRecord, error) {
	connection, err := postgresConnect(ctx, model, "/password/password")
	if err != nil {
		return nil, err
	}
	defer closePostgres(connection)
	return postgresRecords(ctx, connection, model)
}

// postgresRecords returns bounded SQL, filtered JSONB or actual two-hop AGE records.
func postgresRecords(
	ctx context.Context,
	connection *pgx.Conn,
	model string,
) ([]postgresRecord, error) {
	queries := map[string]string{
		"sql":      `SELECT id, value FROM public.catalog_rows WHERE id='retained' ORDER BY id LIMIT 2`,
		"document": `SELECT id, payload->>'value' FROM public.documents WHERE payload @> '{"published":true}'::jsonb ORDER BY id LIMIT 2`,
		"graph":    `SELECT node::text::jsonb->>'id', (node::text::jsonb->>'depth')::int FROM ag_catalog.cypher('lineage', $$ MATCH p=(source:product {id:'source'})-[:feeds*1..2]->(target:product) RETURN {id:target.id, depth:length(p)} ORDER BY length(p) $$) AS (node ag_catalog.agtype) LIMIT 2`,
	}
	query, valid := queries[model]
	if !valid {
		return nil, errors.New("unsupported query model")
	}
	if model == "graph" {
		if _, err := connection.Exec(
			ctx,
			`LOAD '$libdir/plugins/age'; SET search_path=ag_catalog,public`,
		); err != nil {
			return nil, err
		}
	}
	rows, err := connection.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]postgresRecord, 0, 2)
	for rows.Next() {
		var record postgresRecord
		if model == "graph" {
			err = rows.Scan(&record.ID, &record.Depth)
		} else {
			err = rows.Scan(&record.ID, &record.Value)
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// postgresExpected describes independently seeded retained records for each public read contract.
func postgresExpected(model string) []postgresRecord {
	switch model {
	case "sql":
		return []postgresRecord{{ID: "retained", Value: "persistent-row"}}
	case "document":
		return []postgresRecord{{ID: "retained", Value: "persistent-jsonb"}}
	case "graph":
		return []postgresRecord{{ID: "middle", Depth: 1}, {ID: "target", Depth: 2}}
	default:
		return nil
	}
}

// postgresAssertion requires live success around authorization failures and distinguishes stale-password rejection.
func postgresAssertion(model, mode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	passwordPath := "/password/password"
	if mode == "stale-password" {
		passwordPath = "/stale-password/password"
	}
	connection, err := postgresConnect(ctx, model, passwordPath)
	if mode == "stale-password" {
		if connection != nil {
			closePostgres(connection)
		}
		if postgresAuthenticationDenied(err) {
			return nil
		}
		return errors.New("stale password lacked database rejection")
	}
	if err != nil {
		return err
	}
	defer closePostgres(connection)
	before, err := postgresRecords(ctx, connection, model)
	if err != nil || !reflect.DeepEqual(before, postgresExpected(model)) {
		return errors.New("retained read unavailable")
	}
	if mode == "read" {
		return nil
	}
	if mode != "privileges" {
		return errors.New("unsupported database assertion")
	}
	mutations := map[string][]string{
		"sql": {
			`INSERT INTO public.catalog_rows VALUES ('forbidden','denied')`,
			`UPDATE public.catalog_rows SET value='changed' WHERE id='retained'`,
			`DELETE FROM public.catalog_rows WHERE id='retained'`,
		},
		"document": {
			`INSERT INTO public.documents VALUES ('forbidden','{}')`,
			`UPDATE public.documents SET payload='{}' WHERE id='retained'`,
			`DELETE FROM public.documents WHERE id='retained'`,
		},
		"graph": {
			`SELECT * FROM ag_catalog.cypher('lineage', $$ CREATE (:product {id:'forbidden'}) $$) AS (node ag_catalog.agtype)`,
			`SELECT * FROM ag_catalog.cypher('lineage', $$ MATCH (n:product {id:'middle'}) SET n.id='changed' RETURN n $$) AS (node ag_catalog.agtype)`,
			`SELECT * FROM ag_catalog.cypher('lineage', $$ MATCH (n:product {id:'target'}) DETACH DELETE n $$) AS (node ag_catalog.agtype)`,
		},
	}
	attempts := append(
		mutations[model],
		`CREATE ROLE forbidden SUPERUSER`,
		`CREATE SCHEMA forbidden`,
		`SET ROLE catalog_writer`,
	)
	for i, query := range attempts {
		_, err := connection.Exec(ctx, query)
		if !postgresWriteDenied(err) {
			var databaseError *pgconn.PgError
			code := "no-database-rejection"
			if errors.As(err, &databaseError) {
				code = databaseError.Code
			}
			fmt.Fprintf(os.Stderr, "authorization assertion %d returned code %s\n", i+1, code)
			return fmt.Errorf("reader authorization assertion %d failed", i+1)
		}
	}
	after, err := postgresRecords(ctx, connection, model)
	if err != nil || !reflect.DeepEqual(before, after) {
		return errors.New("retained record changed or became unavailable")
	}
	return nil
}

// postgresSeed writes fixed records using a separate application writer credential.
func postgresSeed(model string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ca, err := trust("/database-tls/ca.crt")
	if err != nil {
		return err
	}
	config, err := postgresConfig(model, "/password/password", ca.RootCAs)
	if err != nil {
		return err
	}
	config.User = "catalog_writer"
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer closePostgres(connection)
	switch model {
	case "sql":
		_, err = connection.Exec(
			ctx,
			`INSERT INTO public.catalog_rows VALUES ('retained','persistent-row') ON CONFLICT(id) DO UPDATE SET value=excluded.value`,
		)
	case "document":
		_, err = connection.Exec(
			ctx,
			`INSERT INTO public.documents VALUES ('retained','{"published":true,"value":"persistent-jsonb"}'), ('excluded','{"published":false,"value":"private-excluded-record"}') ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`,
		)
	case "graph":
		_, err = connection.Exec(
			ctx,
			`LOAD '$libdir/plugins/age'; SET search_path=ag_catalog,public`,
		)
		if err == nil {
			_, err = connection.Exec(
				ctx,
				`SELECT * FROM ag_catalog.cypher('lineage', $$ MATCH (n:product) DETACH DELETE n $$) AS (node ag_catalog.agtype)`,
			)
		}
		if err == nil {
			_, err = connection.Exec(
				ctx,
				`SELECT * FROM ag_catalog.cypher('lineage', $$ CREATE (:product {id:'source'})-[:feeds]->(:product {id:'middle'})-[:feeds]->(:product {id:'target'}) $$) AS (node ag_catalog.agtype)`,
			)
		}
	default:
		return errors.New("unsupported seed model")
	}
	return err
}

// postgresProbe requires the published HTTPS query and model contract, with distinct isolation and outage evidence.
func postgresProbe(model, mode string) error {
	path, field := postgresQueryRoute(model)
	if path == "" {
		return errors.New("unsupported query model")
	}
	if mode != "" && mode != "contract" && mode != "denied" && mode != "outage" {
		return errors.New("unsupported PostgreSQL probe mode")
	}
	if mode == "contract" {
		path = "/openapi.json"
	}
	ca, err := trust("/query-tls/ca.crt")
	if err != nil {
		return err
	}
	transport := probeTransport(ca)
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Get(
		"https://postgres-query-" + model + ".products.svc.cluster.local:8443" + path,
	)
	if err != nil {
		if mode == "denied" && networkDenial(err) {
			return nil
		}
		return errors.New("published PostgreSQL query unavailable")
	}
	defer response.Body.Close()
	if mode == "denied" {
		return errors.New("network denial returned an HTTP response")
	}
	if mode == "outage" {
		if response.StatusCode == http.StatusServiceUnavailable {
			return nil
		}
		return errors.New("source outage was not reported")
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("published query unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(body) > 16384 {
		return errors.New("invalid query response size")
	}
	if mode == "contract" {
		var contract struct {
			OpenAPI string                     `json:"openapi"`
			Paths   map[string]json.RawMessage `json:"paths"`
		}
		queryPath, _ := postgresQueryRoute(model)
		if json.Unmarshal(body, &contract) != nil || contract.OpenAPI != "3.1.0" ||
			contract.Paths[queryPath] == nil {
			return errors.New("model contract unavailable")
		}
		return nil
	}
	var result map[string][]postgresRecord
	if json.Unmarshal(body, &result) != nil || len(result) != 1 ||
		!reflect.DeepEqual(result[field], postgresExpected(model)) {
		return errors.New("query did not return retained model records")
	}
	return nil
}

// postgresRun keeps query, writer and assertion modes explicit in the independent fixture.
func postgresRun(mode string) error {
	model := os.Getenv("POSTGRES_MODEL")
	probeMode := ""
	if mode == "postgres-seed" || mode == "postgres-probe" {
		if len(os.Args) < 3 {
			return errors.New("PostgreSQL model argument required")
		}
		model = os.Args[2]
		if mode == "postgres-probe" && len(os.Args) == 4 {
			probeMode = os.Args[3]
		} else if len(os.Args) != 3 {
			return errors.New("unsupported PostgreSQL command arguments")
		}
	}
	path, _ := postgresQueryRoute(model)
	if path == "" {
		return errors.New("unsupported PostgreSQL model")
	}
	switch mode {
	case "postgres-read":
		return postgresAssertion(model, "read")
	case "postgres-privileges":
		return postgresAssertion(model, "privileges")
	case "postgres-stale-password":
		return postgresAssertion(model, "stale-password")
	case "postgres-seed":
		return postgresSeed(model)
	case "postgres-probe":
		return postgresProbe(model, probeMode)
	case "postgres-serve":
		server := &http.Server{
			Addr: ":8443",
			Handler: postgresQueryHandler(
				model,
				func(ctx context.Context) ([]postgresRecord, error) { return readPostgres(ctx, model) },
			),
			ReadHeaderTimeout: 3 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      8 * time.Second,
			IdleTimeout:       10 * time.Second,
			MaxHeaderBytes:    8192,
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
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
	default:
		return errors.New("unsupported PostgreSQL mode")
	}
}
