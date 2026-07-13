package main

import (
	"context"
	"database/sql"
	"fmt"
)

func insertLoadStats(ctx context.Context, db *sql.DB, cfg appConfig, tenantName, fileType, fileID, fileName string, fileSizeMB int64, fileTime string, numRows int, startTimeStr string, batchID, batchTotal int) error {
	table, err := cfg.table("LOAD_STATUS")
	if err != nil {
		return err
	}

	sqlText := fmt.Sprintf(`
		INSERT INTO %s
		(
			SOURCE_TENANT_NAME,
			FILE_TYPE,
			FILE_ID,
			FILE_NAME,
			FILE_SIZE,
			FILE_DATE,
			NUM_ROWS,
			LOAD_START_TIME,
			LOAD_END_TIME,
			AGENT_VERSION,
			BATCH_ID,
			BATCH_TOTAL
		) VALUES (
			:1,
			:2,
			:3,
			:4,
			:5,
			to_date(:6,'YYYY-MM-DD HH24:MI'),
			:7,
			to_date(:8,'YYYY-MM-DD HH24:MI:SS'),
			to_date(:9,'YYYY-MM-DD HH24:MI:SS'),
			:10,
			:11,
			:12
		)`, table)

	_, err = db.ExecContext(ctx, sqlText,
		tenantName,
		fileType,
		fileID,
		fileName,
		fileSizeMB,
		fileTime,
		numRows,
		startTimeStr,
		currentDateTime(),
		version,
		batchID,
		batchTotal,
	)
	if err != nil {
		return fmt.Errorf("insert load stats: %w", err)
	}

	fmt.Printf("[OK] Stats inserted | tenant=%s file=%s rows=%d batch=%d/%d\n", tenantName, fileName, numRows, batchID, batchTotal)
	return nil
}

func insertSQLLoaderAudit(ctx context.Context, db *sql.DB, cfg appConfig, tenantName, fileID, fileName string, result sqlLoaderResult) error {
	table, ok := cfg.optionalTable("SQLLOADER_AUDIT", "SQL_LOADER_AUDIT")
	if !ok {
		table = cfg.defaultTable("SQLLOADER_AUDIT")
	}

	sqlText := fmt.Sprintf(`
		INSERT INTO %s
		(
			SOURCE_TENANT_NAME,
			FILE_ID,
			FILE_NAME,
			DATA_FILE,
			LOG_FILE,
			STATUS,
			ROWS_INSERTED,
			ROWS_FAILED,
			ROWS_REJECTED,
			ROWS_DISCARDED,
			ROWS_SKIPPED,
			TOTAL_ROWS_READ,
			TOTAL_FILE_LINES,
			FILE_SIZE_BYTES,
			LOADER_START_TIME,
			LOADER_END_TIME,
			DURATION_SECONDS,
			ERROR_MESSAGE,
			INSERTED_AT
		) VALUES (
			:1,
			:2,
			:3,
			:4,
			:5,
			:6,
			:7,
			:8,
			:9,
			:10,
			:11,
			:12,
			:13,
			:14,
			:15,
			:16,
			:17,
			:18,
			SYSTIMESTAMP
		)`, table)

	_, err := db.ExecContext(ctx, sqlText,
		tenantName,
		fileID,
		fileName,
		result.DataFile,
		result.LogFile,
		result.Status,
		result.RowsInserted,
		result.RowsFailed,
		result.RowsRejected,
		result.RowsDiscarded,
		result.RowsSkipped,
		result.TotalRowsRead,
		result.TotalFileLines,
		result.FileSizeBytes,
		result.StartedAt,
		result.EndedAt,
		result.DurationSeconds,
		result.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("insert SQL*Loader audit: %w", err)
	}

	fmt.Printf("[OK] SQL*Loader audit inserted | file=%s status=%s inserted=%d failed=%d duration=%.2fs\n",
		fileName, result.Status, result.RowsInserted, result.RowsFailed, result.DurationSeconds)
	return nil
}

func printExecutionSummary(ctx context.Context, db *sql.DB, cfg appConfig, tenantName, programStart string) error {
	tableLoad, err := cfg.table("LOAD_STATUS")
	if err != nil {
		return err
	}

	fmt.Println("\nLoad status summary for this execution:")
	query := fmt.Sprintf(`
		SELECT
			SOURCE_TENANT_NAME,
			FILE_TYPE,
			FILE_ID,
			FILE_NAME,
			FILE_SIZE,
			TO_CHAR(FILE_DATE, 'YYYY-MM-DD HH24:MI:SS') AS FILE_DATE,
			NUM_ROWS,
			TO_CHAR(LOAD_START_TIME, 'YYYY-MM-DD HH24:MI:SS') AS LOAD_START_TIME,
			TO_CHAR(LOAD_END_TIME, 'YYYY-MM-DD HH24:MI:SS') AS LOAD_END_TIME,
			AGENT_VERSION,
			BATCH_ID,
			BATCH_TOTAL
		FROM %s
		WHERE SOURCE_TENANT_NAME = :1
		  AND LOAD_START_TIME >= TO_DATE(:2, 'YYYY-MM-DD HH24:MI:SS')
		ORDER BY LOAD_START_TIME, FILE_NAME`, tableLoad)

	rows, err := db.QueryContext(ctx, query, tenantName, programStart)
	if err != nil {
		return fmt.Errorf("query execution summary: %w", err)
	}
	defer rows.Close()

	type summaryRow struct {
		sourceTenantName string
		fileType         string
		fileID           string
		fileName         string
		fileSize         int64
		fileDate         string
		numRows          int
		loadStartTime    string
		loadEndTime      string
		agentVersion     string
		batchID          int
		batchTotal       int
	}

	var summaries []summaryRow
	totalRows := 0
	for rows.Next() {
		var r summaryRow
		if err := rows.Scan(
			&r.sourceTenantName,
			&r.fileType,
			&r.fileID,
			&r.fileName,
			&r.fileSize,
			&r.fileDate,
			&r.numRows,
			&r.loadStartTime,
			&r.loadEndTime,
			&r.agentVersion,
			&r.batchID,
			&r.batchTotal,
		); err != nil {
			return fmt.Errorf("scan execution summary: %w", err)
		}
		summaries = append(summaries, r)
		totalRows += r.numRows
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read execution summary rows: %w", err)
	}

	if len(summaries) == 0 {
		fmt.Println("  No load status rows found for this execution.")
		return nil
	}

	for _, r := range summaries {
		fmt.Printf("  FILE=%s | ROWS=%d | SIZE=%d MB | START=%s | END=%s | BATCH=%d/%d\n",
			r.fileName, r.numRows, r.fileSize, r.loadStartTime, r.loadEndTime, r.batchID, r.batchTotal)
	}
	fmt.Printf("\n  Total loaded files in this run: %d\n", len(summaries))
	fmt.Printf("  Total loaded rows in this run  : %d\n", totalRows)
	return nil
}
