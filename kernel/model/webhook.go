package model

import "time"

// SealedWebhookSecret contains authenticated ciphertext, never the signing key.
type SealedWebhookSecret struct {
	Ciphertext []byte `json:"-"`
	Nonce      []byte `json:"-"`
	KeyID      string `json:"-"`
}

// NewWebhookEndpoint contains only the authenticated encrypted endpoint secret.
type NewWebhookEndpoint struct {
	ID     string
	URL    string
	Events []string
	Secret SealedWebhookSecret
}

// WebhookDelivery is safe to expose in an authorized space; the body and secret stay internal.
type WebhookDelivery struct {
	ID            string `json:"id"`
	EndpointID    string `json:"endpointId"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	HTTPStatus    int    `json:"httpStatus"`
	DurationMS    int64  `json:"durationMs"`
	FailureReason string `json:"failureReason,omitempty"`
}

// WebhookJob is returned only to the internal worker, never a client or a log.
type WebhookJob struct {
	PreviousSecret     *SealedWebhookSecret `json:"-"`
	PreviousValidUntil *time.Time           `json:"-"`
	WebhookDelivery
	SpaceID    string              `json:"-"`
	URL        string              `json:"-"`
	Body       []byte              `json:"-"`
	Secret     SealedWebhookSecret `json:"-"`
	ClaimToken string              `json:"-"`
}
