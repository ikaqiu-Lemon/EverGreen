package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const (
	KernelURLEnv   = "EG_KERNEL_URL"
	KernelTokenEnv = "EG_KERNEL_TOKEN"

	CallerCLI   = "cli"
	CallerAgent = "agent"
	CallerUI    = "ui"
)

var ErrKernelUnavailable = errors.New("Evergreen kernel endpoint is not configured")

type Client struct {
	baseURL string
	token   string
	caller  string
	http    *http.Client
}

type Options struct {
	BaseURL    string
	Token      string
	Caller     string
	HTTPClient *http.Client
}

func New(options Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Evergreen kernel URL %q", options.BaseURL)
	}
	switch options.Caller {
	case CallerCLI, CallerAgent, CallerUI:
	default:
		return nil, fmt.Errorf("invalid Evergreen caller %q", options.Caller)
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL: base, token: options.Token, caller: options.Caller, http: httpClient,
	}, nil
}

// FromEnvironment is the eg/Agent online-mode switch. Once EG_KERNEL_URL is
// present, callers must use this client and propagate any handshake or request
// failure. There is deliberately no direct-write fallback in this package.
func FromEnvironment(caller string) (*Client, error) {
	endpoint := strings.TrimSpace(os.Getenv(KernelURLEnv))
	if endpoint == "" {
		return nil, ErrKernelUnavailable
	}
	return New(Options{
		BaseURL: endpoint,
		Token:   os.Getenv(KernelTokenEnv),
		Caller:  caller,
	})
}

func (c *Client) Capabilities(ctx context.Context) (evergreencore.Capabilities, error) {
	var response evergreencore.APIResponse[evergreencore.Capabilities]
	if err := c.do(ctx, http.MethodGet, "/api/evergreen/v1/capabilities", nil, &response); err != nil {
		return evergreencore.Capabilities{}, err
	}
	if response.Data.APIVersion != evergreencore.APIVersion {
		return evergreencore.Capabilities{}, &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
			Code: evergreencore.CodeProtocolUnsupported, Level: "error", Path: "api_version",
			Message: fmt.Sprintf("kernel API %q is incompatible with client %q", response.Data.APIVersion, evergreencore.APIVersion),
		}}}
	}
	return response.Data, nil
}

func (c *Client) Schemas(ctx context.Context) (evergreencore.SchemaCatalog, error) {
	var response evergreencore.APIResponse[evergreencore.SchemaCatalog]
	if err := c.do(ctx, http.MethodGet, "/api/evergreen/v1/schemas", nil, &response); err != nil {
		return evergreencore.SchemaCatalog{}, err
	}
	return response.Data, nil
}

func (c *Client) Plan(ctx context.Context, request evergreencore.PlanRequest) (evergreencore.PlannedOperation, error) {
	var response evergreencore.APIResponse[evergreencore.PlannedOperation]
	if err := c.do(ctx, http.MethodPost, "/api/evergreen/v1/plan", request, &response); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return response.Data, nil
}

func (c *Client) Apply(ctx context.Context, request evergreencore.ApplyRequest) (evergreencore.OperationRecord, error) {
	var response evergreencore.APIResponse[evergreencore.OperationRecord]
	if err := c.do(ctx, http.MethodPost, "/api/evergreen/v1/apply", request, &response); err != nil {
		return response.Data, err
	}
	return response.Data, nil
}

func (c *Client) Operation(ctx context.Context, operationID string) (evergreencore.OperationRecord, error) {
	var response evergreencore.APIResponse[evergreencore.OperationRecord]
	path := "/api/evergreen/v1/operations/" + url.PathEscape(operationID)
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return evergreencore.OperationRecord{}, err
	}
	return response.Data, nil
}

func (c *Client) Recover(ctx context.Context) ([]evergreencore.OperationRecord, error) {
	var response evergreencore.APIResponse[[]evergreencore.OperationRecord]
	if err := c.do(ctx, http.MethodPost, "/api/evergreen/v1/recover", struct{}{}, &response); err != nil {
		return response.Data, err
	}
	return response.Data, nil
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Evergreen-Caller", c.caller)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Token "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Evergreen kernel request failed without direct-write fallback: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Evergreen kernel returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err = json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode Evergreen kernel response: %w", err)
	}
	envelope, ok := responseStatus(output)
	if !ok {
		return errors.New("Evergreen kernel response has no status envelope")
	}
	if envelope.OK {
		return nil
	}
	return &evergreencore.DiagnosticError{Diagnostics: envelope.Diagnostics}
}

type responseMeta struct {
	OK          bool
	Diagnostics []evergreencore.Diagnostic
}

func responseStatus(output any) (responseMeta, bool) {
	raw, err := json.Marshal(output)
	if err != nil {
		return responseMeta{}, false
	}
	var status struct {
		OK          *bool                      `json:"ok"`
		Diagnostics []evergreencore.Diagnostic `json:"diagnostics"`
	}
	if err = json.Unmarshal(raw, &status); err != nil || status.OK == nil {
		return responseMeta{}, false
	}
	return responseMeta{OK: *status.OK, Diagnostics: status.Diagnostics}, true
}
