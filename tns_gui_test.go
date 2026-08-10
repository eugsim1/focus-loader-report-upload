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

func TestParseFirstTNSAlias(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "standard wallet file",
			content: `# generated wallet entry
focusdb_high =
  (DESCRIPTION =
    (ADDRESS = (PROTOCOL = TCPS)(HOST = adb.example.com)(PORT = 1522))
  )
focusdb_low = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))
`,
			want: "focusdb_high",
		},
		{
			name: "include directive followed by alias",
			content: `IFILE = /opt/oracle/network/admin/common.ora
FIRST_ALIAS = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))
`,
			want: "FIRST_ALIAS",
		},
		{
			name: "multiple and multiline aliases",
			content: `PRIMARY_ALIAS,
  COMPATIBILITY_ALIAS =
    (DESCRIPTION = (ADDRESS = (PROTOCOL = TCP)))
`,
			want: "PRIMARY_ALIAS",
		},
		{
			name:    "inline comment",
			content: "MYDB.EXAMPLE.COM = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS))) # preferred\n",
			want:    "MYDB.EXAMPLE.COM",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFirstTNSAlias(strings.NewReader(test.content))
			if err != nil {
				t.Fatalf("parseFirstTNSAlias() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseFirstTNSAlias() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseFirstTNSAliasNoEntry(t *testing.T) {
	_, err := parseFirstTNSAlias(strings.NewReader("# comments only\n(DESCRIPTION = (ADDRESS = TCP))\n"))
	if err == nil || !strings.Contains(err.Error(), "no TNS alias") {
		t.Fatalf("expected no-alias error, got %v", err)
	}
}

func TestParseTNSAliasesPreservesOrderAndRemovesDuplicates(t *testing.T) {
	content := `FIRST, FIRST_COMPAT = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))
SECOND = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))
first = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))
`
	got, err := parseTNSAliases(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parseTNSAliases() error = %v", err)
	}
	want := []string{"FIRST", "FIRST_COMPAT", "SECOND"}
	if len(got) != len(want) {
		t.Fatalf("parseTNSAliases() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("parseTNSAliases()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestFirstTNSAliasFromEnvironment(t *testing.T) {
	tnsAdmin := t.TempDir()
	path := filepath.Join(tnsAdmin, tnsNamesFileName)
	if err := os.WriteFile(path, []byte("FINOPSDB_HIGH = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := firstTNSAliasFromEnvironment(func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	})
	if err != nil {
		t.Fatalf("firstTNSAliasFromEnvironment() error = %v", err)
	}
	if response.Alias != "FINOPSDB_HIGH" {
		t.Fatalf("alias = %q, want FINOPSDB_HIGH", response.Alias)
	}
	if response.SourcePath != path {
		t.Fatalf("source path = %q, want %q", response.SourcePath, path)
	}
}

func TestTNSGUIAPI(t *testing.T) {
	tnsAdmin := t.TempDir()
	path := filepath.Join(tnsAdmin, tnsNamesFileName)
	if err := os.WriteFile(path, []byte("FIRST_SERVICE = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := newTNSGUIHandler(func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	})
	request := httptest.NewRequest(http.MethodGet, "/api/tns-alias", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload tnsAliasResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Alias != "FIRST_SERVICE" {
		t.Fatalf("alias = %q, want FIRST_SERVICE", payload.Alias)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestTNSGUIVersionedAliasesAPI(t *testing.T) {
	tnsAdmin := t.TempDir()
	path := filepath.Join(tnsAdmin, tnsNamesFileName)
	content := "FIRST_SERVICE = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\nSECOND_SERVICE = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := newTNSGUIHandler(func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tns/aliases", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload tnsAliasesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.FirstAlias != "FIRST_SERVICE" || len(payload.Aliases) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestTNSGUIHealthAPI(t *testing.T) {
	handler := newTNSGUIHandler(func(string) string { return "" })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload tnsGUIHealthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ok" || payload.Version != version {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestTNSGUIDatabaseTablesAPIUsesFirstAlias(t *testing.T) {
	tnsAdmin := t.TempDir()
	path := filepath.Join(tnsAdmin, tnsNamesFileName)
	content := "FIRST_SERVICE = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\nSECOND_SERVICE = (DESCRIPTION = (ADDRESS = (PROTOCOL = TCPS)))\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotUsername, gotPassword, gotAlias, gotSchema string
	lister := func(
		_ context.Context,
		username string,
		password string,
		alias string,
		schema string,
	) ([]databaseTableInfo, error) {
		gotUsername, gotPassword, gotAlias, gotSchema = username, password, alias, schema
		return []databaseTableInfo{
			{Owner: "FOCUS_APP", TableName: "OCI_FOCUS"},
			{Owner: "FOCUS_APP", TableName: "LOAD_STATUS"},
		}, nil
	}
	handler := newTNSGUIHandlerWithTableLister(func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, lister)
	body := bytes.NewBufferString(`{"username":"ADMIN","password":"test-secret","schema":"focus_app"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/database/tables", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if gotUsername != "ADMIN" || gotPassword != "test-secret" || gotAlias != "FIRST_SERVICE" || gotSchema != "FOCUS_APP" {
		t.Fatalf("unexpected lister input: user=%q password=%q alias=%q schema=%q", gotUsername, gotPassword, gotAlias, gotSchema)
	}
	var payload databaseTablesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ConnectAlias != "FIRST_SERVICE" || payload.TableCount != 2 || len(payload.Tables) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if strings.Contains(response.Body.String(), "test-secret") {
		t.Fatal("response contains the database password")
	}
}

func TestTNSGUIDatabaseTablesAPIRedactsPasswordFromErrors(t *testing.T) {
	tnsAdmin := t.TempDir()
	path := filepath.Join(tnsAdmin, tnsNamesFileName)
	if err := os.WriteFile(path, []byte("FIRST_SERVICE = (DESCRIPTION = (ADDRESS = TCP))\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lister := func(
		_ context.Context,
		_ string,
		password string,
		_ string,
		_ string,
	) ([]databaseTableInfo, error) {
		return nil, errors.New("connection failed with password " + password)
	}
	handler := newTNSGUIHandlerWithTableLister(func(name string) string {
		if name == "TNS_ADMIN" {
			return tnsAdmin
		}
		return ""
	}, lister)
	body := bytes.NewBufferString(`{"username":"ADMIN","password":"do-not-return","schema":"ADMIN"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/database/tables", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "do-not-return") || !strings.Contains(response.Body.String(), "[REDACTED]") {
		t.Fatalf("password was not safely redacted: %s", response.Body.String())
	}
}

func TestTNSGUIDatabaseTablesAPIRejectsMissingPassword(t *testing.T) {
	called := false
	handler := newTNSGUIHandlerWithTableLister(func(string) string { return "" }, func(
		context.Context,
		string,
		string,
		string,
		string,
	) ([]databaseTableInfo, error) {
		called = true
		return nil, nil
	})
	body := bytes.NewBufferString(`{"username":"ADMIN","password":"","schema":"ADMIN"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/database/tables", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("database lister was called for an invalid request")
	}
}

func TestTNSGUIAPIMissingEnvironment(t *testing.T) {
	handler := newTNSGUIHandler(func(string) string { return "" })
	request := httptest.NewRequest(http.MethodGet, "/api/tns-alias", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(response.Body.String(), "TNS_ADMIN is not set") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestTNSGUIPage(t *testing.T) {
	handler := newTNSGUIHandler(func(string) string { return "" })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `id="tnsAlias"`) {
		t.Fatal("page does not contain the TNS alias text box")
	}
	if response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers were not applied")
	}
}
