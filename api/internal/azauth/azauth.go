// Package azauth gets Entra tokens from the Azure CLI (`az account get-access-token`).
// The user signs in once with `az login --use-device-code` (see scripts/setup.sh);
// the API reuses that session for Foundry, Azure OpenAI and Log Analytics.
package azauth

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	ScopeFoundry      = "https://ai.azure.com"
	ScopeCognitive    = "https://cognitiveservices.azure.com"
	ScopeLogAnalytics = "https://api.loganalytics.io"
)

type token struct {
	value   string
	expires time.Time
}

type CLI struct {
	Path  string
	mu    sync.Mutex
	cache map[string]token
}

func NewCLI(path string) *CLI { return &CLI{Path: path, cache: map[string]token{}} }

// Token returns a cached token for resource, refreshing it 5 minutes before expiry.
func (c *CLI) Token(ctx context.Context, resource string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.cache[resource]; ok && time.Until(t.expires) > 5*time.Minute {
		return t.value, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Path, "account", "get-access-token", "--resource", resource, "-o", "json").Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("az account get-access-token failed (run `make login`): %s", msg)
	}
	var r struct {
		AccessToken string `json:"accessToken"`
		ExpiresOn   int64  `json:"expires_on"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", fmt.Errorf("parse az token: %w", err)
	}
	exp := time.Unix(r.ExpiresOn, 0)
	if r.ExpiresOn == 0 {
		exp = time.Now().Add(30 * time.Minute)
	}
	c.cache[resource] = token{r.AccessToken, exp}
	return r.AccessToken, nil
}

// Account describes the signed-in identity.
type Account struct {
	User           string `json:"user"`
	TenantID       string `json:"tenantId"`
	SubscriptionID string `json:"subscriptionId"`
	Subscription   string `json:"subscription"`
}

func (c *CLI) Account(ctx context.Context) (*Account, error) {
	out, err := exec.CommandContext(ctx, c.Path, "account", "show", "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("not signed in to Azure CLI (run `make login`)")
	}
	var r struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		TenantID string `json:"tenantId"`
		User     struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return &Account{User: r.User.Name, TenantID: r.TenantID, SubscriptionID: r.ID, Subscription: r.Name}, nil
}
