package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPostgresConnectionIgnoresAmbientCredentials prevents environment settings from changing the verified database connection.
func TestPostgresConnectionIgnoresAmbientCredentials(t *testing.T) {
	for key, value := range map[string]string{
		"PGHOST": "unapproved", "PGDATABASE": "other", "PGUSER": "postgres", "PGPASSWORD": "ambient",
		"PGSSLMODE": "disable", "PGOPTIONS": "-c default_transaction_read_only=on", "PGCONNECT_TIMEOUT": "invalid",
		"PGTARGETSESSIONATTRS": "standby", "PGSSLROOTCERT": "/unapproved/ca.crt", "PGSSLNEGOTIATION": "invalid",
		"PGCHANNELBINDING": "invalid", "PGREQUIREAUTH": "invalid", "PGMINPROTOCOLVERSION": "invalid",
		"PGMAXPROTOCOLVERSION": "invalid", "PGSSLCERT": "/unapproved/client.crt", "PGSSLKEY": "/unapproved/client.key",
	} {
		t.Setenv(key, value)
	}
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("projected-first"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := postgresConfig("document", path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "warehouse-rw.products.svc.cluster.local" || config.Database != "catalog" || config.User != "document_reader" || config.Password != "projected-first" || config.Port != 5432 || len(config.Fallbacks) != 0 || len(config.RuntimeParams) != 1 || config.RuntimeParams["application_name"] != "provider-fixture" || config.ValidateConnect != nil || config.SSLNegotiation != "postgres" || config.MinProtocolVersion != "3.0" || config.MaxProtocolVersion != "3.0" || config.ChannelBinding != "prefer" {
		t.Fatal("ambient configuration changed the fixed reader connection")
	}
	if err := os.WriteFile(path, []byte("projected-second"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = postgresConfig("document", path, nil)
	if err != nil || config.Password != "projected-second" {
		t.Fatal("password rotation was not reread")
	}
}

// TestPostgresConnectionRejectsAmbientService prevents loading an external libpq profile.
func TestPostgresConnectionRejectsAmbientService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("projected"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "unapproved")
			config, err := postgresConfig("document", path, nil)
			if config != nil || err == nil || err.Error() != "reader configuration unavailable" {
				t.Fatal("ambient service was not rejected before driver parsing")
			}
		})
	}
}

// TestPostgresConnectionRejectsInvalidModelAndProjection rejects unknown models and unavailable password projections.
func TestPostgresConnectionRejectsInvalidModelAndProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"sql", "document", "graph", "postgres", "writer"} {
		if _, err := postgresConfig(model, path, nil); err == nil {
			t.Fatal("empty credential projection accepted")
		}
	}
	if err := os.WriteFile(path, []byte("projected"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"postgres", "writer", ""} {
		if _, err := postgresConfig(model, path, nil); err == nil {
			t.Fatal("unsupported model accepted")
		}
	}
}
