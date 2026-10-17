package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadEnvFile(t *testing.T) {
	path := writeFile(t, "\xef\xbb\xbf# comment\n\n  DB_HOST = db.local \nDB_PASSWORD=p#ss=word\nDB_USER=\"quoted user\"\nDB_NAME='x'\nEMPTY=\n")
	got, err := ReadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DB_HOST":     "db.local",
		"DB_PASSWORD": "p#ss=word",
		"DB_USER":     "quoted user",
		"DB_NAME":     "x",
		"EMPTY":       "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestReadEnvFileBadLine(t *testing.T) {
	if _, err := ReadEnvFile(writeFile(t, "DB_HOST=x\nnot a pair\n")); err == nil {
		t.Error("want error for line without =")
	}
}

func TestBuildURLEscapesSpecialCharacters(t *testing.T) {
	const password = `p@ss:w/rd#1%?& "x"`
	u := BuildURL("localhost", "5432", "postgres", password, "money_manager", "disable")
	cfg, err := pgconn.ParseConfig(u)
	if err != nil {
		t.Fatalf("ParseConfig(%q): %v", u, err)
	}
	if cfg.Password != password || cfg.User != "postgres" || cfg.Host != "localhost" ||
		cfg.Port != 5432 || cfg.Database != "money_manager" {
		t.Errorf("round trip mismatch: %+v", cfg)
	}
}

func TestLoadCreatesTemplate(t *testing.T) {
	t.Setenv(EnvURL, "")
	path := filepath.Join(t.TempDir(), "sub", ".env")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Created {
		t.Error("Created = false, want true")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != Template {
		t.Errorf("template not written: %v", err)
	}
	want := "postgres://postgres@localhost:5432/money_manager?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}

	again, err := LoadFrom(path)
	if err != nil || again.Created {
		t.Errorf("second load: Created=%v err=%v", again.Created, err)
	}
}

func TestSetValues(t *testing.T) {
	path := writeFile(t, "DB_PASSWORD=p#ss\r\n# WHISPER_MODEL=commented\r\nWHISPER_SERVER=old.exe\r\n")
	err := SetValues(path, "Voice input", []KV{
		{"WHISPER_SERVER", `C:\w\whisper-server.exe`},
		{"WHISPER_MODEL", `C:\w\model.bin`},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "DB_PASSWORD=p#ss\r\n# WHISPER_MODEL=commented\r\nWHISPER_SERVER=C:\\w\\whisper-server.exe\r\n" +
		"\r\n# Voice input\r\nWHISPER_MODEL=C:\\w\\model.bin\r\n"
	if string(data) != want {
		t.Errorf("got:\n%q\nwant:\n%q", data, want)
	}
	vals, err := ReadEnvFile(path)
	if err != nil || vals["DB_PASSWORD"] != "p#ss" || vals["WHISPER_MODEL"] != `C:\w\model.bin` {
		t.Errorf("read back: %v %v", vals, err)
	}

	// Setting again replaces in place without duplicating.
	if err := SetValues(path, "Voice input", []KV{{"WHISPER_MODEL", "new.bin"}}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if n := strings.Count(string(data), "WHISPER_MODEL=new.bin"); n != 1 || strings.Count(string(data), "# Voice input") != 1 {
		t.Errorf("not replaced in place:\n%s", data)
	}
}

func TestSetValuesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", ".env")
	if err := SetValues(path, "", []KV{{"A", "1"}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "A=1\n" {
		t.Errorf("got %q", data)
	}
}

func TestLoadPrecedence(t *testing.T) {
	path := writeFile(t, "DB_USER=vandika\nDB_PASSWORD=secret\nDB_NAME=mm\n")

	t.Setenv(EnvURL, "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://vandika:secret@localhost:5432/mm?sslmode=disable"; cfg.DatabaseURL != want {
		t.Errorf("DB_* fields: got %q, want %q", cfg.DatabaseURL, want)
	}
	if cfg.Source != path {
		t.Errorf("Source = %q, want %q", cfg.Source, path)
	}

	withURL := writeFile(t, "DB_PASSWORD=ignored\nDATABASE_URL=postgres://a:b@h:1/d\n")
	if cfg, _ := LoadFrom(withURL); cfg.DatabaseURL != "postgres://a:b@h:1/d" {
		t.Errorf("DATABASE_URL in file: got %q", cfg.DatabaseURL)
	}

	t.Setenv(EnvURL, "postgres://env:var@h:2/e")
	if cfg, _ := LoadFrom(path); cfg.DatabaseURL != "postgres://env:var@h:2/e" || cfg.Source != EnvURL+" environment variable" {
		t.Errorf("env var override: got %q from %q", cfg.DatabaseURL, cfg.Source)
	}
}
