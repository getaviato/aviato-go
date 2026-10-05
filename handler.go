package aviato

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

// Handler returns the net/http handler serving the plugin protocol. Mount it so that it
// receives the full request path, prefix included, under [Options.BasePath]:
//
//	mux.Handle("/aviato/", plugin.Handler())
//
// It serves the PluginService procedures (ConnectRPC, JSON or binary encoding) at
// <basePath>/aviato.plugin.v1.PluginService/<Method>, the DatasourceService procedures of
// custom datasources at <basePath>/aviato.plugin.v1.DatasourceService/<Method>, and one-time
// file downloads at
// GET <basePath>/files/{ref}. Every request must carry a valid Standard Webhooks signature;
// others are rejected with HTTP 401 before their body is decoded.
func (p *Plugin) Handler() http.Handler {
	p.handlerOnce.Do(func() {
		mux := http.NewServeMux()
		path, rpc := pluginv1connect.NewPluginServiceHandler(&service{plugin: p}, connect.WithReadMaxBytes(int(p.options.MaxRequestBytes)))
		mux.Handle(path, p.verified(rpc))
		path, rpc = pluginv1connect.NewDatasourceServiceHandler(&datasourceService{plugin: p}, connect.WithReadMaxBytes(int(p.options.MaxRequestBytes)))
		mux.Handle(path, p.verified(rpc))
		mux.HandleFunc("GET /files/{ref}", p.serveFile)
		if p.basePath == "" {
			p.handler = mux
		} else {
			p.handler = http.StripPrefix(p.basePath, mux)
		}
	})
	return p.handler
}

// ServeHTTP makes the plugin itself an http.Handler; see [Plugin.Handler].
func (p *Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.Handler().ServeHTTP(w, r)
}

func writeUnauthenticated(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": connect.CodeUnauthenticated.String(), "message": message})
}

// verified checks the Standard Webhooks signature of the raw body before handing the request
// to next.
func (p *Plugin) verified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, p.options.MaxRequestBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Cannot read request body", http.StatusBadRequest)
			return
		}
		if err := p.webhook.Verify(body, r.Header); err != nil {
			writeUnauthenticated(w, "Invalid plugin request signature")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) serveFile(w http.ResponseWriter, r *http.Request) {
	if err := p.webhook.Verify(nil, r.Header); err != nil {
		writeUnauthenticated(w, "Invalid signature")
		return
	}
	file, ok := p.files.take(r.PathValue("ref"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", file.MimeType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Content)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(file.Content)
}

// fileStore keeps file results until the agent downloads them, once, or they expire.
type fileStore struct {
	ttl   time.Duration
	mu    sync.Mutex
	files map[string]storedFile
	now   func() time.Time
}

type storedFile struct {
	FileResult
	expiresAt time.Time
}

func newFileStore(ttl time.Duration) *fileStore {
	return &fileStore{ttl: ttl, files: map[string]storedFile{}, now: time.Now}
}

func (s *fileStore) put(file FileResult) string {
	ref := rand.Text()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for existing, stored := range s.files {
		if now.After(stored.expiresAt) {
			delete(s.files, existing)
		}
	}
	s.files[ref] = storedFile{FileResult: file, expiresAt: now.Add(s.ttl)}
	return ref
}

func (s *fileStore) take(ref string) (FileResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.files[ref]
	delete(s.files, ref)
	if !ok || s.now().After(stored.expiresAt) {
		return FileResult{}, false
	}
	return stored.FileResult, true
}
