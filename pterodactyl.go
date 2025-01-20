package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type PterodactylController struct {
	httpClient *http.Client
	cfg        PterodactylConfig
}

func NewPterodactylController(cfg PterodactylConfig) *PterodactylController {
	return &PterodactylController{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cfg: cfg,
	}
}

func (pc *PterodactylController) StartServer() error {
	return pc.sendPowerSignal("start")
}

func (pc *PterodactylController) StopServer() error {
	return pc.sendPowerSignal("stop")
}

func (pc *PterodactylController) GetStatus() (string, error) {
	url := fmt.Sprintf("%s/api/client/servers/%s/resources", pc.cfg.BaseURL, pc.cfg.ServerID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+pc.cfg.APIToken)
	req.Header.Set("Accept", "application/json")

	resp, err := pc.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GetStatus: non-2xx code %d. Body: %s",
			resp.StatusCode, string(bodyBytes))
	}

	var data struct {
		Object     string `json:"object"`
		Attributes struct {
			CurrentState string `json:"current_state"`
		} `json:"attributes"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("GetStatus: failed to parse JSON: %w", err)
	}

	return data.Attributes.CurrentState, nil
}

func (pc *PterodactylController) sendPowerSignal(signal string) error {
	url := fmt.Sprintf("%s/api/client/servers/%s/power", pc.cfg.BaseURL, pc.cfg.ServerID)

	body, err := json.Marshal(map[string]string{"signal": signal})
	if err != nil {
		return fmt.Errorf("sendPowerSignal: JSON marshal error: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+pc.cfg.APIToken)

	resp, err := pc.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendPowerSignal(%s): got HTTP %d, body=%s",
			signal, resp.StatusCode, string(respBody))
	}

	return nil
}
