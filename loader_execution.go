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
	loaderJobRetention         = 2 * time.Hour
	loaderRowCountInterval     = 5 * time.Second
	loaderRowCountTimeout      = 20 * time.Second
	maxLoaderJobRequestBytes   = 64 * 1024
	maxLoaderJobOutputBytes    = 512 * 1024
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
	JobID                string `json:"jobId"`
	Status               string `json:"status"`
	DatabaseUser         string `json:"databaseUser"`
	DatabaseAlias        string `json:"databaseAlias"`
	TableName            string `json:"tableName"`
	StartedAtUTC         string `json:"startedAtUtc"`
	FinishedAtUTC        string `json:"finishedAtUtc"`
	ExitCode             *int   `json:"exitCode"`
	InitialRowCount      int64  `json:"initialRowCount"`
	InitialRowCountKnown bool   `json:"initialRowCountKnown"`
	CurrentRowCount      int64  `json:"currentRowCount"`
	CurrentRowCountKnown bool   `json:"currentRowCountKnown"`
	RowsInserted         int64  `json:"rowsInserted"`
	RowsInsertedKnown    bool   `json:"rowsInsertedKnown"`
	RowCountUpdatedAtUTC string `json:"rowCountUpdatedAtUtc"`
	RowCountError        string `json:"rowCountError"`
	Output               string `json:"output"`
	Error                string `json:"error"`
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
	id                   string
	status               string
	databaseUser         string
	databaseAlias        string
	startedAtUTC         time.Time
	finishedAtUTC        time.Time
	exitCode             *int
	initialRowCount      int64
	initialRowCountKnown bool
	currentRowCount      int64
	currentRowCountKnown bool
	rowsInserted         int64
	rowsInsertedKnown    bool
	rowCountUpdatedAtUTC time.Time
	rowCountError        string
	output               *boundedDeploymentOutput
	finalOutput          string
	errorMessage         string
}

type loaderExecutionService struct {
	getenv           func(string) string
	runner           loaderExecutionRunner
	rowCounter       loaderRowCounter
	passwordResolver loaderPasswordResolver
	pollInterval     time.Duration
	timeout          time.Duration
	retention        time.Duration

	mu       sync.Mutex
	jobs     map[string]*loaderExecutionJob
	activeID string
}

type loaderExecutionAPIError struct {
	status  int
	message string
}

func (err loaderExecutionAPIError) Error() string { return err.message }

func newLoaderExecutionService(getenv func(string) string) *loaderExecutionService {
	return &loaderExecutionService{
		getenv:           getenv,
		runner:           runLoaderExecutionProcess,
		rowCounter:       countOracleTempFocusRows,
		passwordResolver: resolveLoaderExecutionPassword,
		pollInterval:     loaderRowCountInterval,
		timeout:          loaderJobTimeout,
		retention:        loaderJobRetention,
		jobs:             make(map[string]*loaderExecutionJob),
	}
}

func (service *loaderExecutionService) handleCollection(w http.ResponseWriter, r *http.Request) {
	setTNSGUIJSONHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
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
		id:            jobID,
		status:        "starting",
		databaseUser:  request.DatabaseUser,
		databaseAlias: request.DatabaseAlias,
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
	service.jobs[jobID] = job
	service.activeID = jobID
	response, _ := service.snapshotLocked(jobID)
	service.mu.Unlock()

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
	service.mu.Unlock()

	service.refreshRowCount(ctx, jobID, input, true)
	monitorContext, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(service.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorContext.Done():
				return
			case <-ticker.C:
				service.refreshRowCount(monitorContext, jobID, input, false)
			}
		}
	}()

	exitCode, runErr := service.runner(ctx, input, job.output)
	stopMonitor()
	<-monitorDone
	finalCountContext, finalCountCancel := context.WithTimeout(context.Background(), loaderRowCountTimeout)
	service.refreshRowCount(finalCountContext, jobID, input, false)
	finalCountCancel()

	finishedAt := time.Now().UTC()
	finalOutput := redactDatabasePasswords(job.output.String(), input.MonitorPassword, input.VaultSecretOCID)
	status := "succeeded"
	errorMessage := ""
	if runErr != nil || exitCode != 0 {
		status = "failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timed_out"
		}
		if runErr != nil {
			errorMessage = redactDatabasePasswords("loader execution failed: "+runErr.Error(), input.MonitorPassword, input.VaultSecretOCID)
		} else {
			errorMessage = fmt.Sprintf("loader execution failed with exit code %d", exitCode)
		}
	}

	service.mu.Lock()
	if job, ok = service.jobs[jobID]; ok {
		job.status = status
		job.finishedAtUTC = finishedAt
		job.exitCode = &exitCode
		job.finalOutput = finalOutput
		job.output = nil
		job.errorMessage = errorMessage
	}
	if service.activeID == jobID {
		service.activeID = ""
	}
	service.mu.Unlock()

	time.AfterFunc(service.retention, func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		if retained, exists := service.jobs[jobID]; exists && retained.status != "running" && retained.status != "starting" {
			delete(service.jobs, jobID)
		}
	})
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
	defer service.mu.Unlock()
	job, ok := service.jobs[jobID]
	if !ok {
		return
	}
	if err != nil {
		job.rowCountError = redactDatabasePassword(err.Error(), input.MonitorPassword)
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
	return loaderExecutionResponse{
		JobID:                job.id,
		Status:               job.status,
		DatabaseUser:         job.databaseUser,
		DatabaseAlias:        job.databaseAlias,
		TableName:            loaderTargetTableName,
		StartedAtUTC:         formatOptionalLoaderTime(job.startedAtUTC),
		FinishedAtUTC:        formatOptionalLoaderTime(job.finishedAtUTC),
		ExitCode:             job.exitCode,
		InitialRowCount:      job.initialRowCount,
		InitialRowCountKnown: job.initialRowCountKnown,
		CurrentRowCount:      job.currentRowCount,
		CurrentRowCountKnown: job.currentRowCountKnown,
		RowsInserted:         job.rowsInserted,
		RowsInsertedKnown:    job.rowsInsertedKnown,
		RowCountUpdatedAtUTC: formatOptionalLoaderTime(job.rowCountUpdatedAtUTC),
		RowCountError:        job.rowCountError,
		Output:               job.finalOutput,
		Error:                job.errorMessage,
	}, true
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
	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	if err != nil {
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
