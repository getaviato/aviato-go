package aviato

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// SigningTransport is an http.RoundTripper signing request bodies with the Standard Webhooks
// scheme, as the connector does when it calls a plugin. It is meant for tests and tooling that call
// a plugin like the connector would.
type SigningTransport struct {
	webhook *standardwebhooks.Webhook
	// Base performs the requests; defaults to http.DefaultTransport.
	Base http.RoundTripper
	// Now returns the signing time; defaults to time.Now.
	Now func() time.Time
	// BaseURL, when set, binds each signature to the method and the path below it, as the
	// connector does (see [BoundMessageID]).
	BaseURL string
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
	if t.BaseURL != "" {
		if base, err := neturl.Parse(t.BaseURL); err == nil {
			prefix := strings.TrimRight(base.EscapedPath(), "/") + "/"
			if path := request.URL.EscapedPath(); strings.HasPrefix(path, prefix) {
				id = BoundMessageID(randomID(), request.Method, strings.TrimPrefix(path, prefix))
			}
		}
	}
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

// minimumSecretBytes rejects secrets that decode to an empty or guessable key (`whsec_`, a typo):
// anyone could sign requests with them. The dashboard generates 24 random bytes.
const minimumSecretBytes = 16

// checkSecret decodes a whsec_<base64> secret strictly and checks its length.
func checkSecret(secret string) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return fmt.Errorf("aviato: invalid secret: not whsec_<base64>: %w", err)
	}
	if len(key) < minimumSecretBytes {
		return fmt.Errorf("aviato: invalid secret: %d bytes, at least %d are required", len(key), minimumSecretBytes)
	}
	return nil
}

// replayWindow is how long a message id is remembered: the Standard Webhooks tolerance is five
// minutes either way.
const replayWindow = 10 * time.Minute

// BoundMessageID is a webhook-id bound to the request it signs:
// msg_<random>:<base64url("<METHOD> <path>")>, path relative to where the receiver is mounted
// (aviato.plugin.v1.PluginService/ExecuteAction, files/<ref>, replicas/<collection>/refresh).
// The Standard Webhooks signature covers the id, so a captured request cannot be posted to another
// endpoint whose body has the same shape. Same format as packages/protocol/src/plugin-signing.ts.
func BoundMessageID(random, method, path string) string {
	path = strings.TrimLeft(path, "/")
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	return "msg_" + random + ":" + base64.RawURLEncoding.EncodeToString([]byte(strings.ToUpper(method)+" "+path))
}

var errMalformedMessageID = errors.New("malformed webhook-id")

// messageTarget returns the method and path an id is bound to; ok is false for an unbound id.
func messageTarget(id string) (method, path string, ok bool, err error) {
	index := strings.IndexByte(id, ':')
	if index < 0 {
		return "", "", false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id[index+1:])
	if err != nil || !utf8.Valid(decoded) {
		return "", "", false, errMalformedMessageID
	}
	method, path, found := strings.Cut(string(decoded), " ")
	if !found || method == "" || path == "" {
		return "", "", false, errMalformedMessageID
	}
	return method, path, true, nil
}

// targetMatches reports whether a verified id may be used for this request. Unbound ids come from
// connectors that predate the binding and are accepted.
func targetMatches(id, method, requestPath string) bool {
	boundMethod, path, bound, err := messageTarget(id)
	if err != nil {
		return false
	}
	if !bound {
		return true
	}
	return boundMethod == strings.ToUpper(method) && (requestPath == "/"+path || strings.HasSuffix(requestPath, "/"+path))
}

// seenMessages remembers verified message ids so a captured request is accepted once.
type seenMessages struct {
	mu   sync.Mutex
	ids  map[string]time.Time
	now  func() time.Time
	last time.Time
}

func newSeenMessages() *seenMessages {
	return &seenMessages{ids: map[string]time.Time{}, now: time.Now}
}

// add reports false when id was already seen within the replay window.
func (s *seenMessages) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.last) > time.Minute {
		for seen, expires := range s.ids {
			if !expires.After(now) {
				delete(s.ids, seen)
			}
		}
		s.last = now
	}
	if expires, ok := s.ids[id]; ok && expires.After(now) {
		return false
	}
	s.ids[id] = now.Add(replayWindow)
	return true
}
