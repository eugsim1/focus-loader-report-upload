package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLoaderExecutionRequest() loaderExecutionRequest {
	return loaderExecutionRequest{
		OCIAuthMode:            loaderOCIConfigProfile,
		DatabaseAuthMode:       loaderDatabasePassword,
		OCIProfile:             "DEFAULT",
		DatabaseUser:           "FOCUS_APP",
		DatabaseAlias:          "FOCUS_HIGH",
		DatabasePassword:       "runtime-test-secret",
		SourceNamespace:        "bling",
		MinimumDate:            "2026-01-01",
		Workers:                5,
		TagSpecial1:            "Oracle-Tags.CreatedBy",
		TagSpecial2:            "CCA_Basic_Tag.email",
		TagSpecial3:            "Oracle_Tags.CreatedBy",
		TagSpecial4:            "Oracle_Tags.CreatedOn",
		PreloadReport:          true,
		ContinueAfterReport:    true,
		SkipPreloadContentScan: true,
		SkipTagRows:            true,
	}
}

func TestLoaderExecutionArgumentsUsePasswordStdinAndRequestedDefaults(t *testing.T) {
	request := testLoaderExecutionRequest()
	arguments, err := validateAndBuildLoaderArguments(&request, []string{"FOCUS_HIGH"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if strings.Contains(joined, request.DatabasePassword) || strings.Contains(joined, "-dp ") {
		t.Fatalf("loader arguments contain the direct password: %s", joined)
	}
	for _, expected := range []string{
		"-dp-stdin",
		"-preload-report",
		"-skip-preload-content-scan",
		"-skip-tag-rows",
		"-continue-after-report",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("loader arguments are missing %s: %s", expected, joined)
		}
	}
}

func TestLoaderExecutionRejectsAliasOutsideServerCatalog(t *testing.T) {
	request := testLoaderExecutionRequest()
	request.DatabaseAlias = "UNLISTED_HIGH"
	if _, err := validateAndBuildLoaderArguments(&request, []string{"FOCUS_HIGH"}); err == nil || !strings.Contains(err.Error(), "TNS catalog") {
		t.Fatalf("expected TNS catalog validation error, got %v", err)
	}
}

func TestLoaderExecutionVaultArgumentsDoNotReadPasswordStdin(t *testing.T) {
	request := testLoaderExecutionRequest()
	request.DatabaseAuthMode = loaderDatabaseVault
	request.DatabasePassword = ""
	request.VaultSecretOCID = "ocid1.vaultsecret.oc1..testonly"
	request.VaultSecretProfile = "DEFAULT"
	arguments, err := validateAndBuildLoaderArguments(&request, []string{"FOCUS_HIGH"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if strings.Contains(joined, "-dp-stdin") || !strings.Contains(joined, "-ds ocid1.vaultsecret.oc1..testonly") {
		t.Fatalf("unexpected Vault arguments: %s", joined)
	}
}

func TestLoaderExecutionServiceReportsRowCountIncreaseAndRedactsOutput(t *testing.T) {
	tnsAdmin := t.TempDir()
	if err := os.WriteFile(
		tnsAdmin+string(os.PathSeparator)+tnsNamesFileName,
		[]byte("FOCUS_HIGH = (DESCRIPTION = (ADDRESS = TCP))\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workDirectory := t.TempDir()
	getenv := func(name string) string {
		switch name {
		case "TNS_ADMIN":
			return tnsAdmin
		case "FOCUS_LOADER_EXECUTABLE":
			return executable
		case "FOCUS_LOADER_WORK_DIR":
			return workDirectory
		default:
			return ""
		}
	}

	var captured loaderExecutionInput
	runner := func(_ context.Context, input loaderExecutionInput, output io.Writer) (int, error) {
		captured = input
		_, _ = io.WriteString(output, "password="+input.MonitorPassword+" completed")
		return 0, nil
	}
	counts := []int64{100, 125}
	var countMu sync.Mutex
	var counterUser, counterPassword, counterAlias string
	rowCounter := func(_ context.Context, username, password, alias string) (int64, error) {
		countMu.Lock()
		defer countMu.Unlock()
		counterUser, counterPassword, counterAlias = username, password, alias
		value := counts[0]
		if len(counts) > 1 {
			counts = counts[1:]
		}
		return value, nil
	}
	analyticsCalls := 0
	analyticsReader := func(
		_ context.Context,
		username string,
		password string,
		alias string,
	) (loaderAnalyticsSnapshot, error) {
		if username != "FOCUS_APP" || password != "runtime-test-secret" || alias != "FOCUS_HIGH" {
			t.Fatalf("unexpected analytics credentials: %s/%s@%s", username, password, alias)
		}
		analyticsCalls++
		return loaderAnalyticsSnapshot{
			MonthlyCosts: []loaderMonthlyCost{
				{Month: "2026-01", BillingCurrency: "USD", EffectiveCost: "123.45"},
			},
			Services: []string{"Compute", "Object Storage"},
		}, nil
	}
	service := &loaderExecutionService{
		getenv:          getenv,
		runner:          runner,
		rowCounter:      rowCounter,
		analyticsReader: analyticsReader,
		passwordResolver: func(context.Context, loaderExecutionRequest) (string, error) {
			t.Fatal("Vault resolver must not be called for direct-password execution")
			return "", nil
		},
		pollInterval:      time.Hour,
		analyticsInterval: time.Hour,
		timeout:           time.Minute,
		retention:         time.Hour,
		jobs:              make(map[string]*loaderExecutionJob),
	}
	started, err := service.start(testLoaderExecutionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if started.MonthlyCosts == nil || started.Services == nil {
		t.Fatalf("starting response must use empty analytics arrays: %#v", started)
	}
	if started.WorkReportDirectory != filepath.Join(workDirectory, workReportDir) ||
		started.WorkReportResetAtUTC == "" {
		t.Fatalf("work_report_dir reset metadata is missing: %#v", started)
	}
	if started.Executable != executable || started.WorkingDirectory != workDirectory ||
		!strings.Contains(started.CommandLine, "'-dp-stdin'") ||
		!strings.Contains(started.ManualCommand, "Database password:") ||
		!strings.Contains(started.ManualCommand, "TNS_ADMIN="+tnsAdmin) ||
		strings.Contains(started.CommandLine, "runtime-test-secret") ||
		strings.Contains(started.ManualCommand, "runtime-test-secret") {
		t.Fatalf("unsafe or incomplete execution diagnostics: %#v", started)
	}
	deadline := time.Now().Add(2 * time.Second)
	var finished loaderExecutionResponse
	for time.Now().Before(deadline) {
		finished, _ = service.snapshot(started.JobID)
		if finished.Status == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.Status != "succeeded" || finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("unexpected completed job: %#v", finished)
	}
	if !finished.InitialRowCountKnown || finished.InitialRowCount != 100 ||
		!finished.CurrentRowCountKnown || finished.CurrentRowCount != 125 ||
		!finished.RowsInsertedKnown || finished.RowsInserted != 25 {
		t.Fatalf("unexpected row counts: %#v", finished)
	}
	if analyticsCalls != 2 || len(finished.MonthlyCosts) != 1 ||
		finished.MonthlyCosts[0].EffectiveCost != "123.45" || len(finished.Services) != 2 ||
		finished.AnalyticsUpdatedAtUTC == "" || finished.AnalyticsError != "" {
		t.Fatalf("unexpected analytics snapshot: calls=%d response=%#v", analyticsCalls, finished)
	}
	if strings.Contains(finished.Output, "runtime-test-secret") || !strings.Contains(finished.Output, "[REDACTED]") {
		t.Fatalf("loader output was not redacted: %q", finished.Output)
	}
	if captured.StdinPassword != "runtime-test-secret" {
		t.Fatalf("direct password was not provided through stdin input: %#v", captured)
	}
	if strings.Contains(strings.Join(captured.Arguments, " "), "runtime-test-secret") {
		t.Fatal("direct password was placed in process arguments")
	}
	countMu.Lock()
	defer countMu.Unlock()
	if counterUser != "FOCUS_APP" || counterPassword != "runtime-test-secret" || counterAlias != "FOCUS_HIGH" {
		t.Fatalf("unexpected row counter credentials: %s/%s@%s", counterUser, counterPassword, counterAlias)
	}
}

func TestResetLoaderWorkReportDirectoryClearsOnlySelectedUserReportDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	reportDirectory := filepath.Join(workingDirectory, workReportDir)
	if err := os.MkdirAll(filepath.Join(reportDirectory, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDirectory, "nested", "old.log"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(workingDirectory, "focus.conf")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	resetPath, err := resetLoaderWorkReportDirectory(workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if resetPath != reportDirectory {
		t.Fatalf("reset path = %q, want %q", resetPath, reportDirectory)
	}
	entries, err := os.ReadDir(reportDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("work report directory was not emptied: entries=%v error=%v", entries, err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "preserve" {
		t.Fatalf("file outside work_report_dir changed: content=%q error=%v", content, err)
	}
}

func TestLoaderSnapshotReturnsLiveRedactedOutput(t *testing.T) {
	output := &boundedDeploymentOutput{limit: maxLoaderJobOutputBytes}
	_, _ = io.WriteString(output, "startup runtime-secret diagnostic")
	service := &loaderExecutionService{jobs: map[string]*loaderExecutionJob{
		"live-job": {
			id:              "live-job",
			status:          "running",
			databaseUser:    "FOCUS_APP",
			databaseAlias:   "FOCUS_HIGH",
			output:          output,
			redactionValues: []string{"runtime-secret"},
		},
	}}
	response, ok := service.snapshot("live-job")
	if !ok || !strings.Contains(response.Output, "startup [REDACTED] diagnostic") ||
		strings.Contains(response.Output, "runtime-secret") {
		t.Fatalf("live output was missing or unsafe: %#v", response)
	}
}

func TestRunLoaderExecutionProcessCapturesStartFailure(t *testing.T) {
	output := &boundedDeploymentOutput{limit: maxLoaderJobOutputBytes}
	exitCode, err := runLoaderExecutionProcess(context.Background(), loaderExecutionInput{
		Executable:       filepath.Join(t.TempDir(), "missing-loader"),
		WorkingDirectory: t.TempDir(),
	}, output)
	if err == nil || exitCode != -1 {
		t.Fatalf("expected process start failure, got exit=%d error=%v", exitCode, err)
	}
	for _, expected := range []string{"Process exit code: -1", "Process error:"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("process diagnostics missing %q: %s", expected, output.String())
		}
	}
}

func TestLoaderFailureMessageIncludesProcessDiagnosticsWithoutDuplicatingOutput(t *testing.T) {
	message := buildLoaderFailureMessage(
		errors.New("exit status 1"),
		1,
		"/opt/focus-loader/focus-loader-report-upload",
		"/opt/focus-loader",
		"ERROR: sqlldr was not found",
		false,
	)
	for _, expected := range []string{
		"exit status 1",
		"exit code 1",
		"Executable: /opt/focus-loader/focus-loader-report-upload",
		"Working directory: /opt/focus-loader",
		"combined stdout/stderr",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("failure message is missing %q: %s", expected, message)
		}
	}
	if strings.Contains(message, "sqlldr was not found") {
		t.Fatalf("failure summary duplicated the full captured output: %s", message)
	}
}

func TestLoaderAnalyticsErrorRedactsDatabasePassword(t *testing.T) {
	service := &loaderExecutionService{
		analyticsReader: func(
			context.Context,
			string,
			string,
			string,
		) (loaderAnalyticsSnapshot, error) {
			return loaderAnalyticsSnapshot{}, errors.New("query failed with runtime-secret")
		},
		jobs: map[string]*loaderExecutionJob{
			"test-job": {
				id:                   "test-job",
				status:               "running",
				currentRowCount:      1,
				currentRowCountKnown: true,
			},
		},
	}
	service.refreshAnalytics(context.Background(), "test-job", loaderExecutionInput{
		DatabaseUser:    "FOCUS_APP",
		MonitorPassword: "runtime-secret",
		DatabaseAlias:   "FOCUS_HIGH",
	})
	response, ok := service.snapshot("test-job")
	if !ok || strings.Contains(response.AnalyticsError, "runtime-secret") ||
		!strings.Contains(response.AnalyticsError, "[REDACTED]") {
		t.Fatalf("analytics error was not safely redacted: %#v", response)
	}
}

func TestLoaderAnalyticsSkipsQueriesForEmptyTable(t *testing.T) {
	service := &loaderExecutionService{
		analyticsReader: func(
			context.Context,
			string,
			string,
			string,
		) (loaderAnalyticsSnapshot, error) {
			t.Fatal("analytics query must not run while TEMP_OCI_FOCUS is empty")
			return loaderAnalyticsSnapshot{}, nil
		},
		jobs: map[string]*loaderExecutionJob{
			"empty-job": {
				id:                   "empty-job",
				status:               "running",
				currentRowCountKnown: true,
			},
		},
	}
	service.refreshAnalytics(context.Background(), "empty-job", loaderExecutionInput{})
	response, ok := service.snapshot("empty-job")
	if !ok || response.MonthlyCosts == nil || len(response.MonthlyCosts) != 0 ||
		response.Services == nil || len(response.Services) != 0 ||
		response.AnalyticsUpdatedAtUTC == "" || response.AnalyticsError != "" {
		t.Fatalf("unexpected empty-table analytics snapshot: %#v", response)
	}
}

func TestReadLoaderPasswordAndRejectCredentialConflicts(t *testing.T) {
	password, err := readLoaderPassword(strings.NewReader("value-with-##"))
	if err != nil || password != "value-with-##" {
		t.Fatalf("readLoaderPassword() = %q, %v", password, err)
	}
	if _, err := readLoaderPassword(strings.NewReader("bad\nvalue")); err == nil {
		t.Fatal("password containing a newline was accepted")
	}
	if _, err := parseArgs([]string{"-dp-stdin", "-dp", "secret"}); err == nil {
		t.Fatal("-dp-stdin and -dp were accepted together")
	}
}

func TestLoaderExecutionRoutesAreRegisteredAndMethodRestricted(t *testing.T) {
	service := &loaderExecutionService{jobs: make(map[string]*loaderExecutionJob)}
	handler := newTNSGUIHandlerWithLoaderService(
		func(string) string { return "" },
		nil,
		nil,
		service,
	)
	collectionRequest := httptest.NewRequest(http.MethodGet, loaderJobCollectionPath, nil)
	collectionResponse := httptest.NewRecorder()
	handler.ServeHTTP(collectionResponse, collectionRequest)
	if collectionResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection GET status=%d body=%s", collectionResponse.Code, collectionResponse.Body.String())
	}

	itemRequest := httptest.NewRequest(http.MethodGet, loaderJobPathPrefix+"missing-job", nil)
	itemResponse := httptest.NewRecorder()
	handler.ServeHTTP(itemResponse, itemRequest)
	if itemResponse.Code != http.StatusNotFound {
		t.Fatalf("missing job status=%d body=%s", itemResponse.Code, itemResponse.Body.String())
	}
}
