package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nicremo/state/internal/state"
)

type Service struct {
	repository *Repository
	sender     Sender
	clock      func() time.Time
}

func NewService(repository *Repository, sender Sender) *Service {
	return &Service{
		repository: repository,
		sender:     sender,
		clock:      func() time.Time { return time.Now().UTC() },
	}
}

func (service *Service) RegisterDevice(ctx context.Context, actor state.Actor, input RegisterDeviceInput) (DeviceRoute, error) {
	if service == nil || service.repository == nil {
		return DeviceRoute{}, errors.New("push service is unavailable")
	}
	return service.repository.RegisterDevice(ctx, actor, input)
}

func (service *Service) ConfirmOccurrences(ctx context.Context, actor state.Actor, occurrenceIDs []string) error {
	if service == nil || service.repository == nil {
		return errors.New("push service is unavailable")
	}
	return service.repository.ConfirmOccurrences(ctx, actor, occurrenceIDs)
}

func (service *Service) DeleteDevice(ctx context.Context, actor state.Actor) error {
	if service == nil || service.repository == nil {
		return errors.New("push service is unavailable")
	}
	return service.repository.DeleteDevice(ctx, actor)
}

func (service *Service) NotifySync(ctx context.Context, excludedActorID string) error {
	if service == nil || service.repository == nil || service.sender == nil {
		return nil
	}
	routes, err := service.repository.ListRoutes(ctx, excludedActorID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"kind":         "sync",
		"generated_at": service.clock().UTC(),
	})
	if err != nil {
		return err
	}
	var failures []error
	for _, route := range routes {
		if err := service.sender.Send(ctx, route, "sync", "state-sync", payload); err != nil {
			failures = append(failures, fmt.Errorf("notify device %s: %w", route.ActorID, err))
		}
	}
	return errors.Join(failures...)
}

func (service *Service) DeliverDue(ctx context.Context, from time.Time, through time.Time) (int, error) {
	if service == nil || service.repository == nil || service.sender == nil {
		return 0, nil
	}
	due, err := service.repository.ListDueOccurrences(ctx, from.UTC(), through.UTC())
	if err != nil {
		return 0, err
	}
	delivered := 0
	var failures []error
	for _, item := range due {
		routes, err := service.repository.ListUnconfirmedRoutes(ctx, item.Occurrence.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		payload, err := json.Marshal(map[string]any{
			"kind":          "reminder",
			"reminder_id":   item.Reminder.ID,
			"occurrence_id": item.Occurrence.ID,
			"title":         item.Reminder.Title,
			"description":   item.Reminder.Description,
			"notify_at":     item.NotifyAt,
			"revision":      item.Reminder.Revision,
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, route := range routes {
			if err := service.sender.Send(ctx, route, "reminder", item.Occurrence.ID, payload); err != nil {
				failures = append(failures, fmt.Errorf("deliver occurrence %s to device %s: %w", item.Occurrence.ID, route.ActorID, err))
				continue
			}
			if err := service.repository.MarkDelivered(ctx, route.ActorID, item.Occurrence.ID); err != nil {
				failures = append(failures, err)
				continue
			}
			delivered++
		}
	}
	return delivered, errors.Join(failures...)
}
