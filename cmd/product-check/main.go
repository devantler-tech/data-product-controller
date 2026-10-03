// The product-check command validates publisher-selected declarations offline.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/devantler-tech/data-product-controller/internal/preflight"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

func main() {
	code, err := run(
		context.Background(),
		os.Args[1:],
		os.Getenv("PUBLISHER_PREFLIGHT_ENABLED"),
		os.Stdout,
	)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

// run evaluates the default-off gate before parsing arguments or opening any input.
func run(ctx context.Context, args []string, setting string, out io.Writer) (int, error) {
	if setting != "" && setting != "true" && setting != "false" {
		return 1, errors.New("PUBLISHER_PREFLIGHT_ENABLED must be true or false")
	}
	client, err := featureflag.NewClient("publisher-preflight",
		featureflag.NewProvider(map[string]bool{"publisher-preflight": setting == "true"}))
	if err != nil {
		return 1, errors.New("unable to configure publisher preflight")
	}
	if !featureflag.Enabled(ctx, client, "publisher-preflight") {
		return 1, errors.New(
			"publisher preflight is disabled; set PUBLISHER_PREFLIGHT_ENABLED=true to opt in",
		)
	}
	flags := flag.NewFlagSet("product-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "Selected local YAML or JSON file")
	namespace := flags.String("namespace", "", "Namespace for documents that omit it")
	format := flags.String("format", "text", "text or json")
	if err := flags.Parse(
		args,
	); err != nil || *file == "" || flags.NArg() != 0 ||
		(*format != "text" && *format != "json") {
		return 1, errors.New(
			"usage: product-check --file FILE [--namespace NAMESPACE] [--format text|json]",
		)
	}
	// Nonblocking open avoids a regular-file-to-FIFO race while checking the opened object.
	// #nosec G304 -- only the caller selects this local path; document content never selects files.
	input, err := os.OpenFile(*file, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 1, errors.New("select a readable regular local file")
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 1, errors.New("select a readable regular local file")
	}
	report := preflight.Check(ctx, input, *namespace)
	var result []byte
	if *format == "json" {
		result, err = json.Marshal(report)
		if err != nil {
			return 1, errors.New("unable to encode preflight report")
		}
		result = append(result, '\n')
	} else {
		var text strings.Builder
		if report.Valid && report.Complete {
			_, _ = text.WriteString("Validated " + strconv.Itoa(report.Products) +
				" product(s): declarations only; live readiness and access remain unverified.\n")
		} else {
			_, _ = text.WriteString(
				"Preflight is incomplete for " + strconv.Itoa(report.Products) + " product(s).\n",
			)
		}
		for _, diagnostic := range report.Diagnostics {
			_, _ = text.WriteString("document " + strconv.Itoa(diagnostic.Document) + " " +
				diagnostic.Code + " " + diagnostic.Path + ": " + diagnostic.Message + "\n")
		}
		if len(report.RequiredFeatures) != 0 {
			_, _ = text.WriteString(
				"Required operational features: " + strings.Join(
					report.RequiredFeatures,
					", ",
				) + "\n",
			)
		}
		result = []byte(text.String())
	}
	if written, err := out.Write(result); err != nil || written != len(result) {
		return 1, errors.New("unable to write preflight report")
	}
	if !report.Valid {
		return 1, nil
	}
	if !report.Complete {
		return 2, nil
	}
	return 0, nil
}
