package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	maxDatabaseRequestBytes = 16 * 1024
	databaseRequestTimeout  = 30 * time.Second
)

var unquotedOracleIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,127}$`)

type databaseTablesRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Schema   string `json:"schema"`
}

type databaseTableInfo struct {
	Owner     string `json:"owner"`
	TableName string `json:"tableName"`
}

type databaseTablesResponse struct {
	ConnectAlias string              `json:"connectAlias"`
	Username     string              `json:"username"`
	Schema       string              `json:"schema"`
	TableCount   int                 `json:"tableCount"`
	Tables       []databaseTableInfo `json:"tables"`
	Error        string              `json:"error,omitempty"`
}

type databaseTableLister func(
	context.Context,
	string,
	string,
	string,
	string,
) ([]databaseTableInfo, error)

func handleDatabaseTables(
	w http.ResponseWriter,
	r *http.Request,
	getenv func(string) string,
	lister databaseTableLister,
) {
	setTNSGUIJSONHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: "method not allowed"})
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: "Content-Type must be application/json"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDatabaseRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request databaseTablesRequest
	if err := decoder.Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: "request body must be one valid JSON object"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: "request body must contain only one JSON object"})
		return
	}

	request.Username = strings.TrimSpace(request.Username)
	request.Schema = strings.ToUpper(strings.TrimSpace(request.Schema))
	if request.Schema == "" {
		request.Schema = strings.ToUpper(request.Username)
	}
	if !unquotedOracleIdentifier.MatchString(request.Username) {
		writeDatabaseTablesValidationError(w, "database user must be an unquoted Oracle identifier")
		return
	}
	if request.Password == "" {
		writeDatabaseTablesValidationError(w, "database password is required")
		return
	}
	if len(request.Password) > 4096 || strings.ContainsRune(request.Password, '\x00') {
		writeDatabaseTablesValidationError(w, "database password is invalid")
		return
	}
	if !unquotedOracleIdentifier.MatchString(request.Schema) {
		writeDatabaseTablesValidationError(w, "schema owner must be an unquoted Oracle identifier")
		return
	}

	aliases, err := tnsAliasesFromEnvironment(getenv)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), databaseRequestTimeout)
	defer cancel()
	tables, err := lister(
		ctx,
		request.Username,
		request.Password,
		aliases.FirstAlias,
		request.Schema,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(databaseTablesResponse{
			ConnectAlias: aliases.FirstAlias,
			Username:     request.Username,
			Schema:       request.Schema,
			Tables:       []databaseTableInfo{},
			Error:        redactDatabasePassword(err.Error(), request.Password),
		})
		return
	}
	if tables == nil {
		tables = []databaseTableInfo{}
	}
	_ = json.NewEncoder(w).Encode(databaseTablesResponse{
		ConnectAlias: aliases.FirstAlias,
		Username:     request.Username,
		Schema:       request.Schema,
		TableCount:   len(tables),
		Tables:       tables,
	})
}

func writeDatabaseTablesValidationError(w http.ResponseWriter, message string) {
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(databaseTablesResponse{Error: message})
}

func redactDatabasePassword(message, password string) string {
	if password == "" {
		return message
	}
	return strings.ReplaceAll(message, password, "[REDACTED]")
}

func listOracleSchemaTables(
	ctx context.Context,
	username string,
	password string,
	connectAlias string,
	schema string,
) ([]databaseTableInfo, error) {
	cmd := commandLine{dbUser: username, dbName: connectAlias}
	db, err := openOracleDB(cmd, password)
	if err != nil {
		return nil, fmt.Errorf("open Oracle connection: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", connectAlias, err)
	}

	rows, err := db.QueryContext(ctx, `
SELECT OWNER, TABLE_NAME
FROM ALL_TABLES
WHERE OWNER = :1
ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, fmt.Errorf("list tables for schema %s: %w", schema, err)
	}
	defer rows.Close()

	tables := make([]databaseTableInfo, 0, 32)
	for rows.Next() {
		var table databaseTableInfo
		if err := rows.Scan(&table.Owner, &table.TableName); err != nil {
			return nil, fmt.Errorf("read table metadata: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read table metadata: %w", err)
	}
	return tables, nil
}
