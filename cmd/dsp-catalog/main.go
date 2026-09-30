// The dsp-catalog command exports public provider-bound catalog metadata offline.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

// main reports errors separately from catalog output and returns a failing exit status.
func main() {
	if err := run(os.Args[1:], os.Getenv("DSP_CATALOG_EXPORT_ENABLED"), os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run evaluates the OpenFeature gate before opening inputs and writes only a complete export.
func run(args []string, setting string, out io.Writer) error {
	if setting != "" && setting != "false" && setting != "true" {
		return errors.New("DSP_CATALOG_EXPORT_ENABLED must be true or false")
	}
	client, err := featureflag.NewClient(
		"dsp-catalog",
		featureflag.NewProvider(map[string]bool{"dsp-catalog-export": setting == "true"}),
	)
	if err != nil {
		return fmt.Errorf("configure DSP export flag: %w", err)
	}
	if !featureflag.Enabled(context.Background(), client, "dsp-catalog-export") {
		return errors.New(
			"DSP catalog export is disabled; set DSP_CATALOG_EXPORT_ENABLED=true to opt in",
		)
	}
	flags := flag.NewFlagSet("dsp-catalog", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	catalog := flags.String("catalog", "", "Local DCAT snapshot JSON file")
	bindings := flags.String("bindings", "", "Local provider binding JSON file")
	if err := flags.Parse(args); err != nil {
		return errors.New("usage: dsp-catalog --catalog FILE --bindings FILE")
	}
	if *catalog == "" || *bindings == "" || flags.NArg() != 0 {
		return errors.New("usage: dsp-catalog --catalog FILE --bindings FILE")
	}
	source, err := openInput(*catalog)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	provider, err := openInput(*bindings)
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()
	b, err := dataspace.Export(source, provider)
	if err != nil {
		return err
	}
	if _, err := out.Write(b); err != nil {
		return errors.New("unable to write catalog")
	}
	return nil
}

// Inputs are operator-selected public regular files, never URLs or stdin streams.
func openInput(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("open input: provide a readable regular file")
	}
	// #nosec G304 -- an operator explicitly selects both local input paths; no
	// network request or catalog content influences filesystem access.
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open input: unable to read file")
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("open input: provide a readable regular file")
	}
	return f, nil
}
