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

const finopsAnalyticsTimeout = 5 * time.Minute

type finopsAnalyticsRequest struct {
	Username          string  `json:"username"`
	Password          string  `json:"password"`
	Schema            string  `json:"schema"`
	RefreshOML        bool    `json:"refreshOml"`
	ConfirmOMLRefresh bool    `json:"confirmOmlRefresh"`
	OutlierRate       float64 `json:"outlierRate"`
}

type finopsYTDCost struct {
	BillingCurrency string `json:"billingCurrency"`
	EffectiveCost   string `json:"effectiveCost"`
}

type finopsAnomaly struct {
	DimensionType      string `json:"dimensionType"`
	DimensionValue     string `json:"dimensionValue"`
	Month              string `json:"month"`
	BillingCurrency    string `json:"billingCurrency"`
	EffectiveCost      string `json:"effectiveCost"`
	AnomalyProbability string `json:"anomalyProbability"`
	Prediction         int    `json:"prediction"`
}

type finopsForecast struct {
	CreatedBy       string `json:"createdBy"`
	BillingCurrency string `json:"billingCurrency"`
	Month           string `json:"month"`
	HorizonMonths   int    `json:"horizonMonths"`
	Prediction      string `json:"prediction"`
	LowerBound      string `json:"lowerBound"`
	UpperBound      string `json:"upperBound"`
}

type finopsOMLStatus struct {
	Installed    bool     `json:"installed"`
	Refreshed    bool     `json:"refreshed"`
	LastRunAtUTC string   `json:"lastRunAtUtc"`
	Status       string   `json:"status"`
	Message      string   `json:"message"`
	Models       []string `json:"models"`
}

type finopsAnalyticsResponse struct {
	ConnectAlias          string              `json:"connectAlias"`
	Username              string              `json:"username"`
	Schema                string              `json:"schema"`
	TableName             string              `json:"tableName"`
	TableExists           bool                `json:"tableExists"`
	LastLoadedDate        string              `json:"lastLoadedDate"`
	LastChargeDate        string              `json:"lastChargeDate"`
	YearToDateStart       string              `json:"yearToDateStart"`
	MonthlyCosts          []loaderMonthlyCost `json:"monthlyCosts"`
	YearToDateCosts       []finopsYTDCost     `json:"yearToDateCosts"`
	Anomalies             []finopsAnomaly     `json:"anomalies"`
	Forecasts             []finopsForecast    `json:"forecasts"`
	MostExpensiveUser     string              `json:"mostExpensiveUser"`
	MostExpensiveCost     string              `json:"mostExpensiveCost"`
	MostExpensiveCurrency string              `json:"mostExpensiveCurrency"`
	OML                   finopsOMLStatus     `json:"oml"`
	QueriedAtUTC          string              `json:"queriedAtUtc"`
	Error                 string              `json:"error,omitempty"`
}

type finopsAnalyticsReader func(
	context.Context,
	string,
	string,
	string,
	string,
	bool,
	float64,
) (finopsAnalyticsResponse, error)

func handleFinopsAnalytics(
	w http.ResponseWriter,
	r *http.Request,
	getenv func(string) string,
	reader finopsAnalyticsReader,
) {
	setTNSGUIJSONHeaders(w)
	empty := finopsAnalyticsResponse{
		TableName:       loaderTargetTableName,
		MonthlyCosts:    []loaderMonthlyCost{},
		YearToDateCosts: []finopsYTDCost{},
		Anomalies:       []finopsAnomaly{},
		Forecasts:       []finopsForecast{},
		OML:             finopsOMLStatus{Models: []string{}},
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFinopsAnalyticsError(w, http.StatusMethodNotAllowed, empty, "method not allowed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeFinopsAnalyticsError(w, http.StatusUnsupportedMediaType, empty, "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDatabaseRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request finopsAnalyticsRequest
	if err := decoder.Decode(&request); err != nil {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "request body must be one valid JSON object")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "request body must contain only one JSON object")
		return
	}

	request.Username = strings.TrimSpace(request.Username)
	request.Schema = strings.ToUpper(strings.TrimSpace(request.Schema))
	if request.Schema == "" {
		request.Schema = strings.ToUpper(request.Username)
	}
	if !unquotedOracleIdentifier.MatchString(request.Username) {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "database user must be an unquoted Oracle identifier")
		return
	}
	if !validDatabasePassword(request.Password) {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "database password is required or invalid")
		return
	}
	if !unquotedOracleIdentifier.MatchString(request.Schema) {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "schema owner must be an unquoted Oracle identifier")
		return
	}
	if request.OutlierRate == 0 {
		request.OutlierRate = 0.05
	}
	if request.OutlierRate < 0.001 || request.OutlierRate > 0.25 {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "OML outlier rate must be between 0.001 and 0.25")
		return
	}
	if request.RefreshOML && !request.ConfirmOMLRefresh {
		writeFinopsAnalyticsError(w, http.StatusBadRequest, empty, "OML refresh requires explicit confirmation")
		return
	}

	aliases, err := tnsAliasesFromEnvironment(getenv)
	if err != nil {
		writeFinopsAnalyticsError(w, http.StatusServiceUnavailable, empty, err.Error())
		return
	}
	empty.ConnectAlias = aliases.FirstAlias
	empty.Username = request.Username
	empty.Schema = request.Schema
	empty.QueriedAtUTC = time.Now().UTC().Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), finopsAnalyticsTimeout)
	defer cancel()
	response, err := reader(
		ctx,
		request.Username,
		request.Password,
		aliases.FirstAlias,
		request.Schema,
		request.RefreshOML,
		request.OutlierRate,
	)
	if err != nil {
		writeFinopsAnalyticsError(w, http.StatusBadGateway, empty, redactDatabasePassword(err.Error(), request.Password))
		return
	}
	response.ConnectAlias = aliases.FirstAlias
	response.Username = request.Username
	response.Schema = request.Schema
	response.TableName = loaderTargetTableName
	response.QueriedAtUTC = time.Now().UTC().Format(time.RFC3339)
	normalizeFinopsAnalyticsResponse(&response)
	_ = json.NewEncoder(w).Encode(response)
}

func normalizeFinopsAnalyticsResponse(response *finopsAnalyticsResponse) {
	if response.MonthlyCosts == nil {
		response.MonthlyCosts = []loaderMonthlyCost{}
	}
	if response.YearToDateCosts == nil {
		response.YearToDateCosts = []finopsYTDCost{}
	}
	if response.Anomalies == nil {
		response.Anomalies = []finopsAnomaly{}
	}
	if response.Forecasts == nil {
		response.Forecasts = []finopsForecast{}
	}
	if response.OML.Models == nil {
		response.OML.Models = []string{}
	}
}

func writeFinopsAnalyticsError(w http.ResponseWriter, status int, response finopsAnalyticsResponse, message string) {
	response.Error = message
	normalizeFinopsAnalyticsResponse(&response)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func readOracleFinopsAnalytics(
	ctx context.Context,
	username string,
	password string,
	connectAlias string,
	schema string,
	refreshOML bool,
	outlierRate float64,
) (finopsAnalyticsResponse, error) {
	if !unquotedOracleIdentifier.MatchString(schema) {
		return finopsAnalyticsResponse{}, fmt.Errorf("schema owner is invalid")
	}
	cmd := commandLine{dbUser: username, dbName: connectAlias}
	db, err := openOracleDB(cmd, password)
	if err != nil {
		return finopsAnalyticsResponse{}, fmt.Errorf("open Oracle connection for FinOps analytics: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		return finopsAnalyticsResponse{}, fmt.Errorf("connect to %s for FinOps analytics: %w", connectAlias, err)
	}

	response := finopsAnalyticsResponse{
		MonthlyCosts:    []loaderMonthlyCost{},
		YearToDateCosts: []finopsYTDCost{},
		Anomalies:       []finopsAnomaly{},
		Forecasts:       []finopsForecast{},
		OML:             finopsOMLStatus{Models: []string{}},
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM ALL_TABLES
WHERE OWNER = :1 AND TABLE_NAME = :2`, schema, loaderTargetTableName).Scan(&tableCount); err != nil {
		return finopsAnalyticsResponse{}, fmt.Errorf("check %s.%s: %w", schema, loaderTargetTableName, err)
	}
	if tableCount == 0 {
		return response, nil
	}
	response.TableExists = true
	qualifiedTable := schema + "." + loaderTargetTableName
	if err := readFinopsCostSummary(ctx, db, qualifiedTable, &response); err != nil {
		return finopsAnalyticsResponse{}, err
	}

	installed, err := finopsOMLInstalled(ctx, db, schema)
	if err != nil {
		return finopsAnalyticsResponse{}, err
	}
	response.OML.Installed = installed
	if !installed {
		response.OML.Status = "NOT_INSTALLED"
		response.OML.Message = "Install sql_scripts/install_finops_oml.sql in the target schema, then refresh the OML models."
		return response, nil
	}
	if refreshOML {
		statement := fmt.Sprintf("BEGIN %s.FOCUS_OML_ANALYTICS.RUN(:1); END;", schema)
		if _, err := db.ExecContext(ctx, statement, outlierRate); err != nil {
			return finopsAnalyticsResponse{}, fmt.Errorf("refresh %s OML models: %w", schema, err)
		}
		response.OML.Refreshed = true
	}
	if err := readFinopsOMLResults(ctx, db, schema, &response); err != nil {
		return finopsAnalyticsResponse{}, err
	}
	return response, nil
}

func readFinopsCostSummary(ctx context.Context, db *sql.DB, qualifiedTable string, response *finopsAnalyticsResponse) error {
	boundSQL := fmt.Sprintf(`
SELECT
  TO_CHAR(MAX(LOAD_DATE), 'YYYY-MM-DD"T"HH24:MI:SS'),
  TO_CHAR(MAX(CHARGE_PERIOD_START), 'YYYY-MM-DD'),
  TO_CHAR(TRUNC(COALESCE(MAX(LOAD_DATE), MAX(CHARGE_PERIOD_START)), 'YYYY'), 'YYYY-MM-DD')
FROM %s`, qualifiedTable)
	var loadedDate, chargeDate, ytdStart sql.NullString
	if err := db.QueryRowContext(ctx, boundSQL).Scan(&loadedDate, &chargeDate, &ytdStart); err != nil {
		return fmt.Errorf("read %s analytics bounds: %w", qualifiedTable, err)
	}
	if loadedDate.Valid {
		response.LastLoadedDate = loadedDate.String
	}
	if chargeDate.Valid {
		response.LastChargeDate = chargeDate.String
	}
	if ytdStart.Valid {
		response.YearToDateStart = ytdStart.String
	}

	monthlySQL := fmt.Sprintf(`
SELECT
  TO_CHAR(TRUNC(CHARGE_PERIOD_START, 'MM'), 'YYYY-MM') AS COST_MONTH,
  NVL(TRIM(BILLING_CURRENCY), '(not provided)') AS BILLING_CURRENCY,
  SUM(NVL(EFFECTIVE_COST, 0)) AS EFFECTIVE_COST
FROM %s
WHERE CHARGE_PERIOD_START IS NOT NULL
GROUP BY TRUNC(CHARGE_PERIOD_START, 'MM'), NVL(TRIM(BILLING_CURRENCY), '(not provided)')
ORDER BY TRUNC(CHARGE_PERIOD_START, 'MM'), NVL(TRIM(BILLING_CURRENCY), '(not provided)')`, qualifiedTable)
	rows, err := db.QueryContext(ctx, monthlySQL, godror.NumberAsString())
	if err != nil {
		return fmt.Errorf("summarize %s monthly cost: %w", qualifiedTable, err)
	}
	for rows.Next() {
		var row loaderMonthlyCost
		if err := rows.Scan(&row.Month, &row.BillingCurrency, &row.EffectiveCost); err != nil {
			rows.Close()
			return fmt.Errorf("read %s monthly cost: %w", qualifiedTable, err)
		}
		response.MonthlyCosts = append(response.MonthlyCosts, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read %s monthly costs: %w", qualifiedTable, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s monthly costs: %w", qualifiedTable, err)
	}

	ytdSQL := fmt.Sprintf(`
WITH BOUNDS AS (
  SELECT TRUNC(COALESCE(MAX(LOAD_DATE), MAX(CHARGE_PERIOD_START))) AS AS_OF_DATE
  FROM %s
)
SELECT
  NVL(TRIM(F.BILLING_CURRENCY), '(not provided)') AS BILLING_CURRENCY,
  SUM(NVL(F.EFFECTIVE_COST, 0)) AS EFFECTIVE_COST
FROM %s F CROSS JOIN BOUNDS B
WHERE B.AS_OF_DATE IS NOT NULL
  AND F.CHARGE_PERIOD_START >= TRUNC(B.AS_OF_DATE, 'YYYY')
  AND F.CHARGE_PERIOD_START < B.AS_OF_DATE + 1
GROUP BY NVL(TRIM(F.BILLING_CURRENCY), '(not provided)')
ORDER BY NVL(TRIM(F.BILLING_CURRENCY), '(not provided)')`, qualifiedTable, qualifiedTable)
	ytdRows, err := db.QueryContext(ctx, ytdSQL, godror.NumberAsString())
	if err != nil {
		return fmt.Errorf("summarize %s year-to-date cost: %w", qualifiedTable, err)
	}
	defer ytdRows.Close()
	for ytdRows.Next() {
		var row finopsYTDCost
		if err := ytdRows.Scan(&row.BillingCurrency, &row.EffectiveCost); err != nil {
			return fmt.Errorf("read %s year-to-date cost: %w", qualifiedTable, err)
		}
		response.YearToDateCosts = append(response.YearToDateCosts, row)
	}
	if err := ytdRows.Err(); err != nil {
		return fmt.Errorf("read %s year-to-date costs: %w", qualifiedTable, err)
	}
	return nil
}

func finopsOMLInstalled(ctx context.Context, db *sql.DB, schema string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM ALL_PROCEDURES
WHERE OWNER = :1
  AND OBJECT_NAME = 'FOCUS_OML_ANALYTICS'
  AND PROCEDURE_NAME = 'RUN'`, schema).Scan(&count); err != nil {
		return false, fmt.Errorf("check %s OML package: %w", schema, err)
	}
	return count > 0, nil
}

func readFinopsOMLResults(ctx context.Context, db *sql.DB, schema string, response *finopsAnalyticsResponse) error {
	runSQL := fmt.Sprintf(`
SELECT TO_CHAR(RUN_AT_UTC, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), STATUS, MESSAGE
FROM %s.FOCUS_OML_RUNS
ORDER BY RUN_ID DESC
FETCH FIRST 1 ROW ONLY`, schema)
	var runAt, status, message sql.NullString
	if err := db.QueryRowContext(ctx, runSQL).Scan(&runAt, &status, &message); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read %s OML run status: %w", schema, err)
	}
	if runAt.Valid {
		response.OML.LastRunAtUTC = runAt.String
	}
	if status.Valid {
		response.OML.Status = status.String
	}
	if message.Valid {
		response.OML.Message = message.String
	}

	modelRows, err := db.QueryContext(ctx, `
SELECT MODEL_NAME
FROM ALL_MINING_MODELS
WHERE OWNER = :1
  AND MODEL_NAME LIKE 'FOCUS_OML_%'
ORDER BY MODEL_NAME`, schema)
	if err != nil {
		return fmt.Errorf("list %s OML models: %w", schema, err)
	}
	for modelRows.Next() {
		var name string
		if err := modelRows.Scan(&name); err != nil {
			modelRows.Close()
			return fmt.Errorf("read %s OML model: %w", schema, err)
		}
		response.OML.Models = append(response.OML.Models, name)
	}
	if err := modelRows.Err(); err != nil {
		modelRows.Close()
		return fmt.Errorf("read %s OML models: %w", schema, err)
	}
	if err := modelRows.Close(); err != nil {
		return fmt.Errorf("close %s OML models: %w", schema, err)
	}

	anomalySQL := fmt.Sprintf(`
SELECT DIMENSION_TYPE, DIMENSION_VALUE,
       TO_CHAR(COST_MONTH, 'YYYY-MM'), BILLING_CURRENCY,
       EFFECTIVE_COST, ANOMALY_PROBABILITY, PREDICTION
FROM %s.FOCUS_OML_ANOMALIES
ORDER BY ANOMALY_PROBABILITY DESC, COST_MONTH DESC
FETCH FIRST 500 ROWS ONLY`, schema)
	anomalyRows, err := db.QueryContext(ctx, anomalySQL, godror.NumberAsString())
	if err != nil {
		return fmt.Errorf("read %s OML anomalies: %w", schema, err)
	}
	for anomalyRows.Next() {
		var row finopsAnomaly
		if err := anomalyRows.Scan(
			&row.DimensionType,
			&row.DimensionValue,
			&row.Month,
			&row.BillingCurrency,
			&row.EffectiveCost,
			&row.AnomalyProbability,
			&row.Prediction,
		); err != nil {
			anomalyRows.Close()
			return fmt.Errorf("read %s OML anomaly row: %w", schema, err)
		}
		response.Anomalies = append(response.Anomalies, row)
	}
	if err := anomalyRows.Err(); err != nil {
		anomalyRows.Close()
		return fmt.Errorf("read %s OML anomalies: %w", schema, err)
	}
	if err := anomalyRows.Close(); err != nil {
		return fmt.Errorf("close %s OML anomalies: %w", schema, err)
	}

	forecastSQL := fmt.Sprintf(`
SELECT CREATED_BY, BILLING_CURRENCY, TO_CHAR(FORECAST_MONTH, 'YYYY-MM'),
       HORIZON_MONTHS, PREDICTION, LOWER_BOUND, UPPER_BOUND
FROM %s.FOCUS_OML_FORECAST
ORDER BY HORIZON_MONTHS`, schema)
	forecastRows, err := db.QueryContext(ctx, forecastSQL, godror.NumberAsString())
	if err != nil {
		return fmt.Errorf("read %s OML forecast: %w", schema, err)
	}
	for forecastRows.Next() {
		var row finopsForecast
		if err := forecastRows.Scan(
			&row.CreatedBy,
			&row.BillingCurrency,
			&row.Month,
			&row.HorizonMonths,
			&row.Prediction,
			&row.LowerBound,
			&row.UpperBound,
		); err != nil {
			forecastRows.Close()
			return fmt.Errorf("read %s OML forecast row: %w", schema, err)
		}
		response.Forecasts = append(response.Forecasts, row)
	}
	if err := forecastRows.Err(); err != nil {
		forecastRows.Close()
		return fmt.Errorf("read %s OML forecasts: %w", schema, err)
	}
	if err := forecastRows.Close(); err != nil {
		return fmt.Errorf("close %s OML forecasts: %w", schema, err)
	}
	if len(response.Forecasts) > 0 {
		response.MostExpensiveUser = response.Forecasts[0].CreatedBy
		response.MostExpensiveCurrency = response.Forecasts[0].BillingCurrency
	}
	topCostSQL := fmt.Sprintf(`
SELECT CREATED_BY, BILLING_CURRENCY,
       TO_CHAR(YTD_COST, 'TM9', 'NLS_NUMERIC_CHARACTERS=''.,''')
FROM %s.FOCUS_OML_TOP_USER
FETCH FIRST 1 ROW ONLY`, schema)
	var topUser, topCurrency, topCost sql.NullString
	if err := db.QueryRowContext(ctx, topCostSQL).Scan(&topUser, &topCurrency, &topCost); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read %s most expensive user: %w", schema, err)
	}
	if topUser.Valid {
		response.MostExpensiveUser = topUser.String
	}
	if topCurrency.Valid {
		response.MostExpensiveCurrency = topCurrency.String
	}
	if topCost.Valid {
		response.MostExpensiveCost = topCost.String
	}
	return nil
}
