package session

import (
	"fmt"

	"github.com/hewitt-iso/hewitt/gateway"
)

// SessionRequest is a gateway request with the session it came from.
type SessionRequest struct {
	gateway.Request
	SessionID, Session string
}

// Requests lists the gateway requests of every saved session, oldest first
// within each session.
func (s *Store) Requests() ([]SessionRequest, error) {
	records, err := s.List()
	if err != nil {
		return nil, err
	}
	var list []SessionRequest
	for _, m := range records {
		if m.Egress == nil {
			continue
		}
		requests, err := gateway.Requests(m.Egress.StateDir())
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", m.ID[:12], err)
		}
		name := m.Name
		if name == "" {
			name = m.ID[:12]
		}
		for _, r := range requests {
			list = append(list, SessionRequest{Request: r, SessionID: m.ID, Session: name})
		}
	}
	return list, nil
}

// Decide allows or denies a request by ID. A decided request can be decided
// again; the gateway applies the latest decision to later connections.
func (s *Store) Decide(id string, allow bool) (SessionRequest, error) {
	list, err := s.Requests()
	if err != nil {
		return SessionRequest{}, err
	}
	for _, r := range list {
		if r.ID != id {
			continue
		}
		m, err := s.Get(r.SessionID)
		if err != nil {
			return SessionRequest{}, err
		}
		return r, gateway.Decide(m.Egress.StateDir(), id, allow)
	}
	return SessionRequest{}, &RequestNotFoundError{ID: id}
}
