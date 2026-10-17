// Package config resolves the database connection from, in order of precedence:
// the MONEY_MANAGER_DATABASE_URL environment variable, the user's .env file
// (DATABASE_URL, then DB_* fields), and built-in defaults.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvURL = "MONEY_MANAGER_DATABASE_URL"
	appDir = "money-manager-vandika"
)

// Template is written to the .env path on first run.
const Template = `# Money Manager database settings.
# Fill in your PostgreSQL password below, save, then run money-manager-vandika again.
# Values are used exactly as written: no quotes or escaping needed, even for
# passwords with special characters like @ : / # %.

DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=
DB_NAME=money_manager
DB_SSLMODE=disable

# Alternatively, set a full connection URL (overrides the DB_* values above):
# DATABASE_URL=postgres://user:password@localhost:5432/money_manager?sslmode=disable
`

var defaults = map[string]string{
	"DB_HOST":    "localhost",
	"DB_PORT":    "5432",
	"DB_USER":    "postgres",
	"DB_NAME":    "money_manager",
	"DB_SSLMODE": "disable",
}

type Config struct {
	DatabaseURL string
	Source      string // where DatabaseURL came from, for messages
	EnvPath     string // the .env file location (whether or not it was used)
	Created     bool   // the .env template was created by this Load
}

// Path returns the .env location, e.g. %APPDATA%\money-manager-vandika\.env on Windows.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDir, ".env"), nil
}

// Load resolves the configuration using the default .env path.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(path)
}

// LoadFrom resolves the configuration using the .env file at path, creating it from
// Template if it does not exist.
func LoadFrom(path string) (Config, error) {
	cfg := Config{EnvPath: path}
	if u := os.Getenv(EnvURL); u != "" {
		cfg.DatabaseURL, cfg.Source = u, EnvURL+" environment variable"
		return cfg, nil
	}

	vals, err := ReadEnvFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := writeTemplate(path); err != nil {
			return cfg, fmt.Errorf("create %s: %w", path, err)
		}
		cfg.Created = true
		vals, err = ReadEnvFile(path)
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	cfg.Source = path
	if u := vals["DATABASE_URL"]; u != "" {
		cfg.DatabaseURL = u
		return cfg, nil
	}
	get := func(key string) string {
		if v := vals[key]; v != "" {
			return v
		}
		return defaults[key]
	}
	cfg.DatabaseURL = BuildURL(get("DB_HOST"), get("DB_PORT"), get("DB_USER"),
		vals["DB_PASSWORD"], get("DB_NAME"), get("DB_SSLMODE"))
	return cfg, nil
}

// BuildURL assembles a postgres:// URL, escaping every part.
func BuildURL(host, port, user, password, dbname, sslmode string) string {
	u := url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + dbname,
		RawQuery: url.Values{"sslmode": {sslmode}}.Encode(),
	}
	if password != "" {
		u.User = url.UserPassword(user, password)
	} else {
		u.User = url.User(user)
	}
	return u.String()
}

// ReadEnvFile parses KEY=VALUE lines. Blank lines and lines starting with # are
// ignored; values are taken literally (a # inside a value is kept), with one pair of
// matching surrounding quotes removed if present.
func ReadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if n == 1 {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf") // BOM written by some Windows editors
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE, got %q", n, line)
		}
		vals[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return vals, sc.Err()
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// KV is one KEY=VALUE pair for SetValues.
type KV struct{ Key, Value string }

// SetValues writes the given keys into the .env file at path. Existing (uncommented)
// lines for those keys are replaced in place; missing keys are appended under a
// "# header" comment. All other lines, comments and line endings are kept.
func SetValues(path, header string, values []KV) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(data)
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	}

	done := map[string]bool{}
	for i, line := range lines {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "\xef\xbb\xbf")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		for _, kv := range values {
			if strings.TrimSpace(key) == kv.Key {
				prefix := ""
				if i == 0 && strings.HasPrefix(line, "\xef\xbb\xbf") {
					prefix = "\xef\xbb\xbf"
				}
				lines[i] = prefix + kv.Key + "=" + kv.Value
				done[kv.Key] = true
			}
		}
	}

	var missing []string
	for _, kv := range values {
		if !done[kv.Key] {
			missing = append(missing, kv.Key+"="+kv.Value)
		}
	}
	if len(missing) > 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		if header != "" {
			lines = append(lines, "# "+header)
		}
		lines = append(lines, missing...)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, nl)+nl), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Template), 0o600)
}
