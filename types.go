package main

import (
	"sync"
	"time"

	"gopkg.in/ini.v1"
)

const (
	version       = "26.13.0-schema-deployment-diagnostics"
	workReportDir = "work_report_dir"
	tagBatchSize  = 5000
)

var tagKeyMetadataMu sync.Mutex

type commandLine struct {
	configPath             string
	profile                string
	fileNameFull           string
	tagSpecial1            string
	tagSpecial2            string
	tagSpecial3            string
	tagSpecial4            string
	tagSpecial5            string
	fileDate               string
	proxy                  string
	instancePrincipals     bool
	bucketName             string
	namespaceName          string
	dbUser                 string
	dbName                 string
	dbSecretID             string
	dbSecretProfile        string
	force                  bool
	ctlFile                string
	dbPassword             string
	dbPasswordStdin        bool
	workers                int
	stateFile              string
	keepWorkFiles          bool
	skipTags               bool
	skipTagRows            bool
	skipTagKeys            bool
	preloadReport          bool
	preloadReportFile      string
	continueAfterReport    bool
	uploadReports          bool
	reportUploadNamespace  string
	reportUploadBucket     string
	reportUploadPrefix     string
	reportUploadRegion     string
	focusUploadResultsFile string
	latestUploadFile       string
	uploadStateFile        string
	loadAfterUpload        bool
	verbose                bool
	skipPreloadContentScan bool
	tnsGUI                 bool
	tnsGUIListen           string
	showVersion            bool
}

type appConfig struct {
	ini *ini.File
}

type compartmentInfo struct {
	ID   string
	Name string
	Path string
}

type tenancyInfo struct {
	ID   string
	Name string
}

type tagRow struct {
	TenantName        string
	ResourceID        string
	ChargePeriodStart string
	TagKey            string
	TagValue          string
}

type tagKeyValue struct {
	TenantName string
	TagKey     string
	TagValue   string
}

type loadResult struct {
	Loaded     bool
	FileName   string
	FileID     string
	NumRows    int
	FileSizeMB int64
}

type processedRecord struct {
	TenantName  string `json:"tenant_name"`
	FileName    string `json:"file_name"`
	FileID      string `json:"file_id"`
	Rows        int    `json:"rows"`
	FileSizeMB  int64  `json:"file_size_mb"`
	ProcessedAt string `json:"processed_at"`
	Version     string `json:"version"`
}

type ctlFileInfo struct {
	Path           string
	HasInfile      bool
	HasBadfile     bool
	HasDiscardfile bool
	HasDirect      bool
	HasErrors      bool
}

type sqlLoaderResult struct {
	DataFile        string
	LogFile         string
	Status          string
	RowsInserted    int
	RowsFailed      int
	RowsRejected    int
	RowsDiscarded   int
	RowsSkipped     int
	TotalRowsRead   int
	TotalFileLines  int
	FileSizeBytes   int64
	StartedAt       time.Time
	EndedAt         time.Time
	DurationSeconds float64
	ErrorMessage    string
}

type processedStore struct {
	path       string
	tenantName string
	mu         sync.Mutex
	seen       map[string]processedRecord
}
