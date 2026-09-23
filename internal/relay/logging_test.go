package relay

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

type failingDispatcher struct{ err error }

func (dispatcher failingDispatcher) Send(context.Context, Notification) error { return dispatcher.err }

func TestLoggingDispatcherRecordsAPNSReasons(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	dispatcher := NewLoggingDispatcher(failingDispatcher{err: &APNSError{StatusCode: 400, Reason: "BadDeviceToken", RequestID: "req-1"}}, logger)

	err := dispatcher.Send(context.Background(), Notification{APNSToken: "secret-device-token", Environment: "production", PushType: "alert"})

	var apnsError *APNSError
	if !errors.As(err, &apnsError) {
		t.Fatalf("error = %v, want the original APNs error", err)
	}
	logged := output.String()
	for _, required := range []string{"apns delivery failed", `"status":400`, `"reason":"BadDeviceToken"`, `"apns_id":"req-1"`, `"environment":"production"`} {
		if !strings.Contains(logged, required) {
			t.Errorf("log %q misses %q", logged, required)
		}
	}
	if strings.Contains(logged, "secret-device-token") {
		t.Fatal("log leaked the device token")
	}
}

func TestLoggingDispatcherNeverLogsTheRequestURL(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	transportError := &url.Error{Op: "Post", URL: "https://api.push.apple.com/3/device/secret-device-token", Err: errors.New("connection refused")}
	dispatcher := NewLoggingDispatcher(failingDispatcher{err: transportError}, logger)

	_ = dispatcher.Send(context.Background(), Notification{APNSToken: "secret-device-token"})

	logged := output.String()
	if !strings.Contains(logged, "connection refused") {
		t.Errorf("log %q misses the transport cause", logged)
	}
	if strings.Contains(logged, "secret-device-token") || strings.Contains(logged, "/3/device/") {
		t.Fatalf("log leaked the APNs request URL: %s", logged)
	}
}

func TestLoggingDispatcherStaysQuietOnSuccess(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	dispatcher := NewLoggingDispatcher(failingDispatcher{}, logger)

	if err := dispatcher.Send(context.Background(), Notification{}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected log output %q", output.String())
	}
}
