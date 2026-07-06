package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigFromEnvUsesFallbacksAndSplitsPaths(t *testing.T) {
	cfg, err := configFromEnv([]string{
		"INFISICAL_TOKEN=token",
		"PROJECT_ID=project-a",
		"INFISICAL_SECRET_ENV=prod",
		"INFISICAL_PATHS=/common, /service ,,/last",
		"INFISICAL_TAGS=blue,api",
		"INFISICAL_EXPAND=false",
		"INFISICAL_INCLUDE_IMPORTS=true",
	})
	if err != nil {
		t.Fatalf("configFromEnv returned error: %v", err)
	}

	if cfg.ProjectID != "project-a" {
		t.Fatalf("ProjectID = %q", cfg.ProjectID)
	}
	if cfg.Environment != "prod" {
		t.Fatalf("Environment = %q", cfg.Environment)
	}
	wantPaths := []string{"/common", "/service", "/last"}
	if !reflect.DeepEqual(cfg.Paths, wantPaths) {
		t.Fatalf("Paths = %#v, want %#v", cfg.Paths, wantPaths)
	}
}

func TestConfigFromEnvRequiresProjectAndCredentials(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want string
	}{
		{
			name: "missing project",
			env:  []string{"INFISICAL_TOKEN=token"},
			want: "INFISICAL_PROJECT_ID or PROJECT_ID is required",
		},
		{
			name: "missing credentials",
			env:  []string{"INFISICAL_PROJECT_ID=project-a"},
			want: "INFISICAL_TOKEN or both INFISICAL_MACHINE_CLIENT_ID and INFISICAL_MACHINE_CLIENT_SECRET are required",
		},
		{
			name: "partial machine credentials",
			env:  []string{"INFISICAL_PROJECT_ID=project-a", "INFISICAL_MACHINE_CLIENT_ID=id"},
			want: "both INFISICAL_MACHINE_CLIENT_ID and INFISICAL_MACHINE_CLIENT_SECRET must be set together",
		},
		{
			name: "invalid bool",
			env:  []string{"INFISICAL_PROJECT_ID=project-a", "INFISICAL_TOKEN=token", "INFISICAL_EXPAND=yes"},
			want: "INFISICAL_EXPAND must be either true or false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := configFromEnv(tt.env)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigFromEnvReadsTokenFromDockerSecretName(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "infisical_token"), []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := configFromEnvWithSecretsDir([]string{
		"INFISICAL_TOKEN=env-token",
		"INFISICAL_TOKEN_SECRET_NAME=infisical_token",
		"INFISICAL_PROJECT_ID=project-a",
	}, tmp)
	if err != nil {
		t.Fatalf("configFromEnvWithSecretsDir returned error: %v", err)
	}

	if cfg.Token != "file-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "file-token")
	}
}

func TestConfigFromEnvReadsClientSecretFromDockerSecretName(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "infisical_client_secret"), []byte("file-client-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := configFromEnvWithSecretsDir([]string{
		"INFISICAL_PROJECT_ID=project-a",
		"INFISICAL_MACHINE_CLIENT_ID=client-id",
		"INFISICAL_MACHINE_CLIENT_SECRET=env-client-secret",
		"INFISICAL_MACHINE_CLIENT_SECRET_SECRET_NAME=infisical_client_secret",
	}, tmp)
	if err != nil {
		t.Fatalf("configFromEnvWithSecretsDir returned error: %v", err)
	}

	if cfg.ClientSecret != "file-client-secret" {
		t.Fatalf("ClientSecret = %q, want %q", cfg.ClientSecret, "file-client-secret")
	}
}

func TestConfigFromEnvErrorsForMissingDockerSecretFile(t *testing.T) {
	_, err := configFromEnvWithSecretsDir([]string{
		"INFISICAL_TOKEN=env-token",
		"INFISICAL_TOKEN_SECRET_NAME=missing_token",
		"INFISICAL_PROJECT_ID=project-a",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "failed to read Docker secret INFISICAL_TOKEN") {
		t.Fatalf("error = %v, want Docker secret read error", err)
	}
}

func TestConfigFromEnvErrorsForEmptyDockerSecretFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "empty_token"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := configFromEnvWithSecretsDir([]string{
		"INFISICAL_TOKEN=env-token",
		"INFISICAL_TOKEN_SECRET_NAME=empty_token",
		"INFISICAL_PROJECT_ID=project-a",
	}, tmp)
	if err == nil || !strings.Contains(err.Error(), "Docker secret INFISICAL_TOKEN") || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("error = %v, want Docker secret empty error", err)
	}
}

func TestBuildExportArgs(t *testing.T) {
	cfg := config{
		ProjectID:      "project-a",
		Environment:    "prod",
		APIURL:         "https://infisical.example.com",
		Paths:          []string{"/common", "/api"},
		Tags:           "blue,api",
		Expand:         "false",
		IncludeImports: "true",
	}

	got := buildExportArgs(cfg)
	want := []string{
		"export",
		"--format=json",
		"--projectId", "project-a",
		"--env", "prod",
		"--domain", "https://infisical.example.com",
		"--path", "/common",
		"--path", "/api",
		"--tags", "blue,api",
		"--expand=false",
		"--include-imports=true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildExportArgs = %#v, want %#v", got, want)
	}
}

func TestParseSecretsJSON(t *testing.T) {
	secrets, err := parseSecretsJSON([]byte(`{"DATABASE_URL":"postgres://db","COUNT":2,"EMPTY":null}`))
	if err != nil {
		t.Fatalf("parseSecretsJSON returned error: %v", err)
	}

	want := map[string]string{
		"DATABASE_URL": "postgres://db",
		"COUNT":        "2",
		"EMPTY":        "",
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %#v, want %#v", secrets, want)
	}
}

func TestParseSecretsJSONRejectsInvalidEnvName(t *testing.T) {
	_, err := parseSecretsJSON([]byte(`{"BAD=NAME":"value"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid Infisical secret name") {
		t.Fatalf("error = %v", err)
	}
}

func TestAliasesFromMappingLastWins(t *testing.T) {
	secrets := map[string]string{
		"SERVICE_1_DATABASE_URL": "postgres://one",
		"SERVICE_2_DATABASE_URL": "postgres://two",
	}
	mapping := []byte(`
SERVICE_1_DATABASE_URL:
  aliases:
    - POSTGRES_URL
SERVICE_2_DATABASE_URL:
  aliases:
    - POSTGRES_URL
    - SERVICE_DATABASE_URL
`)

	aliases, err := aliasesFromMapping(mapping, secrets)
	if err != nil {
		t.Fatalf("aliasesFromMapping returned error: %v", err)
	}

	want := map[string]string{
		"POSTGRES_URL":         "postgres://two",
		"SERVICE_DATABASE_URL": "postgres://two",
	}
	if !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %#v, want %#v", aliases, want)
	}
}

func TestAliasesFromMappingRejectsMissingSourceSecret(t *testing.T) {
	_, err := aliasesFromMapping([]byte(`
MISSING_SECRET:
  aliases:
    - POSTGRES_URL
`), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), `missing Infisical secret "MISSING_SECRET"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestAliasesFromMappingRejectsInvalidAliasName(t *testing.T) {
	_, err := aliasesFromMapping([]byte(`
DATABASE_URL:
  aliases:
    - BAD=NAME
`), map[string]string{"DATABASE_URL": "postgres://db"})
	if err == nil || !strings.Contains(err.Error(), `invalid alias "BAD=NAME"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunFetchesSecretsAppliesMappingAndExecsApp(t *testing.T) {
	tmp := t.TempDir()
	mapping := []byte(`
SERVICE_1_DATABASE_URL:
  aliases:
    - POSTGRES_URL
SERVICE_2_DATABASE_URL:
  aliases:
    - POSTGRES_URL
`)
	if err := os.WriteFile(filepath.Join(tmp, mappingFileName), mapping, 0o600); err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	runner := func(_ context.Context, args []string, env []string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "export" {
			if !envContains(env, "INFISICAL_TOKEN=token") {
				t.Fatalf("export env did not include token: %#v", env)
			}
			return []byte(`{"SERVICE_1_DATABASE_URL":"postgres://one","SERVICE_2_DATABASE_URL":"postgres://two"}`), nil
		}
		return nil, errors.New("unexpected command")
	}

	var execArgs []string
	var execEnv []string
	execer := func(args []string, env []string) error {
		execArgs = append([]string(nil), args...)
		execEnv = append([]string(nil), env...)
		return nil
	}

	err := run(context.Background(), []string{"printenv"}, []string{
		"PATH=/usr/bin",
		"INFISICAL_TOKEN=token",
		"INFISICAL_PROJECT_ID=project-a",
	}, tmp, runner, execer)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if len(calls) != 1 || calls[0][0] != "export" {
		t.Fatalf("calls = %#v", calls)
	}
	if !reflect.DeepEqual(execArgs, []string{"printenv"}) {
		t.Fatalf("exec args = %#v", execArgs)
	}
	if !envContains(execEnv, "SERVICE_1_DATABASE_URL=postgres://one") {
		t.Fatalf("exec env missing SERVICE_1_DATABASE_URL: %#v", execEnv)
	}
	if !envContains(execEnv, "POSTGRES_URL=postgres://two") {
		t.Fatalf("exec env missing last-wins POSTGRES_URL: %#v", execEnv)
	}
}

func TestRunLogsInWhenTokenIsMissing(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, args []string, _ []string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch args[0] {
		case "login":
			return []byte("fresh-token\n"), nil
		case "export":
			return []byte(`{"API_KEY":"secret"}`), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}

	execer := func(_ []string, env []string) error {
		if !envContains(env, "API_KEY=secret") {
			t.Fatalf("exec env missing API_KEY: %#v", env)
		}
		return nil
	}

	err := run(context.Background(), []string{"app"}, []string{
		"PATH=/usr/bin",
		"INFISICAL_PROJECT_ID=project-a",
		"INFISICAL_MACHINE_CLIENT_ID=client-id",
		"INFISICAL_MACHINE_CLIENT_SECRET=client-secret",
	}, t.TempDir(), runner, execer)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if len(calls) != 2 || calls[0][0] != "login" || calls[1][0] != "export" {
		t.Fatalf("calls = %#v", calls)
	}
}

func envContains(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
