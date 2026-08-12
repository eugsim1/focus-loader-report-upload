package main

import (
	"strings"
	"testing"
	"time"
)

func TestSchemaDeploymentEnvironmentExcludesPasswords(t *testing.T) {
	input := schemaDeploymentInput{
		ScriptDirectory:      "/opt/focus-loader/sql_scripts",
		TNSAdmin:             "/opt/oracle/wallet",
		ConnectAlias:         "FOCUS_HIGH",
		AdminUsername:        "ADMIN",
		AdminPassword:        "new-admin-secret",
		TargetSchema:         "FOCUS_APP",
		TargetSchemaPassword: "new-target-secret",
		DropExisting:         false,
	}
	environment := schemaDeploymentEnvironment([]string{
		"PATH=/usr/bin",
		"DB_ADMIN_PASSWORD=old-admin-secret",
		"TARGET_SCHEMA_PASSWORD=old-target-secret",
	}, input)
	joined := strings.Join(environment, "\n")
	for _, secret := range []string{
		"old-admin-secret",
		"old-target-secret",
		"new-admin-secret",
		"new-target-secret",
	} {
		if strings.Contains(joined, secret) {
			t.Fatalf("deployment environment contains password %q: %s", secret, joined)
		}
	}
	for _, expected := range []string{
		"FOCUS_CREDENTIALS_FD=3",
		"FOCUS_IGNORE_DOTENV=true",
		"DB_TNS_ALIAS=FOCUS_HIGH",
		"DROP_EXISTING=false",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("deployment environment is missing %q: %s", expected, joined)
		}
	}
}

func TestSchemaDeploymentCommandPreviewUsesWrapperAndRedactsPasswords(t *testing.T) {
	input := schemaDeploymentInput{
		ScriptDirectory:      "/opt/focus-loader/sql_scripts",
		TNSAdmin:             "/opt/oracle/wallet",
		ConnectAlias:         "FOCUS_HIGH",
		AdminUsername:        "ADMIN",
		AdminPassword:        "admin-secret",
		TargetSchema:         "FOCUS_APP",
		TargetSchemaPassword: "schema-secret",
		DropExisting:         true,
	}
	preview := schemaDeploymentCommandPreview(input)
	for _, expected := range []string{
		"export DB_ADMIN_PASSWORD='[REDACTED]'",
		"export DB_TNS_ALIAS='FOCUS_HIGH'",
		"export TARGET_SCHEMA='FOCUS_APP'",
		"export TARGET_SCHEMA_PASSWORD='[REDACTED]'",
		"export DROP_EXISTING='true'",
		"./run_deploy_focus_schema_with_sqlloader_audit.sh",
	} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("deployment preview is missing %q: %s", expected, preview)
		}
	}
	for _, secret := range []string{"admin-secret", "schema-secret"} {
		if strings.Contains(preview, secret) {
			t.Fatalf("deployment preview contains password %q: %s", secret, preview)
		}
	}
}

func TestSchemaDeploymentSessionIsOneUseAndExpires(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	store := newSchemaDeploymentSessionStore()
	store.now = func() time.Time { return now }
	store.ttl = time.Hour
	store.newToken = func() (string, error) { return "first-token", nil }

	token, _, err := store.issue("ADMIN", "secret")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.take(token)
	if err != nil || credential.Username != "ADMIN" || credential.Password != "secret" {
		t.Fatalf("unexpected first use: credential=%#v error=%v", credential, err)
	}
	if _, err := store.take(token); err == nil {
		t.Fatal("one-use token was accepted twice")
	}

	store.newToken = func() (string, error) { return "expired-token", nil }
	token, _, err = store.issue("ADMIN", "secret")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := store.take(token); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired-token error, got %v", err)
	}
}

func TestBoundedDeploymentOutputTruncates(t *testing.T) {
	output := &boundedDeploymentOutput{limit: 5}
	if written, err := output.Write([]byte("123456789")); err != nil || written != 9 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	value := output.String()
	if !strings.HasPrefix(value, "12345") || !strings.Contains(value, "truncated") {
		t.Fatalf("unexpected bounded output: %q", value)
	}
}
