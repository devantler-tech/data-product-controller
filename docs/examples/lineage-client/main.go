// The example reads one saved public trace using only the Go standard library.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type observation struct {
	State              string `json:"state"`
	Message            string `json:"message"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type product struct {
	Key         string `json:"key"`
	State       string `json:"state"`
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Version     string `json:"version,omitempty"`
	Owner       *struct {
		Name string `json:"name"`
		URL  string `json:"url,omitempty"`
	} `json:"owner,omitempty"`
	Generation         int64                  `json:"generation"`
	ObservedGeneration int64                  `json:"observedGeneration"`
	Health             map[string]observation `json:"health,omitempty"`
}

type trace struct {
	APIVersion string    `json:"apiVersion"`
	Root       string    `json:"root"`
	Complete   bool      `json:"complete"`
	Issues     []string  `json:"issues"`
	Nodes      []product `json:"nodes"`
	Edges      []struct {
		From          string `json:"from"`
		To            string `json:"to"`
		Input         string `json:"input"`
		Output        string `json:"output"`
		Depth         int    `json:"depth"`
		State         string `json:"state"`
		Compatibility string `json:"compatibility"`
		Requirement   *struct {
			Protocol       string `json:"protocol"`
			MinimumVersion string `json:"minimumVersion"`
		} `json:"requirement,omitempty"`
	} `json:"edges"`
}

// readTraceFile confines a bounded regular-file read to its directory and rejects unknown JSON fields.
// It checks the example's supported profile; untrusted traces also require the published schema.
func readTraceFile(path string) (trace, error) {
	var result trace
	directory, name := filepath.Split(path)
	if directory == "" {
		directory = "."
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("select a regular trace file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return result, errors.New("trace exceeds 2 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return result, errors.New("trailing trace data")
	}
	if result.APIVersion != "data-product-lineage/v1" || result.Root == "" ||
		len(result.Nodes) > 256 ||
		len(result.Edges) > 1024 {
		return result, errors.New("unsupported trace")
	}
	return result, nil
}

// main prints dependency observations without contacting Kubernetes, sources or product endpoints.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run main.go trace.json")
		os.Exit(2)
	}
	result, err := readTraceFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read a supported dependency trace.")
		os.Exit(1)
	}
	state := "incomplete"
	if result.Complete {
		state = "complete"
	}
	fmt.Printf("%q: %s\n", result.Root, state)
	nodes := map[string]string{}
	for _, node := range result.Nodes {
		nodes[node.Key] = node.State
	}
	for _, edge := range result.Edges {
		health := nodes[edge.To]
		if health == "" {
			health = "not-inspected"
		}
		fmt.Printf(
			"%q input %q -> %q output %q: %q; producer %q; trace %q\n",
			edge.From,
			edge.Input,
			edge.To,
			edge.Output,
			edge.Compatibility,
			health,
			edge.State,
		)
	}
}
