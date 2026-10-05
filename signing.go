package aviato

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// SigningTransport is an http.RoundTripper signing request bodies with the Standard Webhooks
// scheme, as the agent does when it calls a plugin. It is meant for tests and tooling that call
// a plugin like the agent would.
type SigningTransport struct {
	webhook *standardwebhooks.Webhook
	// Base performs the requests; defaults to http.DefaultTransport.
	Base http.RoundTripper
	// Now returns the signing time; defaults to time.Now.
	Now func() time.Time
}

// NewSigningTransport returns a transport signing with secret (whsec_<base64>).
func NewSigningTransport(secret string) (*SigningTransport, error) {
	webhook, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		return nil, fmt.Errorf("aviato: invalid secret: %w", err)
	}
	return &SigningTransport{webhook: webhook}, nil
}

// NewSigningClient returns an HTTP client signing its requests with secret.
func NewSigningClient(secret string) (*http.Client, error) {
	transport, err := NewSigningTransport(secret)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport}, nil
}

// RoundTrip signs a copy of request and sends it.
func (t *SigningTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil && request.Body != http.NoBody {
		var err error
		body, err = io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	timestamp := now()
	id := "msg_" + randomID()
	signature, err := t.webhook.Sign(id, timestamp, body)
	if err != nil {
		return nil, err
	}
	signed := request.Clone(request.Context())
	signed.Body = io.NopCloser(bytes.NewReader(body))
	signed.ContentLength = int64(len(body))
	signed.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	signed.Header.Set(standardwebhooks.HeaderWebhookID, id)
	signed.Header.Set(standardwebhooks.HeaderWebhookTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	signed.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(signed)
}

func randomID() string { return rand.Text() }
