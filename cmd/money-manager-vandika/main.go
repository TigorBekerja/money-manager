// Command money-manager-vandika opens an interactive money manager session.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/term"

	"money-manager/internal/audio"
	"money-manager/internal/cli"
	"money-manager/internal/config"
	"money-manager/internal/lineedit"
	"money-manager/internal/speech"
	"money-manager/internal/store"
	"money-manager/internal/voice"
)

const version = "0.1.0"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	showConfig := flag.Bool("config", false, "print the path of the .env settings file and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Couldn't load settings: %v\n\n", err)
		os.Exit(1)
	}
	if *showConfig {
		fmt.Println(cfg.EnvPath)
		return
	}
	if cfg.Created {
		fmt.Printf("\n  Created a settings file for your database credentials:\n    %s\n", cfg.EnvPath)
	}

	vt := isTerminal(os.Stdout) && enableVirtualTerminal()
	color := vt && os.Getenv("NO_COLOR") == ""

	ctx := context.Background()
	db, err := open(ctx, cfg.DatabaseURL)
	if err != nil {
		connectFailure(err, cfg)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Couldn't prepare the database tables: %v\n\n", err)
		os.Exit(1)
	}
	// Entries in the trash for more than TrashDays are removed for good.
	if _, err := db.PurgeDeleted(ctx, time.Now().AddDate(0, 0, -store.TrashDays)); err != nil {
		fmt.Fprintf(os.Stderr, "  ! Couldn't empty old entries from the trash: %v\n", err)
	}

	var lines cli.LineReader
	if vt && isTerminal(os.Stdin) {
		lines = &lineedit.Editor{
			In:      os.Stdin,
			Out:     os.Stdout,
			Raw:     rawMode(os.Stdin),
			Suggest: cli.Suggest,
			Color:   color,
			WakeFn:  wakeConsole(os.Stdin),
		}
	}

	repl := &cli.REPL{
		In:     os.Stdin,
		Lines:  lines,
		Out:    os.Stdout,
		Store:  db,
		Now:    time.Now,
		Color:  color,
		Banner: cli.BannerInfo{Version: version, Name: "Vandika", DB: describeDB(cfg.DatabaseURL)},
	}
	if dir, err := speech.DefaultDir(); err == nil {
		mgr := speech.NewManager(cfg.EnvPath, dir)
		repl.Audio = mgr
		repl.Voice = voice.Launcher{Paths: mgr.InstalledPaths, Open: openAudioSource}
	}

	// Ctrl+C at a prompt is read as a key by the line editor. Outside a prompt it arrives
	// as a signal: it stops a running download, or otherwise quits.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	go func() {
		for range interrupts {
			if repl.Interrupt() {
				continue
			}
			fmt.Println("\n  Bye!")
			db.Close()
			os.Exit(0)
		}
	}()

	if err := repl.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
		os.Exit(1)
	}
}

// open connects to the database, creating it first if the server says it doesn't exist.
func open(ctx context.Context, url string) (*store.Postgres, error) {
	db, err := store.Open(ctx, url)
	if err == nil || !store.IsMissingDatabase(err) {
		return db, err
	}
	name, err := store.CreateDatabase(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("database %q does not exist and couldn't be created: %w", name, err)
	}
	fmt.Printf("\n  Created database %q.\n", name)
	return store.Open(ctx, url)
}

// describeDB returns "host:port/dbname" without credentials.
func describeDB(url string) string {
	cfg, err := pgconn.ParseConfig(url)
	if err != nil {
		return "(invalid database URL)"
	}
	return fmt.Sprintf("%s:%d/%s", cfg.Host, cfg.Port, cfg.Database)
}

func connectFailure(err error, cfg config.Config) {
	user := ""
	if pc, perr := pgconn.ParseConfig(cfg.DatabaseURL); perr == nil {
		user = " as " + pc.User
	}
	fmt.Fprintf(os.Stderr, "\n  ✗ Couldn't connect to PostgreSQL at %s%s.\n    %v\n\n  To fix:\n",
		describeDB(cfg.DatabaseURL), user, err)
	fmt.Fprintln(os.Stderr, "    • Make sure PostgreSQL is running.")
	if cfg.Source == cfg.EnvPath {
		fmt.Fprintf(os.Stderr, "    • Put your PostgreSQL credentials (DB_USER, DB_PASSWORD, ...) in:\n        %s\n", cfg.EnvPath)
		fmt.Fprintln(os.Stderr, "      then run money-manager-vandika again.")
	} else {
		fmt.Fprintf(os.Stderr, "    • The connection comes from the %s.\n", cfg.Source)
		fmt.Fprintf(os.Stderr, "      Fix it, or remove it to use the settings file:\n        %s\n", cfg.EnvPath)
	}
	fmt.Fprintln(os.Stderr)
}

// openAudioSource opens the microphone. For development, MONEY_MANAGER_AUDIO_INPUT names a
// 16 kHz mono WAV file that is played in real time instead.
func openAudioSource() (audio.Source, error) {
	if path := os.Getenv("MONEY_MANAGER_AUDIO_INPUT"); path != "" {
		return audio.OpenWAVFile(path, true)
	}
	return audio.OpenMic()
}

// rawMode switches f to raw mode for the duration of one line edit.
func rawMode(f *os.File) func() (func(), error) {
	return func() (func(), error) {
		fd := int(f.Fd())
		old, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		return func() { term.Restore(fd, old) }, nil
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
