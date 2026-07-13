package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
)

type transformedObjectWriter interface {
	GetBucket(context.Context, objectstorage.GetBucketRequest) (objectstorage.GetBucketResponse, error)
	PutObject(context.Context, objectstorage.PutObjectRequest) (objectstorage.PutObjectResponse, error)
}

type transformedFileUploadResult struct {
	WorkerID              int
	SourceObjectName      string
	LocalCSVPath          string
	DestinationNamespace  string
	DestinationBucket     string
	DestinationObjectName string
	Rows                  int
	SizeBytes             int64
	StartedAt             time.Time
	EndedAt               time.Time
	DurationSeconds       float64
	Status                string
	ETag                  string
	VersionID             string
	OpcRequestID          string
	Error                 string
	LocalFileRetained     bool
}

type transformedUploadSummary struct {
	DestinationNamespace string
	DestinationBucket    string
	ObjectPrefix         string
	ResultsReportPath    string
	ResultsObjectName    string
	SuccessfulFiles      int
	FailedFiles          int
	TotalRows            int
	TotalBytes           int64
}

type transformedUploadSession struct {
	destination          transformedObjectWriter
	destinationNamespace string
	destinationBucket    string
	objectPrefix         string
	resultsReportPath    string
	latestUploadPath     string
	keepWorkFiles        bool
	verbose              bool
	totalFiles           int
	mu                   sync.Mutex
	results              []transformedFileUploadResult
	latestUploadEndedAt  time.Time
}

func newTransformedUploadSession(
	ctx context.Context,
	destination transformedObjectWriter,
	destinationNamespace string,
	destinationBucket string,
	objectPrefix string,
	resultsReportPath string,
	latestUploadPath string,
	keepWorkFiles bool,
	verbose bool,
	totalFiles int,
) (*transformedUploadSession, error) {
	session := &transformedUploadSession{
		destination:          destination,
		destinationNamespace: strings.TrimSpace(destinationNamespace),
		destinationBucket:    strings.TrimSpace(destinationBucket),
		objectPrefix:         normalizeObjectPrefix(objectPrefix),
		resultsReportPath:    strings.TrimSpace(resultsReportPath),
		latestUploadPath:     strings.TrimSpace(latestUploadPath),
		keepWorkFiles:        keepWorkFiles,
		verbose:              verbose,
		totalFiles:           totalFiles,
	}
	if session.destinationNamespace == "" || session.destinationBucket == "" {
		return nil, fmt.Errorf("destination namespace and bucket are required")
	}
	if session.resultsReportPath == "" {
		session.resultsReportPath = filepath.Join(workReportDir, "focus_file_upload_results.csv")
	}
	if session.latestUploadPath == "" {
		session.latestUploadPath = filepath.Join(workReportDir, "latest_focus_file_upload.csv")
	}
	if _, err := destination.GetBucket(ctx, objectstorage.GetBucketRequest{
		NamespaceName: common.String(session.destinationNamespace),
		BucketName:    common.String(session.destinationBucket),
	}); err != nil {
		return nil, fmt.Errorf("validate destination bucket namespace=%s bucket=%s: %w",
			session.destinationNamespace, session.destinationBucket, err)
	}
	return session, nil
}

func (s *transformedUploadSession) Upload(
	ctx context.Context,
	workerID int,
	sourceObjectName string,
	localCSVPath string,
	rows int,
) error {
	result := transformedFileUploadResult{
		WorkerID:              workerID,
		SourceObjectName:      sourceObjectName,
		LocalCSVPath:          localCSVPath,
		DestinationNamespace:  s.destinationNamespace,
		DestinationBucket:     s.destinationBucket,
		DestinationObjectName: transformedDestinationObjectName(s.objectPrefix, sourceObjectName),
		Rows:                  rows,
		StartedAt:             time.Now(),
		Status:                "STARTED",
		LocalFileRetained:     s.keepWorkFiles,
	}

	file, err := os.Open(localCSVPath)
	if err != nil {
		result = finishTransformedFileUpload(result, fmt.Errorf("open transformed CSV: %w", err))
		s.record(result)
		return fmt.Errorf("upload transformed CSV %s: %w", sourceObjectName, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		result = finishTransformedFileUpload(result, fmt.Errorf("stat transformed CSV: %w", err))
		s.record(result)
		return fmt.Errorf("upload transformed CSV %s: %w", sourceObjectName, err)
	}
	result.SizeBytes = info.Size()

	response, err := s.destination.PutObject(ctx, objectstorage.PutObjectRequest{
		NamespaceName: common.String(s.destinationNamespace),
		BucketName:    common.String(s.destinationBucket),
		ObjectName:    common.String(result.DestinationObjectName),
		ContentLength: common.Int64(result.SizeBytes),
		ContentType:   common.String("text/csv"),
		PutObjectBody: file,
	})
	if err != nil {
		result = finishTransformedFileUpload(result, fmt.Errorf("put transformed CSV: %w", err))
		s.record(result)
		return fmt.Errorf("upload transformed CSV %s: %w", sourceObjectName, err)
	}

	result.Status = "SUCCESS"
	result.ETag = value(response.ETag)
	result.VersionID = value(response.VersionId)
	result.OpcRequestID = value(response.OpcRequestId)
	result = finishTransformedFileUpload(result, nil)
	if err := s.record(result); err != nil {
		return fmt.Errorf("upload transformed CSV %s succeeded but latest-upload checkpoint failed: %w", sourceObjectName, err)
	}
	return nil
}

func (s *transformedUploadSession) record(result transformedFileUploadResult) error {
	s.mu.Lock()
	s.results = append(s.results, result)
	completed := len(s.results)
	var checkpointErr error
	if result.Status == "SUCCESS" && !result.EndedAt.Before(s.latestUploadEndedAt) {
		checkpointErr = writeLatestTransformedUpload(s.latestUploadPath, result)
		if checkpointErr == nil {
			s.latestUploadEndedAt = result.EndedAt
		}
	}
	s.mu.Unlock()
	if s.verbose || completed == s.totalFiles || completed%100 == 0 {
		throughput := float64(0)
		if result.DurationSeconds > 0 {
			throughput = float64(result.SizeBytes) / result.DurationSeconds
		}
		fmt.Printf("   Transformed CSV upload %d/%d | worker=%d | status=%s | rows=%d | size=%s | elapsed=%.3fs | throughput=%s/s | destination=%s\n",
			completed, s.totalFiles, result.WorkerID, result.Status, result.Rows, formatUploadBytes(result.SizeBytes),
			result.DurationSeconds, formatUploadBytes(int64(throughput)), result.DestinationObjectName)
		if result.Error != "" {
			fmt.Printf("      Error: %s\n", result.Error)
		}
	}
	return checkpointErr
}

func finishTransformedFileUpload(result transformedFileUploadResult, err error) transformedFileUploadResult {
	result.EndedAt = time.Now()
	result.DurationSeconds = result.EndedAt.Sub(result.StartedAt).Seconds()
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
	}
	return result
}

func (s *transformedUploadSession) Finalize(ctx context.Context) (transformedUploadSummary, error) {
	s.mu.Lock()
	results := append([]transformedFileUploadResult(nil), s.results...)
	s.mu.Unlock()
	sort.Slice(results, func(i, j int) bool { return results[i].SourceObjectName < results[j].SourceObjectName })

	summary := transformedUploadSummary{
		DestinationNamespace: s.destinationNamespace,
		DestinationBucket:    s.destinationBucket,
		ObjectPrefix:         s.objectPrefix,
		ResultsReportPath:    s.resultsReportPath,
	}
	for _, result := range results {
		summary.TotalRows += result.Rows
		summary.TotalBytes += result.SizeBytes
		if result.Status == "SUCCESS" {
			summary.SuccessfulFiles++
		} else {
			summary.FailedFiles++
		}
	}
	if err := writeTransformedUploadResults(summary.ResultsReportPath, results); err != nil {
		return summary, err
	}
	summary.ResultsObjectName = destinationObjectName(s.objectPrefix, filepath.Base(summary.ResultsReportPath))
	if err := uploadLocalCSV(ctx, s.destination, summary.ResultsReportPath, s.destinationNamespace, s.destinationBucket, summary.ResultsObjectName); err != nil {
		return summary, fmt.Errorf("upload transformed-file results CSV: %w", err)
	}
	if summary.FailedFiles > 0 {
		return summary, fmt.Errorf("%d transformed CSV upload(s) failed; see %s", summary.FailedFiles, summary.ResultsReportPath)
	}
	return summary, nil
}

func writeTransformedUploadResults(pathName string, results []transformedFileUploadResult) error {
	if err := ensureUploadReportDir(pathName); err != nil {
		return err
	}
	file, err := os.Create(pathName)
	if err != nil {
		return fmt.Errorf("create transformed upload results %s: %w", pathName, err)
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{
		"worker_id", "source_object_name", "local_csv_path", "destination_namespace",
		"destination_bucket", "destination_object_name", "rows", "size_bytes",
		"started_at", "ended_at", "duration_seconds", "status", "etag", "version_id",
		"opc_request_id", "local_file_retained", "error",
	}); err != nil {
		return err
	}
	for _, result := range results {
		if err := writer.Write([]string{
			fmt.Sprint(result.WorkerID), result.SourceObjectName, result.LocalCSVPath,
			result.DestinationNamespace, result.DestinationBucket, result.DestinationObjectName,
			fmt.Sprint(result.Rows), fmt.Sprint(result.SizeBytes),
			result.StartedAt.Format(time.RFC3339Nano), result.EndedAt.Format(time.RFC3339Nano),
			fmt.Sprintf("%.3f", result.DurationSeconds), result.Status, result.ETag,
			result.VersionID, result.OpcRequestID, fmt.Sprint(result.LocalFileRetained), result.Error,
		}); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writeLatestTransformedUpload(pathName string, result transformedFileUploadResult) error {
	if err := ensureUploadReportDir(pathName); err != nil {
		return err
	}
	file, err := os.Create(pathName)
	if err != nil {
		return fmt.Errorf("create latest upload checkpoint %s: %w", pathName, err)
	}
	writer := csv.NewWriter(file)
	header := []string{
		"source_object_name", "local_csv_path", "local_file_retained",
		"destination_namespace", "destination_bucket", "destination_object_name",
		"rows", "size_bytes", "uploaded_at", "etag", "version_id", "opc_request_id",
	}
	record := []string{
		result.SourceObjectName, result.LocalCSVPath, fmt.Sprint(result.LocalFileRetained),
		result.DestinationNamespace, result.DestinationBucket, result.DestinationObjectName,
		fmt.Sprint(result.Rows), fmt.Sprint(result.SizeBytes), result.EndedAt.Format(time.RFC3339Nano),
		result.ETag, result.VersionID, result.OpcRequestID,
	}
	if err := writer.Write(header); err == nil {
		err = writer.Write(record)
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write latest upload checkpoint %s: %w", pathName, err)
	}
	return nil
}

func uploadLocalCSV(ctx context.Context, destination transformedObjectWriter, localPath, namespaceName, bucketName, objectName string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	_, err = destination.PutObject(ctx, objectstorage.PutObjectRequest{
		NamespaceName: common.String(namespaceName),
		BucketName:    common.String(bucketName),
		ObjectName:    common.String(objectName),
		ContentLength: common.Int64(info.Size()),
		ContentType:   common.String("text/csv"),
		PutObjectBody: file,
	})
	return err
}

func ensureUploadReportDir(pathName string) error {
	dir := filepath.Dir(pathName)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func formatUploadBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func normalizeObjectPrefix(prefix string) string {
	prefix = strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	return strings.Trim(prefix, "/")
}

func destinationObjectName(prefix, objectName string) string {
	objectName = strings.TrimLeft(strings.ReplaceAll(objectName, "\\", "/"), "/")
	if normalized := normalizeObjectPrefix(prefix); normalized != "" {
		return path.Join(normalized, objectName)
	}
	return objectName
}

func transformedDestinationObjectName(prefix, sourceObjectName string) string {
	return destinationObjectName(prefix, strings.TrimSuffix(sourceObjectName, ".gz"))
}
