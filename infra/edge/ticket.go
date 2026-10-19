package main

import (
	"fmt"
	"sync"
	"time"
)

// Ticket binds a one-time open grant to a portal session and instance.
type Ticket struct {
	Token      string
	SessionID  string
	AccountID  string
	TenantID   string
	InstanceID string
	ExpiresAt  time.Time
	Used       bool
}

// InstBinding is the server-side value behind __Host-auriga_inst.
type InstBinding struct {
	Token      string
	SessionID  string
	AccountID  string
	TenantID   string
	InstanceID string
	CookieRaw  string // opaque cookie value
}

// TicketStore issues one-time tickets and instance cookie bindings.
type TicketStore interface {
	Issue(sessionID, accountID, tenantID, instanceID string, ttl time.Duration) (*Ticket, error)
	Redeem(token string) (*Ticket, error)
	BindInstance(sessionID, accountID, tenantID, instanceID string) (*InstBinding, error)
	LookupInstance(cookieRaw string) (*InstBinding, error)
}

type MemoryTicketStore struct {
	mu       sync.Mutex
	tickets  map[string]*Ticket
	bindings map[string]*InstBinding
	now      func() time.Time
}

func NewMemoryTicketStore() *MemoryTicketStore {
	return &MemoryTicketStore{
		tickets:  map[string]*Ticket{},
		bindings: map[string]*InstBinding{},
		now:      time.Now,
	}
}

func (m *MemoryTicketStore) Issue(sessionID, accountID, tenantID, instanceID string, ttl time.Duration) (*Ticket, error) {
	tok, err := randomID(24)
	if err != nil {
		return nil, err
	}
	t := &Ticket{
		Token:      tok,
		SessionID:  sessionID,
		AccountID:  accountID,
		TenantID:   tenantID,
		InstanceID: instanceID,
		ExpiresAt:  m.now().UTC().Add(ttl),
	}
	m.mu.Lock()
	m.tickets[tok] = t
	m.mu.Unlock()
	cp := *t
	return &cp, nil
}

func (m *MemoryTicketStore) Redeem(token string) (*Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[token]
	if !ok || t == nil {
		return nil, fmt.Errorf("ticket not found")
	}
	if t.Used {
		return nil, fmt.Errorf("ticket already used")
	}
	if !m.now().UTC().Before(t.ExpiresAt) {
		return nil, fmt.Errorf("ticket expired")
	}
	t.Used = true
	cp := *t
	return &cp, nil
}

func (m *MemoryTicketStore) BindInstance(sessionID, accountID, tenantID, instanceID string) (*InstBinding, error) {
	raw, err := randomID(24)
	if err != nil {
		return nil, err
	}
	b := &InstBinding{
		Token:      raw,
		SessionID:  sessionID,
		AccountID:  accountID,
		TenantID:   tenantID,
		InstanceID: instanceID,
		CookieRaw:  raw,
	}
	m.mu.Lock()
	m.bindings[raw] = b
	m.mu.Unlock()
	cp := *b
	return &cp, nil
}

func (m *MemoryTicketStore) LookupInstance(cookieRaw string) (*InstBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[cookieRaw]
	if !ok || b == nil {
		return nil, fmt.Errorf("instance cookie not found")
	}
	cp := *b
	return &cp, nil
}
