package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// RegisterAtGateway POSTs route metadata to the Gateway. The Gateway will
// forward /v1/search/* to our `target` URL. visual/hybrid/thumb live behind
// the same prefix in our Echo but the Gateway only knows about /v1/search
// as a whole — so within our server they exist and respond 503/404. To keep
// them out of EXTERNAL clients we don't register them; instead the Gateway
// only proxies the routes we explicitly enumerate when MVP requires that:
//   /v1/search/text, /v1/search/file, /v1/search/chunk, /v1/search/agent/*
// Wider prefix registration is acceptable for MVP (gateway forwards the
// prefix; our stubs respond 503/404 which is the documented behavior).
func RegisterAtGateway(gatewayBase, ourTarget, prefix string) error {
	body := map[string]any{"prefix": prefix, "target": ourTarget}
	buf, _ := json.Marshal(body)
	hc := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, gatewayBase+"/v1/gateway/routes", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gateway register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway register: %d", resp.StatusCode)
	}
	return nil
}
