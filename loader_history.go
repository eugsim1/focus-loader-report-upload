package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	loaderJobHistoryDirectoryName = ".loader_job_history"
	loaderLastFilesLimit          = 10
)

type loaderExecutionHistoryEntry struct {
	JobID                string   `json:"jobId"`
	CreatedAtUTC         string   `json:"createdAtUtc"`
	StartedAtUTC         string   `json:"startedAtUtc"`
	FinishedAtUTC        string   `json:"finishedAtUtc"`
	DatabaseUser         string   `json:"databaseUser"`
	DatabaseAlias        string   `json:"databaseAlias"`
	Status               string   `json:"status"`
	RowsInserted         int64    `json:"rowsInserted"`
	RowsInsertedKnown    bool     `json:"rowsInsertedKnown"`
	CurrentRowCount      int64    `json:"currentRowCount"`
	CurrentRowCountKnown bool     `json:"currentRowCountKnown"`
	LastFilesLoaded      []string `json:"lastFilesLoaded"`
}

type loaderExecutionHistoryResponse struct {
	ActiveJobID string                        `json:"activeJobId"`
	Jobs        []loaderExecutionHistoryEntry `json:"jobs"`
}

func loaderExecutionHistoryDirectory(getenv func(string) string) string {
	workingDirectory := strings.TrimSpace(getenv("FOCUS_LOADER_WORK_DIR"))
	if workingDirectory == "" {
		executable := strings.TrimSpace(getenv("FOCUS_LOADER_EXECUTABLE"))
		if filepath.IsAbs(executable) {
			workingDirectory = filepath.Dir(executable)
		}
	}
	if !filepath.IsAbs(workingDirectory) {
		return ""
	}
	return filepath.Join(filepath.Clean(workingDirectory), workReportDir, loaderJobHistoryDirectoryName)
}

func (service *loaderExecutionService) loadPersistedJobs() {
	if service.historyDirectory == "" {
		return
	}
	entries, err := os.ReadDir(service.historyDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: cannot read loader history %s: %v\n", service.historyDirectory, err)
		return
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(service.historyDirectory, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: cannot read loader history file %s: %v\n", path, err)
			continue
		}
		var response loaderExecutionResponse
		if err := json.Unmarshal(content, &response); err != nil || response.JobID == "" {
			fmt.Fprintf(os.Stderr, "WARN: ignoring invalid loader history file %s\n", path)
			continue
		}
		job := loaderExecutionJobFromResponse(response)
		if job.status == "starting" || job.status == "running" {
			job.status = "interrupted"
			job.finishedAtUTC = now
			job.errorMessage = strings.TrimSpace(job.errorMessage + "\nThe backend service restarted while this loader job was active; its last persisted state was restored, but the process can no longer be monitored.")
		}
		service.jobs[job.id] = job
		if job.status == "interrupted" {
			service.persistJob(job.id)
		}
	}
}

func loaderExecutionJobFromResponse(response loaderExecutionResponse) *loaderExecutionJob {
	return &loaderExecutionJob{
		id:                    response.JobID,
		status:                response.Status,
		databaseUser:          response.DatabaseUser,
		databaseAlias:         response.DatabaseAlias,
		createdAtUTC:          parseOptionalLoaderTime(response.CreatedAtUTC),
		startedAtUTC:          parseOptionalLoaderTime(response.StartedAtUTC),
		finishedAtUTC:         parseOptionalLoaderTime(response.FinishedAtUTC),
		exitCode:              response.ExitCode,
		initialRowCount:       response.InitialRowCount,
		initialRowCountKnown:  response.InitialRowCountKnown,
		currentRowCount:       response.CurrentRowCount,
		currentRowCountKnown:  response.CurrentRowCountKnown,
		rowsInserted:          response.RowsInserted,
		rowsInsertedKnown:     response.RowsInsertedKnown,
		rowCountUpdatedAtUTC:  parseOptionalLoaderTime(response.RowCountUpdatedAtUTC),
		rowCountError:         response.RowCountError,
		monthlyCosts:          append([]loaderMonthlyCost(nil), response.MonthlyCosts...),
		services:              append([]string(nil), response.Services...),
		analyticsUpdatedAtUTC: parseOptionalLoaderTime(response.AnalyticsUpdatedAtUTC),
		analyticsError:        response.AnalyticsError,
		executable:            response.Executable,
		workingDirectory:      response.WorkingDirectory,
		commandLine:           response.CommandLine,
		manualCommand:         response.ManualCommand,
		tnsAdmin:              response.TNSAdmin,
		homeDirectory:         response.HomeDirectory,
		pathEnvironment:       response.PathEnvironment,
		workReportDirectory:   response.WorkReportDirectory,
		workReportResetAtUTC:  parseOptionalLoaderTime(response.WorkReportResetAtUTC),
		lastFilesLoaded:       append([]string(nil), response.LastFilesLoaded...),
		finalOutput:           response.Output,
		outputTruncated:       response.OutputTruncated,
		errorMessage:          response.Error,
	}
}

func parseOptionalLoaderTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func (service *loaderExecutionService) persistJob(jobID string) {
	if service.historyDirectory == "" {
		return
	}
	service.historyMu.Lock()
	defer service.historyMu.Unlock()
	response, ok := service.snapshot(jobID)
	if !ok {
		return
	}
	content, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: cannot encode loader job %s: %v\n", jobID, err)
		return
	}
	if err := os.MkdirAll(service.historyDirectory, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: cannot create loader history %s: %v\n", service.historyDirectory, err)
		return
	}
	path := filepath.Join(service.historyDirectory, jobID+".json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: cannot persist loader job %s: %v\n", jobID, err)
		return
	}
	_ = os.Chmod(path, 0o600)
}

func (service *loaderExecutionService) history() loaderExecutionHistoryResponse {
	service.mu.Lock()
	defer service.mu.Unlock()
	jobs := make([]loaderExecutionHistoryEntry, 0, len(service.jobs))
	for _, job := range service.jobs {
		lastFiles := append([]string(nil), job.lastFilesLoaded...)
		if lastFiles == nil {
			lastFiles = []string{}
		}
		jobs = append(jobs, loaderExecutionHistoryEntry{
			JobID:                job.id,
			CreatedAtUTC:         formatOptionalLoaderTime(job.createdAtUTC),
			StartedAtUTC:         formatOptionalLoaderTime(job.startedAtUTC),
			FinishedAtUTC:        formatOptionalLoaderTime(job.finishedAtUTC),
			DatabaseUser:         job.databaseUser,
			DatabaseAlias:        job.databaseAlias,
			Status:               job.status,
			RowsInserted:         job.rowsInserted,
			RowsInsertedKnown:    job.rowsInsertedKnown,
			CurrentRowCount:      job.currentRowCount,
			CurrentRowCountKnown: job.currentRowCountKnown,
			LastFilesLoaded:      lastFiles,
		})
	}
	sort.Slice(jobs, func(left, right int) bool {
		return jobs[left].CreatedAtUTC > jobs[right].CreatedAtUTC
	})
	return loaderExecutionHistoryResponse{ActiveJobID: service.activeID, Jobs: jobs}
}

func readLastLoadedFiles(workReportDirectory string) []string {
	if workReportDirectory == "" {
		return []string{}
	}
	path := filepath.Join(workReportDirectory, "processed_files.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer file.Close()

	files := make([]string, 0, loaderLastFilesLimit)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record processedRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.FileName == "" {
			continue
		}
		if _, exists := seen[record.FileName]; exists {
			continue
		}
		seen[record.FileName] = struct{}{}
		files = append(files, record.FileName)
		if len(files) > loaderLastFilesLimit {
			delete(seen, files[0])
			files = files[1:]
		}
	}
	return files
}
