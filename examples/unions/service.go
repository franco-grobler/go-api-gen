// Package unions schedules notifications, to show what go-api-gen makes of
// oneOf.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml. Two unions carry the request: Channel, which the client names by
// its "type", and SendAt, which is a moment or a delay and is told apart by
// whether the JSON is a string or a number. By the time SendNotification
// runs, each has been decoded into exactly one of its members, and that
// member has been validated.
package unions

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/example/unions/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Service implements the generated handler interface. It records what it
// would send rather than sending it.
type Service struct {
	now func() time.Time

	mu        sync.Mutex
	scheduled []api.Scheduled
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*Service)(nil)

// New returns a Service that reads the time from now.
func New(now func() time.Time) *Service {
	return &Service{now: now}
}

// Scheduled returns what has been scheduled, oldest first.
func (s *Service) Scheduled() []api.Scheduled {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.Scheduled(nil), s.scheduled...)
}

// SendNotification schedules a notification.
//
// A union is a struct with one pointer field per member, and exactly one is
// set, so handling one is a switch. The generated code has already rejected a
// channel whose type names no member, and an SMS number that is not E.164; the
// rule left here is one no schema states.
func (s *Service) SendNotification(
	_ context.Context, req api.SendNotificationRequest,
) (*api.Scheduled, error) {
	if hook := req.Body.Channel.Webhook; hook != nil {
		if u, err := url.Parse(hook.URL); err != nil || u.Scheme != "https" {
			return nil, apierr.Validation().
				Field("body", "channel", "url").
				Fail("url_scheme", "Webhook URL should use https")
		}
	}

	at := s.now()
	if when, ok := req.Body.SendAt.Get(); ok {
		switch {
		case when.At != nil && when.At.After(at):
			at = *when.At // a moment already past means now
		case when.Delay != nil:
			at = at.Add(time.Duration(*when.Delay) * time.Second)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sched := api.Scheduled{
		ID:      "ntf_" + strconv.Itoa(len(s.scheduled)+1),
		Channel: req.Body.Channel,
		SendAt:  at.UTC().Truncate(time.Second),
	}
	s.scheduled = append(s.scheduled, sched)
	return &sched, nil
}

// NewServer returns the generated server over s.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}
