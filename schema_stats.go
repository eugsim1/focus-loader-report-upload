package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/godror/godror"
)

type schemaStatsResponse struct {
	ConnectAlias      string              `json:"connectAlias"`
	Username          string              `json:"username"`
	Schema            string              `json:"schema"`
	TableName         string              `json:"tableName"`
	TableExists       bool                `json:"tableExists"`
	HasData           bool                `json:"hasData"`
	TotalRows         int64               `json:"totalRows"`
	LastLoadDate      string              `json:"lastLoadDate"`
	CurrentMonth      string              `json:"currentMonth"`
	CurrentMonthCosts []loaderMonthlyCost `json:"currentMonthCosts"`
	QueriedAtUTC      string              `json:"queriedAtUtc"`
	Error             string              `json:"error,omitempty"`
}

type schemaStatsSnapshot struct {
	TableExists       bool
	TotalRows         int64
	LastLoadDate      string
	CurrentMonth      string
	CurrentMonthCosts []loaderMonthlyCost
}

type schemaStatsReader func(
	context.Context,
	string,
	string,
	string,
	string,
) (schemaStatsSnapshot, error)

func handleSchemaStats(
	w http.ResponseWriter,
	r *http.Request,
	getenv func(string) string,
	reader schemaStatsReader,
) {
	setTNSGUIJSONHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeSchemaStatsError(w, http.StatusMethodNotAllowed, schemaStatsResponse{}, "method not allowed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeSchemaStatsError(w, http.StatusUnsupportedMediaType, schemaStatsResponse{}, "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDatabaseRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request databaseTablesRequest
	if err := decoder.Decode(&request); err != nil {
		writeSchemaStatsError(w, http.StatusBadRequest, schemaStatsResponse{}, "request body must be one valid JSON object")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeSchemaStatsError(w, http.StatusBadRequest, schemaStatsResponse{}, "request body must contain only one JSON object")
		return
	}

	request.Username = strings.TrimSpace(request.Username)
	request.Schema = strings.ToUpper(strings.TrimSpace(request.Schema))
	if request.Schema == "" {
		request.Schema = strings.ToUpper(request.Username)
	}
	if !unquotedOracleIdentifier.MatchString(request.Username) {
		writeSchemaStatsError(w, http.StatusBadRequest, schemaStatsResponse{}, "database user must be an unquoted Oracle identifier")
		return
	}
	if !validDatabasePassword(request.Password) {
		writeSchemaStatsError(w, http.StatusBadRequest, schemaStatsResponse{}, "database password is required or invalid")
		return
	}
	if !unquotedOracleIdentifier.MatchString(request.Schema) {
		writeSchemaStatsError(w, http.StatusBadRequest, schemaStatsResponse{}, "schema owner must be an unquoted Oracle identifier")
		return
	}

	aliases, err := tnsAliasesFromEnvironment(getenv)
	if err != nil {
		writeSchemaStatsError(w, http.StatusServiceUnavailable, schemaStatsResponse{}, err.Error())
		return
	}
	response := schemaStatsResponse{
		ConnectAlias:      aliases.FirstAlias,
		Username:          request.Username,
		Schema:            request.Schema,
		TableName:         loaderTargetTableName,
		CurrentMonthCosts: []loaderMonthlyCost{},
		QueriedAtUTC:      time.Now().UTC().Format(time.RFC3339),
	}
	ctx, cancel := context.WithTimeout(r.Context(), databaseRequestTimeout)
	defer cancel()
	snapshot, err := reader(ctx, request.Username, request.Password, aliases.FirstAlias, request.Schema)
	if err != nil {
		writeSchemaStatsError(w, http.StatusBadGateway, response, redactDatabasePassword(err.Error(), request.Password))
		return
	}
	if snapshot.CurrentMonthCosts == nil {
		snapshot.CurrentMonthCosts = []loaderMonthlyCost{}
	}
	response.TableExists = snapshot.TableExists
	response.HasData = snapshot.TotalRows > 0
	response.TotalRows = snapshot.TotalRows
	response.LastLoadDate = snapshot.LastLoadDate
	response.CurrentMonth = snapshot.CurrentMonth
	response.CurrentMonthCosts = snapshot.CurrentMonthCosts
	_ = json.NewEncoder(w).Encode(response)
}

func writeSchemaStatsError(w http.ResponseWriter, status int, response schemaStatsResponse, message string) {
	response.Error = message
	if response.CurrentMonthCosts == nil {
		response.CurrentMonthCosts = []loaderMonthlyCost{}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func readOracleSchemaStats(
	ctx context.Context,
	username string,
	password string,
	connectAlias string,
	schema string,
) (schemaStatsSnapshot, error) {
	if !unquotedOracleIdentifier.MatchString(schema) {
		return schemaStatsSnapshot{}, fmt.Errorf("schema owner is invalid")
	}
	cmd := commandLine{dbUser: username, dbName: connectAlias}
	db, err := openOracleDB(cmd, password)
	if err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("open Oracle connection for schema statistics: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("connect to %s for schema statistics: %w", connectAlias, err)
	}

	var tableCount int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM ALL_TABLES
WHERE OWNER = :1 AND TABLE_NAME = :2`, schema, loaderTargetTableName).Scan(&tableCount); err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("check %s.%s: %w", schema, loaderTargetTableName, err)
	}
	if tableCount == 0 {
		return schemaStatsSnapshot{
			CurrentMonthCosts: []loaderMonthlyCost{},
		}, nil
	}

	qualifiedTable := schema + "." + loaderTargetTableName
	snapshot := schemaStatsSnapshot{TableExists: true, CurrentMonthCosts: []loaderMonthlyCost{}}
	var lastLoadDate sql.NullString
	rowSQL := fmt.Sprintf(`
SELECT
  COUNT(*),
  TO_CHAR(MAX(LOAD_DATE), 'YYYY-MM-DD"T"HH24:MI:SS'),
  TO_CHAR(TRUNC(SYSDATE, 'MM'), 'YYYY-MM')
FROM %s`, qualifiedTable)
	if err := db.QueryRowContext(ctx, rowSQL).Scan(
		&snapshot.TotalRows,
		&lastLoadDate,
		&snapshot.CurrentMonth,
	); err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("read %s statistics: %w", qualifiedTable, err)
	}
	if lastLoadDate.Valid {
		snapshot.LastLoadDate = lastLoadDate.String
	}
	if snapshot.TotalRows == 0 {
		return snapshot, nil
	}

	costSQL := fmt.Sprintf(`
SELECT
  NVL(TRIM(BILLING_CURRENCY), '(not provided)') AS BILLING_CURRENCY,
  SUM(NVL(EFFECTIVE_COST, 0)) AS EFFECTIVE_COST
FROM %s
WHERE CHARGE_PERIOD_START >= TRUNC(SYSDATE, 'MM')
  AND CHARGE_PERIOD_START < ADD_MONTHS(TRUNC(SYSDATE, 'MM'), 1)
GROUP BY NVL(TRIM(BILLING_CURRENCY), '(not provided)')
ORDER BY NVL(TRIM(BILLING_CURRENCY), '(not provided)')`, qualifiedTable)
	rows, err := db.QueryContext(ctx, costSQL, godror.NumberAsString())
	if err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("read %s current-month cost: %w", qualifiedTable, err)
	}
	defer rows.Close()
	for rows.Next() {
		cost := loaderMonthlyCost{Month: snapshot.CurrentMonth}
		if err := rows.Scan(&cost.BillingCurrency, &cost.EffectiveCost); err != nil {
			return schemaStatsSnapshot{}, fmt.Errorf("read %s current-month cost row: %w", qualifiedTable, err)
		}
		snapshot.CurrentMonthCosts = append(snapshot.CurrentMonthCosts, cost)
	}
	if err := rows.Err(); err != nil {
		return schemaStatsSnapshot{}, fmt.Errorf("read %s current-month costs: %w", qualifiedTable, err)
	}
	return snapshot, nil
}
