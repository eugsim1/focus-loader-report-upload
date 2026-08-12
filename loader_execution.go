package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	loaderJobPathPrefix        = "/api/v1/loader/jobs/"
	loaderJobCollectionPath    = "/api/v1/loader/jobs"
	loaderJobTimeout           = 24 * time.Hour
	loaderRowCountInterval     = 5 * time.Second
	loaderRowCountTimeout      = 20 * time.Second
	loaderAnalyticsInterval    = 30 * time.Second
	loaderAnalyticsTimeout     = 25 * time.Second
	maxLoaderJobRequestBytes   = 64 * 1024
	maxLoaderJobOutputBytes    = 8 * 1024 * 1024
	maxLoaderJobFieldBytes     = 4096
	loaderTargetTableName      = "TEMP_OCI_FOCUS"
	loaderOCIConfigProfile     = "config_profile"
	loaderOCIInstancePrincipal = "instance_principal"
	loaderDatabasePassword     = "password"
	loaderDatabaseVault        = "vault"
)

type loaderExecutionRequest struct {
	OCIAuthMode            string `json:"ociAuthMode"`
	DatabaseAuthMode       string `json:"databaseAuthMode"`
	OCIConfigFile          string `json:"ociConfigFile"`
	OCIProfile             string `json:"ociProfile"`
	DatabaseUser           string `json:"databaseUser"`
	DatabaseAlias          string `json:"databaseAlias"`
	DatabasePassword       string `json:"databasePassword"`
	VaultSecretOCID        string `json:"vaultSecretOcid"`
	VaultSecretProfile     string `json:"vaultSecretProfile"`
	SourceNamespace        string `json:"sourceNamespace"`
	SourceBucket           string `json:"sourceBucket"`
	MinimumDate            string `json:"minimumDate"`
	Workers                int    `json:"workers"`
	ExactObject            string `json:"exactObject"`
	TagSpecial1            string `json:"tagSpecial1"`
	TagSpecial2            string `json:"tagSpecial2"`
	TagSpecial3            string `json:"tagSpecial3"`
	TagSpecial4            string `json:"tagSpecial4"`
	DestinationNamespace   string `json:"destinationNamespace"`
	DestinationBucket      string `json:"destinationBucket"`
	DestinationPrefix      string `json:"destinationPrefix"`
	PreloadReport          bool   `json:"preloadReport"`
	ContinueAfterReport    bool   `json:"continueAfterReport"`
	SkipPreloadContentScan bool   `json:"skipPreloadContentScan"`
	UploadReports          bool   `json:"uploadReports"`
	LoadAfterUpload        bool   `json:"loadAfterUpload"`
	Force                  bool   `json:"force"`
	SkipTags               bool   `json:"skipTags"`
	SkipTagRows            bool   `json:"skipTagRows"`
	SkipTagKeys            bool   `json:"skipTagKeys"`
	KeepWorkFiles          bool   `json:"keepWorkFiles"`
	Verbose                bool   `json:"verbose"`
}

type loaderExecutionResponse struct {
	JobID                 string              `json:"jobId"`
	Status                string              `json:"status"`
	DatabaseUser          string              `json:"databaseUser"`
	DatabaseAlias         string              `json:"databaseAlias"`
	TableName             string              `json:"tableName"`
	CreatedAtUTC          string              `json:"createdAtUtc"`
	StartedAtUTC          string              `json:"startedAtUtc"`
	FinishedAtUTC         string              `json:"finishedAtUtc"`
	ExitCode              *int                `json:"exitCode"`
	InitialRowCount       int64               `json:"initialRowCount"`
	InitialRowCountKnown  bool                `json:"initialRowCountKnown"`
	CurrentRowCount       int64               `json:"currentRowCount"`
	CurrentRowCountKnown  bool                `json:"currentRowCountKnown"`
	RowsInserted          int64               `json:"rowsInserted"`
	RowsInsertedKnown     bool                `json:"rowsInsertedKnown"`
	RowCountUpdatedAtUTC  string              `json:"rowCountUpdatedAtUtc"`
	RowCountError         string              `json:"rowCountError"`
	MonthlyCosts          []loaderMonthlyCost `json:"monthlyCosts"`
	Services              []string            `json:"services"`
	AnalyticsUpdatedAtUTC string              `json:"analyticsUpdatedAtUtc"`
	AnalyticsError        string              `json:"analyticsError"`
	Executable            string              `json:"executable"`
	WorkingDirectory      string              `json:"workingDirectory"`
	CommandLine           string              `json:"commandLine"`
	ManualCommand         string              `json:"manualCommand"`
	TNSAdmin              string              `json:"tnsAdmin"`
	HomeDirectory         string              `json:"homeDirectory"`
	PathEnvironment       string              `json:"pathEnvironment"`
	WorkReportDirectory   string              `json:"workReportDirectory"`
	WorkReportResetAtUTC  string              `json:"workReportResetAtUtc"`
	LastFilesLoaded       []string            `json:"lastFilesLoaded"`
	Output                string              `json:"output"`
	OutputTruncated       bool                `json:"outputTruncated"`
	Error                 string              `json:"error"`
}

type loaderExecutionInput struct {
	Executable       string
	WorkingDirectory string
	Arguments        []string
	StdinPassword    string
	MonitorPassword  string
	DatabaseUser     string
	DatabaseAlias    string
	VaultSecretOCID  string
}

type loaderExecutionRunner func(
	context.Context,
	loaderExecutionInput,
	io.Writer,
) (int, error)

type loaderRowCounter func(context.Context, string, string, string) (int64, error)
type loaderPasswordResolver func(context.Context, loaderExecutionRequest) (string, error)

type loaderExecutionJob struct {
	id                    string
	status                string
	databaseUser          string
	databaseAlias         string
	createdAtUTC          time.Time
	startedAtUTC          time.Time
	finishedAtUTC         time.Time
	exitCode              *int
	initialRowCount       int64
	initialRowCountKnown  bool
	currentRowCount       int64
	currentRowCountKnown  bool
	rowsInserted          int64
	rowsInsertedKnown     bool
	rowCountUpdatedAtUTC  time.Time
	rowCountError         string
	monthlyCosts          []loaderMonthlyCost
	services              []string
	analyticsUpdatedAtUTC time.Time
	analyticsError        string
	executable            string
	workingDirectory      string
	commandLine           string
	manualCommand         string
	tnsAdmin              string
	homeDirectory         string
	pathEnvironment       string
	workReportDirectory   string
	workReportResetAtUTC  time.Time
	lastFilesLoaded       []string
	output                *boundedDeploymentOutput
	finalOutput           string
	outputTruncated       bool
	redactionValues       []string
	errorMessage          string
}

type loaderExecutionService struct {
	getenv            func(string) string
	runner            loaderExecutionRunner
	rowCounter        loaderRowCounter
	analyticsReader   loaderAnalyticsReader
	passwordResolver  loaderPasswordResolver
	pollInterval      time.Duration
	analyticsInterval time.Duration
	timeout           time.Duration
	historyDirectory  string

	mu        sync.Mutex
	historyMu sync.Mutex
	jobs      map[string]*loaderExecutionJob
	activeID  string
}

type loaderExecutionAPIError struct {
	status  int
	message string
}

func (err loaderExecutionAPIError) Error() string { return err.message }

func newLoaderExecutionService(getenv func(string) string) *loaderExecutionService {
	service := &loaderExecutionService{
		getenv:            getenv,
		runner:            runLoaderExecutionProcess,
		rowCounter:        countOracleTempFocusRows,
		analyticsReader:   readOracleTempFocusAnalytics,
		passwordResolver:  resolveLoaderExecutionPassword,
		pollInterval:      loaderRowCountInterval,
		analyticsInterval: loaderAnalyticsInterval,
		timeout:           loaderJobTimeout,
		historyDirectory:  loaderExecutionHistoryDirectory(getenv),
		jobs:              make(map[string]*loaderExecutionJob),
	}
	service.loadPersistedJobs()
	return service
}

func (service *loaderExecutionService) handleCollection(w http.ResponseWriter, r *http.Request) {
	setTNSGUIJSONHeaders(w)
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(service.history())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeLoaderExecutionError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeLoaderExecutionError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoaderJobRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request loaderExecutionRequest
	if err := decoder.Decode(&request); err != nil {
		writeLoaderExecutionError(w, http.StatusBadRequest, "request body must be one valid JSON object")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeLoaderExecutionError(w, http.StatusBadRequest, "request body must contain only one JSON object")
		return
	}

	response, err := service.start(request)
	if err != nil {
		var apiError loaderExecutionAPIError
		if errors.As(err, &apiError) {
			writeLoaderExecutionError(w, apiError.status, apiError.message)
		} else {
			writeLoaderExecutionError(w, http.StatusInternalServerError, "could not start loader execution")
		}
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

func (service *loaderExecutionService) handleItem(w http.ResponseWriter, r *http.Request) {
	setTNSGUIJSONHeaders(w)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeLoaderExecutionError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, loaderJobPathPrefix)
	if jobID == "" || strings.Contains(jobID, "/") || len(jobID) > 512 {
		writeLoaderExecutionError(w, http.StatusBadRequest, "loader job id is invalid")
		return
	}
	response, ok := service.snapshot(jobID)
	if !ok {
		writeLoaderExecutionError(w, http.StatusNotFound, "loader job was not found or has expired")
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (service *loaderExecutionService) start(request loaderExecutionRequest) (loaderExecutionResponse, error) {
	service.mu.Lock()
	if service.activeID != "" {
		service.mu.Unlock()
		return loaderExecutionResponse{}, loaderExecutionAPIError{
			status: http.StatusConflict, message: "another loader execution is already running",
		}
	}
	service.mu.Unlock()

	aliases, err := tnsAliasesFromEnvironment(service.getenv)
	if err != nil {
		return loaderExecutionResponse{}, loaderExecutionAPIError{status: http.StatusServiceUnavailable, message: err.Error()}
	}
	arguments, err := validateAndBuildLoaderArguments(&request, aliases.Aliases)
	if err != nil {
		return loaderExecutionResponse{}, loaderExecutionAPIError{status: http.StatusBadRequest, message: err.Error()}
	}
	executable, workingDirectory, err := loaderExecutionLocation(service.getenv)
	if err != nil {
		return loaderExecutionResponse{}, loaderExecutionAPIError{status: http.StatusServiceUnavailable, message: err.Error()}
	}

	monitorPassword := request.DatabasePassword
	stdinPassword := request.DatabasePassword
	if request.DatabaseAuthMode == loaderDatabaseVault {
		resolveContext, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		monitorPassword, err = service.passwordResolver(resolveContext, request)
		cancel()
		if err != nil {
			return loaderExecutionResponse{}, loaderExecutionAPIError{
				status: http.StatusBadGateway,
				message: redactDatabasePasswords(
					"could not resolve the OCI Vault database secret: "+err.Error(),
					request.VaultSecretOCID,
				),
			}
		}
		stdinPassword = ""
	}
	if !validDatabasePassword(monitorPassword) {
		return loaderExecutionResponse{}, loaderExecutionAPIError{status: http.StatusBadRequest, message: "database password is invalid"}
	}
	jobID, err := newSchemaDeploymentToken()
	if err != nil {
		return loaderExecutionResponse{}, loaderExecutionAPIError{status: http.StatusInternalServerError, message: "could not create loader job id"}
	}
	job := &loaderExecutionJob{
		id:               jobID,
		status:           "starting",
		databaseUser:     request.DatabaseUser,
		databaseAlias:    request.DatabaseAlias,
		createdAtUTC:     time.Now().UTC(),
		executable:       executable,
		workingDirectory: workingDirectory,
		commandLine:      buildLoaderCommandLine(executable, arguments),
		manualCommand: buildManualLoaderCommand(
			executable,
			workingDirectory,
			arguments,
			request.DatabaseAuthMode == loaderDatabasePassword,
			service.getenv("TNS_ADMIN"),
			service.getenv("HOME"),
			service.getenv("PATH"),
		),
		tnsAdmin:        service.getenv("TNS_ADMIN"),
		homeDirectory:   service.getenv("HOME"),
		pathEnvironment: service.getenv("PATH"),
		redactionValues: []string{monitorPassword, request.VaultSecretOCID},
		monthlyCosts:    []loaderMonthlyCost{},
		services:        []string{},
		output: &boundedDeploymentOutput{
			limit: maxLoaderJobOutputBytes,
		},
	}
	input := loaderExecutionInput{
		Executable:       executable,
		WorkingDirectory: workingDirectory,
		Arguments:        arguments,
		StdinPassword:    stdinPassword,
		MonitorPassword:  monitorPassword,
		DatabaseUser:     request.DatabaseUser,
		DatabaseAlias:    request.DatabaseAlias,
		VaultSecretOCID:  request.VaultSecretOCID,
	}

	service.mu.Lock()
	if service.activeID != "" {
		service.mu.Unlock()
		return loaderExecutionResponse{}, loaderExecutionAPIError{
			status: http.StatusConflict, message: "another loader execution is already running",
		}
	}
	workReportDirectory, err := resetLoaderWorkReportDirectory(workingDirectory)
	if err != nil {
		service.mu.Unlock()
		return loaderExecutionResponse{}, loaderExecutionAPIError{
			status:  http.StatusInternalServerError,
			message: fmt.Sprintf("could not reset the selected user's work_report_dir: %v", err),
		}
	}
	job.workReportDirectory = workReportDirectory
	job.workReportResetAtUTC = time.Now().UTC()
	service.jobs[jobID] = job
	service.activeID = jobID
	response, _ := service.snapshotLocked(jobID)
	service.mu.Unlock()
	service.persistJob(jobID)

	go service.run(jobID, input)
	return response, nil
}

func (service *loaderExecutionService) run(jobID string, input loaderExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), service.timeout)
	defer cancel()

	service.mu.Lock()
	job, ok := service.jobs[jobID]
	if !ok {
		service.mu.Unlock()
		return
	}
	job.status = "running"
	job.startedAtUTC = time.Now().UTC()
	writeLoaderExecutionHeader(job.output, jobID, input, job.workReportDirectory, job.workReportResetAtUTC)
	service.mu.Unlock()
	service.persistJob(jobID)

	service.refreshRowCount(ctx, jobID, input, true)
	monitorContext, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		service.refreshAnalytics(monitorContext, jobID, input)
		rowTicker := time.NewTicker(service.pollInterval)
		analyticsTicker := time.NewTicker(service.analyticsInterval)
		defer rowTicker.Stop()
		defer analyticsTicker.Stop()
		for {
			select {
			case <-monitorContext.Done():
				return
			case <-rowTicker.C:
				service.refreshRowCount(monitorContext, jobID, input, false)
			case <-analyticsTicker.C:
				service.refreshAnalytics(monitorContext, jobID, input)
			}
		}
	}()

	exitCode, runErr := service.runner(ctx, input, job.output)
	stopMonitor()
	<-monitorDone
	finalCountContext, finalCountCancel := context.WithTimeout(context.Background(), loaderRowCountTimeout)
	service.refreshRowCount(finalCountContext, jobID, input, false)
	finalCountCancel()
	finalAnalyticsContext, finalAnalyticsCancel := context.WithTimeout(context.Background(), loaderAnalyticsTimeout)
	service.refreshAnalytics(finalAnalyticsContext, jobID, input)
	finalAnalyticsCancel()

	finishedAt := time.Now().UTC()
	finalOutput := redactDatabasePasswords(job.output.String(), input.MonitorPassword, input.VaultSecretOCID)
	outputTruncated := job.output.Truncated()
	status := "succeeded"
	errorMessage := ""
	if runErr != nil || exitCode != 0 {
		status = "failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timed_out"
		}
		errorMessage = buildLoaderFailureMessage(
			runErr,
			exitCode,
			input.Executable,
			input.WorkingDirectory,
			finalOutput,
			outputTruncated,
		)
	}

	service.mu.Lock()
	if job, ok = service.jobs[jobID]; ok {
		job.status = status
		job.finishedAtUTC = finishedAt
		job.exitCode = &exitCode
		job.finalOutput = finalOutput
		job.outputTruncated = outputTruncated
		job.output = nil
		job.redactionValues = nil
		job.errorMessage = errorMessage
	}
	if service.activeID == jobID {
		service.activeID = ""
	}
	service.mu.Unlock()
	service.persistJob(jobID)
}

func (service *loaderExecutionService) refreshAnalytics(
	parent context.Context,
	jobID string,
	input loaderExecutionInput,
) {
	if service.analyticsReader == nil {
		return
	}
	now := time.Now().UTC()
	service.mu.Lock()
	job, ok := service.jobs[jobID]
	if !ok {
		service.mu.Unlock()
		return
	}
	if !job.currentRowCountKnown {
		service.mu.Unlock()
		return
	}
	if job.currentRowCount == 0 {
		job.monthlyCosts = []loaderMonthlyCost{}
		job.services = []string{}
		job.analyticsUpdatedAtUTC = now
		job.analyticsError = ""
		service.mu.Unlock()
		service.persistJob(jobID)
		return
	}
	service.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, loaderAnalyticsTimeout)
	defer cancel()
	snapshot, err := service.analyticsReader(
		ctx,
		input.DatabaseUser,
		input.MonitorPassword,
		input.DatabaseAlias,
	)
	now = time.Now().UTC()

	service.mu.Lock()
	job, ok = service.jobs[jobID]
	if !ok {
		service.mu.Unlock()
		return
	}
	if err != nil {
		job.analyticsError = redactDatabasePassword(err.Error(), input.MonitorPassword)
		service.mu.Unlock()
		service.persistJob(jobID)
		return
	}
	job.monthlyCosts = append([]loaderMonthlyCost(nil), snapshot.MonthlyCosts...)
	job.services = append([]string(nil), snapshot.Services...)
	job.analyticsUpdatedAtUTC = now
	job.analyticsError = ""
	service.mu.Unlock()
	service.persistJob(jobID)
}

func (service *loaderExecutionService) refreshRowCount(
	parent context.Context,
	jobID string,
	input loaderExecutionInput,
	baseline bool,
) {
	ctx, cancel := context.WithTimeout(parent, loaderRowCountTimeout)
	defer cancel()
	count, err := service.rowCounter(ctx, input.DatabaseUser, input.MonitorPassword, input.DatabaseAlias)
	now := time.Now().UTC()

	service.mu.Lock()
	job, ok := service.jobs[jobID]
	if !ok {
		service.mu.Unlock()
		return
	}
	if err != nil {
		job.rowCountError = redactDatabasePassword(err.Error(), input.MonitorPassword)
		service.mu.Unlock()
		service.persistJob(jobID)
		return
	}
	job.rowCountError = ""
	job.currentRowCount = count
	job.currentRowCountKnown = true
	job.rowCountUpdatedAtUTC = now
	if baseline {
		job.initialRowCount = count
		job.initialRowCountKnown = true
	}
	if job.initialRowCountKnown {
		job.rowsInserted = count - job.initialRowCount
		job.rowsInsertedKnown = true
	}
	job.lastFilesLoaded = readLastLoadedFiles(job.workReportDirectory)
	service.mu.Unlock()
	service.persistJob(jobID)
}

func (service *loaderExecutionService) snapshot(jobID string) (loaderExecutionResponse, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.snapshotLocked(jobID)
}

func (service *loaderExecutionService) snapshotLocked(jobID string) (loaderExecutionResponse, bool) {
	job, ok := service.jobs[jobID]
	if !ok {
		return loaderExecutionResponse{}, false
	}
	monthlyCosts := append([]loaderMonthlyCost(nil), job.monthlyCosts...)
	if monthlyCosts == nil {
		monthlyCosts = []loaderMonthlyCost{}
	}
	services := append([]string(nil), job.services...)
	if services == nil {
		services = []string{}
	}
	lastFilesLoaded := append([]string(nil), job.lastFilesLoaded...)
	if lastFilesLoaded == nil {
		lastFilesLoaded = []string{}
	}
	output := job.finalOutput
	outputTruncated := job.outputTruncated
	if job.output != nil {
		output = redactDatabasePasswords(job.output.String(), job.redactionValues...)
		outputTruncated = job.output.Truncated()
	}
	return loaderExecutionResponse{
		JobID:                 job.id,
		Status:                job.status,
		DatabaseUser:          job.databaseUser,
		DatabaseAlias:         job.databaseAlias,
		TableName:             loaderTargetTableName,
		CreatedAtUTC:          formatOptionalLoaderTime(job.createdAtUTC),
		StartedAtUTC:          formatOptionalLoaderTime(job.startedAtUTC),
		FinishedAtUTC:         formatOptionalLoaderTime(job.finishedAtUTC),
		ExitCode:              job.exitCode,
		InitialRowCount:       job.initialRowCount,
		InitialRowCountKnown:  job.initialRowCountKnown,
		CurrentRowCount:       job.currentRowCount,
		CurrentRowCountKnown:  job.currentRowCountKnown,
		RowsInserted:          job.rowsInserted,
		RowsInsertedKnown:     job.rowsInsertedKnown,
		RowCountUpdatedAtUTC:  formatOptionalLoaderTime(job.rowCountUpdatedAtUTC),
		RowCountError:         job.rowCountError,
		MonthlyCosts:          monthlyCosts,
		Services:              services,
		AnalyticsUpdatedAtUTC: formatOptionalLoaderTime(job.analyticsUpdatedAtUTC),
		AnalyticsError:        job.analyticsError,
		Executable:            job.executable,
		WorkingDirectory:      job.workingDirectory,
		CommandLine:           job.commandLine,
		ManualCommand:         job.manualCommand,
		TNSAdmin:              job.tnsAdmin,
		HomeDirectory:         job.homeDirectory,
		PathEnvironment:       job.pathEnvironment,
		WorkReportDirectory:   job.workReportDirectory,
		WorkReportResetAtUTC:  formatOptionalLoaderTime(job.workReportResetAtUTC),
		LastFilesLoaded:       lastFilesLoaded,
		Output:                output,
		OutputTruncated:       outputTruncated,
		Error:                 job.errorMessage,
	}, true
}

func resetLoaderWorkReportDirectory(workingDirectory string) (string, error) {
	cleanWorkingDirectory := filepath.Clean(workingDirectory)
	if !filepath.IsAbs(cleanWorkingDirectory) {
		return "", fmt.Errorf("working directory must be absolute")
	}
	reportDirectory := filepath.Join(cleanWorkingDirectory, workReportDir)
	relative, err := filepath.Rel(cleanWorkingDirectory, reportDirectory)
	if err != nil || relative != workReportDir {
		return "", fmt.Errorf("refusing unsafe work report path %s", reportDirectory)
	}

	info, err := os.Lstat(reportDirectory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(reportDirectory, 0o750); err != nil {
			return "", fmt.Errorf("create %s: %w", reportDirectory, err)
		}
		return reportDirectory, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", reportDirectory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("refusing to clear non-directory or symbolic link %s", reportDirectory)
	}

	entries, err := os.ReadDir(reportDirectory)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", reportDirectory, err)
	}
	for _, entry := range entries {
		if entry.Name() == loaderJobHistoryDirectoryName {
			info, err := entry.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("refusing unsafe loader history path %s", filepath.Join(reportDirectory, entry.Name()))
			}
			continue
		}
		target := filepath.Join(reportDirectory, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return "", fmt.Errorf("remove %s: %w", target, err)
		}
	}
	if err := os.Chmod(reportDirectory, 0o750); err != nil {
		return "", fmt.Errorf("set permissions on %s: %w", reportDirectory, err)
	}
	return reportDirectory, nil
}

func writeLoaderExecutionHeader(
	output io.Writer,
	jobID string,
	input loaderExecutionInput,
	workReportDirectory string,
	resetAt time.Time,
) {
	fmt.Fprintln(output, "FOCUS Loader Streamlit execution log")
	fmt.Fprintf(output, "Job ID: %s\n", jobID)
	fmt.Fprintf(output, "Started UTC: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(output, "Executable: %s\n", input.Executable)
	fmt.Fprintf(output, "Working directory: %s\n", input.WorkingDirectory)
	fmt.Fprintf(output, "Reset work_report_dir: %s at %s\n", workReportDirectory, resetAt.Format(time.RFC3339))
	fmt.Fprintf(output, "Database target: %s@%s\n", input.DatabaseUser, input.DatabaseAlias)
	fmt.Fprintf(output, "Command: %s\n", buildLoaderCommandLine(input.Executable, input.Arguments))
	fmt.Fprintln(output, "--- combined loader stdout and stderr ---")
}

func formatOptionalLoaderTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func validateAndBuildLoaderArguments(request *loaderExecutionRequest, aliases []string) ([]string, error) {
	trimLoaderExecutionRequest(request)
	for label, value := range map[string]string{
		"OCI config file":       request.OCIConfigFile,
		"OCI profile":           request.OCIProfile,
		"database user":         request.DatabaseUser,
		"database alias":        request.DatabaseAlias,
		"Vault secret OCID":     request.VaultSecretOCID,
		"Vault secret profile":  request.VaultSecretProfile,
		"source namespace":      request.SourceNamespace,
		"source bucket":         request.SourceBucket,
		"minimum date":          request.MinimumDate,
		"exact object":          request.ExactObject,
		"tag special 1":         request.TagSpecial1,
		"tag special 2":         request.TagSpecial2,
		"tag special 3":         request.TagSpecial3,
		"tag special 4":         request.TagSpecial4,
		"destination namespace": request.DestinationNamespace,
		"destination bucket":    request.DestinationBucket,
		"destination prefix":    request.DestinationPrefix,
	} {
		if len(value) > maxLoaderJobFieldBytes || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("%s is invalid", label)
		}
	}
	if !unquotedOracleIdentifier.MatchString(request.DatabaseUser) {
		return nil, fmt.Errorf("database user must be an unquoted Oracle identifier")
	}
	matchedAlias := ""
	for _, alias := range aliases {
		if strings.EqualFold(alias, request.DatabaseAlias) {
			matchedAlias = alias
			break
		}
	}
	if matchedAlias == "" {
		return nil, fmt.Errorf("database alias must be selected from the server TNS catalog")
	}
	request.DatabaseAlias = matchedAlias
	if request.SourceNamespace == "" {
		return nil, fmt.Errorf("source namespace is required")
	}
	if request.Workers < 1 || request.Workers > 128 {
		return nil, fmt.Errorf("workers must be between 1 and 128")
	}
	if request.MinimumDate != "" {
		if _, err := time.Parse("2006-01-02", request.MinimumDate); err != nil {
			return nil, fmt.Errorf("minimum date must use YYYY-MM-DD format")
		}
	}
	if request.OCIAuthMode != loaderOCIConfigProfile && request.OCIAuthMode != loaderOCIInstancePrincipal {
		return nil, fmt.Errorf("OCI authentication mode is invalid")
	}
	if request.OCIAuthMode == loaderOCIConfigProfile && request.OCIProfile == "" {
		return nil, fmt.Errorf("OCI profile is required for config/profile authentication")
	}
	if request.DatabaseAuthMode != loaderDatabasePassword && request.DatabaseAuthMode != loaderDatabaseVault {
		return nil, fmt.Errorf("database authentication mode is invalid")
	}
	if request.DatabaseAuthMode == loaderDatabasePassword && !validDatabasePassword(request.DatabasePassword) {
		return nil, fmt.Errorf("database password is required and must not contain control characters")
	}
	if request.DatabaseAuthMode == loaderDatabasePassword && request.VaultSecretOCID != "" {
		return nil, fmt.Errorf("Vault secret OCID must be empty for database-password authentication")
	}
	if request.DatabaseAuthMode == loaderDatabaseVault && !strings.HasPrefix(request.VaultSecretOCID, "ocid1.vaultsecret.") {
		return nil, fmt.Errorf("OCI Vault secret OCID is invalid")
	}
	if request.DatabaseAuthMode == loaderDatabaseVault && request.DatabasePassword != "" {
		return nil, fmt.Errorf("database password must be empty for Vault authentication")
	}
	if request.UploadReports && request.DestinationBucket == "" {
		return nil, fmt.Errorf("destination bucket is required with upload reports")
	}
	if request.LoadAfterUpload && !request.UploadReports {
		return nil, fmt.Errorf("load after upload requires upload reports")
	}
	if request.ContinueAfterReport && request.UploadReports {
		return nil, fmt.Errorf("continue after report cannot be combined with upload reports")
	}

	arguments := make([]string, 0, 48)
	if request.OCIAuthMode == loaderOCIInstancePrincipal {
		arguments = append(arguments, "-ip")
	} else {
		if request.OCIConfigFile != "" {
			arguments = append(arguments, "-c", request.OCIConfigFile)
		}
		arguments = append(arguments, "-t", request.OCIProfile)
	}
	arguments = append(arguments, "-du", request.DatabaseUser, "-dn", request.DatabaseAlias)
	if request.DatabaseAuthMode == loaderDatabasePassword {
		arguments = append(arguments, "-dp-stdin")
	} else {
		arguments = append(arguments, "-ds", request.VaultSecretOCID)
		if request.VaultSecretProfile != "" {
			arguments = append(arguments, "-dst", request.VaultSecretProfile)
		}
	}
	arguments = append(arguments, "-ns", request.SourceNamespace)
	appendLoaderValueArgument(&arguments, "-bn", request.SourceBucket)
	appendLoaderValueArgument(&arguments, "-d", request.MinimumDate)
	arguments = append(arguments, "-workers", fmt.Sprintf("%d", request.Workers))
	appendLoaderValueArgument(&arguments, "-f", request.ExactObject)
	appendLoaderValueArgument(&arguments, "-ts1", request.TagSpecial1)
	appendLoaderValueArgument(&arguments, "-ts2", request.TagSpecial2)
	appendLoaderValueArgument(&arguments, "-ts3", request.TagSpecial3)
	appendLoaderValueArgument(&arguments, "-ts4", request.TagSpecial4)
	if request.UploadReports {
		arguments = append(arguments, "-report-upload-bucket", request.DestinationBucket)
		appendLoaderValueArgument(&arguments, "-report-upload-namespace", request.DestinationNamespace)
		appendLoaderValueArgument(&arguments, "-report-upload-prefix", request.DestinationPrefix)
	}
	for _, flag := range []struct {
		enabled bool
		name    string
	}{
		{request.PreloadReport, "-preload-report"},
		{request.ContinueAfterReport, "-continue-after-report"},
		{request.SkipPreloadContentScan, "-skip-preload-content-scan"},
		{request.UploadReports, "-upload-reports"},
		{request.LoadAfterUpload, "-load-after-upload"},
		{request.Force, "-force"},
		{request.SkipTags, "-skip-tags"},
		{request.SkipTagRows, "-skip-tag-rows"},
		{request.SkipTagKeys, "-skip-tag-keys"},
		{request.KeepWorkFiles, "-keep-work-files"},
		{request.Verbose, "-verbose"},
	} {
		if flag.enabled {
			arguments = append(arguments, flag.name)
		}
	}
	if _, err := parseArgs(arguments); err != nil {
		return nil, fmt.Errorf("loader arguments are invalid: %w", err)
	}
	return arguments, nil
}

func trimLoaderExecutionRequest(request *loaderExecutionRequest) {
	request.OCIAuthMode = strings.TrimSpace(request.OCIAuthMode)
	request.DatabaseAuthMode = strings.TrimSpace(request.DatabaseAuthMode)
	request.OCIConfigFile = strings.TrimSpace(request.OCIConfigFile)
	request.OCIProfile = strings.TrimSpace(request.OCIProfile)
	request.DatabaseUser = strings.ToUpper(strings.TrimSpace(request.DatabaseUser))
	request.DatabaseAlias = strings.TrimSpace(request.DatabaseAlias)
	request.VaultSecretOCID = strings.TrimSpace(request.VaultSecretOCID)
	request.VaultSecretProfile = strings.TrimSpace(request.VaultSecretProfile)
	request.SourceNamespace = strings.TrimSpace(request.SourceNamespace)
	request.SourceBucket = strings.TrimSpace(request.SourceBucket)
	request.MinimumDate = strings.TrimSpace(request.MinimumDate)
	request.ExactObject = strings.TrimSpace(request.ExactObject)
	request.TagSpecial1 = strings.TrimSpace(request.TagSpecial1)
	request.TagSpecial2 = strings.TrimSpace(request.TagSpecial2)
	request.TagSpecial3 = strings.TrimSpace(request.TagSpecial3)
	request.TagSpecial4 = strings.TrimSpace(request.TagSpecial4)
	request.DestinationNamespace = strings.TrimSpace(request.DestinationNamespace)
	request.DestinationBucket = strings.TrimSpace(request.DestinationBucket)
	request.DestinationPrefix = strings.TrimSpace(request.DestinationPrefix)
}

func appendLoaderValueArgument(arguments *[]string, flag, value string) {
	if value != "" {
		*arguments = append(*arguments, flag, value)
	}
}

func shellQuoteLoaderArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func buildLoaderCommandLine(executable string, arguments []string) string {
	parts := make([]string, 0, len(arguments)+1)
	parts = append(parts, shellQuoteLoaderArgument(executable))
	for _, argument := range arguments {
		parts = append(parts, shellQuoteLoaderArgument(argument))
	}
	return strings.Join(parts, " ")
}

func buildManualLoaderCommand(
	executable string,
	workingDirectory string,
	arguments []string,
	promptForPassword bool,
	tnsAdmin string,
	homeDirectory string,
	pathEnvironment string,
) string {
	commandLine := buildLoaderCommandLine(executable, arguments)
	environment := []string{"env"}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"TNS_ADMIN", tnsAdmin},
		{"HOME", homeDirectory},
		{"PATH", pathEnvironment},
	} {
		if item.value != "" {
			environment = append(environment, shellQuoteLoaderArgument(item.name+"="+item.value))
		}
	}
	if len(environment) > 1 {
		commandLine = strings.Join(environment, " ") + " " + commandLine
	}
	changeDirectory := "cd -- " + shellQuoteLoaderArgument(workingDirectory) + " && "
	if !promptForPassword {
		return changeDirectory + commandLine
	}
	return changeDirectory +
		"{ IFS= read -r -s -p 'Database password: ' FOCUS_DB_PASSWORD; " +
		"printf '\\n'; printf '%s' \"$FOCUS_DB_PASSWORD\" | " + commandLine +
		"; FOCUS_LOADER_STATUS=${PIPESTATUS[1]}; unset FOCUS_DB_PASSWORD; " +
		"exit \"$FOCUS_LOADER_STATUS\"; }"
}

func buildLoaderFailureMessage(
	runErr error,
	exitCode int,
	executable string,
	workingDirectory string,
	output string,
	outputTruncated bool,
) string {
	detail := fmt.Sprintf("loader execution failed with exit code %d", exitCode)
	if runErr != nil {
		detail = fmt.Sprintf("loader execution failed: %v (exit code %d)", runErr, exitCode)
	}
	detail += fmt.Sprintf("\nExecutable: %s\nWorking directory: %s", executable, workingDirectory)
	if strings.TrimSpace(output) == "" {
		return detail + "\nNo stdout or stderr was captured from the loader process."
	}
	if outputTruncated {
		detail += "\nThe captured stdout/stderr exceeded the 8 MiB API limit and was truncated."
	}
	return detail + "\nSee the captured combined stdout/stderr below for the loader's full diagnostic message."
}

func loaderExecutionLocation(getenv func(string) string) (string, string, error) {
	executable := strings.TrimSpace(getenv("FOCUS_LOADER_EXECUTABLE"))
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return "", "", fmt.Errorf("resolve the installed loader executable: %w", err)
		}
	}
	if !filepath.IsAbs(executable) {
		return "", "", fmt.Errorf("FOCUS_LOADER_EXECUTABLE must be an absolute path")
	}
	info, err := os.Stat(executable)
	if err != nil || info.IsDir() {
		return "", "", fmt.Errorf("configured loader executable is not available")
	}
	workingDirectory := strings.TrimSpace(getenv("FOCUS_LOADER_WORK_DIR"))
	if workingDirectory == "" {
		workingDirectory = filepath.Dir(executable)
	}
	if !filepath.IsAbs(workingDirectory) {
		return "", "", fmt.Errorf("FOCUS_LOADER_WORK_DIR must be an absolute path")
	}
	workingInfo, err := os.Stat(workingDirectory)
	if err != nil || !workingInfo.IsDir() {
		return "", "", fmt.Errorf("configured loader working directory is not available")
	}
	return executable, workingDirectory, nil
}

func runLoaderExecutionProcess(
	ctx context.Context,
	input loaderExecutionInput,
	output io.Writer,
) (int, error) {
	command := exec.CommandContext(ctx, input.Executable, input.Arguments...)
	command.Dir = input.WorkingDirectory
	command.Env = os.Environ()
	if input.StdinPassword != "" {
		command.Stdin = strings.NewReader(input.StdinPassword)
	}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	fmt.Fprintln(output, "--- end combined loader stdout and stderr ---")
	fmt.Fprintf(output, "Finished UTC: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(output, "Process exit code: %d\n", exitCode)
	if err != nil {
		fmt.Fprintf(output, "Process error: %v\n", err)
		return exitCode, err
	}
	return exitCode, nil
}

func resolveLoaderExecutionPassword(ctx context.Context, request loaderExecutionRequest) (string, error) {
	cmd := commandLine{
		configPath:         request.OCIConfigFile,
		profile:            request.OCIProfile,
		instancePrincipals: request.OCIAuthMode == loaderOCIInstancePrincipal,
		dbSecretID:         request.VaultSecretOCID,
		dbSecretProfile:    request.VaultSecretProfile,
	}
	provider, err := createConfigProvider(cmd, true)
	if err != nil {
		return "", err
	}
	password, err := getSecretPassword(ctx, provider, "", request.VaultSecretOCID)
	if err != nil {
		return "", err
	}
	if !validDatabasePassword(password) {
		return "", fmt.Errorf("resolved Vault secret is not a valid database password")
	}
	return password, nil
}

func countOracleTempFocusRows(
	ctx context.Context,
	username string,
	password string,
	connectAlias string,
) (int64, error) {
	cmd := commandLine{dbUser: username, dbName: connectAlias}
	db, err := openOracleDB(cmd, password)
	if err != nil {
		return 0, fmt.Errorf("open Oracle connection for row count: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("connect for %s row count: %w", loaderTargetTableName, err)
	}
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+loaderTargetTableName).Scan(&count); err != nil {
		return 0, fmt.Errorf("count rows in %s: %w", loaderTargetTableName, err)
	}
	return count, nil
}

func writeLoaderExecutionError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
