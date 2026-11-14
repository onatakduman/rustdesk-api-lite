package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// oidcCallbackPath is the redirect URI path to register at the provider.
const oidcCallbackPath = "/api/oidc/callback"

var oidcHTTP = &http.Client{Timeout: 15 * time.Second}

type oidcMeta struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	fetched               time.Time
}

type oidcCache struct {
	mu sync.Mutex
	m  map[string]*oidcMeta
}

func newOIDCCache() *oidcCache { return &oidcCache{m: map[string]*oidcMeta{}} }

func (c *oidcCache) forget(name string) {
	c.mu.Lock()
	delete(c.m, name)
	c.mu.Unlock()
}

// isGitHub reports whether a provider is GitHub, which speaks plain OAuth 2
// (no discovery document, its own user API) instead of OpenID Connect.
func isGitHub(p *store.OIDCProvider) bool {
	u, err := url.Parse(p.Issuer)
	return err == nil && strings.EqualFold(u.Host, "github.com")
}

// discover loads (and caches for an hour) the provider's endpoints.
func (c *oidcCache) discover(p *store.OIDCProvider) (*oidcMeta, error) {
	if isGitHub(p) {
		return &oidcMeta{AuthorizationEndpoint: "https://github.com/login/oauth/authorize", TokenEndpoint: "https://github.com/login/oauth/access_token",
			UserinfoEndpoint: "https://api.github.com/user"}, nil
	}
	c.mu.Lock()
	m := c.m[p.Name]
	c.mu.Unlock()
	if m != nil && time.Since(m.fetched) < time.Hour {
		return m, nil
	}
	resp, err := oidcHTTP.Get(strings.TrimRight(p.Issuer, "/") + "/.well-known/openid-configuration")
	if err != nil {
		return nil, fmt.Errorf("provider discovery failed: %w", err)
	}
	defer resp.Body.Close()
	m = &oidcMeta{fetched: time.Now()}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(m) != nil ||
		m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" || m.UserinfoEndpoint == "" {
		return nil, errors.New("provider discovery returned an unusable document")
	}
	c.mu.Lock()
	c.m[p.Name] = m
	c.mu.Unlock()
	return m, nil
}

func (s *Server) oidcRoutes() {
	s.mux.HandleFunc("GET /oidc/login", s.handleOIDCLogin)
	s.mux.HandleFunc("GET "+oidcCallbackPath, s.handleOIDCCallback)
	// Alias used by lejianwen/rustdesk-api, so redirect URIs registered for it keep working.
	s.mux.HandleFunc("GET /api/oauth/callback", s.handleOIDCCallback)
}

func oidcPage(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>body{font:16px/1.5 system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f4f5f7;color:#1c1e21}
main{background:#fff;padding:32px 40px;border-radius:12px;max-width:420px;box-shadow:0 1px 3px rgba(0,0,0,.12)}h1{font-size:20px;margin:0 0 8px}
@media(prefers-color-scheme:dark){body{background:#18191e;color:#e6e6e6}main{background:#24252b}}</style>
<main><h1>%s</h1><p>%s</p></main>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
}

// handleOIDCLogin sends the browser to the provider. It is reached either with
// ?code= (a sign-in started by a RustDesk client) or ?provider= (the console).
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var sess *store.OIDCSession
	if code := q.Get("code"); code != "" {
		var err error
		if sess, err = s.st.OIDCSessionByCode(code); err != nil || sess.Result != "" {
			oidcPage(w, http.StatusBadRequest, "Sign-in expired", "Start the sign-in again from RustDesk.")
			return
		}
	} else {
		sess = &store.OIDCSession{Provider: q.Get("provider"), Purpose: "console"}
		if q.Get("link") == "1" {
			// Add this sign-in to the account that is signed in to the console.
			u, _ := s.consoleUser(r)
			if u == nil {
				oidcPage(w, http.StatusUnauthorized, "Sign in first", "Sign in to the console, then link the provider from Settings.")
				return
			}
			sess.Purpose = "link:" + strconv.FormatInt(u.ID, 10)
		}
		if _, err := s.st.OIDCProvider(sess.Provider); err != nil {
			oidcPage(w, http.StatusNotFound, "Unknown provider", "This sign-in provider is not configured.")
			return
		}
		if err := s.st.NewOIDCSession(sess); err != nil {
			oidcPage(w, http.StatusInternalServerError, "Sign-in failed", "Could not start the sign-in.")
			return
		}
	}
	p, err := s.st.OIDCProvider(sess.Provider)
	if err != nil {
		oidcPage(w, http.StatusNotFound, "Unknown provider", "This sign-in provider is not configured.")
		return
	}
	meta, err := s.oidc.discover(p)
	if err != nil {
		log.Printf("oidc %s: %v", p.Name, err)
		oidcPage(w, http.StatusBadGateway, "Sign-in failed", "The sign-in provider could not be reached.")
		return
	}
	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		oidcPage(w, http.StatusBadGateway, "Sign-in failed", "The sign-in provider is misconfigured.")
		return
	}
	v := u.Query()
	v.Set("response_type", "code")
	v.Set("client_id", p.ClientID)
	v.Set("redirect_uri", s.publicURL(r)+oidcCallbackPath)
	v.Set("scope", p.Scopes)
	v.Set("state", sess.State)
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

type oidcClaims struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     any    `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

func (c *oidcClaims) verified() bool {
	switch v := c.EmailVerified.(type) {
	case bool:
		return v
	case string:
		b, _ := strconv.ParseBool(v)
		return b
	}
	return false
}

// exchange trades the authorization code for the user's claims. The claims are
// read from the userinfo endpoint over TLS, so no token signature check is needed.
func (s *Server) exchange(r *http.Request, p *store.OIDCProvider, code string) (*oidcClaims, error) {
	meta, err := s.oidc.discover(p)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.publicURL(r) + oidcCallbackPath},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}
	treq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	treq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	treq.Header.Set("Accept", "application/json")
	resp, err := oidcHTTP.Do(treq)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok) != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("token request refused (%d %s)", resp.StatusCode, tok.Error)
	}
	if isGitHub(p) {
		return githubClaims(r, tok.AccessToken)
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, meta.UserinfoEndpoint, nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	ui, err := oidcHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer ui.Body.Close()
	c := &oidcClaims{}
	if ui.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(ui.Body, 1<<20)).Decode(c) != nil || c.Sub == "" {
		return nil, errors.New("userinfo response is unusable")
	}
	return c, nil
}

// githubClaims reads the account and its primary verified e-mail from GitHub's API.
func githubClaims(r *http.Request, token string) (*oidcClaims, error) {
	get := func(u string, v any) error {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "rustdesk-api-lite")
		res, err := oidcHTTP.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("github answered %d", res.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := get("https://api.github.com/user", &u); err != nil || u.ID == 0 {
		return nil, errors.New("userinfo response is unusable")
	}
	c := &oidcClaims{Sub: strconv.FormatInt(u.ID, 10), PreferredUsername: u.Login, Name: u.Name}
	var mails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if get("https://api.github.com/user/emails", &mails) == nil {
		for _, m := range mails {
			if m.Primary && m.Verified {
				c.Email, c.EmailVerified = m.Email, true
			}
		}
	}
	return c, nil
}

var usernameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// oidcUser maps provider claims to a local user: an already linked identity,
// else an existing account with the same verified e-mail, else a new account
// when the provider allows self-registration.
func (s *Server) oidcUser(p *store.OIDCProvider, c *oidcClaims) (*store.User, error) {
	u, err := s.st.UserByIdentity(p.Name, c.Sub)
	if err == nil {
		if u.Disabled {
			return nil, errors.New("this account is disabled")
		}
		return u, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if c.Email != "" && c.verified() {
		us, err := s.st.ListUsers()
		if err != nil {
			return nil, err
		}
		for _, cand := range us {
			if strings.EqualFold(cand.Email, c.Email) {
				if cand.Disabled {
					return nil, errors.New("this account is disabled")
				}
				return cand, s.st.LinkIdentity(p.Name, c.Sub, cand.ID, c.Email)
			}
		}
	}
	if !p.AutoRegister {
		return nil, errors.New("no account is linked to this sign-in; ask an administrator to create one with your e-mail address")
	}
	base := c.PreferredUsername
	if base == "" {
		base, _, _ = strings.Cut(c.Email, "@")
	}
	base = strings.Trim(usernameUnsafe.ReplaceAllString(base, "-"), "-.")
	if base == "" {
		base = "user"
	}
	nu := &store.User{Email: c.Email, DisplayName: c.Name}
	for i := 0; i < 50; i++ {
		nu.Username = base
		if i > 0 {
			nu.Username = base + strconv.Itoa(i+1)
		}
		if err = s.st.CreateUser(nu, "", ""); err == nil {
			return nu, s.st.LinkIdentity(p.Name, c.Sub, nu.ID, c.Email)
		}
	}
	return nil, err
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sess, err := s.st.OIDCSessionByState(q.Get("state"))
	if err != nil || sess.Result != "" {
		oidcPage(w, http.StatusBadRequest, "Sign-in expired", "Start the sign-in again.")
		return
	}
	finish := func(u *store.User, err error) {
		if err != nil {
			log.Printf("oidc %s: %v", sess.Provider, err)
			if sess.Purpose == "client" {
				_ = s.st.SetOIDCResult(sess.Code, "error:"+err.Error())
			} else {
				s.st.DeleteOIDCSession(sess.Code)
			}
			oidcPage(w, http.StatusForbidden, "Sign-in failed", err.Error())
			return
		}
		if sess.Purpose == "client" {
			_ = s.st.SetOIDCResult(sess.Code, "user:"+strconv.FormatInt(u.ID, 10))
			oidcPage(w, http.StatusOK, "Signed in", "You can close this window and return to RustDesk.")
			return
		}
		s.st.DeleteOIDCSession(sess.Code)
		s.st.AddLoginLog(&store.LoginLog{UserID: u.ID, Username: u.Username, Kind: "console", Method: sess.Provider, DeviceName: "Web console", IP: s.clientIP(r), OK: true})
		if err := s.newConsoleSession(w, r, u); err != nil {
			oidcPage(w, http.StatusInternalServerError, "Sign-in failed", "Could not create a session.")
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	}
	if e := q.Get("error"); e != "" {
		finish(nil, fmt.Errorf("the provider refused the sign-in (%s)", e))
		return
	}
	p, err := s.st.OIDCProvider(sess.Provider)
	if err != nil {
		finish(nil, errors.New("the sign-in provider is no longer configured"))
		return
	}
	claims, err := s.exchange(r, p, q.Get("code"))
	if err != nil {
		log.Printf("oidc %s: %v", p.Name, err)
		finish(nil, errors.New("the sign-in provider did not confirm the sign-in"))
		return
	}
	if strings.HasPrefix(sess.Purpose, "link:") {
		uid, _ := strconv.ParseInt(strings.TrimPrefix(sess.Purpose, "link:"), 10, 64)
		s.st.DeleteOIDCSession(sess.Code)
		cur, _ := s.consoleUser(r)
		if cur == nil || cur.ID != uid {
			oidcPage(w, http.StatusForbidden, "Sign-in not linked", "The console session changed in the meantime. Try again.")
			return
		}
		if other, err := s.st.UserByIdentity(p.Name, claims.Sub); err == nil && other.ID != uid {
			oidcPage(w, http.StatusConflict, "Sign-in not linked", "This sign-in already belongs to another account.")
			return
		}
		if err := s.st.LinkIdentity(p.Name, claims.Sub, uid, claims.Email); err != nil {
			oidcPage(w, http.StatusInternalServerError, "Sign-in not linked", "Could not save the link.")
			return
		}
		http.Redirect(w, r, "/#/settings", http.StatusFound)
		return
	}
	finish(s.oidcUser(p, claims))
}
