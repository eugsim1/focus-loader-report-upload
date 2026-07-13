package main

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
)

type mockTransformedDestination struct {
	mu             sync.Mutex
	requests       []objectstorage.PutObjectRequest
	bodies         map[string]string
	failOn         string
	bucketError    error
	getBucketCalls int
}

func (m *mockTransformedDestination) GetBucket(_ context.Context, _ objectstorage.GetBucketRequest) (objectstorage.GetBucketResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBucketCalls++
	return objectstorage.GetBucketResponse{}, m.bucketError
}

func (m *mockTransformedDestination) PutObject(_ context.Context, request objectstorage.PutObjectRequest) (objectstorage.PutObjectResponse, error) {
	data, err := io.ReadAll(request.PutObjectBody)
	if err != nil {
		return objectstorage.PutObjectResponse{}, err
	}
	objectName := value(request.ObjectName)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	if m.bodies == nil {
		m.bodies = map[string]string{}
	}
	m.bodies[objectName] = string(data)
	if objectName == m.failOn {
		return objectstorage.PutObjectResponse{}, errors.New("simulated upload failure")
	}
	return objectstorage.PutObjectResponse{
		ETag:         common.String("etag-" + filepath.Base(objectName)),
		VersionId:    common.String("version-1"),
		OpcRequestId: common.String("request-1"),
	}, nil
}

func TestTransformedUploadSessionUploadsCSVBeforeFinalize(t *testing.T) {
	dir := t.TempDir()
	csvA := writeUploadTestFile(t, dir, "a.csv", "transformed-a")
	csvB := writeUploadTestFile(t, dir, "b.csv", "transformed-b")
	resultsPath := filepath.Join(dir, "focus_file_upload_results.csv")
	destination := &mockTransformedDestination{}

	session, err := newTransformedUploadSession(
		context.Background(), destination, "destination_ns", "destination_bucket",
		"archive", resultsPath, filepath.Join(dir, "latest.csv"), false, false, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Upload(context.Background(), 1, "FOCUS Reports/2025/01/01/a.csv.gz", csvA, 10); err != nil {
		t.Fatal(err)
	}
	if err := session.Upload(context.Background(), 2, "FOCUS Reports/2025/01/02/b.csv.gz", csvB, 20); err != nil {
		t.Fatal(err)
	}
	summary, err := session.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if summary.SuccessfulFiles != 2 || summary.FailedFiles != 0 || summary.TotalRows != 30 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if destination.getBucketCalls != 1 {
		t.Fatalf("GetBucket calls = %d, want 1", destination.getBucketCalls)
	}
	if got, want := len(destination.requests), 3; got != want {
		t.Fatalf("PutObject calls = %d, want %d", got, want)
	}
	for objectName, content := range map[string]string{
		"archive/FOCUS Reports/2025/01/01/a.csv": "transformed-a",
		"archive/FOCUS Reports/2025/01/02/b.csv": "transformed-b",
	} {
		if got := destination.bodies[objectName]; got != content {
			t.Fatalf("destination object %q = %q, want %q", objectName, got, content)
		}
	}
	if _, ok := destination.bodies["archive/focus_file_upload_results.csv"]; !ok {
		t.Fatal("upload-results CSV was not uploaded")
	}

	file, err := os.Open(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 3; got != want {
		t.Fatalf("CSV rows including header = %d, want %d", got, want)
	}
	if records[1][11] != "SUCCESS" || records[2][11] != "SUCCESS" {
		t.Fatalf("unexpected statuses: %q, %q", records[1][11], records[2][11])
	}

	latestFile, err := os.Open(filepath.Join(dir, "latest.csv"))
	if err != nil {
		t.Fatal(err)
	}
	latestRecords, err := csv.NewReader(latestFile).ReadAll()
	latestFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(latestRecords), 2; got != want {
		t.Fatalf("latest checkpoint rows including header = %d, want %d", got, want)
	}
	if got, want := latestRecords[1][0], "FOCUS Reports/2025/01/02/b.csv.gz"; got != want {
		t.Fatalf("latest checkpoint source = %q, want %q", got, want)
	}
	if got := latestRecords[1][2]; got != "false" {
		t.Fatalf("local_file_retained = %q, want false", got)
	}
}

func TestTransformedUploadSessionRecordsFailure(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeUploadTestFile(t, dir, "a.csv", "transformed-a")
	objectName := "FOCUS Reports/2025/01/01/a.csv"
	destination := &mockTransformedDestination{failOn: objectName}
	session, err := newTransformedUploadSession(
		context.Background(), destination, "destination_ns", "destination_bucket",
		"", filepath.Join(dir, "results.csv"), filepath.Join(dir, "latest.csv"), false, false, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Upload(context.Background(), 1, objectName+".gz", csvPath, 10); err == nil {
		t.Fatal("expected transformed upload to fail")
	}
	summary, err := session.Finalize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1 transformed CSV") {
		t.Fatalf("finalize error = %v, want failed-upload summary", err)
	}
	if summary.FailedFiles != 1 {
		t.Fatalf("failed files = %d, want 1", summary.FailedFiles)
	}
	if _, ok := destination.bodies["results.csv"]; !ok {
		t.Fatal("results CSV should be uploaded after a transformed-file failure")
	}
}

func TestTransformedUploadSessionValidatesDestination(t *testing.T) {
	destination := &mockTransformedDestination{bucketError: errors.New("bucket not found")}
	_, err := newTransformedUploadSession(
		context.Background(), destination, "destination_ns", "missing_bucket",
		"", filepath.Join(t.TempDir(), "results.csv"), filepath.Join(t.TempDir(), "latest.csv"), false, false, 1,
	)
	if err == nil || !strings.Contains(err.Error(), "validate destination bucket") {
		t.Fatalf("error = %v, want destination validation error", err)
	}
	if len(destination.requests) != 0 {
		t.Fatalf("unexpected PutObject requests: %d", len(destination.requests))
	}
}

func TestParseArgsUploadMode(t *testing.T) {
	cmd, err := parseArgs([]string{"-upload-reports", "-report-upload-bucket", "reports"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.preloadReport || !cmd.uploadReports || cmd.loadAfterUpload || !cmd.skipTagRows || !cmd.skipTagKeys {
		t.Fatalf("unexpected safe upload-only command: %+v", cmd)
	}
	if cmd.uploadStateFile != filepath.Join(workReportDir, "uploaded_files.jsonl") {
		t.Fatalf("upload state file = %q", cmd.uploadStateFile)
	}

	cmd, err = parseArgs([]string{"-upload-reports", "-report-upload-bucket", "reports", "-load-after-upload"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.preloadReport || !cmd.uploadReports || !cmd.loadAfterUpload {
		t.Fatalf("unexpected upload-and-load command: %+v", cmd)
	}

	if _, err := parseArgs([]string{"-load-after-upload"}); err == nil {
		t.Fatal("expected -load-after-upload without -upload-reports to fail")
	}
	if _, err := parseArgs([]string{"-upload-reports"}); err == nil {
		t.Fatal("expected -upload-reports without destination bucket to fail")
	}
	if _, err := parseArgs([]string{"-upload-reports", "-report-upload-bucket", "reports", "-continue-after-report"}); err == nil {
		t.Fatal("expected conflicting continuation flags to fail")
	}
}

func TestTransformedDestinationObjectName(t *testing.T) {
	if got, want := transformedDestinationObjectName("", "FOCUS Reports/2025/01/a.csv.gz"), "FOCUS Reports/2025/01/a.csv"; got != want {
		t.Fatalf("transformedDestinationObjectName = %q, want %q", got, want)
	}
	if got, want := transformedDestinationObjectName("archive", "FOCUS Reports/2025/01/a.csv.gz"), "archive/FOCUS Reports/2025/01/a.csv"; got != want {
		t.Fatalf("transformedDestinationObjectName with prefix = %q, want %q", got, want)
	}
}

func writeUploadTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	pathName := filepath.Join(dir, name)
	if err := os.WriteFile(pathName, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return pathName
}
