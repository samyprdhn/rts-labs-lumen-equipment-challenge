package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// RepoRoot is where .env, db/ and data/ live. Defaults to the parent directory.
func RepoRoot() string {
	if r := os.Getenv("REPO_ROOT"); r != "" {
		return r
	}
	return ".."
}

// LoadEnv reads KEY=VALUE pairs from <repo>/.env without overriding existing env vars.
func LoadEnv() {
	f, err := os.Open(filepath.Join(RepoRoot(), ".env"))
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func Get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
