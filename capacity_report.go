package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

type focusTableCapacity struct {
	Owner             string
	TableName         string
	SegmentBytes      int64
	StatsRows         int64
	StatsLastAnalyzed string
	PartitionCount    int
	Error             string
}

type focusPartitionCapacity struct {
	PartitionName string
	HighValue     string
	StatsRows     int64
	Blocks        int64
	SampleSize    int64
	LastAnalyzed  string
	Error         string
}

type oracleSessionIdentity struct {
	SessionUser   string
	CurrentSchema string
}

const (
	userSegmentBytesQuery = `
		SELECT NVL(SUM(bytes), 0)
		FROM SYS.USER_SEGMENTS
		WHERE SEGMENT_NAME = :1
		  AND SEGMENT_TYPE IN ('TABLE', 'TABLE PARTITION', 'TABLE SUBPARTITION')`

	allSegmentBytesQuery = `
		SELECT NVL(SUM(bytes), 0)
		FROM SYS.ALL_SEGMENTS
		WHERE OWNER = :1
		  AND SEGMENT_NAME = :2
		  AND SEGMENT_TYPE IN ('TABLE', 'TABLE PARTITION', 'TABLE SUBPARTITION')`

	userTableStatsQuery = `
		SELECT NVL(NUM_ROWS, 0), TO_CHAR(LAST_ANALYZED, 'YYYY-MM-DD HH24:MI:SS')
		FROM SYS.USER_TABLES
		WHERE TABLE_NAME = :1`

	allTableStatsQuery = `
		SELECT NVL(NUM_ROWS, 0), TO_CHAR(LAST_ANALYZED, 'YYYY-MM-DD HH24:MI:SS')
		FROM SYS.ALL_TABLES
		WHERE OWNER = :1
		  AND TABLE_NAME = :2`

	userPartitionStatsQuery = `
		SELECT
			PARTITION_NAME,
			HIGH_VALUE,
			NVL(NUM_ROWS, 0),
			NVL(BLOCKS, 0),
			NVL(SAMPLE_SIZE, 0),
			TO_CHAR(LAST_ANALYZED, 'YYYY-MM-DD HH24:MI:SS')
		FROM SYS.USER_TAB_PARTITIONS
		WHERE TABLE_NAME = :1
		ORDER BY PARTITION_POSITION`

	allPartitionStatsQuery = `
		SELECT
			PARTITION_NAME,
			HIGH_VALUE,
			NVL(NUM_ROWS, 0),
			NVL(BLOCKS, 0),
			NVL(SAMPLE_SIZE, 0),
			TO_CHAR(LAST_ANALYZED, 'YYYY-MM-DD HH24:MI:SS')
		FROM SYS.ALL_TAB_PARTITIONS
		WHERE TABLE_OWNER = :1
		  AND TABLE_NAME = :2
		ORDER BY PARTITION_POSITION`
)

func writeCapacityPlanningReport(ctx context.Context, db *sql.DB, cfg appConfig, path string, preload preloadReportSummary) error {
	if err := ensureReportDir(path); err != nil {
		return err
	}

	tableName, err := cfg.table("FOCUS")
	if err != nil {
		return err
	}

	capacity, partitions, queryErr := readFocusTableCapacity(ctx, db, tableName)
	if queryErr != nil {
		capacity.Error = queryErr.Error()
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create capacity planning report %s: %w", path, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"section",
		"owner",
		"table_name",
		"partition_name",
		"high_value",
		"current_segment_bytes",
		"current_segment_size",
		"current_stats_rows",
		"partition_stats_rows",
		"incoming_files",
		"incoming_csv_lines",
		"incoming_compressed_bytes",
		"incoming_compressed_size",
		"estimated_bytes_per_existing_row",
		"estimated_incremental_bytes",
		"estimated_incremental_size",
		"projected_segment_bytes",
		"projected_segment_size",
		"projected_rows",
		"last_analyzed",
		"generated_at",
		"notes",
		"error",
	}); err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339)
	estimatedBytesPerRow := estimateBytesPerRow(capacity.SegmentBytes, capacity.StatsRows)
	estimatedIncrementalBytes := int64(math.Round(estimatedBytesPerRow * float64(preload.TotalCSVLines)))
	projectedSegmentBytes := capacity.SegmentBytes + estimatedIncrementalBytes
	projectedRows := capacity.StatsRows + int64(preload.TotalCSVLines)
	incomingCSVLines := fmt.Sprint(preload.TotalCSVLines)
	estimatedIncrementalBytesText := fmt.Sprint(estimatedIncrementalBytes)
	estimatedIncrementalSizeText := formatBytes(estimatedIncrementalBytes)
	projectedSegmentBytesText := fmt.Sprint(projectedSegmentBytes)
	projectedSegmentSizeText := formatBytes(projectedSegmentBytes)
	projectedRowsText := fmt.Sprint(projectedRows)

	notes := "Capacity estimate uses current table segment bytes divided by optimizer table statistics rows. Gather table stats for better estimates."
	if capacity.StatsRows <= 0 {
		notes = "Current table row statistics are zero or unavailable; incremental and projected segment estimates cannot be derived from historical density."
	}
	if queryErr != nil {
		notes = "Database metadata query failed; only incoming pre-load values are reliable."
	}
	if preload.ContentScanSkipped {
		incomingCSVLines = ""
		estimatedIncrementalBytesText = ""
		estimatedIncrementalSizeText = ""
		projectedSegmentBytesText = ""
		projectedSegmentSizeText = ""
		projectedRowsText = ""
		notes = "Metadata-only pre-load mode skipped object download, gzip decompression, and CSV row counting; row-based capacity projections are unavailable."
	}

	if err := writer.Write([]string{
		"TABLE_CAPACITY",
		capacity.Owner,
		capacity.TableName,
		"",
		"",
		fmt.Sprint(capacity.SegmentBytes),
		formatBytes(capacity.SegmentBytes),
		fmt.Sprint(capacity.StatsRows),
		"",
		fmt.Sprint(preload.TotalFiles),
		incomingCSVLines,
		fmt.Sprint(preload.TotalBytes),
		formatBytes(preload.TotalBytes),
		fmt.Sprintf("%.2f", estimatedBytesPerRow),
		estimatedIncrementalBytesText,
		estimatedIncrementalSizeText,
		projectedSegmentBytesText,
		projectedSegmentSizeText,
		projectedRowsText,
		capacity.StatsLastAnalyzed,
		now,
		notes,
		capacity.Error,
	}); err != nil {
		return err
	}

	for _, partition := range partitions {
		if err := writer.Write([]string{
			"PARTITION",
			capacity.Owner,
			capacity.TableName,
			partition.PartitionName,
			partition.HighValue,
			"",
			"",
			"",
			fmt.Sprint(partition.StatsRows),
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			partition.LastAnalyzed,
			now,
			fmt.Sprintf("blocks=%d sample_size=%d", partition.Blocks, partition.SampleSize),
			partition.Error,
		}); err != nil {
			return err
		}
	}

	return writer.Error()
}

func readFocusTableCapacity(ctx context.Context, db *sql.DB, qualifiedTable string) (focusTableCapacity, []focusPartitionCapacity, error) {
	owner, tableName, err := splitOracleTableName(ctx, db, qualifiedTable)
	if err != nil {
		return focusTableCapacity{}, nil, err
	}
	identity, err := readOracleSessionIdentity(ctx, db)
	if err != nil {
		return focusTableCapacity{}, nil, err
	}

	capacity := focusTableCapacity{
		Owner:     strings.ToUpper(owner),
		TableName: strings.ToUpper(tableName),
	}
	// USER_* views describe SESSION_USER, not CURRENT_SCHEMA. Comparing the
	// owner with CURRENT_SCHEMA can silently read the wrong schema after an
	// ALTER SESSION SET CURRENT_SCHEMA statement.
	useUserViews := shouldUseUserDictionary(capacity.Owner, identity.SessionUser)

	if useUserViews {
		if err := db.QueryRowContext(ctx, userSegmentBytesQuery,
			capacity.TableName,
		).Scan(&capacity.SegmentBytes); err != nil {
			return capacity, nil, fmt.Errorf("query table segment bytes from SYS.USER_SEGMENTS: %w", err)
		}
	} else {
		if err := db.QueryRowContext(ctx, allSegmentBytesQuery,
			capacity.Owner,
			capacity.TableName,
		).Scan(&capacity.SegmentBytes); err != nil {
			return capacity, nil, fmt.Errorf("query table segment bytes from SYS.ALL_SEGMENTS: %w", err)
		}
	}

	var tableRows sql.NullInt64
	var tableLastAnalyzed sql.NullString
	if useUserViews {
		if err := db.QueryRowContext(ctx, userTableStatsQuery,
			capacity.TableName,
		).Scan(&tableRows, &tableLastAnalyzed); err != nil {
			return capacity, nil, fmt.Errorf("query table statistics from SYS.USER_TABLES: %w", err)
		}
	} else {
		if err := db.QueryRowContext(ctx, allTableStatsQuery,
			capacity.Owner,
			capacity.TableName,
		).Scan(&tableRows, &tableLastAnalyzed); err != nil {
			return capacity, nil, fmt.Errorf("query table statistics from SYS.ALL_TABLES: %w", err)
		}
	}
	capacity.StatsRows = tableRows.Int64
	capacity.StatsLastAnalyzed = tableLastAnalyzed.String

	partitions, err := readFocusPartitionCapacity(ctx, db, capacity.Owner, capacity.TableName, useUserViews)
	if err != nil {
		return capacity, nil, err
	}
	capacity.PartitionCount = len(partitions)
	return capacity, partitions, nil
}

func readFocusPartitionCapacity(ctx context.Context, db *sql.DB, owner, tableName string, useUserViews bool) ([]focusPartitionCapacity, error) {
	var rows *sql.Rows
	var err error
	if useUserViews {
		rows, err = db.QueryContext(ctx, userPartitionStatsQuery,
			tableName,
		)
	} else {
		rows, err = db.QueryContext(ctx, allPartitionStatsQuery,
			owner,
			tableName,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query partition statistics: %w", err)
	}
	defer rows.Close()

	var partitions []focusPartitionCapacity
	for rows.Next() {
		var partition focusPartitionCapacity
		var highValue sql.NullString
		var lastAnalyzed sql.NullString
		if err := rows.Scan(
			&partition.PartitionName,
			&highValue,
			&partition.StatsRows,
			&partition.Blocks,
			&partition.SampleSize,
			&lastAnalyzed,
		); err != nil {
			return nil, fmt.Errorf("scan partition statistics: %w", err)
		}
		partition.HighValue = highValue.String
		partition.LastAnalyzed = lastAnalyzed.String
		partitions = append(partitions, partition)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read partition statistics: %w", err)
	}
	return partitions, nil
}

func splitOracleTableName(ctx context.Context, db *sql.DB, qualifiedTable string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(qualifiedTable), ".")
	if len(parts) == 2 {
		return cleanOracleIdentifier(parts[0]), cleanOracleIdentifier(parts[1]), nil
	}
	if len(parts) == 1 && strings.TrimSpace(parts[0]) != "" {
		currentSchema, err := readCurrentSchema(ctx, db)
		if err != nil {
			return "", "", err
		}
		return cleanOracleIdentifier(currentSchema), cleanOracleIdentifier(parts[0]), nil
	}
	return "", "", fmt.Errorf("invalid table name %q", qualifiedTable)
}

func readCurrentSchema(ctx context.Context, db *sql.DB) (string, error) {
	identity, err := readOracleSessionIdentity(ctx, db)
	if err != nil {
		return "", err
	}
	return identity.CurrentSchema, nil
}

func readOracleSessionIdentity(ctx context.Context, db *sql.DB) (oracleSessionIdentity, error) {
	var identity oracleSessionIdentity
	err := db.QueryRowContext(ctx, `
		SELECT
			SYS_CONTEXT('USERENV', 'SESSION_USER'),
			SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')
		FROM SYS.DUAL`).Scan(&identity.SessionUser, &identity.CurrentSchema)
	if err != nil {
		return oracleSessionIdentity{}, fmt.Errorf("read Oracle session identity: %w", err)
	}
	identity.SessionUser = cleanOracleIdentifier(identity.SessionUser)
	identity.CurrentSchema = cleanOracleIdentifier(identity.CurrentSchema)
	return identity, nil
}

func shouldUseUserDictionary(owner, sessionUser string) bool {
	return cleanOracleIdentifier(owner) == cleanOracleIdentifier(sessionUser)
}

func cleanOracleIdentifier(value string) string {
	return strings.ToUpper(strings.Trim(strings.TrimSpace(value), `"`))
}

func estimateBytesPerRow(segmentBytes, statsRows int64) float64 {
	if segmentBytes <= 0 || statsRows <= 0 {
		return 0
	}
	return float64(segmentBytes) / float64(statsRows)
}
