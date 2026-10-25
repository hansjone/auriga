package network

import "context"

// NodeIdentity is the authenticated node taken from ingress/session, never from payload claims.
type NodeIdentity struct {
	TenantID    string
	Realm       string
	NodeID      string
	Incarnation string
	Epoch       uint64 // connection_epoch the client believes it holds
}

// Route is the durable mailbox cursor state for one (tenant, realm, node).
type Route struct {
	ConnectionEpoch uint64
	HeadSeq         uint64
	CommittedCursor uint64
}

// RecipientView is one local under a delivery.
type RecipientView struct {
	RecipientID     string
	Local           string
	TransportStatus string
	StateVersion    uint64
}

// DeliveryItem is one FETCH row (mail_seq > resume_cursor).
type DeliveryItem struct {
	DeliveryID string
	MailSeq    uint64
	MessageID  string
	Body       string
	Kind       string
	Recipients []RecipientView
}

// ReceiptItem is one result inside a RECEIPT.
type ReceiptItem struct {
	RecipientID     string
	TransportStatus string // host_accepted | expired | revoked | … (platform terminal after map)
	StateVersion    uint64 // expected; mailbox may bump
}

// ReceiptCommit is the platform view after committing a RECEIPT.
type ReceiptCommit struct {
	DeliveryID      string
	MailSeq         uint64
	Results         []RecipientView
	MailItemClosed  bool
	CommittedCursor uint64
}

// Mailbox is the collab persistence port. Production will call collab RPC/HTTP;
// N1 tests inject an in-memory implementation.
type Mailbox interface {
	// Route returns current head/cursor/epoch without mutating.
	Route(ctx context.Context, id NodeIdentity) (Route, error)
	// Fetch returns items with mail_seq > afterCursor, up to limit (already clamped).
	Fetch(ctx context.Context, id NodeIdentity, afterCursor uint64, limit int) (Route, []DeliveryItem, error)
	// Receipt applies idempotent terminal/quarantine results and advances the gapless prefix.
	Receipt(ctx context.Context, id NodeIdentity, deliveryID string, mailSeq uint64, results []ReceiptItem) (ReceiptCommit, error)
}
