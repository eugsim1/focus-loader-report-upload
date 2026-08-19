package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinopsAnalyticsAPIUsesFixedTableAndFirstAlias(t *testing.T) {
	tnsAdmin := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tnsAdmin, tnsNamesFileName),
		[]byte("FIRST_SERVICE = (DESCRIPTION = (ADDRESS = TCP))\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	reader := func(
		_ context.Context,
		username string,
		password string,
		alias string,
		schema string,
		refreshOML bool,
		outlierRate float64,
	) (finopsAnalyticsResponse, error) {
		if username != "ADMIN" || password != "test-secret" ||
			alias != "FIRST_SERVICE" || schema != "FOCUS_APP" ||
			!refreshOML || outlierRate != 0.075 {
			t.Fatalf(
				"unexpected FinOps input: %s/%s@%s schema=%s refresh=%t rate=%f",
				username, password, alias, schema, refreshOML, outlierRate,
			)
		}
		return finopsAnalyticsResponse{
			TableExists:     true,
			LastLoadedDate:  "2026-08-11T18:30:00",
			LastChargeDate:  "2026-08-10",
			YearToDateStart: "2026-01-01",
			MonthlyCosts: []loaderMonthlyCost{
				{Month: "2026-08", BillingCurrency: "EUR", EffectiveCost: "42.75"},
			},
			YearToDateCosts: []finopsYTDCost{
				{BillingCurrency: "EUR", EffectiveCost: "142.75"},
			},
			OML: finopsOMLStatus{
				Installed: true,
				Refreshed: true,
				Status:    "SUCCEEDED",
			},
		}, nil
	}
	body := bytes.NewBufferString(`{
  "username":"ADMIN",
  "password":"test-secret",
  "schema":"focus_app",
  "refreshOml":true,
  "confirmOmlRefresh":true,
  "outlierRate":0.075
}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/finops", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handleFinopsAnalytics(response, request, func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, reader)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload finopsAnalyticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.TableExists || payload.LastLoadedDate != "2026-08-11T18:30:00" ||
		payload.YearToDateStart != "2026-01-01" || len(payload.MonthlyCosts) != 1 ||
		len(payload.YearToDateCosts) != 1 || !payload.OML.Installed ||
		payload.TableName != loaderTargetTableName || payload.QueriedAtUTC == "" {
		t.Fatalf("unexpected FinOps response: %#v", payload)
	}
	if strings.Contains(response.Body.String(), "test-secret") {
		t.Fatal("FinOps response contains the database password")
	}
}

func TestFinopsAnalyticsAPIRequiresExplicitOMLConfirmation(t *testing.T) {
	body := bytes.NewBufferString(`{
  "username":"ADMIN",
  "password":"test-secret",
  "schema":"FOCUS_APP",
  "refreshOml":true,
  "outlierRate":0.05
}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/finops", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	called := false
	handleFinopsAnalytics(response, request, func(string) string { return "" }, func(
		context.Context, string, string, string, string, bool, float64,
	) (finopsAnalyticsResponse, error) {
		called = true
		return finopsAnalyticsResponse{}, nil
	})

	if response.Code != http.StatusBadRequest || called ||
		!strings.Contains(response.Body.String(), "explicit confirmation") {
		t.Fatalf("status=%d called=%t body=%s", response.Code, called, response.Body.String())
	}
}

func TestFinopsAnalyticsAPIRedactsDatabasePassword(t *testing.T) {
	tnsAdmin := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tnsAdmin, tnsNamesFileName),
		[]byte("FIRST_SERVICE = (DESCRIPTION = (ADDRESS = TCP))\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{
  "username":"ADMIN",
  "password":"do-not-return",
  "schema":"FOCUS_APP",
  "outlierRate":0.05
}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/finops", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handleFinopsAnalytics(response, request, func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, func(
		context.Context, string, string, string, string, bool, float64,
	) (finopsAnalyticsResponse, error) {
		return finopsAnalyticsResponse{}, errors.New("query failed with do-not-return")
	})

	if response.Code != http.StatusBadGateway ||
		strings.Contains(response.Body.String(), "do-not-return") ||
		!strings.Contains(response.Body.String(), "[REDACTED]") {
		t.Fatalf("unsafe FinOps error response: status=%d body=%s", response.Code, response.Body.String())
	}
}
