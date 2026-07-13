package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
)

func TestInspectPreloadObjectMetadataDoesNotScanContent(t *testing.T) {
	result := inspectPreloadObjectMetadata(3, objectstorage.ObjectSummary{
		Name: common.String("FOCUS Reports/2026/07/01/example.csv.gz"),
		Size: common.Int64(12345),
	})

	if result.WorkerID != 3 {
		t.Fatalf("worker ID = %d, want 3", result.WorkerID)
	}
	if result.InspectionMode != "METADATA_ONLY" {
		t.Fatalf("inspection mode = %q, want METADATA_ONLY", result.InspectionMode)
	}
	if result.Status != "SUCCESS" {
		t.Fatalf("status = %q, want SUCCESS", result.Status)
	}
	if result.CSVLines != 0 || result.DownloadSeconds != 0 {
		t.Fatalf("metadata-only result unexpectedly scanned content: rows=%d seconds=%v", result.CSVLines, result.DownloadSeconds)
	}
	if result.SizeBytes != 12345 {
		t.Fatalf("size = %d, want 12345", result.SizeBytes)
	}
}

func TestMetadataOnlyReportSchemaMarksSkippedScan(t *testing.T) {
	dir := t.TempDir()
	detailPath := filepath.Join(dir, "preload.csv")
	summaryPath := filepath.Join(dir, "preload_summary.csv")
	row := inspectPreloadObjectMetadata(1, objectstorage.ObjectSummary{
		Name: common.String("FOCUS Reports/2026/07/01/example.csv.gz"),
		Size: common.Int64(12345),
	})
	summary := preloadReportSummary{
		TotalFiles:         1,
		SuccessfulFiles:    1,
		TotalBytes:         12345,
		ContentScanSkipped: true,
	}

	if err := writePreloadReports(detailPath, summaryPath, []preloadFileReport{row}, summary); err != nil {
		t.Fatal(err)
	}
	detail := readCSVForTest(t, detailPath)
	if got, want := detail[0][0], "worker_id"; got != want {
		t.Fatalf("first detail header = %q, want %q", got, want)
	}
	if got, want := detail[1][6], "METADATA_ONLY"; got != want {
		t.Fatalf("inspection mode = %q, want %q", got, want)
	}
	summaryRows := readCSVForTest(t, summaryPath)
	if got, want := summaryRows[0][13], "content_scan_skipped"; got != want {
		t.Fatalf("summary header = %q, want %q", got, want)
	}
	if got, want := summaryRows[1][13], "true"; got != want {
		t.Fatalf("content_scan_skipped = %q, want %q", got, want)
	}
	if got, want := summaryRows[1][14], "0"; got != want {
		t.Fatalf("inspected_bytes = %q, want %q", got, want)
	}
}

func readCSVForTest(t *testing.T, pathName string) [][]string {
	t.Helper()
	file, err := os.Open(pathName)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
