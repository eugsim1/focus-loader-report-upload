package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func insertTagRows(ctx context.Context, db *sql.DB, tableName string, rows []tagRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tag insert transaction: %w", err)
	}
	defer tx.Rollback()

	sqlText := fmt.Sprintf(`
		INSERT INTO %s
		(SOURCE_TENANT_NAME, RESOURCE_ID, CHARGE_PERIOD_START, TAG_KEY, TAG_VALUE)
		VALUES (:1,:2,:3,:4,:5)`, tableName)

	stmt, err := tx.PrepareContext(ctx, sqlText)
	if err != nil {
		return fmt.Errorf("prepare tag insert: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, row.TenantName, row.ResourceID, row.ChargePeriodStart, row.TagKey, row.TagValue); err != nil {
			return fmt.Errorf("insert tag row: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tag rows: %w", err)
	}
	return nil
}

func insertTagKeyValues(ctx context.Context, db *sql.DB, cfg appConfig, values map[tagKeyValue]struct{}) error {
	tagKeyMetadataMu.Lock()
	defer tagKeyMetadataMu.Unlock()

	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := insertTagKeyValuesOnce(ctx, db, cfg, values)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isOracleDeadlock(err) || attempt == maxAttempts {
			return err
		}

		delay := time.Duration(attempt*2) * time.Second
		fmt.Printf("   WARN: tag key/value metadata deadlock, retrying attempt %d/%d after %s\n", attempt+1, maxAttempts, delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

func insertTagKeyValuesOnce(ctx context.Context, db *sql.DB, cfg appConfig, values map[tagKeyValue]struct{}) error {
	tableTag, err := cfg.table("TAG_KEYS")
	if err != nil {
		return err
	}
	tableTagRef := tableTag
	if configuredRef, ok := cfg.optionalTable("TAG_KEYS_REF"); ok {
		tableTagRef = configuredRef
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tag key transaction: %w", err)
	}
	defer tx.Rollback()

	sqlText := fmt.Sprintf(`
		DECLARE
			duplicate_key EXCEPTION;
			PRAGMA EXCEPTION_INIT(duplicate_key, -1);
		BEGIN
			INSERT INTO %s (SOURCE_TENANT_NAME, TAG_KEY, TAG_VALUE)
			SELECT :1, :2, :3 FROM DUAL
			WHERE NOT EXISTS (
				SELECT 1 FROM %s
				WHERE SOURCE_TENANT_NAME = :4
				  AND TAG_KEY = :5
				  AND TAG_VALUE = :6
			);
		EXCEPTION
			WHEN duplicate_key THEN
				NULL;
		END;`, tableTag, tableTagRef)

	stmt, err := tx.PrepareContext(ctx, sqlText)
	if err != nil {
		return fmt.Errorf("prepare tag key insert: %w", err)
	}
	defer stmt.Close()

	for item := range values {
		if _, err := stmt.ExecContext(ctx,
			item.TenantName,
			item.TagKey,
			item.TagValue,
			item.TenantName,
			item.TagKey,
			item.TagValue,
		); err != nil {
			return fmt.Errorf("insert tag key value: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tag key values: %w", err)
	}
	fmt.Printf("   Tag key/value metadata checked: unique_values=%d table=%s duplicate_check=%s duplicate_races=ignored deadlock_retries=enabled\n", len(values), tableTag, tableTagRef)
	return nil
}

func isOracleDeadlock(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ORA-00060")
}
