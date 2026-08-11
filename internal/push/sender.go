package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nicremo/state/internal/pushcrypto"
)

type Sender interface {
	Send(context.Context, DeviceRoute, string, string, []byte) error
}

type HTTPSender struct {
	client *http.Client
}

func NewHTTPSender(client *http.Client) *HTTPSender {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPSender{client: client}
}

func (sender *HTTPSender) Send(ctx context.Context, route DeviceRoute, kind string, collapseID string, plaintext []byte) error {
	if !validRelayURL(route.RelayURL) || route.RouteID == "" || route.Authorization == "" || len(route.PublicKey) != 32 || (kind != "sync" && kind != "reminder") || len(plaintext) == 0 {
		return fmt.Errorf("invalid relay notification")
	}
	envelope, err := pushcrypto.Seal(route.PublicKey, plaintext, []byte(route.RouteID))
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(map[string]any{
		"kind":        kind,
		"collapse_id": collapseID,
		"envelope":    envelope,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(route.RelayURL, "/")+"/v1/routes/"+route.RouteID+"/notifications", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+route.Authorization)
	request.Header.Set("Content-Type", "application/json")
	response, err := sender.client.Do(request)
	if err != nil {
		return fmt.Errorf("send encrypted relay notification: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var relayError struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(body, &relayError)
		if relayError.Code == "" {
			relayError.Code = "request_failed"
		}
		return fmt.Errorf("relay returned status %d with code %s", response.StatusCode, relayError.Code)
	}
	return nil
}

var _ Sender = (*HTTPSender)(nil)
