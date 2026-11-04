// Package kamailio is a JSON-RPC client for Kamailio's dispatcher module.
package kamailio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/dispatch"
)

// ErrOngoingReload is returned when Kamailio's dispatcher module rejects a
// call because it is in the middle of rebuilding its list. Callers can
// distinguish this from other errors via errors.Is(err, ErrOngoingReload).
var ErrOngoingReload = errors.New("kamailio dispatcher: ongoing reload")

// Client is a JSON-RPC client for Kamailio's dispatcher module.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient returns a Client that POSTs JSON-RPC requests to baseURL.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

type rpcErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a JSON-RPC 2.0 request and returns the raw response body on
// success, or an error (wrapping ErrOngoingReload when applicable) on a
// non-2xx response.
func (c *Client) call(ctx context.Context, method string, params interface{}) ([]byte, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	})
	if err != nil {
		return nil, fmt.Errorf("kamailio rpc %s: marshal request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("kamailio rpc %s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kamailio rpc %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kamailio rpc %s: read response: %w", method, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp rpcErrorResponse
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.Error.Message == "Ongoing reload" {
			return nil, fmt.Errorf("kamailio rpc %s: %w", method, ErrOngoingReload)
		} else if jsonErr == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("kamailio rpc %s: status %d: %s", method, resp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("kamailio rpc %s: status %d", method, resp.StatusCode)
	}

	return body, nil
}

type listResponse struct {
	Result struct {
		NRSETS  int `json:"NRSETS"`
		RECORDS []struct {
			SET struct {
				ID      int `json:"ID"`
				TARGETS []struct {
					DEST struct {
						URI string `json:"URI"`
					} `json:"DEST"`
				} `json:"TARGETS"`
			} `json:"SET"`
		} `json:"RECORDS"`
	} `json:"result"`
}

// List returns the set of IPs currently in the given dispatcher group.
func (c *Client) List(ctx context.Context, group int) (dispatch.IPSet, error) {
	body, err := c.call(ctx, "dispatcher.list", []interface{}{})
	if err != nil {
		return nil, err
	}

	var lr listResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("kamailio rpc dispatcher.list: decode response: %w", err)
	}

	result := dispatch.IPSet{}
	for _, rec := range lr.Result.RECORDS {
		if rec.SET.ID != group {
			continue
		}
		for _, t := range rec.SET.TARGETS {
			parts := strings.Split(t.DEST.URI, ":")
			if len(parts) > 1 {
				result[parts[1]] = true
			}
		}
	}
	return result, nil
}

// Add adds uri to the given dispatcher group.
func (c *Client) Add(ctx context.Context, group int, uri string) error {
	_, err := c.call(ctx, "dispatcher.add", []interface{}{group, uri, 0, 0})
	return err
}

// Remove removes uri from the given dispatcher group.
func (c *Client) Remove(ctx context.Context, group int, uri string) error {
	_, err := c.call(ctx, "dispatcher.remove", []interface{}{group, uri})
	return err
}
