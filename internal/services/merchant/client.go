package merchant

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
)

var ErrNotConfigured = errors.New("merchant facade client not configured")

const defaultTimeout = 15 * time.Second

type Client struct {
	baseURL    string
	appKey     string
	httpClient *http.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		baseURL: strings.TrimSpace(cfg.MerchantFacadeBaseURL),
		appKey:  strings.TrimSpace(cfg.MerchantFacadeAppKey),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

func (c *Client) Send(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
	if strings.TrimSpace(c.baseURL) == "" || strings.TrimSpace(c.appKey) == "" {
		return 0, "", nil, ErrNotConfigured
	}

	baseURL, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, "", nil, fmt.Errorf("%w: invalid merchant facade base url", ErrNotConfigured)
	}

	targetURL := baseURL.ResolveReference(&url.URL{Path: path})
	if len(query) > 0 {
		targetURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL.String(), bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Merchant-App-Key", c.appKey)
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", nil, err
	}

	return resp.StatusCode, resp.Header.Get("Content-Type"), respBody, nil
}
