// Package uibundle gives embedded browser assets content-addressed URLs.
package uibundle

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Source identifies one compile-time asset and its response type.
type Source struct {
	FS          fs.FS
	Path        string
	ContentType string
}

type asset struct {
	contents    []byte
	contentType string
	immutable   bool
}

// Bundle holds a document and the exact asset names that it references.
type Bundle struct {
	html   []byte
	assets map[string]asset
}

// Load rewrites embedded asset references using a SHA-256 fingerprint of their bytes.
func Load(indexFS fs.FS, indexPath string, sources ...Source) (*Bundle, error) {
	html, err := fs.ReadFile(indexFS, indexPath)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{html: html, assets: make(map[string]asset, len(sources)*2)}
	for _, source := range sources {
		contents, readErr := fs.ReadFile(source.FS, source.Path)
		if readErr != nil {
			return nil, readErr
		}
		name := path.Base(source.Path)
		ext := path.Ext(name)
		fingerprinted := fmt.Sprintf(
			"%s-%x%s",
			strings.TrimSuffix(name, ext),
			sha256.Sum256(contents),
			ext,
		)
		if _, duplicate := bundle.assets[name]; duplicate {
			return nil, fmt.Errorf("duplicate UI asset: %s", name)
		}
		bundle.assets[name] = asset{contents: contents, contentType: source.ContentType}
		bundle.assets[fingerprinted] = asset{
			contents:    contents,
			contentType: source.ContentType,
			immutable:   true,
		}
		bundle.html = bytes.ReplaceAll(
			bundle.html,
			[]byte("assets/"+name),
			[]byte("assets/"+fingerprinted),
		)
	}
	return bundle, nil
}

// ServeHTML prevents a release's document from outliving its embedded asset set.
func (b *Bundle) ServeHTML(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(b.html)
}

// ServeAsset permits only exact compiled asset names, including legacy URLs.
func (b *Bundle) ServeAsset(writer http.ResponseWriter, request *http.Request, name string) {
	file, ok := b.assets[name]
	if !ok {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", file.contentType)
	writer.Header().Set("Cache-Control", "no-store")
	if file.immutable {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	// Only bytes loaded from the compile-time embedded bundle reach this response.
	//nolint:gosec
	_, _ = writer.Write(file.contents)
}
