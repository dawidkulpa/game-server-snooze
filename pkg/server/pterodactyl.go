package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
)

type State string

const (
	StateOffline  State = "offline"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
)

type Controller interface {
	Status(context.Context) (State, error)
	Start(context.Context) error
	Stop(context.Context) error
}

type permanentError struct {
	err error
}

type redactedError struct {
	err     error
	message string
}

func (err redactedError) Error() string { return err.message }
func (err redactedError) Unwrap() error { return err.err }

func (err permanentError) Error() string   { return err.err.Error() }
func (err permanentError) Unwrap() error   { return err.err }
func (err permanentError) Permanent() bool { return true }

func IsPermanent(err error) bool {
	var target interface{ Permanent() bool }
	return errors.As(err, &target) && target.Permanent()
}

func markPermanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

var safeServerID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type PterodactylClient struct {
	baseURL    *url.URL
	apiToken   string
	serverID   string
	httpClient *http.Client
}

func NewPterodactylClient(cfg config.PterodactylConfig, httpClient *http.Client) (*PterodactylClient, error) {
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("invalid Pterodactyl base URL")
	}
	if baseURL.Scheme == "http" {
		host := baseURL.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("pterodactyl plaintext HTTP is restricted to loopback tests")
		}
	}
	if strings.TrimSpace(cfg.APIToken) == "" || !safeServerID.MatchString(cfg.ServerID) {
		return nil, fmt.Errorf("pterodactyl API token and valid server ID are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	safeClient := *httpClient
	if safeClient.Timeout <= 0 || safeClient.Timeout > 10*time.Second {
		safeClient.Timeout = 10 * time.Second
	}
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &PterodactylClient{
		baseURL:    baseURL,
		apiToken:   cfg.APIToken,
		serverID:   cfg.ServerID,
		httpClient: &safeClient,
	}, nil
}

func (client *PterodactylClient) Status(ctx context.Context) (State, error) {
	request, err := client.request(ctx, http.MethodGet, "resources", nil)
	if err != nil {
		return "", err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("get Pterodactyl status: %w", client.sanitizeError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", client.responseError("get Pterodactyl status", response)
	}
	statusBody, err := io.ReadAll(io.LimitReader(response.Body, maxPterodactylStatusBody+1))
	if err != nil {
		return "", fmt.Errorf("read Pterodactyl status: %w", err)
	}
	if len(statusBody) > maxPterodactylStatusBody {
		return "", markPermanent(fmt.Errorf("pterodactyl status response exceeds %d bytes", maxPterodactylStatusBody))
	}
	var payload struct {
		Attributes struct {
			CurrentState State `json:"current_state"`
		} `json:"attributes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(statusBody))
	if err := decoder.Decode(&payload); err != nil {
		return "", markPermanent(fmt.Errorf("decode Pterodactyl status: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", markPermanent(fmt.Errorf("decode Pterodactyl status: trailing JSON value"))
	}
	switch payload.Attributes.CurrentState {
	case StateOffline, StateStarting, StateRunning, StateStopping:
		return payload.Attributes.CurrentState, nil
	default:
		return "", markPermanent(fmt.Errorf("unknown Pterodactyl state"))
	}
}

func (client *PterodactylClient) Start(ctx context.Context) error {
	return client.power(ctx, "start")
}

func (client *PterodactylClient) Stop(ctx context.Context) error {
	return client.power(ctx, "stop")
}

func (client *PterodactylClient) power(ctx context.Context, signal string) error {
	body, err := json.Marshal(map[string]string{"signal": signal})
	if err != nil {
		return fmt.Errorf("encode Pterodactyl power signal: %w", err)
	}
	request, err := client.request(ctx, http.MethodPost, "power", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send Pterodactyl %s signal: %w", signal, client.sanitizeError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return client.responseError("send Pterodactyl "+signal+" signal", response)
	}
	return nil
}

const (
	maxPterodactylStatusBody = 64 * 1024
	maxPterodactylErrorBody  = 4096
)

func (client *PterodactylClient) responseError(operation string, response *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxPterodactylErrorBody+1))
	if readErr != nil {
		return classifyHTTPError(response.StatusCode, fmt.Errorf("%s: HTTP %d (read error body: %v)", operation, response.StatusCode, readErr))
	}
	truncated := len(body) > maxPterodactylErrorBody
	if truncated {
		body = body[:maxPterodactylErrorBody]
	}
	sanitized := client.sanitize(string(body))
	suffix := ""
	if truncated {
		suffix = " (truncated)"
	}
	return classifyHTTPError(response.StatusCode, fmt.Errorf("%s: HTTP %d body=%q%s", operation, response.StatusCode, sanitized, suffix))
}

func (client *PterodactylClient) sanitize(message string) string {
	message = strings.ReplaceAll(message, client.apiToken, "[REDACTED]")
	return strings.ReplaceAll(message, client.serverID, "[REDACTED]")
}

func (client *PterodactylClient) sanitizeError(err error) error {
	return redactedError{err: err, message: "Pterodactyl transport request failed"}
}

func classifyHTTPError(statusCode int, err error) error {
	if statusCode >= 400 && statusCode < 500 && statusCode != http.StatusRequestTimeout && statusCode != http.StatusTooManyRequests {
		return markPermanent(err)
	}
	return err
}

func (client *PterodactylClient) request(ctx context.Context, method, endpoint string, body *bytes.Reader) (*http.Request, error) {
	requestURL, err := url.JoinPath(client.baseURL.String(), "api", "client", "servers", client.serverID, endpoint)
	if err != nil {
		return nil, fmt.Errorf("construct Pterodactyl request URL: %w", err)
	}
	var request *http.Request
	if body == nil {
		request, err = http.NewRequestWithContext(ctx, method, requestURL, nil)
	} else {
		request, err = http.NewRequestWithContext(ctx, method, requestURL, body)
	}
	if err != nil {
		return nil, fmt.Errorf("construct Pterodactyl request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiToken)
	request.Header.Set("Accept", "application/json")
	return request, nil
}
