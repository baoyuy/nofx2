package lighter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	core "github.com/elliottech/lighter-go/client"
	"nofx/internal/apischeduler"
)

// The SDK hides its package-global HTTP client and exposes no injection option.
// Implement its two read-only methods locally so nonce/key reads share the same
// budget as the rest of Lighter. Signing and submission remain in the SDK.
type scheduledLighterHTTPClient struct {
	ctx     context.Context
	baseURL string
	client  *http.Client
}

var _ core.MinimalHTTPClient = (*scheduledLighterHTTPClient)(nil)

func newScheduledLighterHTTPClient(ctx context.Context, baseURL string) *scheduledLighterHTTPClient {
	return &scheduledLighterHTTPClient{ctx: ctx, baseURL: baseURL, client: &http.Client{Timeout: 30 * time.Second}}
}
func (c *scheduledLighterHTTPClient) read(path string, account int64, key uint8, out any) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	u.Path = path
	q := u.Query()
	q.Set("account_index", strconv.FormatInt(account, 10))
	q.Set("api_key_index", strconv.Itoa(int(key)))
	u.RawQuery = q.Encode()
	r, err := http.NewRequestWithContext(c.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := apischeduler.Do(c.client, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("Lighter SDK response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Lighter SDK read HTTP %d", resp.StatusCode)
	}
	var status struct {
		Code int `json:"code"`
	}
	if err = json.Unmarshal(raw, &status); err != nil {
		return err
	}
	if status.Code != 200 {
		return fmt.Errorf("Lighter SDK read rejected (code %d)", status.Code)
	}
	return json.Unmarshal(raw, out)
}
func (c *scheduledLighterHTTPClient) GetNextNonce(account int64, key uint8) (int64, error) {
	var result struct {
		Nonce *int64 `json:"nonce"`
	}
	if err := c.read("/api/v1/nextNonce", account, key, &result); err != nil {
		return -1, err
	}
	if result.Nonce == nil || *result.Nonce < 0 {
		return -1, fmt.Errorf("Lighter returned no valid nonce")
	}
	return *result.Nonce, nil
}
func (c *scheduledLighterHTTPClient) GetApiKey(account int64, key uint8) (string, error) {
	var result struct {
		Keys []struct {
			PublicKey string `json:"public_key"`
		} `json:"api_keys"`
	}
	if err := c.read("/api/v1/apikeys", account, key, &result); err != nil {
		return "", err
	}
	if len(result.Keys) == 0 || result.Keys[0].PublicKey == "" {
		return "", fmt.Errorf("Lighter returned no API public key")
	}
	return result.Keys[0].PublicKey, nil
}
