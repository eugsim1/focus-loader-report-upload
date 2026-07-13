package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"os"
	"path/filepath"
	"time"
)

func main() {
	programStart := time.Now()
	exitCode := 0

	fmt.Println("Start of script")

	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "\nERROR: %v\n", err)
		exitCode = 1
	}

	fmt.Printf("\nTotal runtime: %s\n", formatElapsed(time.Since(programStart)))
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run(ctx context.Context) error {
	cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if cmd.showVersion {
		fmt.Printf("%s %s\n", filepath.Base(os.Args[0]), version)
		return nil
	}

	if cmd.dbUser == "" || cmd.dbName == "" || (cmd.dbPassword == "" && cmd.dbSecretID == "") {
		printUsage()
		printHeader("You must specify database credentials using either -dp or -ds!!", 0)
		return nil
	}
	if cmd.workers < 1 {
		return fmt.Errorf("-workers must be at least 1")
	}

	if err := os.MkdirAll(workReportDir, 0o755); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	if cmd.stateFile == "" {
		cmd.stateFile = filepath.Join(workReportDir, "processed_files.jsonl")
	}

	cfg, err := loadConfig("focus.conf")
	if err != nil {
		return err
	}

	provider, err := createConfigProvider(cmd, false)
	if err != nil {
		return err
	}

	programStart := currentDateTime()
	focusNamespaceName := cmd.namespaceName

	printHeader("Running Focus Load to ADW", 0)
	fmt.Println("Starts at " + currentDateTime())
	fmt.Println("Command Line : " + maskedCommandLine(os.Args[1:]))

	dbPass := cmd.dbPassword
	if dbPass != "" {
		fmt.Println("INFO: Using database password from command line (-dp)")
	} else {
		fmt.Println("INFO: Retrieving database password from OCI Vault secret")
		secretProvider, err := createConfigProvider(cmd, true)
		if err != nil {
			return err
		}
		dbPass, err = getSecretPassword(ctx, secretProvider, cmd.proxy, cmd.dbSecretID)
		if err != nil {
			return err
		}
	}

	fmt.Println("\nConnecting to Identity Service...")
	identityClient, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return fmt.Errorf("create identity client: %w", err)
	}
	applyProxy(&identityClient.BaseClient, cmd.proxy)

	tenancyID, err := provider.TenancyOCID()
	if err != nil {
		return fmt.Errorf("read tenancy OCID from provider: %w", err)
	}

	tenancyResp, err := identityClient.GetTenancy(ctx, identity.GetTenancyRequest{
		TenancyId: common.String(tenancyID),
	})
	if err != nil {
		return fmt.Errorf("get tenancy: %w", err)
	}

	tenancy := tenancyInfo{
		ID:   value(tenancyResp.Tenancy.Id),
		Name: value(tenancyResp.Tenancy.Name),
	}

	homeRegion, err := getHomeRegion(ctx, identityClient, tenancy.ID)
	if err != nil {
		return err
	}

	identityClient.SetRegion(homeRegion)

	focusBucketName := cmd.bucketName
	if focusBucketName == "" {
		focusBucketName = tenancy.ID
	}

	startDate := cmd.fileDate
	if startDate == "" {
		startDate = "FULL LOAD"
	}

	fmt.Println("   Tenant Name  : " + tenancy.Name)
	fmt.Println("   Tenant Id    : " + tenancy.ID)
	fmt.Println("   App Version  : " + version)
	fmt.Println("   Home Region  : " + homeRegion)
	fmt.Println("   OS Namespace : " + focusNamespaceName)
	fmt.Println("   OS Bucket    : " + focusBucketName)
	fmt.Println("   Start Date   : " + startDate)
	fmt.Println()

	compartments, err := identityReadCompartments(ctx, identityClient, tenancy)
	if err != nil {
		return err
	}

	fmt.Println("\nConnecting to database " + cmd.dbName)
	fmt.Println("   DB Driver : " + databaseDriverDescription())
	db, err := openOracleDB(cmd, dbPass)
	if err != nil {
		return fmt.Errorf("open database connection: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	db.SetMaxOpenConns(cmd.workers*3 + 2)
	db.SetMaxIdleConns(cmd.workers + 1)
	fmt.Println("   Connected")

	fmt.Println("\nChecking Database Structure...")
	if _, err := db.ExecContext(ctx, "ALTER SESSION SET OPTIMIZER_IGNORE_HINTS=FALSE"); err != nil {
		return fmt.Errorf("alter session optimizer hints: %w", err)
	}
	if _, err := db.ExecContext(ctx, "ALTER SESSION SET OPTIMIZER_IGNORE_PARALLEL_HINTS=FALSE"); err != nil {
		return fmt.Errorf("alter session optimizer parallel hints: %w", err)
	}

	fmt.Println("\nChecking Last Loaded Files... started at " + currentDateTime())
	maxFocusFileName, err := getMaxLoadedFile(ctx, db, cfg, tenancy.Name)
	if err != nil {
		return err
	}
	fmt.Println("   Max FOCUS File Name Processed = '" + maxFocusFileName + "'")
	loadedFiles, err := getLoadedFiles(ctx, db, cfg, tenancy.Name)
	if err != nil {
		return err
	}
	fmt.Printf("   Loaded FOCUS files in DB       = %d\n", len(loadedFiles))

	stateStore, err := newProcessedStore(cmd.stateFile, tenancy.Name)
	if err != nil {
		return err
	}
	fmt.Printf("   Loaded FOCUS files in state    = %d\n", stateStore.Count())
	var uploadStateStore *processedStore
	if cmd.uploadReports && !cmd.loadAfterUpload {
		uploadStateStore, err = newProcessedStore(cmd.uploadStateFile, tenancy.Name)
		if err != nil {
			return err
		}
		fmt.Printf("   Uploaded FOCUS files in state  = %d (%s)\n", uploadStateStore.Count(), cmd.uploadStateFile)
	}
	fmt.Println("Completed Checking at " + currentDateTime())

	fmt.Println("\nConnecting to Object Storage Service...")
	objectClient, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return fmt.Errorf("create object storage client: %w", err)
	}
	applyProxy(&objectClient.BaseClient, cmd.proxy)
	objectClient.SetRegion(homeRegion)
	fmt.Println("   Connected")

	fmt.Println("\nHandling FOCUS Report... started at " + currentDateTime())
	objects, err := listAllObjects(ctx, objectClient, focusNamespaceName, focusBucketName, "FOCUS Reports/", "")
	if err != nil {
		return err
	}

	processedFiles := mergeProcessedFiles(loadedFiles, stateStore.ProcessedFiles())
	if uploadStateStore != nil {
		// Upload-only state is intentionally independent from database load state.
		// A source file already loaded into Oracle may still need to be copied to a
		// newly configured destination bucket.
		processedFiles = uploadStateStore.ProcessedFiles()
	}
	if cmd.force {
		fmt.Println("   Force enabled: ignoring DB/state processed-file skips")
		processedFiles = map[string]struct{}{}
	}

	pendingObjects := filterPendingObjects(objects, cmd, processedFiles)
	fmt.Printf("Total %d FOCUS files found, %d pending after restart/date/name filters.\n", len(objects), len(pendingObjects))
	if cmd.preloadReport {
		if cmd.skipPreloadContentScan {
			fmt.Println("Pre-load metadata-only mode: object downloads, gzip decompression, and CSV row counting are disabled.")
		} else if cmd.verbose {
			fmt.Println("Verbose pre-load progress enabled: one completion line will be printed per object.")
		}
		report, err := createPreloadReport(
			ctx,
			db,
			objectClient,
			pendingObjects,
			cmd,
			cfg,
			focusNamespaceName,
			focusBucketName,
		)
		if err != nil {
			return err
		}
		fmt.Printf("Pre-load report written: %s\n", report.DetailReportPath)
		fmt.Printf("Pre-load summary written: %s\n", report.SummaryReportPath)
		fmt.Printf("Pre-load capacity planning written: %s\n", report.CapacityReportPath)
		fmt.Printf("Pre-load totals: files=%d bytes=%d (%s) csv_lines=%d duration=%s download_eta=%s\n",
			report.TotalFiles,
			report.TotalBytes,
			formatBytes(report.TotalBytes),
			report.TotalCSVLines,
			formatElapsed(report.Duration),
			formatElapsed(report.EstimatedDownloadETA),
		)
		if !cmd.continueAfterReport && !cmd.uploadReports {
			fmt.Println("Stopping after pre-load report. Use -continue-after-report to continue with database load.")
			return nil
		}
	}

	var uploadSession *transformedUploadSession
	if cmd.uploadReports {
		uploadNamespace := cmd.reportUploadNamespace
		if uploadNamespace == "" {
			uploadNamespace = focusNamespaceName
		}
		uploadRegion := cmd.reportUploadRegion
		if uploadRegion == "" {
			uploadRegion = homeRegion
		}

		destinationClient, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
		if err != nil {
			return fmt.Errorf("create destination object storage client: %w", err)
		}
		applyProxy(&destinationClient.BaseClient, cmd.proxy)
		destinationClient.SetRegion(uploadRegion)

		fmt.Printf("Transformed CSV upload enabled for %d pending FOCUS files: namespace=%s bucket=%s region=%s prefix=%q\n",
			len(pendingObjects), uploadNamespace, cmd.reportUploadBucket, uploadRegion, cmd.reportUploadPrefix)
		uploadSession, err = newTransformedUploadSession(
			ctx,
			destinationClient,
			uploadNamespace,
			cmd.reportUploadBucket,
			cmd.reportUploadPrefix,
			cmd.focusUploadResultsFile,
			cmd.latestUploadFile,
			cmd.keepWorkFiles,
			cmd.verbose,
			len(pendingObjects),
		)
		if err != nil {
			return err
		}
		if !cmd.loadAfterUpload {
			fmt.Println("Upload-only mode: each source gzip will be transformed, uploaded as CSV, and skipped by SQL*Loader.")
			fmt.Printf("Latest successful upload checkpoint: %s\n", cmd.latestUploadFile)
		} else {
			fmt.Println("Upload-and-load mode: each transformed CSV will be uploaded immediately before SQL*Loader starts for that file.")
		}
	}
	fmt.Printf("Processing with %d worker(s). State file: %s\n", cmd.workers, cmd.stateFile)

	loaded, processErr := processFocusFiles(
		ctx,
		db,
		objectClient,
		pendingObjects,
		cmd,
		tenancy,
		compartments,
		focusNamespaceName,
		focusBucketName,
		dbPass,
		cfg,
		stateStore,
		uploadStateStore,
		uploadSession,
	)
	if uploadSession != nil {
		uploadSummary, finalizeErr := uploadSession.Finalize(ctx)
		fmt.Printf("Transformed upload results written: %s\n", uploadSummary.ResultsReportPath)
		fmt.Printf("Transformed CSV uploads: successful=%d failed=%d rows=%d bytes=%d (%s) results_object=%s\n",
			uploadSummary.SuccessfulFiles,
			uploadSummary.FailedFiles,
			uploadSummary.TotalRows,
			uploadSummary.TotalBytes,
			formatBytes(uploadSummary.TotalBytes),
			uploadSummary.ResultsObjectName,
		)
		if processErr != nil || finalizeErr != nil {
			return errors.Join(processErr, finalizeErr)
		}
		if !cmd.loadAfterUpload {
			fmt.Println("Stopping after transformed CSV upload. Use -load-after-upload with -upload-reports to run SQL*Loader after each upload.")
			return nil
		}
	}
	if processErr != nil {
		return processErr
	}

	fmt.Printf("\n   Total %d Cost Files Loaded, completed at %s\n", loaded, currentDateTime())
	if err := printExecutionSummary(ctx, db, cfg, tenancy.Name, programStart); err != nil {
		return err
	}

	fmt.Println("\nCompleted at " + currentDateTime())
	return nil
}
