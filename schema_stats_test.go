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

func TestSchemaStatsAPIUsesFixedTableAndFirstAlias(t *testing.T) {
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
	) (schemaStatsSnapshot, error) {
		if username != "ADMIN" || password != "test-secret" ||
			alias != "FIRST_SERVICE" || schema != "FOCUS_APP" {
			t.Fatalf("unexpected schema statistics input: %s/%s@%s schema=%s", username, password, alias, schema)
		}
		return schemaStatsSnapshot{
			TableExists:  true,
			TotalRows:    125,
			LastLoadDate: "2026-08-11T18:30:00",
			CurrentMonth: "2026-08",
			CurrentMonthCosts: []loaderMonthlyCost{
				{Month: "2026-08", BillingCurrency: "EUR", EffectiveCost: "42.75"},
			},
		}, nil
	}
	body := bytes.NewBufferString(`{"username":"ADMIN","password":"test-secret","schema":"focus_app"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schema/stats", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handleSchemaStats(response, request, func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, reader)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload schemaStatsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.TableExists || !payload.HasData || payload.TotalRows != 125 ||
		payload.LastLoadDate != "2026-08-11T18:30:00" || payload.CurrentMonth != "2026-08" ||
		len(payload.CurrentMonthCosts) != 1 || payload.CurrentMonthCosts[0].EffectiveCost != "42.75" ||
		payload.TableName != loaderTargetTableName || payload.QueriedAtUTC == "" {
		t.Fatalf("unexpected schema statistics response: %#v", payload)
	}
	if strings.Contains(response.Body.String(), "test-secret") {
		t.Fatal("schema statistics response contains the database password")
	}
}

func TestSchemaStatsAPIRedactsDatabasePassword(t *testing.T) {
	tnsAdmin := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tnsAdmin, tnsNamesFileName),
		[]byte("FIRST_SERVICE = (DESCRIPTION = (ADDRESS = TCP))\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	reader := func(
		context.Context,
		string,
		string,
		string,
		string,
	) (schemaStatsSnapshot, error) {
		return schemaStatsSnapshot{}, errors.New("query failed with do-not-return")
	}
	body := bytes.NewBufferString(`{"username":"ADMIN","password":"do-not-return","schema":"FOCUS_APP"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schema/stats", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handleSchemaStats(response, request, func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, reader)

	if response.Code != http.StatusBadGateway ||
		strings.Contains(response.Body.String(), "do-not-return") ||
		!strings.Contains(response.Body.String(), "[REDACTED]") {
		t.Fatalf("unsafe schema statistics error response: status=%d body=%s", response.Code, response.Body.String())
	}
}
