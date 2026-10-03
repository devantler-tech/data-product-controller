package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// graphBootstrap creates independent reader and writer grants and reads each effective grant back.
func graphBootstrap(ctx context.Context, client *graphClient) error {
	for _, name := range []string{"catalog", "isolated"} {
		if err := client.request(
			ctx,
			http.MethodPost,
			"/_db/_system/_api/database",
			map[string]string{"name": name},
			nil,
		); err != nil {
			return err
		}
	}
	if err := client.request(ctx, http.MethodPost, "/_db/catalog/_api/gharial", map[string]any{
		"name": "lineage", "edgeDefinitions": []any{map[string]any{
			"collection": "relations", "from": []string{"products"}, "to": []string{"products"},
		}},
	}, nil); err != nil {
		return err
	}
	if err := client.request(
		ctx,
		http.MethodPost,
		"/_db/catalog/_api/collection",
		map[string]string{"name": "private"},
		nil,
	); err != nil {
		return err
	}
	if err := client.request(
		ctx,
		http.MethodPost,
		"/_db/catalog/_api/document/private",
		map[string]string{"_key": "unpublished", "value": "synthetic-private-graph"},
		nil,
	); err != nil {
		return err
	}
	for _, user := range []struct{ name, passwordPath, grant string }{
		{"catalog-writer", "/writer-password/password", "rw"},
		{"catalog-reader", "/password/password", "ro"},
	} {
		password, err := os.ReadFile(user.passwordPath)
		if err != nil || len(password) == 0 {
			return errors.New("password projection unavailable")
		}
		if err = client.request(ctx, http.MethodPost, "/_db/_system/_api/user", map[string]any{
			"user": user.name, "passwd": string(password), "active": true,
		}, nil); err != nil {
			return err
		}
		for _, grant := range []struct{ scope, level string }{
			{"*", "none"},
			{"_system", "none"},
			{"isolated", "none"},
			{"catalog", user.grant},
			{"catalog/*", "none"},
			{"catalog/products", user.grant},
			{"catalog/relations", user.grant},
			{"catalog/private", "none"},
		} {
			path := "/_db/_system/_api/user/" + user.name + "/database/" + grant.scope
			if err = client.request(
				ctx,
				http.MethodPut,
				path,
				map[string]string{"grant": grant.level},
				nil,
			); err != nil {
				return err
			}
			var observed struct {
				Result string `json:"result"`
			}
			if err = client.request(ctx, http.MethodGet, path, nil, &observed); err != nil {
				return err
			}
			if observed.Result != grant.level {
				return errors.New("application privilege readback disagrees")
			}
		}
	}
	return nil
}

// graphAssertion exercises real grants, stale authentication and independent seed or rotation operations.
func graphAssertion(mode string) error {
	user, passwordPath := "catalog-reader", "/password/password"
	if mode == "bootstrap" || mode == "rotate" {
		user, passwordPath = "root", "/root-password/password"
	}
	if mode == "seed" || mode == "writer-check" {
		user = "catalog-writer"
	}
	if mode == "stale-password" {
		passwordPath = "/stale-password/password"
	}
	client, closeTransport, err := connectGraph(user, passwordPath)
	if err != nil {
		return err
	}
	defer closeTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch mode {
	case "bootstrap":
		return graphBootstrap(ctx, client)
	case "rotate":
		password, readError := os.ReadFile("/password/password")
		if readError != nil || len(password) == 0 {
			return errors.New("password projection unavailable")
		}
		return client.request(
			ctx,
			http.MethodPatch,
			"/_db/_system/_api/user/catalog-reader",
			map[string]string{"passwd": string(password)},
			nil,
		)
	case "seed":
		vertices := []any{
			map[string]string{"_key": "persistent-lineage-source"},
			map[string]string{"_key": "persistent-lineage-middle"},
			map[string]string{"_key": "persistent-lineage-target"},
		}
		for _, vertex := range vertices {
			if err = client.request(
				ctx,
				http.MethodPost,
				"/_db/catalog/_api/document/products",
				vertex,
				nil,
			); err != nil {
				return err
			}
		}
		edges := []any{
			map[string]string{
				"_key":  "first",
				"_from": "products/persistent-lineage-source",
				"_to":   "products/persistent-lineage-middle",
			},
			map[string]string{
				"_key":  "second",
				"_from": "products/persistent-lineage-middle",
				"_to":   "products/persistent-lineage-target",
			},
		}
		for _, edge := range edges {
			if err = client.request(
				ctx,
				http.MethodPost,
				"/_db/catalog/_api/document/relations",
				edge,
				nil,
			); err != nil {
				return err
			}
		}
		return nil
	case "writer-check":
		for _, attempt := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/_db/catalog/_api/document/products", map[string]string{"_key": "writer-proof"}},
			{http.MethodPatch, "/_db/catalog/_api/document/products/writer-proof", map[string]string{"verified": "write"}},
			{http.MethodDelete, "/_db/catalog/_api/document/products/writer-proof", nil},
		} {
			if err = client.request(
				ctx,
				attempt.method,
				attempt.path,
				attempt.body,
				nil,
			); err != nil {
				return err
			}
		}
		return nil
	case "stale-password":
		err = client.request(
			ctx,
			http.MethodPost,
			"/_db/catalog/_api/cursor",
			map[string]string{"query": traversal},
			nil,
		)
		var rejected *graphAPIError
		if errors.As(err, &rejected) && rejected.status == http.StatusUnauthorized &&
			rejected.code == 11 {
			return nil
		}
		return errors.New("stale password lacked an authentication denial")
	case "privileges":
		// Successful authenticated reads bracket every denial; an outage or invalid
		// credential is never accepted as proof of read-only application access.
		if _, err = readLineage(ctx); err != nil {
			return err
		}
		if err = graphServerWritable(ctx, client); err != nil {
			return err
		}
		if err = graphUndeclaredDenied(ctx, client); err != nil {
			return err
		}
		for index, attempt := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/_db/catalog/_api/document/products", map[string]string{"_key": "forbidden"}},
			{http.MethodPatch, "/_db/catalog/_api/document/products/persistent-lineage-source", map[string]string{"changed": "forbidden"}},
			{http.MethodDelete, "/_db/catalog/_api/document/products/persistent-lineage-target", nil},
		} {
			err = client.request(ctx, attempt.method, attempt.path, attempt.body, nil)
			if !graphWriteDenied(err) {
				graphDenialDiagnostic("mutation", index+1, err)
				return errors.New("write lacked an authorization denial")
			}
		}
		for index, attempt := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/_db/catalog/_api/user", map[string]string{"user": "forbidden", "passwd": "synthetic-denied"}},
			{http.MethodPut, "/_db/catalog/_api/user/catalog-reader/database/catalog", map[string]string{"grant": "rw"}},
			{http.MethodGet, "/_db/_system/_api/collection", nil},
			{http.MethodGet, "/_db/isolated/_api/collection", nil},
		} {
			err = client.request(ctx, attempt.method, attempt.path, attempt.body, nil)
			var rejected *graphAPIError
			if !errors.As(err, &rejected) || rejected.code != 11 ||
				(rejected.status != http.StatusUnauthorized && rejected.status != http.StatusForbidden) {
				graphDenialDiagnostic("privilege", index+1, err)
				return errors.New("privileged access lacked an authorization denial")
			}
		}
		if err = graphServerWritable(ctx, client); err != nil {
			return err
		}
		_, err = readLineage(ctx)
		return err
	default:
		return errors.New("unknown graph assertion")
	}
}

// graphUndeclaredDenied requires a real authorization rejection for an existing unpublished collection.
func graphUndeclaredDenied(ctx context.Context, client *graphClient) error {
	err := client.request(
		ctx,
		http.MethodGet,
		"/_db/catalog/_api/document/private/unpublished",
		nil,
		nil,
	)
	var rejected *graphAPIError
	if errors.As(err, &rejected) && rejected.status == http.StatusForbidden &&
		(rejected.code == 11 || rejected.code == 1004) {
		return nil
	}
	graphDenialDiagnostic("undeclared collection", 1, err)
	return errors.New("undeclared collection lacked an authorization denial")
}

// graphDenialDiagnostic reports only fixed assertion identity and numeric API evidence, never server text.
func graphDenialDiagnostic(kind string, index int, err error) {
	var rejected *graphAPIError
	if errors.As(err, &rejected) {
		fmt.Fprintf(
			os.Stderr,
			"graph %s assertion %d: HTTP=%d code=%d\n",
			kind,
			index,
			rejected.status,
			rejected.code,
		)
		return
	}
	fmt.Fprintf(os.Stderr, "graph %s assertion %d: no API denial\n", kind, index)
}
