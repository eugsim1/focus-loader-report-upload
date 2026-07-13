package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"os"
	"path/filepath"
	"strings"
)

func getMaxLoadedFile(ctx context.Context, db *sql.DB, cfg appConfig, tenantName string) (string, error) {
	table, err := cfg.table("LOAD_STATUS")
	if err != nil {
		return "", err
	}

	var maxFile sql.NullString
	query := fmt.Sprintf("select nvl(max(file_name),'0') as max_file_name from %s a where Source_Tenant_Name=:1", table)
	if err := db.QueryRowContext(ctx, query, tenantName).Scan(&maxFile); err != nil {
		return "", fmt.Errorf("query max loaded file: %w", err)
	}

	if maxFile.Valid {
		return maxFile.String, nil
	}
	return "0", nil
}

func getLoadedFiles(ctx context.Context, db *sql.DB, cfg appConfig, tenantName string) (map[string]struct{}, error) {
	table, err := cfg.table("LOAD_STATUS")
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("select distinct file_name from %s where Source_Tenant_Name=:1 and FILE_TYPE='FOCUS'", table)
	rows, err := db.QueryContext(ctx, query, tenantName)
	if err != nil {
		return nil, fmt.Errorf("query loaded files: %w", err)
	}
	defer rows.Close()

	loaded := map[string]struct{}{}
	for rows.Next() {
		var fileName sql.NullString
		if err := rows.Scan(&fileName); err != nil {
			return nil, fmt.Errorf("scan loaded file: %w", err)
		}
		if fileName.Valid && fileName.String != "" {
			loaded[fileName.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read loaded files: %w", err)
	}
	return loaded, nil
}

func newProcessedStore(path, tenantName string) (*processedStore, error) {
	store := &processedStore{
		path:       path,
		tenantName: tenantName,
		seen:       map[string]processedRecord{},
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open state file %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record processedRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			fmt.Printf("WARN: ignoring invalid state line in %s: %v\n", path, err)
			continue
		}
		if record.TenantName == tenantName && record.FileName != "" {
			store.seen[record.FileName] = record
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	return store, nil
}

func (s *processedStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

func (s *processedStore) ProcessedFiles() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]struct{}, len(s.seen))
	for fileName := range s.seen {
		out[fileName] = struct{}{}
	}
	return out
}

func (s *processedStore) Mark(result loadResult) error {
	if !result.Loaded || result.FileName == "" {
		return nil
	}
	return s.markResult(result)
}

func (s *processedStore) MarkUploaded(result loadResult) error {
	if result.FileName == "" {
		return nil
	}
	return s.markResult(result)
}

func (s *processedStore) markResult(result loadResult) error {

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[result.FileName]; ok {
		return nil
	}

	record := processedRecord{
		TenantName:  s.tenantName,
		FileName:    result.FileName,
		FileID:      result.FileID,
		Rows:        result.NumRows,
		FileSizeMB:  result.FileSizeMB,
		ProcessedAt: currentDateTime(),
		Version:     version,
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open state file for append: %w", err)
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(record); err != nil {
		file.Close()
		return fmt.Errorf("append processed state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync processed state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close processed state: %w", err)
	}

	s.seen[result.FileName] = record
	return nil
}

func mergeProcessedFiles(sets ...map[string]struct{}) map[string]struct{} {
	merged := map[string]struct{}{}
	for _, set := range sets {
		for fileName := range set {
			merged[fileName] = struct{}{}
		}
	}
	return merged
}

func filterPendingObjects(objects []objectstorage.ObjectSummary, cmd commandLine, processed map[string]struct{}) []objectstorage.ObjectSummary {
	pending := make([]objectstorage.ObjectSummary, 0, len(objects))
	for _, objectFile := range objects {
		objectName := value(objectFile.Name)
		if objectName == "" {
			continue
		}
		if _, ok := processed[objectName]; ok {
			continue
		}
		if cmd.fileNameFull != "" && objectName != cmd.fileNameFull {
			continue
		}
		if cmd.fileDate != "" && objectFileDate(objectName) < cmd.fileDate {
			continue
		}
		pending = append(pending, objectFile)
	}
	return pending
}
