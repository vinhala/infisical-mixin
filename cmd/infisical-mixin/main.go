package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const (
	defaultInfisicalBin = "infisical"
	mappingFileName     = "infisical_mapping.yml"
)

type config struct {
	Token          string
	ClientID       string
	ClientSecret   string
	ProjectID      string
	Environment    string
	APIURL         string
	Paths          []string
	Tags           string
	Expand         string
	IncludeImports string
}

type infisicalRunner func(ctx context.Context, args []string, env []string) ([]byte, error)
type processExecer func(args []string, env []string) error

func main() {
	if err := run(context.Background(), os.Args[1:], os.Environ(), ".", realInfisicalRunner, realExec); err != nil {
		fmt.Fprintf(os.Stderr, "infisical-mixin: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, appArgs []string, baseEnv []string, workingDir string, runner infisicalRunner, execer processExecer) error {
	if len(appArgs) == 0 {
		return errors.New("no application command provided")
	}

	cfg, err := configFromEnv(baseEnv)
	if err != nil {
		return err
	}

	token := cfg.Token
	infisicalEnv := setEnv(baseEnv, "INFISICAL_DISABLE_UPDATE_CHECK", "true")
	if token == "" {
		tokenBytes, err := runner(ctx, buildLoginArgs(cfg), infisicalEnv)
		if err != nil {
			return fmt.Errorf("failed to authenticate with Infisical: %w", err)
		}
		token = strings.TrimSpace(string(tokenBytes))
		if token == "" {
			return errors.New("Infisical login returned an empty token")
		}
	}
	infisicalEnv = setEnv(infisicalEnv, "INFISICAL_TOKEN", token)

	exportBytes, err := runner(ctx, buildExportArgs(cfg), infisicalEnv)
	if err != nil {
		return fmt.Errorf("failed to export Infisical secrets: %w", err)
	}

	secrets, err := parseSecretsJSON(exportBytes)
	if err != nil {
		return err
	}

	mappingPath := filepath.Join(workingDir, mappingFileName)
	if _, err := os.Stat(mappingPath); err == nil {
		aliases, err := aliasesFromMappingFile(mappingPath, secrets)
		if err != nil {
			return err
		}
		for key, value := range aliases {
			secrets[key] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect %s: %w", mappingFileName, err)
	}

	finalEnv, err := mergeEnv(baseEnv, secrets)
	if err != nil {
		return err
	}

	return execer(appArgs, finalEnv)
}

func configFromEnv(env []string) (config, error) {
	values := envMap(env)
	cfg := config{
		Token:          values["INFISICAL_TOKEN"],
		ClientID:       values["INFISICAL_MACHINE_CLIENT_ID"],
		ClientSecret:   values["INFISICAL_MACHINE_CLIENT_SECRET"],
		ProjectID:      firstNonEmpty(values["INFISICAL_PROJECT_ID"], values["PROJECT_ID"]),
		Environment:    firstNonEmpty(values["INFISICAL_ENV"], values["INFISICAL_SECRET_ENV"]),
		APIURL:         values["INFISICAL_API_URL"],
		Paths:          splitList(values["INFISICAL_PATHS"]),
		Tags:           values["INFISICAL_TAGS"],
		Expand:         values["INFISICAL_EXPAND"],
		IncludeImports: values["INFISICAL_INCLUDE_IMPORTS"],
	}

	if cfg.ProjectID == "" {
		return config{}, errors.New("INFISICAL_PROJECT_ID or PROJECT_ID is required")
	}
	if cfg.ClientID == "" && cfg.ClientSecret != "" || cfg.ClientID != "" && cfg.ClientSecret == "" {
		return config{}, errors.New("both INFISICAL_MACHINE_CLIENT_ID and INFISICAL_MACHINE_CLIENT_SECRET must be set together")
	}
	if cfg.Token == "" {
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			return config{}, errors.New("INFISICAL_TOKEN or both INFISICAL_MACHINE_CLIENT_ID and INFISICAL_MACHINE_CLIENT_SECRET are required")
		}
	}
	if err := validateOptionalBool("INFISICAL_EXPAND", cfg.Expand); err != nil {
		return config{}, err
	}
	if err := validateOptionalBool("INFISICAL_INCLUDE_IMPORTS", cfg.IncludeImports); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func buildLoginArgs(cfg config) []string {
	args := []string{
		"login",
		"--method=universal-auth",
		"--client-id", cfg.ClientID,
		"--client-secret", cfg.ClientSecret,
		"--plain",
		"--silent",
	}
	if cfg.APIURL != "" {
		args = append(args, "--domain", cfg.APIURL)
	}
	return args
}

func buildExportArgs(cfg config) []string {
	args := []string{
		"export",
		"--format=json",
		"--projectId", cfg.ProjectID,
	}
	if cfg.Environment != "" {
		args = append(args, "--env", cfg.Environment)
	}
	if cfg.APIURL != "" {
		args = append(args, "--domain", cfg.APIURL)
	}
	for _, path := range cfg.Paths {
		args = append(args, "--path", path)
	}
	if cfg.Tags != "" {
		args = append(args, "--tags", cfg.Tags)
	}
	if cfg.Expand != "" {
		args = append(args, "--expand="+cfg.Expand)
	}
	if cfg.IncludeImports != "" {
		args = append(args, "--include-imports="+cfg.IncludeImports)
	}
	return args
}

func parseSecretsJSON(data []byte) (map[string]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Infisical JSON export: %w", err)
	}

	secrets := make(map[string]string, len(raw))
	for key, value := range raw {
		if err := validateEnvName(key); err != nil {
			return nil, fmt.Errorf("invalid Infisical secret name %q: %w", key, err)
		}
		switch typed := value.(type) {
		case string:
			secrets[key] = typed
		case nil:
			secrets[key] = ""
		default:
			secrets[key] = fmt.Sprint(typed)
		}
	}
	return secrets, nil
}

func aliasesFromMappingFile(path string, secrets map[string]string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", mappingFileName, err)
	}
	return aliasesFromMapping(data, secrets)
}

func aliasesFromMapping(data []byte, secrets map[string]string) (map[string]string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", mappingFileName, err)
	}
	if len(root.Content) == 0 {
		return map[string]string{}, nil
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must contain a top-level mapping", mappingFileName)
	}

	aliases := make(map[string]string)
	for i := 0; i < len(doc.Content); i += 2 {
		source := doc.Content[i].Value
		sourceValue, ok := secrets[source]
		if !ok {
			return nil, fmt.Errorf("%s references missing Infisical secret %q", mappingFileName, source)
		}

		aliasNodes, err := aliasesForSource(doc.Content[i+1], source)
		if err != nil {
			return nil, err
		}
		for _, alias := range aliasNodes {
			if err := validateEnvName(alias); err != nil {
				return nil, fmt.Errorf("invalid alias %q for %q: %w", alias, source, err)
			}
			aliases[alias] = sourceValue
		}
	}
	return aliases, nil
}

func aliasesForSource(node *yaml.Node, source string) ([]string, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s entry for %q must be a mapping", mappingFileName, source)
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "aliases" {
			continue
		}
		aliases := node.Content[i+1]
		if aliases.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s aliases for %q must be a list", mappingFileName, source)
		}
		values := make([]string, 0, len(aliases.Content))
		for _, item := range aliases.Content {
			if item.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("%s aliases for %q must contain only scalar values", mappingFileName, source)
			}
			values = append(values, item.Value)
		}
		return values, nil
	}
	return nil, fmt.Errorf("%s entry for %q must contain aliases", mappingFileName, source)
}

func mergeEnv(base []string, values map[string]string) ([]string, error) {
	merged := make([]string, 0, len(base)+len(values))
	indexByKey := make(map[string]int, len(base)+len(values))

	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if err := validateEnvName(key); err != nil {
			return nil, fmt.Errorf("invalid existing environment name %q: %w", key, err)
		}
		if existing, ok := indexByKey[key]; ok {
			merged[existing] = item
			continue
		}
		indexByKey[key] = len(merged)
		merged = append(merged, item)
	}

	for key, value := range values {
		if err := validateEnvName(key); err != nil {
			return nil, fmt.Errorf("invalid environment name %q: %w", key, err)
		}
		item := key + "=" + value
		if existing, ok := indexByKey[key]; ok {
			merged[existing] = item
			continue
		}
		indexByKey[key] = len(merged)
		merged = append(merged, item)
	}

	return merged, nil
}

func realInfisicalRunner(ctx context.Context, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, defaultInfisicalBin, args...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

func realExec(args []string, env []string) error {
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			_ = os.Setenv(key, value)
		}
	}

	binary, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("failed to find application command %q: %w", args[0], err)
	}
	return syscall.Exec(binary, args, env)
}

func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func setEnv(env []string, key string, value string) []string {
	next, _ := mergeEnv(env, map[string]string{key: value})
	return next
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func validateEnvName(name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	if strings.Contains(name, "=") {
		return errors.New("name cannot contain '='")
	}
	return nil
}

func validateOptionalBool(name string, value string) error {
	if value == "" || value == "true" || value == "false" {
		return nil
	}
	return fmt.Errorf("%s must be either true or false", name)
}
