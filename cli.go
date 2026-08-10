package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func parseArgs(args []string) (commandLine, error) {
	var cmd commandLine
	fs := flag.NewFlagSet(filepath.Base(os.Args[0]), flag.ContinueOnError)
	fs.StringVar(&cmd.configPath, "c", "", "Config File")
	fs.StringVar(&cmd.profile, "t", "", "Config file section to use (tenancy profile)")
	fs.StringVar(&cmd.fileNameFull, "f", "", "File Name to load")
	fs.StringVar(&cmd.tagSpecial1, "ts1", "", "tag special key 1 to load the data to TAG_SPECIAL1 column")
	fs.StringVar(&cmd.tagSpecial2, "ts2", "", "tag special key 2 to load the data to TAG_SPECIAL2 column")
	fs.StringVar(&cmd.tagSpecial3, "ts3", "", "tag special key 3 to load the data to TAG_SPECIAL3 column")
	fs.StringVar(&cmd.tagSpecial4, "ts4", "", "tag special key 4 to load the data to TAG_SPECIAL4 column")
	fs.StringVar(&cmd.tagSpecial5, "ts5", "", "tag special key 5 to load the data to TAG_SPECIAL5 column")
	fs.StringVar(&cmd.fileDate, "d", "", "Minimum File Date to load, inclusive (yyyy-mm-dd)")
	fs.StringVar(&cmd.proxy, "p", "", "Set Proxy (i.e. www-proxy-server.com:80)")
	fs.BoolVar(&cmd.instancePrincipals, "ip", false, "Use Instance Principals for Authentication")
	fs.StringVar(&cmd.bucketName, "bn", "", "Override Bucket Name for Cost and Usage Files")
	fs.StringVar(&cmd.namespaceName, "ns", "bling", "Override Namespace Name for Cost and Usage Files")
	fs.StringVar(&cmd.dbUser, "du", "", "ADB User")
	fs.StringVar(&cmd.dbName, "dn", "", "ADB Name")
	fs.StringVar(&cmd.dbSecretID, "ds", "", "ADB Secret Id")
	fs.StringVar(&cmd.dbSecretProfile, "dst", "", "ADB Secret tenancy profile (local or blank = instance principal)")
	fs.BoolVar(&cmd.force, "force", false, "Force Update without updated file")
	fs.StringVar(&cmd.ctlFile, "ctl", "focus.ctl", "Path to SQL*Loader control file")
	fs.StringVar(&cmd.dbPassword, "dp", "", "ADB Password")
	fs.BoolVar(&cmd.dbPasswordStdin, "dp-stdin", false, "Read ADB password from standard input")
	fs.IntVar(&cmd.workers, "workers", 1, "Number of files to process concurrently")
	fs.StringVar(&cmd.stateFile, "state-file", filepath.Join(workReportDir, "processed_files.jsonl"), "Restart checkpoint file")
	fs.BoolVar(&cmd.keepWorkFiles, "keep-work-files", false, "Keep downloaded gzip and generated CSV files after successful load")
	fs.BoolVar(&cmd.skipTags, "skip-tags", false, "Skip all tag JSON parsing, tag row inserts, tag key metadata, and TAG_SPECIAL extraction")
	fs.BoolVar(&cmd.skipTagRows, "skip-tag-rows", false, "Skip loading row-level tag values into TEMP_OCI_FOCUS_TAGS")
	fs.BoolVar(&cmd.skipTagKeys, "skip-tag-keys", false, "Skip loading unique tag key/value metadata into TAG_KEYS")
	fs.BoolVar(&cmd.preloadReport, "preload-report", false, "Create pre-load report with pending file count, size, CSV line count, and download ETA")
	fs.StringVar(&cmd.preloadReportFile, "preload-report-file", filepath.Join(workReportDir, "preload_report.csv"), "Pre-load detail CSV report path")
	fs.BoolVar(&cmd.continueAfterReport, "continue-after-report", false, "Continue with database load after creating -preload-report")
	fs.BoolVar(&cmd.uploadReports, "upload-reports", false, "Upload each transformed CSV immediately before its SQL*Loader stage")
	fs.StringVar(&cmd.reportUploadNamespace, "report-upload-namespace", "", "Destination Object Storage namespace (default: source namespace)")
	fs.StringVar(&cmd.reportUploadBucket, "report-upload-bucket", "", "Destination Object Storage bucket (required for -upload-reports)")
	fs.StringVar(&cmd.reportUploadPrefix, "report-upload-prefix", "", "Optional destination prefix; source object paths are preserved and .csv.gz becomes .csv")
	fs.StringVar(&cmd.reportUploadRegion, "report-upload-region", "", "Destination Object Storage region (default: source/home region)")
	fs.StringVar(&cmd.focusUploadResultsFile, "focus-upload-results-file", filepath.Join(workReportDir, "focus_file_upload_results.csv"), "Per-file transformed CSV upload results path")
	fs.StringVar(&cmd.latestUploadFile, "latest-upload-file", filepath.Join(workReportDir, "latest_focus_file_upload.csv"), "Local CSV checkpoint for the latest successful transformed-file upload")
	fs.StringVar(&cmd.uploadStateFile, "upload-state-file", filepath.Join(workReportDir, "uploaded_files.jsonl"), "Restart checkpoint for successful upload-only files")
	fs.BoolVar(&cmd.loadAfterUpload, "load-after-upload", false, "Run SQL*Loader for each file immediately after its transformed CSV uploads successfully")
	fs.BoolVar(&cmd.verbose, "verbose", false, "Print detailed per-file pre-load progress and diagnostics")
	fs.BoolVar(&cmd.skipPreloadContentScan, "skip-preload-content-scan", false, "Do not download, decompress, or count rows in gzip objects during pre-load reporting")
	fs.BoolVar(&cmd.tnsGUI, "tns-gui", false, "Start the local TNS and database-metadata API; startup credentials are not required")
	fs.StringVar(&cmd.tnsGUIListen, "tns-gui-listen", "127.0.0.1:8080", "TNS GUI listen address")
	fs.BoolVar(&cmd.showVersion, "version", false, "show version")

	if err := fs.Parse(args); err != nil {
		return cmd, err
	}
	if cmd.dbPasswordStdin && (cmd.dbPassword != "" || cmd.dbSecretID != "") {
		return cmd, fmt.Errorf("-dp-stdin cannot be combined with -dp or -ds")
	}
	if cmd.skipTags {
		cmd.skipTagRows = true
		cmd.skipTagKeys = true
	}
	if cmd.loadAfterUpload && !cmd.uploadReports {
		return cmd, fmt.Errorf("-load-after-upload requires -upload-reports")
	}
	if cmd.uploadReports && cmd.continueAfterReport {
		return cmd, fmt.Errorf("-continue-after-report cannot be combined with -upload-reports; use -load-after-upload")
	}
	if cmd.uploadReports && cmd.reportUploadBucket == "" {
		return cmd, fmt.Errorf("-report-upload-bucket is required with -upload-reports")
	}
	if cmd.uploadReports && !cmd.loadAfterUpload {
		cmd.skipTagRows = true
		cmd.skipTagKeys = true
	}
	if cmd.skipPreloadContentScan && !cmd.uploadReports {
		cmd.preloadReport = true
	}
	return cmd, nil
}

func printUsage() {
	fmt.Printf("Usage of %s:\n", filepath.Base(os.Args[0]))
	fmt.Println("  -c string     Config File")
	fmt.Println("  -t string     Config file section to use (tenancy profile)")
	fmt.Println("  -f string     File Name to load")
	fmt.Println("  -ts1 string   tag special key 1")
	fmt.Println("  -ts2 string   tag special key 2")
	fmt.Println("  -ts3 string   tag special key 3")
	fmt.Println("  -ts4 string   tag special key 4")
	fmt.Println("  -ts5 string   tag special key 5")
	fmt.Println("  -d string     Minimum File Date to load, inclusive (yyyy-mm-dd)")
	fmt.Println("  -p string     Proxy (i.e. www-proxy-server.com:80)")
	fmt.Println("  -ip           Use Instance Principals for Authentication")
	fmt.Println("  -bn string    Override Bucket Name")
	fmt.Println("  -ns string    Override Namespace Name (default bling)")
	fmt.Println("  -du string    ADB User")
	fmt.Println("  -dn string    ADB Name / connect string")
	fmt.Println("  -ds string    ADB Secret Id")
	fmt.Println("  -dst string   ADB Secret tenancy profile")
	fmt.Println("  -force        Force Update without updated file")
	fmt.Println("  -ctl string   SQL*Loader control file (default focus.ctl)")
	fmt.Println("  -dp string    ADB Password")
	fmt.Println("  -dp-stdin     Read ADB password from standard input (cannot combine with -dp or -ds)")
	fmt.Println("  -workers int  Number of files to process concurrently (default 1)")
	fmt.Println("  -state-file   Restart checkpoint file (default work_report_dir/processed_files.jsonl)")
	fmt.Println("  -keep-work-files")
	fmt.Println("  -skip-tags     Skip all tag parsing/inserts and TAG_SPECIAL extraction")
	fmt.Println("  -skip-tag-rows Skip loading row-level tag values into TEMP_OCI_FOCUS_TAGS")
	fmt.Println("  -skip-tag-keys Skip loading unique tag key/value metadata into TAG_KEYS")
	fmt.Println("  -preload-report Create pre-load report, then stop unless -continue-after-report is set")
	fmt.Println("  -preload-report-file string Detail CSV report path (default work_report_dir/preload_report.csv)")
	fmt.Println("  -continue-after-report Continue database load after creating -preload-report")
	fmt.Println("  -upload-reports Transform each pending FOCUS gzip and upload the generated CSV; skip SQL*Loader unless -load-after-upload is set")
	fmt.Println("  -report-upload-namespace string Destination namespace (default: source namespace)")
	fmt.Println("  -report-upload-bucket string Destination bucket (required with -upload-reports)")
	fmt.Println("  -report-upload-prefix string Optional prefix; source paths are preserved and .csv.gz becomes .csv")
	fmt.Println("  -report-upload-region string Destination region (default: source/home region)")
	fmt.Println("  -focus-upload-results-file string Transformed upload results CSV (default: work_report_dir/focus_file_upload_results.csv)")
	fmt.Println("  -latest-upload-file string Latest successful upload checkpoint CSV (default: work_report_dir/latest_focus_file_upload.csv)")
	fmt.Println("  -upload-state-file string Upload-only restart checkpoint (default: work_report_dir/uploaded_files.jsonl)")
	fmt.Println("  -load-after-upload Run SQL*Loader immediately after each transformed CSV upload succeeds")
	fmt.Println("  -verbose      Print detailed per-file pre-load progress")
	fmt.Println("  -skip-preload-content-scan Metadata-only pre-load report; no object download, gzip decompression, or CSV row count")
	fmt.Println("  -tns-gui      Start the local TNS, database-metadata, and gated schema-deployment API")
	fmt.Println("  -tns-gui-listen string TNS GUI listen address (default 127.0.0.1:8080)")
	fmt.Println("  -version      Show version")
}
