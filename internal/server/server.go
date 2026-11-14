// Package server implements the HTTP API the RustDesk client talks to and the
// web console.
package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// Version is set at build time.
var Version = "dev"

// Config holds the runtime options that are not stored in the database.
type Config struct {
	// TrustProxy makes the server read the client address from
	// X-Forwarded-For / X-Forwarded-Proto. Enable it only behind a reverse proxy.
	TrustProxy bool
	// Web is the console's static files.
	Web fs.FS
}

// Server is the HTTP application.
type Server struct {
	st     *store.Store
	cfg    Config
	mux    *http.ServeMux
	fails  *failCounter
	oidc   *oidcCache
	nonces *nonceSet
	kicks  *kickList
	tfa    *challenges
}

// New builds the application.
func New(st *store.Store, cfg Config) *Server {
	s := &Server{st: st, cfg: cfg, mux: http.NewServeMux(), fails: newFailCounter(), oidc: newOIDCCache(), nonces: &nonceSet{m: map[string]time.Time{}}, kicks: &kickList{m: map[string][]int64{}}, tfa: &challenges{m: map[string]*challenge{}}}
	s.clientRoutes()
	s.oidcRoutes()
	s.consoleRoutes()
	return s
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		s.mux.ServeHTTP(w, r)
	})
}

// Settings keys.
const (
	keyIDServer    = "id_server"
	keyRelayServer = "relay_server"
	keyServerKey   = "server_key"
	keyPublicURL   = "public_url"
	// keyAllowPresets lets installations enrol themselves (owner, group, address book)
	// through the preset-* options of a custom client. Off unless set to "1".
	keyAllowPresets = "allow_presets"
	// keyStrategy holds a JSON object of client options pushed to every device;
	// keyStrategyAt is the unix time of its last change.
	keyStrategy   = "strategy_options"
	keyStrategyAt = "strategy_modified_at"
	// keyLogDays is how long logs are kept (0 or empty = forever).
	keyLogDays = "log_retention_days"
)

// publicURL is the externally visible base URL, without a trailing slash.
func (s *Server) publicURL(r *http.Request) string {
	if u := strings.TrimRight(s.st.Setting(keyPublicURL), "/"); u != "" {
		return u
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP returns the address of the caller.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The right-most entry is the one our own proxy appended.
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

// readJSON decodes a request body of at most limit bytes. An empty body is not an error.
func readJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return errors.New("request body too large or unreadable")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

const (
	bodySmall = 1 << 20  // 1 MiB
	bodyLarge = 16 << 20 // 16 MiB, whole address books in the legacy format
)

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// failCounter throttles password guessing: a few failures per key and window.
type failCounter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

const (
	failWindow = 10 * time.Minute
	failMax    = 8
)

func newFailCounter() *failCounter { return &failCounter{m: map[string][]time.Time{}} }

func (f *failCounter) recent(key string) []time.Time {
	cut := time.Now().Add(-failWindow)
	kept := f.m[key][:0]
	for _, t := range f.m[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(f.m, key)
	} else {
		f.m[key] = kept
	}
	return kept
}

func (f *failCounter) blocked(keys ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		if len(f.recent(k)) >= failMax {
			return true
		}
	}
	return false
}

func (f *failCounter) fail(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.m) > 50000 { // bound memory under a flood
		f.m = map[string][]time.Time{}
	}
	for _, k := range keys {
		f.m[k] = append(f.recent(k), time.Now())
	}
}

func (f *failCounter) reset(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.m, k)
	}
}

// checkLogin verifies a password with throttling by address and by account.
func (s *Server) checkLogin(r *http.Request, username, password string) (*store.User, error) {
	ipKey := "ip:" + s.clientIP(r)
	userKey := "user:" + strings.ToLower(strings.TrimSpace(username))
	if s.fails.blocked(ipKey, userKey) {
		return nil, errors.New("too many failed attempts, try again in a few minutes")
	}
	u, err := s.st.CheckPassword(username, password)
	if err != nil {
		s.fails.fail(ipKey, userKey)
		return nil, err
	}
	s.fails.reset(userKey)
	return u, nil
}

// ClientConfig is what a RustDesk client needs to use this server.
type ClientConfig struct {
	IDServer    string `json:"id_server"`
	RelayServer string `json:"relay_server"`
	APIServer   string `json:"api_server"`
	Key         string `json:"key"`
	// Encoded is the string accepted by the client's "import server config".
	Encoded string `json:"encoded"`
}

func (s *Server) clientConfig(r *http.Request) ClientConfig {
	c := ClientConfig{
		IDServer:    s.st.Setting(keyIDServer),
		RelayServer: s.st.Setting(keyRelayServer),
		APIServer:   s.publicURL(r),
		Key:         s.st.Setting(keyServerKey),
	}
	if c.IDServer != "" {
		raw, _ := json.Marshal(map[string]string{"host": c.IDServer, "relay": c.RelayServer, "api": c.APIServer, "key": c.Key})
		enc := []rune(base64.URLEncoding.EncodeToString(raw))
		for i, j := 0, len(enc)-1; i < j; i, j = i+1, j-1 {
			enc[i], enc[j] = enc[j], enc[i]
		}
		c.Encoded = string(enc)
	}
	return c
}

// nonceSet remembers recently seen report nonces so that retried reports are
// applied once.
type nonceSet struct {
	mu sync.Mutex
	m  map[string]time.Time
}

const nonceTTL = 10 * time.Minute

// seen reports whether the nonce was already recorded, and records it.
func (n *nonceSet) seen(nonce string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if len(n.m) > 20000 {
		for k, t := range n.m {
			if now.Sub(t) > nonceTTL {
				delete(n.m, k)
			}
		}
		if len(n.m) > 20000 {
			n.m = map[string]time.Time{}
		}
	}
	if t, ok := n.m[nonce]; ok && now.Sub(t) <= nonceTTL {
		return true
	}
	n.m[nonce] = now
	return false
}

// Maintain runs periodic housekeeping until the process exits: logs older
// than the configured retention are removed.
func (s *Server) Maintain() {
	for {
		if days, _ := strconv.Atoi(s.st.Setting(keyLogDays)); days > 0 {
			if n := s.st.PruneLogs(time.Now().Unix() - int64(days)*86400); n > 0 {
				log.Printf("removed %d log rows older than %d days", n, days)
			}
		}
		time.Sleep(time.Hour)
	}
}
