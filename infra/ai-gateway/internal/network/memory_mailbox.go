package network

import (
	"context"
	"sync"
)

// MemoryMailbox is a test double for Mailbox: one node, in-order deliveries.
type MemoryMailbox struct {
	mu      sync.Mutex
	epoch   uint64
	head    uint64
	cursor  uint64
	items   map[uint64]*memItem // mail_seq -> item
	byDeliv map[string]uint64
}

type memItem struct {
	deliveryID string
	messageID  string
	body       string
	kind       string
	recipients map[string]*RecipientView // recipient_id
}

// NewMemoryMailbox starts at epoch 1 with empty mailbox.
func NewMemoryMailbox(epoch uint64) *MemoryMailbox {
	if epoch == 0 {
		epoch = 1
	}
	return &MemoryMailbox{
		epoch:   epoch,
		items:   map[uint64]*memItem{},
		byDeliv: map[string]uint64{},
	}
}

// Seed appends a delivery at the next mail_seq.
func (m *MemoryMailbox) Seed(deliveryID, messageID, body, kind string, recipients []RecipientView) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.head++
	seq := m.head
	rec := map[string]*RecipientView{}
	for _, r := range recipients {
		cp := r
		if cp.TransportStatus == "" {
			cp.TransportStatus = "pending"
		}
		if cp.StateVersion == 0 {
			cp.StateVersion = 1
		}
		rec[cp.RecipientID] = &cp
	}
	m.items[seq] = &memItem{
		deliveryID: deliveryID,
		messageID:  messageID,
		body:       body,
		kind:       kind,
		recipients: rec,
	}
	m.byDeliv[deliveryID] = seq
	return seq
}

func (m *MemoryMailbox) Route(_ context.Context, _ NodeIdentity) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Route{ConnectionEpoch: m.epoch, HeadSeq: m.head, CommittedCursor: m.cursor}, nil
}

func (m *MemoryMailbox) Fetch(_ context.Context, id NodeIdentity, after uint64, limit int) (Route, []DeliveryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id.Epoch != m.epoch {
		return Route{}, nil, staleEpoch("")
	}
	if limit < 1 {
		limit = 1
	}
	out := make([]DeliveryItem, 0, limit)
	for seq := after + 1; seq <= m.head && len(out) < limit; seq++ {
		it, ok := m.items[seq]
		if !ok {
			continue
		}
		recs := make([]RecipientView, 0, len(it.recipients))
		for _, r := range it.recipients {
			recs = append(recs, *r)
		}
		out = append(out, DeliveryItem{
			DeliveryID: it.deliveryID,
			MailSeq:    seq,
			MessageID:  it.messageID,
			Body:       it.body,
			Kind:       it.kind,
			Recipients: recs,
		})
	}
	return Route{ConnectionEpoch: m.epoch, HeadSeq: m.head, CommittedCursor: m.cursor}, out, nil
}

func (m *MemoryMailbox) Receipt(_ context.Context, id NodeIdentity, deliveryID string, mailSeq uint64, results []ReceiptItem) (ReceiptCommit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id.Epoch != m.epoch {
		return ReceiptCommit{}, staleEpoch("")
	}
	seq, ok := m.byDeliv[deliveryID]
	if !ok || seq != mailSeq {
		return ReceiptCommit{}, protocolError("", "delivery/mail_seq")
	}
	it := m.items[seq]
	committed := make([]RecipientView, 0, len(results))
	for _, r := range results {
		cur, ok := it.recipients[r.RecipientID]
		if !ok {
			return ReceiptCommit{}, protocolError("", "unknown recipient")
		}
		term := isTerminal(cur.TransportStatus) || cur.TransportStatus == "quarantined"
		if term {
			if cur.TransportStatus != r.TransportStatus {
				return ReceiptCommit{}, &AppError{Code: CodeConflict, Scope: "request"}
			}
			committed = append(committed, *cur)
			continue
		}
		if cur.TransportStatus != "pending" {
			return ReceiptCommit{}, &AppError{Code: CodeConflict, Scope: "request"}
		}
		cur.TransportStatus = r.TransportStatus
		cur.StateVersion++
		committed = append(committed, *cur)
	}
	closed := true
	for _, r := range it.recipients {
		if r.TransportStatus == "pending" || r.TransportStatus == "quarantined" {
			closed = false
			break
		}
	}
	m.advanceLocked()
	return ReceiptCommit{
		DeliveryID:      deliveryID,
		MailSeq:         mailSeq,
		Results:         committed,
		MailItemClosed:  closed,
		CommittedCursor: m.cursor,
	}, nil
}

func (m *MemoryMailbox) advanceLocked() {
	for seq := m.cursor + 1; seq <= m.head; seq++ {
		it, ok := m.items[seq]
		if !ok {
			break
		}
		open := false
		for _, r := range it.recipients {
			if r.TransportStatus == "pending" || r.TransportStatus == "quarantined" {
				open = true
				break
			}
		}
		if open {
			break
		}
		m.cursor = seq
	}
}

func isTerminal(s string) bool {
	switch s {
	case "host_accepted", "expired", "canceled", "revoked", "skipped", "unresolved":
		return true
	default:
		return false
	}
}
