package rvideo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ReadStatus struct{}

type responseBody struct {
	Id     string   `json:"id"`
	Result []result `json:"result"`
}

type result struct {
	Avalible bool   `json:"avalible"`
	Name     string `json:"name"`
}

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
	client := &http.Client{
		Timeout: 1500 * time.Millisecond,
	}
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, err
	}

	var rb responseBody
	if err := json.Unmarshal(body, &rb); err == nil {
		// var available int = 0
		if len(rb.Result) > 0 {
			for _, r := range rb.Result {
				if r.Avalible && (r.Name == "HDMI" || r.Name == "SDI") {
					available++
					// streams = append(streams, r)
				}
			}
		}
		return available, nil
	} else {
		return -1, err
	}
}
