package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	schemaDeploymentScriptName = "deploy_focus_schema_with_sqlloader_audit.sh"
	schemaDeploymentSessionTTL = 15 * time.Minute
	schemaDeploymentTimeout    = 10 * time.Minute
	maxSchemaDeploymentOutput  = 512 * 1024
)

type schemaDeploymentRequest struct {
	DeploymentToken      string `json:"deploymentToken"`
	TargetSchema         string `json:"targetSchema"`
	TargetSchemaPassword string `json:"targetSchemaPassword"`
	DropExisting         bool   `json:"dropExisting"`
	// DropConfirmation is accepted for backward compatibility but is no longer required.
	DropConfirmation string `json:"dropConfirmation,omitempty"`
}

type schemaDeploymentResponse struct {
	ConnectAlias         string              `json:"connectAlias"`
	AdminUsername        string              `json:"adminUsername"`
	Schema               string              `json:"schema"`
	ScriptName           string              `json:"scriptName"`
	DropExisting         bool                `json:"dropExisting"`
	StartedAtUTC         string              `json:"startedAtUtc"`
	FinishedAtUTC        string              `json:"finishedAtUtc"`
	ExitCode             int                 `json:"exitCode"`
	DeploymentSucceeded  bool                `json:"deploymentSucceeded"`
	TableLookupSucceeded bool                `json:"tableLookupSucceeded"`
	TableCount           int                 `json:"tableCount"`
	Tables               []databaseTableInfo `json:"tables"`
	Output               string              `json:"output"`
	Error                string              `json:"error,omitempty"`
}

type schemaDeploymentInput struct {
	ScriptDirectory      string
	TNSAdmin             string
	ConnectAlias         string
	AdminUsername        string
	AdminPassword        string
	TargetSchema         string
	TargetSchemaPassword string
	DropExisting         bool
}

type schemaDeploymentExecution struct {
	Output        string
	ExitCode      int
	StartedAtUTC  time.Time
	FinishedAtUTC time.Time
}

type schemaDeploymentRunner func(
	context.Context,
	schemaDeploymentInput,
) (schemaDeploymentExecution, error)

type schemaDeploymentCredential struct {
	Username  string
	Password  string
	ExpiresAt time.Time
}

type schemaDeploymentSessionStore struct {
	mu       sync.Mutex
	sessions map[string]schemaDeploymentCredential
	ttl      time.Duration
	now      func() time.Time
	newToken func() (string, error)
}

func newSchemaDeploymentSessionStore() *schemaDeploymentSessionStore {
	return &schemaDeploymentSessionStore{
		sessions: make(map[string]schemaDeploymentCredential),
		ttl:      schemaDeploymentSessionTTL,
		now:      time.Now,
		newToken: newSchemaDeploymentToken,
	}
}

func newSchemaDeploymentToken() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := cryptorand.Read(randomBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func (store *schemaDeploymentSessionStore) issue(username, password string) (string, time.Time, error) {
	token, err := store.newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := store.now().UTC()
	expiresAt := now.Add(store.ttl)

	store.mu.Lock()
	defer store.mu.Unlock()
	for candidate, credential := range store.sessions {
		if !credential.ExpiresAt.After(now) {
			delete(store.sessions, candidate)
		}
	}
	store.sessions[token] = schemaDeploymentCredential{
		Username:  username,
		Password:  password,
		ExpiresAt: expiresAt,
	}
	time.AfterFunc(store.ttl, func() {
		store.deleteIfExpired(token)
	})
	return token, expiresAt, nil
}

func (store *schemaDeploymentSessionStore) deleteIfExpired(token string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	credential, ok := store.sessions[token]
	if ok && !credential.ExpiresAt.After(store.now().UTC()) {
		delete(store.sessions, token)
	}
}

func (store *schemaDeploymentSessionStore) take(token string) (schemaDeploymentCredential, error) {
	if token == "" || len(token) > 512 || strings.TrimSpace(token) != token {
		return schemaDeploymentCredential{}, errors.New("the schema deployment session is invalid")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	credential, ok := store.sessions[token]
	delete(store.sessions, token)
	if !ok {
		return schemaDeploymentCredential{}, errors.New("the schema deployment session is missing or was already used")
	}
	if !credential.ExpiresAt.After(store.now().UTC()) {
		return schemaDeploymentCredential{}, errors.New("the schema deployment session has expired")
	}
	return credential, nil
}

var schemaDeploymentMu sync.Mutex

var protectedDeploymentSchemas = map[string]struct{}{
	"ADMIN": {}, "AUDSYS": {}, "PDBADMIN": {}, "SYS": {}, "SYSTEM": {},
}

func handleSchemaDeployment(
	w http.ResponseWriter,
	r *http.Request,
	getenv func(string) string,
	sessions *schemaDeploymentSessionStore,
	runner schemaDeploymentRunner,
	lister databaseTableLister,
) {
	setTNSGUIJSONHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(schemaDeploymentResponse{Error: "method not allowed"})
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_ = json.NewEncoder(w).Encode(schemaDeploymentResponse{Error: "Content-Type must be application/json"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDatabaseRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request schemaDeploymentRequest
	if err := decoder.Decode(&request); err != nil {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "request body must be one valid JSON object")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "request body must contain only one JSON object")
		return
	}

	request.TargetSchema = strings.ToUpper(strings.TrimSpace(request.TargetSchema))
	if !unquotedOracleIdentifier.MatchString(request.TargetSchema) {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "target schema must be an unquoted Oracle identifier")
		return
	}
	if !validDatabasePassword(request.TargetSchemaPassword) {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "target schema password is invalid")
		return
	}
	if _, protected := protectedDeploymentSchemas[request.TargetSchema]; protected {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "the target schema is protected and cannot be deployed by this interface")
		return
	}
	if request.DeploymentToken == "" {
		writeSchemaDeploymentError(w, http.StatusUnauthorized, "a successful database login is required before schema deployment")
		return
	}

	aliases, err := tnsAliasesFromEnvironment(getenv)
	if err != nil {
		writeSchemaDeploymentError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	tnsAdmin := strings.TrimSpace(getenv("TNS_ADMIN"))
	scriptDirectory := strings.TrimSpace(getenv("FOCUS_SQL_SCRIPTS_DIR"))
	if scriptDirectory == "" {
		scriptDirectory = filepath.Join(".", "sql_scripts")
	}
	scriptDirectory, err = filepath.Abs(scriptDirectory)
	if err != nil {
		writeSchemaDeploymentError(w, http.StatusInternalServerError, "could not resolve the configured sql_scripts directory")
		return
	}

	if !schemaDeploymentMu.TryLock() {
		writeSchemaDeploymentError(w, http.StatusConflict, "another schema deployment is already running")
		return
	}
	defer schemaDeploymentMu.Unlock()

	credential, err := sessions.take(request.DeploymentToken)
	if err != nil {
		writeSchemaDeploymentError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if strings.EqualFold(credential.Username, request.TargetSchema) {
		writeSchemaDeploymentError(w, http.StatusBadRequest, "the target schema cannot be the authenticated administrator user")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), schemaDeploymentTimeout)
	defer cancel()
	execution, runErr := runner(ctx, schemaDeploymentInput{
		ScriptDirectory:      scriptDirectory,
		TNSAdmin:             tnsAdmin,
		ConnectAlias:         aliases.FirstAlias,
		AdminUsername:        credential.Username,
		AdminPassword:        credential.Password,
		TargetSchema:         request.TargetSchema,
		TargetSchemaPassword: request.TargetSchemaPassword,
		DropExisting:         request.DropExisting,
	})

	response := schemaDeploymentResponse{
		ConnectAlias:         aliases.FirstAlias,
		AdminUsername:        credential.Username,
		Schema:               request.TargetSchema,
		ScriptName:           schemaDeploymentScriptName,
		DropExisting:         request.DropExisting,
		StartedAtUTC:         execution.StartedAtUTC.UTC().Format(time.RFC3339),
		FinishedAtUTC:        execution.FinishedAtUTC.UTC().Format(time.RFC3339),
		ExitCode:             execution.ExitCode,
		DeploymentSucceeded:  runErr == nil,
		TableLookupSucceeded: false,
		Tables:               []databaseTableInfo{},
		Output: redactDatabasePasswords(
			execution.Output,
			credential.Password,
			request.TargetSchemaPassword,
		),
	}
	if runErr != nil {
		response.Error = redactDatabasePasswords(
			fmt.Sprintf("schema deployment failed: %v", runErr),
			credential.Password,
			request.TargetSchemaPassword,
		)
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	tables, listErr := lister(
		ctx,
		request.TargetSchema,
		request.TargetSchemaPassword,
		aliases.FirstAlias,
		request.TargetSchema,
	)
	if listErr != nil {
		response.Error = redactDatabasePasswords(
			fmt.Sprintf("schema was deployed, but its tables could not be listed: %v", listErr),
			credential.Password,
			request.TargetSchemaPassword,
		)
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	if tables == nil {
		tables = []databaseTableInfo{}
	}
	response.TableLookupSucceeded = true
	response.TableCount = len(tables)
	response.Tables = tables
	_ = json.NewEncoder(w).Encode(response)
}

func writeSchemaDeploymentError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(schemaDeploymentResponse{Error: message})
}

func redactDatabasePasswords(message string, passwords ...string) string {
	for _, password := range passwords {
		message = redactDatabasePassword(message, password)
	}
	return message
}

func runFocusSchemaDeployment(
	ctx context.Context,
	input schemaDeploymentInput,
) (schemaDeploymentExecution, error) {
	result := schemaDeploymentExecution{ExitCode: -1, StartedAtUTC: time.Now().UTC()}
	scriptPath := filepath.Join(input.ScriptDirectory, schemaDeploymentScriptName)
	info, err := os.Lstat(scriptPath)
	if err != nil {
		result.FinishedAtUTC = time.Now().UTC()
		return result, fmt.Errorf("load deployment script %s: %w", scriptPath, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		result.FinishedAtUTC = time.Now().UTC()
		return result, fmt.Errorf("deployment script is not a regular executable file: %s", scriptPath)
	}

	command := exec.CommandContext(ctx, scriptPath)
	command.Dir = input.ScriptDirectory
	command.Env = schemaDeploymentEnvironment(os.Environ(), input)
	output := &boundedDeploymentOutput{limit: maxSchemaDeploymentOutput}
	command.Stdout = output
	command.Stderr = output
	credentialsReader, credentialsWriter, err := os.Pipe()
	if err != nil {
		result.FinishedAtUTC = time.Now().UTC()
		return result, fmt.Errorf("create deployment credential pipe: %w", err)
	}
	command.ExtraFiles = []*os.File{credentialsReader}
	if err = command.Start(); err != nil {
		_ = credentialsReader.Close()
		_ = credentialsWriter.Close()
		result.FinishedAtUTC = time.Now().UTC()
		return result, err
	}
	_ = credentialsReader.Close()
	_, credentialWriteErr := io.WriteString(
		credentialsWriter,
		input.AdminPassword+"\n"+input.TargetSchemaPassword+"\n",
	)
	_ = credentialsWriter.Close()
	err = command.Wait()
	result.FinishedAtUTC = time.Now().UTC()
	result.Output = output.String()
	if err == nil && credentialWriteErr != nil {
		return result, fmt.Errorf("send deployment credentials: %w", credentialWriteErr)
	}
	if err == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("deployment exceeded the %s timeout", schemaDeploymentTimeout)
	}
	return result, err
}

func schemaDeploymentEnvironment(base []string, input schemaDeploymentInput) []string {
	replaced := map[string]struct{}{
		"TNS_ADMIN": {}, "DB_ADMIN_USER": {}, "DB_ADMIN_PASSWORD": {},
		"DB_TNS_ALIAS": {}, "TARGET_SCHEMA": {}, "TARGET_SCHEMA_PASSWORD": {},
		"FOCUS_CONFIG_FILE": {}, "PARENT_CONFIG_FILE": {}, "DROP_EXISTING": {},
		"FOCUS_IGNORE_DOTENV": {}, "FOCUS_CREDENTIALS_FD": {},
	}
	environment := make([]string, 0, len(base)+10)
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if _, remove := replaced[strings.ToUpper(name)]; found && remove {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment,
		"TNS_ADMIN="+input.TNSAdmin,
		"DB_ADMIN_USER="+input.AdminUsername,
		"DB_TNS_ALIAS="+input.ConnectAlias,
		"TARGET_SCHEMA="+input.TargetSchema,
		"FOCUS_CONFIG_FILE="+filepath.Join(input.ScriptDirectory, "focus.conf"),
		"PARENT_CONFIG_FILE="+filepath.Join(filepath.Dir(input.ScriptDirectory), "focus.conf"),
		"DROP_EXISTING="+strconv.FormatBool(input.DropExisting),
		"FOCUS_IGNORE_DOTENV=true",
		"FOCUS_CREDENTIALS_FD=3",
	)
	return environment
}

type boundedDeploymentOutput struct {
	mu        sync.Mutex
	buffer    strings.Builder
	limit     int
	truncated bool
}

func (output *boundedDeploymentOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	remaining := output.limit - output.buffer.Len()
	if remaining > 0 {
		writeLength := len(data)
		if writeLength > remaining {
			writeLength = remaining
			output.truncated = true
		}
		_, _ = output.buffer.Write(data[:writeLength])
	} else if len(data) > 0 {
		output.truncated = true
	}
	return len(data), nil
}

func (output *boundedDeploymentOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	value := output.buffer.String()
	if output.truncated {
		value += "\n[output truncated by the FOCUS API]\n"
	}
	return value
}

func (output *boundedDeploymentOutput) Truncated() bool {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.truncated
}
