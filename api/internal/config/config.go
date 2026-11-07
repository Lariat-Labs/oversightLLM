// Package config loads settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	// Required
	ProjectEndpoint string // FOUNDRY_PROJECT_ENDPOINT, e.g. https://<res>.services.ai.azure.com/api/projects/<project>
	WorkspaceID     string // LOG_ANALYTICS_WORKSPACE_ID (customerId GUID of the App Insights workspace)
	JudgeDeployment string // JUDGE_MODEL_DEPLOYMENT, e.g. gpt-5-mini

	// Optional
	OpenAIEndpoint string        // AZURE_OPENAI_ENDPOINT, derived from the project endpoint when unset
	EvalDeployment string        // OVERSIGHT_EVAL_DEPLOYMENT, grader model for Foundry evals (default: judge)
	Port           string        // OVERSIGHT_PORT (default 8080)
	Host           string        // OVERSIGHT_HOST (default 127.0.0.1: single user, local only)
	AutoJudge      bool          // OVERSIGHT_AUTO_JUDGE initial state of the auto-judge switch (default false)
	CORSOrigins    []string      // OVERSIGHT_CORS_ORIGINS, comma separated (default *)
	PollInterval   time.Duration // OVERSIGHT_POLL_INTERVAL (default 15s)
	Lookback       time.Duration // OVERSIGHT_LOOKBACK (default 168h)
	DataDir        string        // OVERSIGHT_DATA_DIR (default ./data)
	WebDir         string        // OVERSIGHT_WEB_DIR, serve the built frontend from here (optional)
	AzCLIPath      string        // AZ_CLI_PATH (default "az")
	MaxSpans       int           // OVERSIGHT_MAX_SPANS rows per activity query (default 5000)
	AppInsightsCS  string        // APPLICATIONINSIGHTS_CONNECTION_STRING: traces the judge's own calls (optional)
}

var Required = []string{"FOUNDRY_PROJECT_ENDPOINT", "LOG_ANALYTICS_WORKSPACE_ID", "JUDGE_MODEL_DEPLOYMENT"}

// LoadDotEnv sets variables from a KEY=VALUE file without overriding the real environment.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
			if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
				v = v[1 : end+1]
			}
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func Load() (*Config, error) {
	var missing []string
	for _, k := range Required {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s (run `make setup`)", strings.Join(missing, ", "))
	}
	c := &Config{
		ProjectEndpoint: strings.TrimRight(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"), "/"),
		WorkspaceID:     os.Getenv("LOG_ANALYTICS_WORKSPACE_ID"),
		JudgeDeployment: os.Getenv("JUDGE_MODEL_DEPLOYMENT"),
		OpenAIEndpoint:  strings.TrimRight(os.Getenv("AZURE_OPENAI_ENDPOINT"), "/"),
		EvalDeployment:  env("OVERSIGHT_EVAL_DEPLOYMENT", os.Getenv("JUDGE_MODEL_DEPLOYMENT")),
		Port:            env("OVERSIGHT_PORT", "8080"),
		Host:            env("OVERSIGHT_HOST", "127.0.0.1"),
		AutoJudge:       strings.EqualFold(env("OVERSIGHT_AUTO_JUDGE", "false"), "true"),
		DataDir:         env("OVERSIGHT_DATA_DIR", "./data"),
		WebDir:          os.Getenv("OVERSIGHT_WEB_DIR"),
		AzCLIPath:       env("AZ_CLI_PATH", "az"),
		AppInsightsCS:   os.Getenv("APPLICATIONINSIGHTS_CONNECTION_STRING"),
	}
	for _, o := range strings.Split(env("OVERSIGHT_CORS_ORIGINS", "*"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	var err error
	if c.PollInterval, err = time.ParseDuration(env("OVERSIGHT_POLL_INTERVAL", "15s")); err != nil {
		return nil, fmt.Errorf("OVERSIGHT_POLL_INTERVAL: %w", err)
	}
	if c.Lookback, err = time.ParseDuration(env("OVERSIGHT_LOOKBACK", "168h")); err != nil {
		return nil, fmt.Errorf("OVERSIGHT_LOOKBACK: %w", err)
	}
	if _, err := fmt.Sscan(env("OVERSIGHT_MAX_SPANS", "5000"), &c.MaxSpans); err != nil {
		return nil, fmt.Errorf("OVERSIGHT_MAX_SPANS: %w", err)
	}
	u, err := url.Parse(c.ProjectEndpoint)
	if err != nil || u.Host == "" || !strings.Contains(u.Path, "/api/projects/") {
		return nil, fmt.Errorf("FOUNDRY_PROJECT_ENDPOINT must look like https://<resource>.services.ai.azure.com/api/projects/<project>")
	}
	if c.WebDir == "" {
		c.WebDir = bundledWebDir()
	}
	if c.OpenAIEndpoint == "" {
		c.OpenAIEndpoint = DeriveOpenAIEndpoint(c.ProjectEndpoint)
	}
	return c, nil
}

// DeriveOpenAIEndpoint maps https://<res>.services.ai.azure.com/api/projects/<p>
// to https://<res>.openai.azure.com/openai/v1.
func DeriveOpenAIEndpoint(project string) string {
	u, err := url.Parse(project)
	if err != nil {
		return ""
	}
	res, _, _ := strings.Cut(u.Host, ".")
	return "https://" + res + ".openai.azure.com/openai/v1"
}

// ProjectName returns the project segment of the endpoint.
func (c *Config) ProjectName() string {
	_, p, _ := strings.Cut(c.ProjectEndpoint, "/api/projects/")
	return p
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// bundledWebDir returns the "web" folder next to the executable (release archives ship
// the UI there), or "" when it isn't present (go run, local dev).
func bundledWebDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Join(filepath.Dir(exe), "web")
	if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
		return dir
	}
	return ""
}
