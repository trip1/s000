package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
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
  help               Show this help

Examples:
  s000ctl backup-create --data-dir ./data --metadata-dsn file:./data/s000-metadata.db --out ./backup
  s000ctl restore-validate --path ./backup
  s000ctl health-inspect --endpoint http://127.0.0.1:9000
  s000ctl completion --shell bash
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
	commands := "backup-create restore-validate health-inspect completion help"
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return fmt.Sprintf("complete -W \"%s\" s000ctl", commands), nil
	case "zsh":
		return "#compdef s000ctl\n_arguments '1: :((backup-create restore-validate health-inspect completion help))'", nil
	case "fish":
		return "complete -c s000ctl -f -a \"backup-create restore-validate health-inspect completion help\"", nil
	case "powershell", "pwsh":
		return "Register-ArgumentCompleter -CommandName s000ctl -ScriptBlock { param($wordToComplete) 'backup-create','restore-validate','health-inspect','completion','help' | Where-Object { $_ -like \"$wordToComplete*\" } }", nil
	default:
		return "", errors.New("unsupported shell")
	}
}
