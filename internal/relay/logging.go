package relay

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
)

// LoggingDispatcher records failed deliveries. Clients only ever see an
// opaque internal_error, so without this log an operator cannot tell a
// revoked device token from an expired provider key.
type LoggingDispatcher struct {
	next   Dispatcher
	logger *slog.Logger
}

func NewLoggingDispatcher(next Dispatcher, logger *slog.Logger) *LoggingDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggingDispatcher{next: next, logger: logger}
}

func (dispatcher *LoggingDispatcher) Send(ctx context.Context, notification Notification) error {
	err := dispatcher.next.Send(ctx, notification)
	if err == nil {
		return nil
	}
	attributes := []any{
		"environment", string(notification.Environment),
		"push_type", string(notification.PushType),
	}
	var apnsError *APNSError
	var transportError *url.Error
	switch {
	case errors.As(err, &apnsError):
		attributes = append(attributes, "status", apnsError.StatusCode, "reason", apnsError.Reason, "apns_id", apnsError.RequestID)
	case errors.As(err, &transportError):
		// The request URL carries the device token, so only the cause is kept.
		attributes = append(attributes, "error", transportError.Op+": "+transportError.Err.Error())
	default:
		attributes = append(attributes, "error", err.Error())
	}
	dispatcher.logger.WarnContext(ctx, "apns delivery failed", attributes...)
	return err
}
