package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
)

type preloadFileReport struct {
	WorkerID        int
	ObjectName      string
	ReportDate      string
	TimeCreated     string
	SizeBytes       int64
	SizeDisplay     string
	InspectionMode  string
	CSVLines        int
	DownloadSeconds float64
	Status          string
	Error           string
}

type preloadReportSummary struct {
	DetailReportPath     string
	SummaryReportPath    string
	CapacityReportPath   string
	StartedAt            time.Time
	EndedAt              time.Time
	Duration             time.Duration
	TotalFiles           int
	SuccessfulFiles      int
	FailedFiles          int
	TotalBytes           int64
	InspectedBytes       int64
	TotalCSVLines        int
	ContentScanSkipped   bool
	ObservedBytesPerSec  float64
	EstimatedDownloadETA time.Duration
}

func createPreloadReport(
	ctx context.Context,
	db *sql.DB,
	objectClient objectstorage.ObjectStorageClient,
	objects []objectstorage.ObjectSummary,
	cmd commandLine,
	cfg appConfig,
	namespaceName string,
	bucketName string,
) (preloadReportSummary, error) {
	startedAt := time.Now()
	reportPath := cmd.preloadReportFile
	if strings.TrimSpace(reportPath) == "" {
		reportPath = filepath.Join(workReportDir, "preload_report.csv")
	}
	summaryPath := summaryReportPath(reportPath)
	capacityPath := capacityReportPath(reportPath)

	if len(objects) == 0 {
		summary := preloadReportSummary{
			DetailReportPath:   reportPath,
			SummaryReportPath:  summaryPath,
			CapacityReportPath: capacityPath,
			StartedAt:          startedAt,
			EndedAt:            time.Now(),
			ContentScanSkipped: cmd.skipPreloadContentScan,
		}
		summary.Duration = summary.EndedAt.Sub(summary.StartedAt)
		if err := writePreloadReports(reportPath, summaryPath, nil, summary); err != nil {
			return summary, err
		}
		if err := writeCapacityPlanningReport(ctx, db, cfg, capacityPath, summary); err != nil {
			return summary, err
		}
		return summary, nil
	}

	workerCount := cmd.workers
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(objects) {
		workerCount = len(objects)
	}

	jobs := make(chan objectstorage.ObjectSummary)
	results := make(chan preloadFileReport, len(objects))
	var wg sync.WaitGroup

	for workerID := 1; workerID <= workerCount; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for objectFile := range jobs {
				if cmd.skipPreloadContentScan {
					results <- inspectPreloadObjectMetadata(workerID, objectFile)
					continue
				}
				results <- inspectPreloadObject(ctx, objectClient, workerID, objectFile, namespaceName, bucketName)
			}
		}(workerID)
	}

sendJobs:
	for _, objectFile := range objects {
		select {
		case <-ctx.Done():
			break sendJobs
		case jobs <- objectFile:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	var rows []preloadFileReport
	completed := 0
	for result := range results {
		rows = append(rows, result)
		completed++
		printPreloadProgress(result, completed, len(objects), cmd.verbose)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ObjectName < rows[j].ObjectName })

	summary := preloadReportSummary{
		DetailReportPath:   reportPath,
		SummaryReportPath:  summaryPath,
		CapacityReportPath: capacityPath,
		StartedAt:          startedAt,
		EndedAt:            time.Now(),
		TotalFiles:         len(objects),
		ContentScanSkipped: cmd.skipPreloadContentScan,
	}
	summary.Duration = summary.EndedAt.Sub(summary.StartedAt)
	for _, row := range rows {
		summary.TotalBytes += row.SizeBytes
		summary.TotalCSVLines += row.CSVLines
		if row.Status == "SUCCESS" {
			summary.SuccessfulFiles++
			if row.InspectionMode == "CONTENT_SCAN" {
				summary.InspectedBytes += row.SizeBytes
			}
		} else {
			summary.FailedFiles++
		}
	}
	if summary.Duration.Seconds() > 0 {
		summary.ObservedBytesPerSec = float64(summary.InspectedBytes) / summary.Duration.Seconds()
	}
	if summary.ObservedBytesPerSec > 0 {
		summary.EstimatedDownloadETA = time.Duration(float64(summary.TotalBytes)/summary.ObservedBytesPerSec) * time.Second
	}

	if err := writePreloadReports(reportPath, summaryPath, rows, summary); err != nil {
		return summary, err
	}
	if err := writeCapacityPlanningReport(ctx, db, cfg, capacityPath, summary); err != nil {
		return summary, err
	}
	if summary.FailedFiles > 0 {
		return summary, fmt.Errorf("%d file(s) failed during pre-load inspection; see %s", summary.FailedFiles, reportPath)
	}
	return summary, nil
}

func inspectPreloadObject(
	ctx context.Context,
	objectClient objectstorage.ObjectStorageClient,
	workerID int,
	objectFile objectstorage.ObjectSummary,
	namespaceName string,
	bucketName string,
) preloadFileReport {
	start := time.Now()
	objectName := value(objectFile.Name)
	report := preloadFileReport{
		WorkerID:       workerID,
		ObjectName:     objectName,
		ReportDate:     objectFileDate(objectName),
		SizeBytes:      valueInt64(objectFile.Size),
		SizeDisplay:    formatBytes(valueInt64(objectFile.Size)),
		InspectionMode: "CONTENT_SCAN",
		Status:         "STARTED",
	}
	if objectFile.TimeCreated != nil {
		report.TimeCreated = objectFile.TimeCreated.Time.Format(time.RFC3339)
	}

	resp, err := objectClient.GetObject(ctx, objectstorage.GetObjectRequest{
		NamespaceName: common.String(namespaceName),
		BucketName:    common.String(bucketName),
		ObjectName:    common.String(objectName),
	})
	if err != nil {
		return finishPreloadReport(report, start, fmt.Errorf("get object: %w", err))
	}
	defer resp.Content.Close()

	lines, err := countGzipCSVLines(resp.Content)
	if err != nil {
		return finishPreloadReport(report, start, err)
	}

	report.CSVLines = lines
	report.Status = "SUCCESS"
	return finishPreloadReport(report, start, nil)
}

func inspectPreloadObjectMetadata(workerID int, objectFile objectstorage.ObjectSummary) preloadFileReport {
	objectName := value(objectFile.Name)
	report := preloadFileReport{
		WorkerID:       workerID,
		ObjectName:     objectName,
		ReportDate:     objectFileDate(objectName),
		SizeBytes:      valueInt64(objectFile.Size),
		SizeDisplay:    formatBytes(valueInt64(objectFile.Size)),
		InspectionMode: "METADATA_ONLY",
		Status:         "SUCCESS",
	}
	if objectFile.TimeCreated != nil {
		report.TimeCreated = objectFile.TimeCreated.Time.Format(time.RFC3339)
	}
	return report
}

func printPreloadProgress(result preloadFileReport, completed, total int, verbose bool) {
	if !verbose && completed != total && completed%100 != 0 {
		return
	}
	if result.InspectionMode == "METADATA_ONLY" {
		fmt.Printf("   Pre-load progress %d/%d | worker=%d | mode=metadata-only | status=%s | size=%s | object=%s\n",
			completed, total, result.WorkerID, result.Status, result.SizeDisplay, result.ObjectName)
		return
	}
	throughput := float64(0)
	if result.DownloadSeconds > 0 {
		throughput = float64(result.SizeBytes) / result.DownloadSeconds
	}
	fmt.Printf("   Pre-load progress %d/%d | worker=%d | status=%s | size=%s | rows=%d | elapsed=%.3fs | throughput=%s/s | object=%s\n",
		completed, total, result.WorkerID, result.Status, result.SizeDisplay, result.CSVLines,
		result.DownloadSeconds, formatBytes(int64(throughput)), result.ObjectName)
	if result.Error != "" {
		fmt.Printf("      Error: %s\n", result.Error)
	}
}

func finishPreloadReport(report preloadFileReport, start time.Time, err error) preloadFileReport {
	report.DownloadSeconds = time.Since(start).Seconds()
	if err != nil {
		report.Status = "FAILED"
		report.Error = err.Error()
	}
	return report
}

func countGzipCSVLines(input io.Reader) (int, error) {
	gz, err := gzip.NewReader(input)
	if err != nil {
		return 0, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	reader := csv.NewReader(gz)
	reader.FieldsPerRecord = -1

	rows := 0
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rows, fmt.Errorf("read csv row %d: %w", rows+1, err)
		}
		rows++
	}
	if rows > 0 {
		return rows - 1, nil
	}
	return 0, nil
}

func writePreloadReports(detailPath, summaryPath string, rows []preloadFileReport, summary preloadReportSummary) error {
	if err := ensureReportDir(detailPath); err != nil {
		return err
	}
	if err := ensureReportDir(summaryPath); err != nil {
		return err
	}
	if err := writePreloadDetailReport(detailPath, rows); err != nil {
		return err
	}
	return writePreloadSummaryReport(summaryPath, summary)
}

func writePreloadDetailReport(path string, rows []preloadFileReport) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create pre-load detail report %s: %w", path, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"worker_id",
		"object_name",
		"report_date",
		"time_created",
		"size_bytes",
		"size",
		"inspection_mode",
		"csv_lines_excluding_header",
		"download_inspection_seconds",
		"status",
		"error",
	}); err != nil {
		return err
	}

	for _, row := range rows {
		if err := writer.Write([]string{
			fmt.Sprint(row.WorkerID),
			row.ObjectName,
			row.ReportDate,
			row.TimeCreated,
			fmt.Sprint(row.SizeBytes),
			row.SizeDisplay,
			row.InspectionMode,
			fmt.Sprint(row.CSVLines),
			fmt.Sprintf("%.3f", row.DownloadSeconds),
			row.Status,
			row.Error,
		}); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writePreloadSummaryReport(path string, summary preloadReportSummary) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create pre-load summary report %s: %w", path, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"started_at",
		"ended_at",
		"duration_seconds",
		"duration",
		"total_files",
		"successful_files",
		"failed_files",
		"total_bytes",
		"total_size",
		"total_csv_lines_excluding_headers",
		"observed_bytes_per_second",
		"estimated_download_eta_seconds",
		"estimated_download_eta",
		"content_scan_skipped",
		"inspected_bytes",
	}); err != nil {
		return err
	}

	return writer.Write([]string{
		summary.StartedAt.Format(time.RFC3339),
		summary.EndedAt.Format(time.RFC3339),
		fmt.Sprintf("%.0f", summary.Duration.Seconds()),
		formatElapsed(summary.Duration),
		fmt.Sprint(summary.TotalFiles),
		fmt.Sprint(summary.SuccessfulFiles),
		fmt.Sprint(summary.FailedFiles),
		fmt.Sprint(summary.TotalBytes),
		formatBytes(summary.TotalBytes),
		fmt.Sprint(summary.TotalCSVLines),
		fmt.Sprintf("%.2f", summary.ObservedBytesPerSec),
		fmt.Sprintf("%.0f", summary.EstimatedDownloadETA.Seconds()),
		formatElapsed(summary.EstimatedDownloadETA),
		fmt.Sprint(summary.ContentScanSkipped),
		fmt.Sprint(summary.InspectedBytes),
	})
}

func ensureReportDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func summaryReportPath(detailPath string) string {
	ext := filepath.Ext(detailPath)
	if ext == "" {
		return detailPath + "_summary.csv"
	}
	return strings.TrimSuffix(detailPath, ext) + "_summary" + ext
}

func capacityReportPath(detailPath string) string {
	ext := filepath.Ext(detailPath)
	if ext == "" {
		return detailPath + "_capacity.csv"
	}
	return strings.TrimSuffix(detailPath, ext) + "_capacity" + ext
}

func formatBytes(bytes int64) string {
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
