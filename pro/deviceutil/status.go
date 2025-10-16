// Package deviceutil provides utility functions for device management.
package deviceutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type responseBody struct {
	Id     string   `json:"id"`
	Result []result `json:"result"`
}

type result struct {
	Avalible bool   `json:"avalible"`
	Name     string `json:"name"`
}

// GetInputStatusIsAvalible checks device input status and returns count of available inputs.
func GetInputStatusIsAvalible(ip string) (int, error) {
	available := 0
	timestamp := time.Now().UnixMilli()
	requestBody := fmt.Sprintf(`{"id":%d,"jsonrpc": "2.0","method": "enc.getInputState"}`, timestamp)
	url := "http://" + ip + "/RPC"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte(requestBody)))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 1500 * time.Millisecond}

	resp, err := client.Do(req)
	if err != nil {
		return -1, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return -1, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, fmt.Errorf("read body failed: %w", err)
	}

	// 尝试解析 JSON，如果失败返回 0 而不是报错刷屏
	var rb responseBody
	if err := json.Unmarshal(body, &rb); err != nil {
		return 0, nil
	}

	for _, r := range rb.Result {
		if r.Avalible && (r.Name == "HDMI" || r.Name == "SDI") {
			available++
		}
	}
	return available, nil
}
