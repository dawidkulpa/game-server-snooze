package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

func checkHealth(ctx context.Context, address string) error {
	endpoint, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("parse health endpoint: %w", err)
	}
	host := endpoint.Hostname()
	ip := net.ParseIP(host)
	if endpoint.Scheme != "http" || endpoint.User != nil || host == "" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return fmt.Errorf("health endpoint must be an HTTP loopback URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil) // #nosec G704 -- endpoint is restricted to loopback above.
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("health endpoint redirects are not allowed")
		},
	}
	response, err := client.Do(request) // #nosec G704 -- redirects are disabled and the validated endpoint is loopback.
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
