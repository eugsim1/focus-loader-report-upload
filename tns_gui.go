package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	tnsNamesFileName = "tnsnames.ora"
	maxTNSLineSize   = 1024 * 1024
)

type tnsAliasResponse struct {
	Alias      string `json:"alias"`
	SourcePath string `json:"sourcePath"`
	ReadAtUTC  string `json:"readAtUtc"`
	Error      string `json:"error,omitempty"`
}

type tnsAliasesResponse struct {
	Aliases    []string `json:"aliases"`
	FirstAlias string   `json:"firstAlias"`
	SourcePath string   `json:"sourcePath"`
	ReadAtUTC  string   `json:"readAtUtc"`
	Error      string   `json:"error,omitempty"`
}

type tnsGUIHealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func runTNSGUI(ctx context.Context, listenAddress string) error {
	listenAddress = strings.TrimSpace(listenAddress)
	if listenAddress == "" {
		return fmt.Errorf("-tns-gui-listen cannot be empty")
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("start TNS GUI listener on %s: %w", listenAddress, err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           newTNSGUIHandler(os.Getenv),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	host, _, splitErr := net.SplitHostPort(listener.Addr().String())
	if splitErr == nil && !isLoopbackHost(host) {
		fmt.Println("WARNING: the TNS GUI has no built-in authentication; use a firewall or reverse proxy before exposing it")
	}
	fmt.Printf("TNS GUI listening on http://%s\n", listener.Addr().String())
	fmt.Println("The page reads only the first alias from $TNS_ADMIN/tnsnames.ora; it never modifies the file.")

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve TNS GUI: %w", err)
		}
		return nil
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop TNS GUI: %w", err)
		}
		return nil
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newTNSGUIHandler(getenv func(string) string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		setTNSGUIJSONHeaders(w)
		if !requireTNSGUIGet(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(tnsGUIHealthResponse{Status: "ok", Version: version})
	})
	mux.HandleFunc("/api/v1/tns/aliases", func(w http.ResponseWriter, r *http.Request) {
		setTNSGUIJSONHeaders(w)
		if !requireTNSGUIGet(w, r) {
			return
		}

		response, err := tnsAliasesFromEnvironment(getenv)
		if err != nil {
			response.Error = err.Error()
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("/api/tns-alias", func(w http.ResponseWriter, r *http.Request) {
		setTNSGUIJSONHeaders(w)
		if !requireTNSGUIGet(w, r) {
			return
		}

		response, err := firstTNSAliasFromEnvironment(getenv)
		if err != nil {
			response.Error = err.Error()
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setTNSGUISecurityHeaders(w)
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, tnsGUIPage)
	})
	return mux
}

func setTNSGUIJSONHeaders(w http.ResponseWriter) {
	setTNSGUISecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
}

func requireTNSGUIGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	w.WriteHeader(http.StatusMethodNotAllowed)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
	return false
}

func setTNSGUISecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func firstTNSAliasFromEnvironment(getenv func(string) string) (tnsAliasResponse, error) {
	aliasesResponse, err := tnsAliasesFromEnvironment(getenv)
	response := tnsAliasResponse{
		SourcePath: aliasesResponse.SourcePath,
		ReadAtUTC:  aliasesResponse.ReadAtUTC,
	}
	if err != nil {
		return response, err
	}
	response.Alias = aliasesResponse.FirstAlias
	return response, nil
}

func tnsAliasesFromEnvironment(getenv func(string) string) (tnsAliasesResponse, error) {
	response := tnsAliasesResponse{
		Aliases:   []string{},
		ReadAtUTC: time.Now().UTC().Format(time.RFC3339),
	}
	tnsAdmin := strings.TrimSpace(getenv("TNS_ADMIN"))
	if tnsAdmin == "" {
		return response, fmt.Errorf("TNS_ADMIN is not set for the GUI process")
	}

	path, err := filepath.Abs(filepath.Join(tnsAdmin, tnsNamesFileName))
	if err != nil {
		return response, fmt.Errorf("resolve $TNS_ADMIN/%s: %w", tnsNamesFileName, err)
	}
	response.SourcePath = path

	file, err := os.Open(path)
	if err != nil {
		return response, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	aliases, err := parseTNSAliases(file)
	if err != nil {
		return response, fmt.Errorf("read %s: %w", path, err)
	}
	response.Aliases = aliases
	response.FirstAlias = aliases[0]
	return response, nil
}

func parseFirstTNSAlias(reader io.Reader) (string, error) {
	aliases, err := parseTNSAliases(reader)
	if err != nil {
		return "", err
	}
	return aliases[0], nil
}

func parseTNSAliases(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxTNSLineSize)
	pending := make([]string, 0, 2)
	aliases := make([]string, 0, 8)
	seen := make(map[string]struct{})

	for scanner.Scan() {
		line := strings.TrimSpace(stripTNSComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "(") {
			pending = pending[:0]
			continue
		}

		equalsIndex := strings.IndexRune(line, '=')
		if equalsIndex < 0 {
			if isTNSAliasFragment(line) && len(pending) < 16 {
				pending = append(pending, line)
			} else {
				pending = pending[:0]
			}
			continue
		}

		leftSide := strings.TrimSpace(strings.Join(append(pending, line[:equalsIndex]), " "))
		pending = pending[:0]
		candidates := strings.Split(leftSide, ",")
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if strings.EqualFold(candidate, "IFILE") {
				break
			}
			if isTNSAlias(candidate) {
				key := strings.ToLower(candidate)
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					aliases = append(aliases, candidate)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(aliases) == 0 {
		return nil, fmt.Errorf("no TNS alias entry was found")
	}
	return aliases, nil
}

func stripTNSComment(line string) string {
	inSingleQuote := false
	inDoubleQuote := false
	for index, char := range line {
		switch char {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '#':
			if !inSingleQuote && !inDoubleQuote {
				return line[:index]
			}
		}
	}
	return line
}

func isTNSAliasFragment(value string) bool {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !isTNSAlias(part) {
			return false
		}
	}
	return value != ""
}

func isTNSAlias(value string) bool {
	if value == "" || len(value) > 1024 || strings.EqualFold(value, "IFILE") {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n()=#")
}

const tnsGUIPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>FOCUS Loader - Oracle TNS connection</title>
  <style>
    :root { color-scheme: light dark; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: #f4f5f7; color: #171717; }
    main { max-width: 760px; margin: 8vh auto; padding: 0 20px; }
    section { background: #fff; border: 1px solid #d9dce1; border-radius: 14px; padding: 28px; box-shadow: 0 10px 32px rgba(0,0,0,.08); }
    h1 { margin: 0 0 8px; font-size: 1.65rem; }
    p { color: #50545b; line-height: 1.5; }
    label { display: block; margin: 24px 0 8px; font-weight: 650; }
    input { box-sizing: border-box; width: 100%; padding: 12px 14px; border: 1px solid #a9afb8; border-radius: 8px; background: #fafafa; color: #171717; font: inherit; }
    input[readonly] { cursor: text; }
    .actions { display: flex; gap: 10px; margin-top: 18px; }
    button { border: 0; border-radius: 8px; padding: 10px 16px; background: #c74634; color: white; font: inherit; font-weight: 650; cursor: pointer; }
    button.secondary { background: #343a40; }
    button:disabled { opacity: .55; cursor: wait; }
    .meta { margin-top: 20px; padding-top: 16px; border-top: 1px solid #e4e6e9; font-size: .9rem; overflow-wrap: anywhere; }
    .status { min-height: 1.4em; margin-top: 14px; font-weight: 600; }
    .status.error { color: #b42318; }
    @media (prefers-color-scheme: dark) {
      body { background: #121417; color: #f1f3f5; }
      section { background: #1d2125; border-color: #3a4047; }
      p { color: #c4c9cf; }
      input { background: #111417; color: #f1f3f5; border-color: #59616b; }
      .meta { border-color: #3a4047; }
    }
  </style>
</head>
<body>
<main>
  <section>
    <h1>Oracle database connection</h1>
    <p>The value below is the first valid network-service alias in <code>$TNS_ADMIN/tnsnames.ora</code>. The file is read-only in this interface.</p>
    <label for="tnsAlias">First TNS alias</label>
    <input id="tnsAlias" type="text" value="Loading..." readonly spellcheck="false" aria-describedby="status">
    <div class="actions">
      <button id="refresh" type="button">Refresh</button>
      <button id="copy" class="secondary" type="button">Copy alias</button>
    </div>
    <div id="status" class="status" role="status" aria-live="polite"></div>
    <div class="meta"><strong>Source:</strong> <span id="source">Waiting for server...</span><br><strong>Read at (UTC):</strong> <span id="readAt">-</span></div>
  </section>
</main>
<script>
  const alias = document.getElementById('tnsAlias');
  const source = document.getElementById('source');
  const readAt = document.getElementById('readAt');
  const status = document.getElementById('status');
  const refresh = document.getElementById('refresh');
  const copy = document.getElementById('copy');

  async function loadAlias() {
    refresh.disabled = true;
    status.className = 'status';
    status.textContent = 'Reading tnsnames.ora...';
    try {
      const response = await fetch('/api/tns-alias', { cache: 'no-store' });
      const data = await response.json();
      source.textContent = data.sourcePath || '$TNS_ADMIN is not configured';
      readAt.textContent = data.readAtUtc || '-';
      if (!response.ok || data.error) throw new Error(data.error || 'Unable to read the TNS alias');
      alias.value = data.alias;
      status.textContent = 'Alias loaded.';
    } catch (error) {
      alias.value = '';
      status.className = 'status error';
      status.textContent = error.message;
    } finally {
      refresh.disabled = false;
    }
  }

  refresh.addEventListener('click', loadAlias);
  copy.addEventListener('click', async () => {
    if (!alias.value) return;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(alias.value);
      } else {
        alias.select();
        if (!document.execCommand('copy')) throw new Error('Browser copy command was rejected');
      }
      status.className = 'status';
      status.textContent = 'Alias copied.';
    } catch (error) {
      status.className = 'status error';
      status.textContent = 'Copy failed; select the alias and copy it manually.';
    }
  });
  loadAlias();
</script>
</body>
</html>`
