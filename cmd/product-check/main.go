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

// main reports command errors to stderr and exits with the offline validation result.
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
	var files selectedFiles
	flags.Var(&files, "file", "Selected local YAML or JSON file; repeat for a bundle")
	version := flags.String("report-version", "v1", "v1 or v2 report")
	namespace := flags.String("namespace", "", "Namespace for documents that omit it")
	format := flags.String("format", "text", "text or json")
	if err := flags.Parse(
		args,
	); err != nil || len(files) == 0 || len(files) > 32 || flags.NArg() != 0 ||
		(*format != "text" && *format != "json") || (*version != "v1" && *version != "v2") {
		return 1, errors.New(
			"usage: product-check --file FILE [--file FILE ...] [--namespace NAMESPACE] [--report-version v1|v2] [--format text|json]",
		)
	}
	inputs := make([]io.Reader, 0, len(files))
	selected := make([]*selectedInput, 0, len(files))
	for _, file := range files {
		input := &selectedInput{path: file}
		selected = append(selected, input)
		inputs = append(inputs, input)
	}
	defer func() {
		for _, input := range selected {
			input.close()
		}
	}()
	var report preflight.Report
	var encodedReport any
	var rich *preflight.BundleReport
	if *version == "v2" {
		bundle := preflight.CheckBundle(ctx, inputs, *namespace)
		rich = &bundle
		encodedReport = bundle
		report.Valid = bundle.Valid
		report.Complete = bundle.Complete
		report.Products = bundle.Products
		report.RequiredFeatures = bundle.RequiredFeatures
	} else {
		report = preflight.CheckFiles(ctx, inputs, *namespace)
		encodedReport = report
	}
	for _, input := range selected {
		if input.failed {
			return 1, errors.New("select a readable regular local file")
		}
	}
	var result []byte
	if *format == "json" {
		result, err = json.Marshal(encodedReport)
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
		if rich != nil {
			for _, finding := range rich.Diagnostics {
				_, _ = fmt.Fprintf(
					&text,
					"source %d document %d line %d column %d %s %s: %s\n",
					finding.Source,
					finding.Document,
					finding.Line,
					finding.Column,
					finding.Code,
					finding.Path,
					finding.Message,
				)
			}
			if rich.DiagnosticCounts.Omitted > 0 {
				_, _ = fmt.Fprintf(
					&text,
					"%d additional finding(s) omitted.\n",
					rich.DiagnosticCounts.Omitted,
				)
			}
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

type selectedFiles []string

// String leaves the repeatable file flag's default empty without publishing selected paths.
func (f *selectedFiles) String() string { return "" }

// Set retains each explicit selection and rejects attempts to exceed the shared file limit.
func (f *selectedFiles) Set(value string) error {
	if value == "" || len(*f) >= 32 {
		return errors.New("invalid selection")
	}
	*f = append(*f, value)
	return nil
}

// selectedInput opens only an explicitly selected file and closes it after its bounded read.
type selectedInput struct {
	path           string
	file           *os.File
	opened, failed bool
}

// close releases an opened input and allows the command's deferred cleanup to run repeatedly.
func (s *selectedInput) close() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// Read lazily opens a selected regular file without blocking on a FIFO and closes it on completion.
func (s *selectedInput) Read(b []byte) (int, error) {
	if !s.opened {
		s.opened = true
		// Nonblocking open prevents a regular-file-to-FIFO race; content never selects paths.
		// #nosec G304 -- the command caller explicitly selects this local path.
		file, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			s.failed = true
			return 0, errors.New("unreadable selection")
		}
		s.file = file
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			s.failed = true
			s.close()
			return 0, errors.New("invalid selection")
		}
	}
	if s.file == nil {
		return 0, io.EOF
	}
	n, err := s.file.Read(b)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.failed = true
		}
		s.close()
	}
	return n, err
}
