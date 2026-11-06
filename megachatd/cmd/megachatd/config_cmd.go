package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

// configUsage is the "megachatd config" help.
func configUsage(w io.Writer) {
	fmt.Fprint(w, `usage: megachatd config <command> [flags]

commands:
  migrate   convert a legacy flat config.json (and the project .env) to the
            nested per-domain schema; see "megachatd config migrate -h"
`)
}

// migrateUsage is the "megachatd config migrate" help.
func migrateUsage(w io.Writer) {
	fmt.Fprint(w, `usage: megachatd config migrate [--project-dir DIR] [--dry-run]

Converts the legacy flat config.json to the nested per-domain schema
(signal.user_number, telegram.enabled, transcription.provider, ...) and folds
the project .env into it. config.json wins over .env when both set a setting.
Unknown .env keys are reported, not copied.

The original config.json is kept as config.json.pre-nested.bak (never
overwritten; the file is written with mode 0600). .env is renamed to
.env.migrated once every key was migrated or reported. Running the command on
an already migrated project changes nothing.

flags:
  --project-dir DIR   directory holding config.json / .env (default:
                      $MEGACHAT_PROJECT_DIR, else the repository containing the
                      executable, else the working directory)
  --dry-run           print the resulting config.json with secret values
                      masked; write nothing

examples:
  megachatd config migrate --dry-run
  megachatd config migrate --project-dir ~/Megachat
`)
}

// runConfig dispatches "megachatd config ...".
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		configUsage(stderr)
		return 2
	}
	switch args[0] {
	case "migrate":
		return migrateCommand(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		configUsage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "megachatd config: unknown command %q\n", args[0])
	configUsage(stderr)
	return 2
}

// migrateCommand runs the migration (or prints the plan with --dry-run).
func migrateCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("megachatd config migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { migrateUsage(stderr) }
	var projectDir string
	var dryRun bool
	fs.StringVar(&projectDir, "project-dir", "", "directory holding config.json and .env")
	fs.BoolVar(&dryRun, "dry-run", false, "print the resulting config.json with secrets masked; write nothing")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "megachatd config migrate: unexpected argument %q\n", fs.Arg(0))
		migrateUsage(stderr)
		return 2
	}

	exe, _ := os.Executable()
	cfg := config.New()
	dir := ResolveProjectDir(projectDir, cfg.Env, exe)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	m, err := config.PlanMigration(dir)
	if err != nil {
		fmt.Fprintf(stderr, "❌ %v\n", err)
		return 1
	}
	if dryRun {
		printMigrationReport(stderr, m, true)
		body, err := m.MaskedJSON()
		if err != nil {
			fmt.Fprintf(stderr, "❌ config migrate: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(body)
		return 0
	}
	if err := m.Apply(); err != nil {
		fmt.Fprintf(stderr, "❌ %v\n", err)
		return 1
	}
	printMigrationReport(stdout, m, false)
	return 0
}

// printMigrationReport lists what the migration moved, dropped and ignored and
// what it wrote (or would write with --dry-run).
func printMigrationReport(w io.Writer, m *config.Migration, dry bool) {
	title := "config migrate"
	if dry {
		title += " (dry run)"
	}
	fmt.Fprintf(w, "%s: %s\n", title, m.ProjectDir)

	for _, x := range m.MovedKeys {
		fmt.Fprintf(w, "  move config.json key %s -> %s\n", x.From, x.To)
	}
	for _, x := range m.DroppedKeys {
		fmt.Fprintf(w, "  drop config.json key %s (%s)\n", x.Key, x.Reason)
	}
	for _, x := range m.MigratedEnv {
		fmt.Fprintf(w, "  move .env key %s -> %s\n", x.From, x.To)
	}
	for _, x := range m.SkippedEnv {
		fmt.Fprintf(w, "  skip .env key %s: config.json already sets %s\n", x.From, x.To)
	}
	for _, k := range m.RemovedEnv {
		fmt.Fprintf(w, "  drop .env key %s (setting removed)\n", k)
	}
	for _, x := range m.InvalidEnv {
		fmt.Fprintf(w, "  ignore .env key %s: %s\n", x.Key, x.Reason)
	}
	for _, k := range m.UnknownEnv {
		fmt.Fprintf(w, "  unknown .env key %s: not copied\n", k)
	}

	if m.ConfigChanged {
		if dry {
			fmt.Fprintf(w, "  would write %s (mode 0600)\n", m.ConfigFile)
		} else {
			fmt.Fprintf(w, "  wrote %s (mode 0600)\n", m.ConfigFile)
		}
		switch {
		case m.BackupCreated:
			fmt.Fprintf(w, "  created %s\n", m.BackupPath)
		case m.BackupNeeded:
			fmt.Fprintf(w, "  would create %s\n", m.BackupPath)
		case m.ConfigExisted:
			fmt.Fprintf(w, "  kept existing %s\n", m.BackupPath)
		}
	} else {
		fmt.Fprintln(w, "  no changes to config.json")
	}

	switch {
	case !m.DotenvExists:
		fmt.Fprintln(w, "  no .env found")
	case m.DotenvRenamed:
		fmt.Fprintf(w, "  renamed .env -> %s\n", filepath.Base(m.DotenvTo))
	case dry:
		fmt.Fprintf(w, "  would rename .env -> %s\n", filepath.Base(m.DotenvTo))
	}
}
