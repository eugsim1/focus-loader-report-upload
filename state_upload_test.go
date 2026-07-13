package main

import (
	"path/filepath"
	"testing"
)

func TestUploadStateCheckpoint(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "uploaded_files.jsonl")
	store, err := newProcessedStore(statePath, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	result := loadResult{FileName: "FOCUS Reports/2026/07/01/file.csv.gz", FileID: "file", NumRows: 42, FileSizeMB: 3}
	if err := store.MarkUploaded(result); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newProcessedStore(statePath, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.ProcessedFiles()[result.FileName]; !ok {
		t.Fatalf("uploaded file %q was not restored from checkpoint", result.FileName)
	}
}
