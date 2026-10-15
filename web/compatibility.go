package web

import (
	"bytes"
	"io/fs"
)

// CompatibilityAssets preserves the self-contained public presentation script using the canonical metadata source.
// Inert inspection uses Assets directly and never includes the presentation runtime.
var CompatibilityAssets fs.FS = compatibilityAssets{}

type compatibilityAssets struct{}

// Open composes only the legacy runtime; every other asset retains its embedded contents.
func (compatibilityAssets) Open(name string) (fs.File, error) {
	file, err := Assets.Open(name)
	if err != nil || name != "ui-contract.js" {
		return file, err
	}
	metadata, err := Assets.ReadFile("metadata.js")
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	runtime, err := Assets.ReadFile(name)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	contents := append(append(metadata, '\n'), runtime...)
	return &compatibilityFile{File: file, contents: bytes.NewReader(contents)}, nil
}

type compatibilityFile struct {
	fs.File
	contents *bytes.Reader
}

// Read serves the combined compile-time sources without fetching another script.
func (f *compatibilityFile) Read(buffer []byte) (int, error) {
	return f.contents.Read(buffer)
}

// Seek retains the file-server support needed for range and conditional requests.
func (f *compatibilityFile) Seek(offset int64, whence int) (int64, error) {
	return f.contents.Seek(offset, whence)
}

// Stat reports the composed size so HTTP responses cannot truncate the metadata prefix.
func (f *compatibilityFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return compatibilityInfo{FileInfo: info, size: f.contents.Size()}, nil
}

type compatibilityInfo struct {
	fs.FileInfo
	size int64
}

// Size describes the delivered compatibility asset rather than its runtime-only source.
func (i compatibilityInfo) Size() int64 { return i.size }
