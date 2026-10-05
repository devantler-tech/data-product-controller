// Command publisher-identity resolves the release verifier's immutable signer.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"
)

var publisherPattern = regexp.MustCompile(
	`^devantler-tech/actions/\.github/workflows/publish-app\.yaml@[a-f0-9]{40}$`,
)

func publisherIdentity(input io.Reader) (string, error) {
	var workflow struct {
		Jobs map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	decoder := yaml.NewDecoder(input)
	if err := decoder.Decode(&workflow); err != nil {
		return "", errors.New("invalid publisher declaration")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("expected one publisher document")
	}
	publisher := workflow.Jobs["publish"].Uses
	if !publisherPattern.MatchString(publisher) {
		return "", errors.New("expected an immutable application publisher")
	}
	return "https://github.com/" + publisher, nil
}

func main() {
	os.Exit(run())
}

func run() int {
	file, err := os.Open(".github/workflows/cd.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, "publisher identity unavailable")
		return 1
	}
	defer func() { _ = file.Close() }()
	identity, err := publisherIdentity(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "publisher identity unavailable")
		return 1
	}
	fmt.Println(identity)
	return 0
}
