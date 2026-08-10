package main

import (
	"encoding/json"
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
