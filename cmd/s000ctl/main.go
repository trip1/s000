package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ds9labs.com/s000/internal/recovery"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) == 0 {
		writeRootHelp(out)
		return 0
	}

	cmd := args[0]
	switch cmd {
	case "help", "-h", "--help":
		writeRootHelp(out)
		return 0
	case "backup-create":
		return runBackupCreate(args[1:], out, errOut)
	case "restore-validate":
		return runRestoreValidate(args[1:], out, errOut)
	case "health-inspect":
		return runHealthInspect(args[1:], out, errOut)
	case "completion":
		return runCompletion(args[1:], out, errOut)
	case "functions-list":
		return runFunctionsList(args[1:], out, errOut)
	case "functions-get":
		return runFunctionsGet(args[1:], out, errOut)
	case "functions-create":
		return runFunctionsCreate(args[1:], out, errOut)
	case "functions-delete":
		return runFunctionsDelete(args[1:], out, errOut)
	case "functions-invoke":
		return runFunctionsInvoke(args[1:], out, errOut)
	case "functions-templates":
		return runFunctionsTemplates(args[1:], out, errOut)
	case "functions-metrics":
		return runFunctionsMetrics(args[1:], out, errOut)
	case "functions-alerts":
		return runFunctionsAlerts(args[1:], out, errOut)
	case "functions-logs":
		return runFunctionsLogs(args[1:], out, errOut)
	default:
		_, _ = fmt.Fprintf(errOut, "unknown command %q\n\n", cmd)
		writeRootHelp(errOut)
		return 2
	}
}

func writeRootHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `s000ctl - s000 admin and ops CLI

Usage:
  s000ctl <command> [flags]

Commands:
  backup-create      Create cold backup snapshot
  restore-validate   Validate backup restore layout
  health-inspect     Check /healthz and /readyz on one endpoint
  completion         Print shell completion script snippet
  functions-list     List registered functions
  functions-get      Get one function metadata
  functions-create   Create function from wasm module
  functions-delete   Delete one function
  functions-invoke   Invoke one function locally via API
  functions-templates  List built-in templates
  functions-metrics  Show function metrics
  functions-alerts   Show function alerts
  functions-logs     Show recent function logs
  help               Show this help

Examples:
  s000ctl backup-create --data-dir ./data --metadata-dsn file:./data/s000-metadata.db --out ./backup
  s000ctl restore-validate --path ./backup
  s000ctl health-inspect --endpoint http://127.0.0.1:9000
  s000ctl completion --shell bash
  s000ctl functions-list --endpoint http://127.0.0.1:9000
  s000ctl functions-create --endpoint http://127.0.0.1:9000 --name fn --trigger onPutObjectPre --module ./fn.wasm
`)
}

func runBackupCreate(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("backup-create", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var dataDir, metadataDSN, outDir string
	fs.StringVar(&dataDir, "data-dir", "", "s000 data directory")
	fs.StringVar(&metadataDSN, "metadata-dsn", "", "metadata DSN")
	fs.StringVar(&outDir, "out", "", "backup output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if err := recovery.CreateBackup(recovery.BackupConfig{DataDir: dataDir, MetadataDSN: metadataDSN, OutputDir: outDir}); err != nil {
		_, _ = fmt.Fprintf(errOut, "backup-create failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, "backup created")
	return 0
}

func runRestoreValidate(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("restore-validate", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var backupDir string
	fs.StringVar(&backupDir, "path", "", "backup directory path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if err := recovery.ValidateRestore(backupDir); err != nil {
		_, _ = fmt.Fprintf(errOut, "restore validation failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, "restore validation passed")
	return 0
}

func runHealthInspect(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("health-inspect", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	timeout := 5 * time.Second
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.DurationVar(&timeout, "timeout", timeout, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	client := &http.Client{Timeout: timeout}
	if err := probeEndpoint(client, endpoint, "/healthz"); err != nil {
		_, _ = fmt.Fprintf(errOut, "healthz failed: %v\n", err)
		return 1
	}
	if err := probeEndpoint(client, endpoint, "/readyz"); err != nil {
		_, _ = fmt.Fprintf(errOut, "readyz failed: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintln(out, "health: ok")
	_, _ = fmt.Fprintln(out, "ready: ok")
	return 0
}

func probeEndpoint(client *http.Client, endpoint, path string) error {
	url := strings.TrimRight(endpoint, "/") + path
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func runCompletion(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var shell string
	fs.StringVar(&shell, "shell", "bash", "target shell: bash|zsh|fish|powershell")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	script, err := completionScript(shell)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "completion failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, script)
	return 0
}

func completionScript(shell string) (string, error) {
	commands := "backup-create restore-validate health-inspect completion functions-list functions-get functions-create functions-delete functions-invoke functions-templates functions-metrics functions-alerts functions-logs help"
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return fmt.Sprintf("complete -W \"%s\" s000ctl", commands), nil
	case "zsh":
		return "#compdef s000ctl\n_arguments '1: :((backup-create restore-validate health-inspect completion functions-list functions-get functions-create functions-delete functions-invoke functions-templates functions-metrics functions-alerts functions-logs help))'", nil
	case "fish":
		return "complete -c s000ctl -f -a \"backup-create restore-validate health-inspect completion functions-list functions-get functions-create functions-delete functions-invoke functions-templates functions-metrics functions-alerts functions-logs help\"", nil
	case "powershell", "pwsh":
		return "Register-ArgumentCompleter -CommandName s000ctl -ScriptBlock { param($wordToComplete) 'backup-create','restore-validate','health-inspect','completion','functions-list','functions-get','functions-create','functions-delete','functions-invoke','functions-templates','functions-metrics','functions-alerts','functions-logs','help' | Where-Object { $_ -like \"$wordToComplete*\" } }", nil
	default:
		return "", errors.New("unsupported shell")
	}
}

func runFunctionsList(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-list", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/functions", nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-list failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsGet(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-get", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	name := ""
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.StringVar(&name, "name", name, "function name")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(name) == "" {
		_, _ = fmt.Fprintln(errOut, "functions-get: --name is required")
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/functions/"+name, nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-get failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsCreate(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-create", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	name := ""
	trigger := "onPutObjectPre"
	runtime := "wazero"
	modulePath := ""
	enabled := true
	priority := 100
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.StringVar(&name, "name", name, "function name")
	fs.StringVar(&trigger, "trigger", trigger, "function trigger")
	fs.StringVar(&runtime, "runtime", runtime, "function runtime")
	fs.StringVar(&modulePath, "module", modulePath, "path to wasm module")
	fs.BoolVar(&enabled, "enabled", enabled, "enable function")
	fs.IntVar(&priority, "priority", priority, "lower runs first")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(modulePath) == "" {
		_, _ = fmt.Fprintln(errOut, "functions-create: --name and --module are required")
		return 2
	}
	module, err := os.ReadFile(filepath.Clean(modulePath))
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-create: read module failed: %v\n", err)
		return 1
	}
	payload := map[string]any{
		"name":          name,
		"runtime":       runtime,
		"trigger":       trigger,
		"priority":      priority,
		"enabled":       enabled,
		"module_base64": base64.StdEncoding.EncodeToString(module),
	}
	body, _ := json.Marshal(payload)
	resp, err := doJSONRequest(http.MethodPost, strings.TrimRight(endpoint, "/")+"/functions", body)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-create failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsDelete(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-delete", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	name := ""
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.StringVar(&name, "name", name, "function name")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(name) == "" {
		_, _ = fmt.Fprintln(errOut, "functions-delete: --name is required")
		return 2
	}
	if _, err := doJSONRequest(http.MethodDelete, strings.TrimRight(endpoint, "/")+"/functions/"+name, nil); err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-delete failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, "deleted")
	return 0
}

func runFunctionsInvoke(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-invoke", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	name := ""
	payload := "{}"
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.StringVar(&name, "name", name, "function name")
	fs.StringVar(&payload, "payload", payload, "JSON payload")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(name) == "" {
		_, _ = fmt.Fprintln(errOut, "functions-invoke: --name is required")
		return 2
	}
	reqBody, _ := json.Marshal(map[string]any{"payload": json.RawMessage(payload)})
	resp, err := doJSONRequest(http.MethodPost, strings.TrimRight(endpoint, "/")+"/functions/"+name+"/invoke", reqBody)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-invoke failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsTemplates(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-templates", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/functions/templates", nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-templates failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsMetrics(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-metrics", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/functions/metrics", nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-metrics failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsAlerts(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-alerts", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/functions/alerts", nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-alerts failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func runFunctionsLogs(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("functions-logs", flag.ContinueOnError)
	fs.SetOutput(errOut)
	endpoint := "http://127.0.0.1:9000"
	limit := 50
	fs.StringVar(&endpoint, "endpoint", endpoint, "service endpoint URL")
	fs.IntVar(&limit, "limit", limit, "max log entries")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	resp, err := doJSONRequest(http.MethodGet, fmt.Sprintf("%s/functions/logs?limit=%d", strings.TrimRight(endpoint, "/"), limit), nil)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "functions-logs failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(resp))
	return 0
}

func doJSONRequest(method string, url string, payload []byte) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
