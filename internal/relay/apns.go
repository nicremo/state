package relay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultAPNSProductionURL = "https://api.push.apple.com"
	defaultAPNSSandboxURL    = "https://api.sandbox.push.apple.com"
)

type APNSConfig struct {
	TeamID        string
	KeyID         string
	Topic         string
	PrivateKey    *ecdsa.PrivateKey
	HTTPClient    *http.Client
	ProductionURL string
	SandboxURL    string
	Clock         func() time.Time
}

type APNSDispatcher struct {
	teamID        string
	keyID         string
	topic         string
	privateKey    *ecdsa.PrivateKey
	httpClient    *http.Client
	productionURL string
	sandboxURL    string
	clock         func() time.Time
	tokenMutex    sync.Mutex
	cachedToken   string
	tokenCreated  time.Time
}

type APNSError struct {
	StatusCode int
	Reason     string
	RequestID  string
}

func (err *APNSError) Error() string {
	return fmt.Sprintf("APNs returned status %d with reason %s", err.StatusCode, err.Reason)
}

func NewAPNSDispatcher(config APNSConfig) (*APNSDispatcher, error) {
	if config.TeamID == "" || config.KeyID == "" || config.Topic == "" || config.PrivateKey == nil {
		return nil, errors.New("APNs configuration is incomplete")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.ProductionURL == "" {
		config.ProductionURL = defaultAPNSProductionURL
	}
	if config.SandboxURL == "" {
		config.SandboxURL = defaultAPNSSandboxURL
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &APNSDispatcher{
		teamID:        config.TeamID,
		keyID:         config.KeyID,
		topic:         config.Topic,
		privateKey:    config.PrivateKey,
		httpClient:    config.HTTPClient,
		productionURL: strings.TrimRight(config.ProductionURL, "/"),
		sandboxURL:    strings.TrimRight(config.SandboxURL, "/"),
		clock:         config.Clock,
	}, nil
}

func ParseAPNSPrivateKey(contents []byte) (*ecdsa.PrivateKey, error) {
	key, err := jwt.ParseECPrivateKeyFromPEM(contents)
	if err != nil {
		return nil, fmt.Errorf("parse APNs private key: %w", err)
	}
	return key, nil
}

func (dispatcher *APNSDispatcher) Send(ctx context.Context, notification Notification) error {
	if !validAPNSToken(notification.APNSToken) || !validEnvironment(notification.Environment) || (notification.PushType != PushTypeAlert && notification.PushType != PushTypeBackground) || len(notification.Payload) == 0 || len(notification.Payload) > 4096 {
		return ErrInvalidInput
	}
	authorization, err := dispatcher.authorizationToken()
	if err != nil {
		return err
	}
	endpoint := dispatcher.productionURL
	if notification.Environment == EnvironmentSandbox {
		endpoint = dispatcher.sandboxURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/3/device/"+notification.APNSToken, bytes.NewReader(notification.Payload))
	if err != nil {
		return fmt.Errorf("create APNs request: %w", err)
	}
	request.Header.Set("Authorization", "bearer "+authorization)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("apns-topic", dispatcher.topic)
	request.Header.Set("apns-push-type", string(notification.PushType))
	request.Header.Set("apns-expiration", "0")
	if notification.PushType == PushTypeAlert {
		request.Header.Set("apns-priority", "10")
	} else {
		request.Header.Set("apns-priority", "5")
	}
	if notification.CollapseID != "" {
		request.Header.Set("apns-collapse-id", notification.CollapseID)
	}
	response, err := dispatcher.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send APNs request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	result := struct {
		Reason string `json:"reason"`
	}{}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result)
	if result.Reason == "" {
		result.Reason = "Unknown"
	}
	return &APNSError{
		StatusCode: response.StatusCode,
		Reason:     result.Reason,
		RequestID:  response.Header.Get("apns-id"),
	}
}

func (dispatcher *APNSDispatcher) authorizationToken() (string, error) {
	dispatcher.tokenMutex.Lock()
	defer dispatcher.tokenMutex.Unlock()
	now := dispatcher.clock().UTC()
	if dispatcher.cachedToken != "" && now.Sub(dispatcher.tokenCreated) < 50*time.Minute {
		return dispatcher.cachedToken, nil
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": dispatcher.teamID,
		"iat": now.Unix(),
	})
	token.Header["kid"] = dispatcher.keyID
	signed, err := token.SignedString(dispatcher.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign APNs provider token: %w", err)
	}
	dispatcher.cachedToken = signed
	dispatcher.tokenCreated = now
	return signed, nil
}

var _ Dispatcher = (*APNSDispatcher)(nil)
