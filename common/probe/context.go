package probe

import (
	"context"
	"errors"
	"sync"
)

type urlTestContextKey struct{}

// CompatibilityController owns transport-specific candidate selection for one
// URLTest. The URLTest runner only asks whether an error is safe to retry; it
// never needs to know which transport or candidate is involved.
type CompatibilityController interface {
	Next(error) bool
	Success()
	Failure(error)
}

// ClassifiedError exposes a stable, redacted URLTest failure classification.
// Transport implementations must not include credentials or full request URLs
// in the returned message.
type ClassifiedError interface {
	error
	URLTestErrorCode() string
	URLTestErrorMessage() string
}

// ClassifyError extracts a transport-provided URLTest classification through
// ordinary error wrapping.
func ClassifyError(err error) (code string, message string, loaded bool) {
	var classified ClassifiedError
	if !errors.As(err, &classified) {
		return "", "", false
	}
	return classified.URLTestErrorCode(), classified.URLTestErrorMessage(), true
}

// URLTestSession coordinates one public URLTest result with any internal,
// retry-safe transport compatibility attempts.
type URLTestSession struct {
	access     sync.Mutex
	controller CompatibilityController
}

// NewURLTestSession creates an isolated compatibility session.
func NewURLTestSession() *URLTestSession {
	return new(URLTestSession)
}

// WithURLTest marks retry-safe connectivity work. Transports may use this
// marker for compatibility discovery, but never for replaying user traffic.
func WithURLTest(ctx context.Context, session *URLTestSession) context.Context {
	return context.WithValue(ctx, urlTestContextKey{}, session)
}

// IsURLTest reports whether the context belongs to retry-safe URLTest work.
func IsURLTest(ctx context.Context) bool {
	return URLTestSessionFromContext(ctx) != nil
}

// URLTestSessionFromContext returns the URLTest compatibility session, if any.
func URLTestSessionFromContext(ctx context.Context) *URLTestSession {
	session, _ := ctx.Value(urlTestContextKey{}).(*URLTestSession)
	return session
}

// BindController installs the first transport compatibility controller. A
// URLTest is expected to traverse at most one auto-negotiated transport.
func (s *URLTestSession) BindController(controller CompatibilityController) bool {
	s.access.Lock()
	defer s.access.Unlock()
	if s.controller != nil {
		return false
	}
	s.controller = controller
	return true
}

// Controller returns the bound transport compatibility controller.
func (s *URLTestSession) Controller() CompatibilityController {
	s.access.Lock()
	defer s.access.Unlock()
	return s.controller

}

// Next asks the transport whether the failed attempt may move to another
// compatibility candidate.
func (s *URLTestSession) Next(err error) bool {
	controller := s.Controller()
	return controller != nil && controller.Next(err)
}

// Success commits the transport candidate only after the complete URLTest has
// succeeded.
func (s *URLTestSession) Success() {
	if controller := s.Controller(); controller != nil {
		controller.Success()
	}
}

// Failure releases any transport waiters without caching a candidate.
func (s *URLTestSession) Failure(err error) {
	if controller := s.Controller(); controller != nil {
		controller.Failure(err)
	}
}
