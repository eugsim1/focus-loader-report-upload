package main

import (
	"context"
	"fmt"

	"github.com/godror/godror"
)

type loaderMonthlyCost struct {
	Month           string `json:"month"`
	BillingCurrency string `json:"billingCurrency"`
	EffectiveCost   string `json:"effectiveCost"`
}

type loaderAnalyticsSnapshot struct {
	MonthlyCosts []loaderMonthlyCost
	Services     []string
}

type loaderAnalyticsReader func(
	context.Context,
	string,
	string,
	string,
) (loaderAnalyticsSnapshot, error)

func readOracleTempFocusAnalytics(
	ctx context.Context,
	username string,
	password string,
	connectAlias string,
) (loaderAnalyticsSnapshot, error) {
	cmd := commandLine{dbUser: username, dbName: connectAlias}
	db, err := openOracleDB(cmd, password)
	if err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("open Oracle connection for cost analytics: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("connect for %s cost analytics: %w", loaderTargetTableName, err)
	}

	monthlyRows, err := db.QueryContext(ctx, `
SELECT
  EXTRACT(YEAR FROM TRUNC(CHARGE_PERIOD_START, 'MM')) AS COST_YEAR,
  EXTRACT(MONTH FROM TRUNC(CHARGE_PERIOD_START, 'MM')) AS COST_MONTH,
  NVL(TRIM(BILLING_CURRENCY), '(not provided)') AS BILLING_CURRENCY,
  SUM(NVL(EFFECTIVE_COST, 0)) AS EFFECTIVE_COST
FROM TEMP_OCI_FOCUS
WHERE CHARGE_PERIOD_START IS NOT NULL
GROUP BY TRUNC(CHARGE_PERIOD_START, 'MM'), NVL(TRIM(BILLING_CURRENCY), '(not provided)')
ORDER BY TRUNC(CHARGE_PERIOD_START, 'MM'), NVL(TRIM(BILLING_CURRENCY), '(not provided)')`, godror.NumberAsString())
	if err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("summarize monthly effective cost: %w", err)
	}
	monthlyCosts := make([]loaderMonthlyCost, 0, 24)
	for monthlyRows.Next() {
		var row loaderMonthlyCost
		var year, month int
		if err := monthlyRows.Scan(&year, &month, &row.BillingCurrency, &row.EffectiveCost); err != nil {
			monthlyRows.Close()
			return loaderAnalyticsSnapshot{}, fmt.Errorf("read monthly effective cost: %w", err)
		}
		if month < 1 || month > 12 {
			monthlyRows.Close()
			return loaderAnalyticsSnapshot{}, fmt.Errorf("read monthly effective cost: invalid month %d", month)
		}
		row.Month = fmt.Sprintf("%04d-%02d", year, month)
		monthlyCosts = append(monthlyCosts, row)
	}
	if err := monthlyRows.Err(); err != nil {
		monthlyRows.Close()
		return loaderAnalyticsSnapshot{}, fmt.Errorf("read monthly effective cost: %w", err)
	}
	if err := monthlyRows.Close(); err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("close monthly effective cost rows: %w", err)
	}

	serviceRows, err := db.QueryContext(ctx, `
SELECT DISTINCT TRIM(SERVICE_NAME) AS SERVICE_NAME
FROM TEMP_OCI_FOCUS
WHERE TRIM(SERVICE_NAME) IS NOT NULL
ORDER BY SERVICE_NAME`)
	if err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("list unique services: %w", err)
	}
	defer serviceRows.Close()
	services := make([]string, 0, 128)
	for serviceRows.Next() {
		var service string
		if err := serviceRows.Scan(&service); err != nil {
			return loaderAnalyticsSnapshot{}, fmt.Errorf("read unique service: %w", err)
		}
		services = append(services, service)
	}
	if err := serviceRows.Err(); err != nil {
		return loaderAnalyticsSnapshot{}, fmt.Errorf("read unique services: %w", err)
	}
	return loaderAnalyticsSnapshot{MonthlyCosts: monthlyCosts, Services: services}, nil
}
