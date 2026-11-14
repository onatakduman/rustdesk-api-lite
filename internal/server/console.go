package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

const (
	sessionCookie = "rdl_session"
	consoleTTL    = 14 * 24 * 3600
)

type consoleCtx struct {
	w    http.ResponseWriter
	r    *http.Request
	user *store.User
	tok  string
}

type consoleHandler func(c *consoleCtx)

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (c *consoleCtx) fail(status int, msg string) { fail(c.w, status, msg) }
func (c *consoleCtx) ok(v any)                    { writeJSON(c.w, http.StatusOK, v) }
func (c *consoleCtx) done()                       { writeJSON(c.w, http.StatusOK, map[string]bool{"ok": true}) }

// err reports a store error with a fitting status and tells whether there was one.
func (c *consoleCtx) err(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		c.fail(http.StatusNotFound, "not found")
	} else {
		c.fail(http.StatusBadRequest, err.Error())
	}
	return true
}

func (c *consoleCtx) body(v any) bool {
	if err := readJSON(c.w, c.r, v, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// sameOrigin rejects state-changing requests that a foreign page could have
// triggered: they must carry a custom header and, when present, a matching Origin.
func (s *Server) sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if r.Header.Get("X-RDL") == "" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

func (s *Server) consoleUser(r *http.Request) (*store.User, string) {
	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		return nil, ""
	}
	u, t, err := s.st.UserByToken(ck.Value)
	if err != nil || t.Kind != "console" {
		return nil, ""
	}
	return u, ck.Value
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, token string) {
	ck := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.isHTTPS(r)}
	if token == "" {
		ck.MaxAge = -1
	} else {
		ck.MaxAge = consoleTTL
	}
	http.SetCookie(w, ck)
}

func (s *Server) newConsoleSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	tok, err := s.st.NewToken(&store.Token{UserID: u.ID, Kind: "console", DeviceName: "Web console", DeviceOS: r.UserAgent(), IP: s.clientIP(r)}, consoleTTL)
	if err != nil {
		return err
	}
	s.setSession(w, r, tok)
	return nil
}

// authed wraps a handler that needs a signed-in console user.
func (s *Server) authed(admin bool, h consoleHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sameOrigin(r) {
			fail(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		u, tok := s.consoleUser(r)
		if u == nil {
			fail(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if admin && !u.IsAdmin {
			fail(w, http.StatusForbidden, "administrator rights required")
			return
		}
		h(&consoleCtx{w: w, r: r, user: u, tok: tok})
	}
}

func (s *Server) consoleRoutes() {
	m := s.mux
	const p = "/console/api"

	m.HandleFunc("GET "+p+"/bootstrap", s.handleBootstrap)
	m.HandleFunc("POST "+p+"/login", s.handleConsoleLogin)
	m.HandleFunc("POST "+p+"/logout", s.handleConsoleLogout)

	m.HandleFunc("GET "+p+"/overview", s.authed(false, s.cOverview))
	m.HandleFunc("POST "+p+"/me/password", s.authed(false, s.cOwnPassword))
	m.HandleFunc("DELETE "+p+"/me/identities/{provider}", s.authed(false, s.cOwnUnlink))
	m.HandleFunc("POST "+p+"/me/totp/new", s.authed(false, s.cTOTPNew))
	m.HandleFunc("POST "+p+"/me/totp", s.authed(false, s.cTOTPEnable))
	m.HandleFunc("POST "+p+"/me/totp/off", s.authed(false, s.cTOTPDisable))

	m.HandleFunc("GET "+p+"/devices", s.authed(false, s.cDevices))
	m.HandleFunc("PATCH "+p+"/devices/{id}", s.authed(true, s.cDevicePatch))
	m.HandleFunc("POST "+p+"/devices/{id}/disconnect", s.authed(true, s.cDeviceDisconnect))
	m.HandleFunc("DELETE "+p+"/devices/{id}", s.authed(true, s.cDeviceDelete))

	m.HandleFunc("GET "+p+"/address-books", s.authed(false, s.cBooks))
	m.HandleFunc("POST "+p+"/address-books", s.authed(false, s.cBookCreate))
	m.HandleFunc("GET "+p+"/address-books/{guid}", s.authed(false, s.cBook))
	m.HandleFunc("PATCH "+p+"/address-books/{guid}", s.authed(false, s.cBookPatch))
	m.HandleFunc("DELETE "+p+"/address-books/{guid}", s.authed(false, s.cBookDelete))
	m.HandleFunc("PUT "+p+"/address-books/{guid}/peers", s.authed(false, s.cPeerPut))
	m.HandleFunc("POST "+p+"/address-books/{guid}/peers/delete", s.authed(false, s.cPeerDelete))
	m.HandleFunc("PUT "+p+"/address-books/{guid}/tags", s.authed(false, s.cTagPut))
	m.HandleFunc("POST "+p+"/address-books/{guid}/tags/delete", s.authed(false, s.cTagDelete))
	m.HandleFunc("PUT "+p+"/address-books/{guid}/shares", s.authed(false, s.cSharesPut))

	m.HandleFunc("GET "+p+"/users", s.authed(false, s.cUsers))
	m.HandleFunc("POST "+p+"/users", s.authed(true, s.cUserCreate))
	m.HandleFunc("PATCH "+p+"/users/{id}", s.authed(true, s.cUserPatch))
	m.HandleFunc("POST "+p+"/users/{id}/password", s.authed(true, s.cUserPassword))
	m.HandleFunc("DELETE "+p+"/users/{id}", s.authed(true, s.cUserDelete))

	m.HandleFunc("GET "+p+"/sessions", s.authed(false, s.cSessions))
	m.HandleFunc("DELETE "+p+"/sessions/{id}", s.authed(false, s.cSessionDelete))
	m.HandleFunc("POST "+p+"/tokens", s.authed(false, s.cTokenCreate))

	m.HandleFunc("GET "+p+"/logs/connections", s.authed(true, s.cLogConns))
	m.HandleFunc("GET "+p+"/logs/files", s.authed(true, s.cLogFiles))
	m.HandleFunc("GET "+p+"/logs/logins", s.authed(false, s.cLogLogins))
	m.HandleFunc("GET "+p+"/logs/alarms", s.authed(true, s.cLogAlarms))

	m.HandleFunc("GET "+p+"/settings", s.authed(true, s.cSettings))
	m.HandleFunc("PUT "+p+"/settings", s.authed(true, s.cSettingsPut))
	m.HandleFunc("PUT "+p+"/oidc/{name}", s.authed(true, s.cOIDCPut))
	m.HandleFunc("DELETE "+p+"/oidc/{name}", s.authed(true, s.cOIDCDelete))

	// Static console files; unknown paths get the single-page app.
	if s.cfg.Web != nil {
		files := http.FileServerFS(s.cfg.Web)
		m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
			name := strings.TrimPrefix(r.URL.Path, "/")
			if name == "" {
				name = "index.html"
			}
			if _, err := fs.Stat(s.cfg.Web, name); err != nil {
				if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, p+"/") {
					fail(w, http.StatusNotFound, "not found")
					return
				}
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		})
	}
}

type userView struct {
	*store.User
	Providers []string `json:"providers"`
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"version": Version, "user": nil}
	names := []string{}
	if ps, err := s.st.OIDCProviders(); err == nil {
		for _, p := range ps {
			names = append(names, p.Name)
		}
	}
	out["providers"] = names
	if u, _ := s.consoleUser(r); u != nil {
		out["user"] = userView{u, s.st.IdentityProviders(u.ID)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleConsoleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		fail(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	var in struct{ Username, Password, Code string }
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.checkLogin(r, in.Username, in.Password)
	if err == nil && u.HasTOTP {
		if strings.TrimSpace(in.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "enter the code from your authenticator app", "totp": true})
			return
		}
		if !totpValid(s.st.TOTPSecret(u.ID), in.Code) {
			s.fails.fail("user:" + strings.ToLower(u.Username))
			err = errors.New("that code is not right")
		}
	}
	entry := &store.LoginLog{Username: in.Username, Kind: "console", Method: "password", DeviceName: "Web console", IP: s.clientIP(r)}
	if err != nil {
		s.st.AddLoginLog(entry)
		fail(w, http.StatusUnauthorized, err.Error())
		return
	}
	entry.UserID, entry.Username, entry.OK = u.ID, u.Username, true
	s.st.AddLoginLog(entry)
	if err := s.newConsoleSession(w, r, u); err != nil {
		fail(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userView{u, s.st.IdentityProviders(u.ID)}})
}

func (s *Server) handleConsoleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		fail(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	if _, tok := s.consoleUser(r); tok != "" {
		s.st.DeleteToken(tok)
	}
	s.setSession(w, r, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) cOverview(c *consoleCtx) {
	c.ok(map[string]any{"counts": s.st.Counts(), "client": s.clientConfig(c.r), "version": Version})
}

func (s *Server) cOwnPassword(c *consoleCtx) {
	var in struct{ Current, New string }
	if !c.body(&in) {
		return
	}
	if len(in.New) < 8 {
		c.fail(http.StatusBadRequest, "the new password needs at least 8 characters")
		return
	}
	if c.user.HasPassword {
		if _, err := s.checkLogin(c.r, c.user.Username, in.Current); err != nil {
			c.fail(http.StatusForbidden, "current password is wrong")
			return
		}
	}
	if c.err(s.st.SetPassword(c.user.ID, in.New, c.tok)) {
		return
	}
	c.done()
}

// cOwnUnlink removes an external sign-in from the caller's account, unless it
// is the only way left to sign in.
func (s *Server) cOwnUnlink(c *consoleCtx) {
	if !c.user.HasPassword && len(s.st.IdentityProviders(c.user.ID)) <= 1 {
		c.fail(http.StatusBadRequest, "set a password first; this is your only way to sign in")
		return
	}
	if c.err(s.st.UnlinkIdentity(c.r.PathValue("provider"), c.user.ID)) {
		return
	}
	c.done()
}

// ---- devices ----

func (s *Server) cDevices(c *consoleCtx) {
	scope := c.user.ID
	if c.user.IsAdmin {
		scope = 0
	}
	ds, err := s.st.Devices(scope)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"devices": ds})
}

func (s *Server) cDevicePatch(c *consoleCtx) {
	in := struct {
		Note, Group *string
		OwnerID     *int64 `json:"owner_id"`
	}{}
	if !c.body(&in) {
		return
	}
	id := c.r.PathValue("id")
	if _, err := s.st.Device(id); c.err(err) {
		return
	}
	if in.Note != nil && c.err(s.st.SetDeviceNote(id, *in.Note)) {
		return
	}
	if in.Group != nil && c.err(s.st.SetDeviceGroup(id, strings.TrimSpace(*in.Group))) {
		return
	}
	if in.OwnerID != nil {
		if *in.OwnerID != 0 {
			if _, err := s.st.UserByID(*in.OwnerID); c.err(err) {
				return
			}
		}
		if c.err(s.st.SetDeviceOwner(id, *in.OwnerID)) {
			return
		}
	}
	c.done()
}

// cDeviceDisconnect asks a device to close one of its incoming sessions.
func (s *Server) cDeviceDisconnect(c *consoleCtx) {
	var in struct {
		ConnID int64 `json:"conn_id"`
	}
	if !c.body(&in) {
		return
	}
	d, err := s.st.Device(c.r.PathValue("id"))
	if c.err(err) {
		return
	}
	s.kicks.add(d.ID, in.ConnID)
	c.done()
}

func (s *Server) cDeviceDelete(c *consoleCtx) {
	if c.err(s.st.DeleteDevice(c.r.PathValue("id"))) {
		return
	}
	c.done()
}

// ---- address books ----

// book loads an address book and checks that the caller has at least rule `need`.
func (s *Server) book(c *consoleCtx, need int) *store.AddressBook {
	b, err := s.st.AB(c.r.PathValue("guid"), c.user.ID)
	if c.err(err) {
		return nil
	}
	if c.user.IsAdmin {
		b.Rule = store.RuleFull
	}
	if b.Rule < need {
		if b.Rule == store.RuleNone {
			c.fail(http.StatusNotFound, "not found")
		} else {
			c.fail(http.StatusForbidden, "you do not have permission to change this address book")
		}
		return nil
	}
	return b
}

func (s *Server) cBooks(c *consoleCtx) {
	var bs []*store.AddressBook
	var err error
	if c.user.IsAdmin && c.r.URL.Query().Get("all") == "1" {
		bs, err = s.st.AllABs(c.user.ID)
		for _, b := range bs {
			b.Rule = store.RuleFull
		}
	} else {
		bs, err = s.st.ABsFor(c.user.ID)
	}
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"address_books": bs})
}

func (s *Server) cBookCreate(c *consoleCtx) {
	var in struct{ Name, Note string }
	if !c.body(&in) {
		return
	}
	guid, err := s.st.CreateAB(c.user.ID, strings.TrimSpace(in.Name), in.Note)
	if c.err(err) {
		return
	}
	c.ok(map[string]string{"guid": guid})
}

func (s *Server) cBook(c *consoleCtx) {
	b := s.book(c, store.RuleRead)
	if b == nil {
		return
	}
	peers, _, err := s.st.Peers(b.GUID, 0, 0)
	if c.err(err) {
		return
	}
	tags, err := s.st.Tags(b.GUID)
	if c.err(err) {
		return
	}
	out := map[string]any{"book": b, "peers": peers, "tags": tags}
	if b.Rule == store.RuleFull {
		shares, err := s.st.Shares(b.GUID)
		if c.err(err) {
			return
		}
		out["shares"] = shares
	}
	c.ok(out)
}

func (s *Server) cBookPatch(c *consoleCtx) {
	b := s.book(c, store.RuleFull)
	if b == nil {
		return
	}
	in := struct {
		Name, Note      *string
		DefaultPassword *string `json:"default_password"`
	}{}
	if !c.body(&in) {
		return
	}
	if in.DefaultPassword != nil && c.err(s.st.SetABPassword(b.GUID, *in.DefaultPassword)) {
		return
	}
	if in.Name != nil {
		b.Name = strings.TrimSpace(*in.Name)
	}
	if in.Note != nil {
		b.Note = *in.Note
	}
	if c.err(s.st.UpdateAB(b.GUID, b.Name, b.Note)) {
		return
	}
	c.done()
}

func (s *Server) cBookDelete(c *consoleCtx) {
	b := s.book(c, store.RuleFull)
	if b == nil {
		return
	}
	if c.err(s.st.DeleteAB(b.GUID)) {
		return
	}
	c.done()
}

// cPeerPut creates a peer or merges fields into an existing one.
func (s *Server) cPeerPut(c *consoleCtx) {
	b := s.book(c, store.RuleReadWrite)
	if b == nil {
		return
	}
	in := store.Peer{}
	if !c.body(&in) {
		return
	}
	id := strings.TrimSpace(store.PeerID(in))
	if id == "" {
		c.fail(http.StatusBadRequest, "peer id is required")
		return
	}
	in["id"] = id
	err := s.st.PatchPeer(b.GUID, in)
	if errors.Is(err, store.ErrNotFound) {
		err = s.st.PutPeer(b.GUID, in)
	}
	if c.err(err) {
		return
	}
	c.done()
}

func (s *Server) cPeerDelete(c *consoleCtx) {
	b := s.book(c, store.RuleReadWrite)
	if b == nil {
		return
	}
	var in struct{ IDs []string }
	if !c.body(&in) {
		return
	}
	if c.err(s.st.DeletePeers(b.GUID, in.IDs)) {
		return
	}
	c.done()
}

func (s *Server) cTagPut(c *consoleCtx) {
	b := s.book(c, store.RuleReadWrite)
	if b == nil {
		return
	}
	var in struct {
		Name   string
		Color  int64
		Rename string
	}
	if !c.body(&in) {
		return
	}
	in.Name, in.Rename = strings.TrimSpace(in.Name), strings.TrimSpace(in.Rename)
	if in.Rename != "" && in.Rename != in.Name {
		if c.err(s.st.RenameTag(b.GUID, in.Name, in.Rename)) {
			return
		}
		in.Name = in.Rename
	}
	if c.err(s.st.PutTag(b.GUID, store.Tag{Name: in.Name, Color: in.Color})) {
		return
	}
	c.done()
}

func (s *Server) cTagDelete(c *consoleCtx) {
	b := s.book(c, store.RuleReadWrite)
	if b == nil {
		return
	}
	var in struct{ Names []string }
	if !c.body(&in) {
		return
	}
	if c.err(s.st.DeleteTags(b.GUID, in.Names)) {
		return
	}
	c.done()
}

func (s *Server) cSharesPut(c *consoleCtx) {
	b := s.book(c, store.RuleFull)
	if b == nil {
		return
	}
	var in struct{ Shares []store.Share }
	if !c.body(&in) {
		return
	}
	if c.err(s.st.SetShares(b.GUID, in.Shares)) {
		return
	}
	c.done()
}

// ---- users ----

func (s *Server) cUsers(c *consoleCtx) {
	us, err := s.st.ListUsers()
	if c.err(err) {
		return
	}
	if !c.user.IsAdmin {
		// Everyone may see who exists (needed to share an address book), nothing more.
		type brief struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		}
		out := []brief{}
		for _, u := range us {
			if !u.Disabled {
				out = append(out, brief{u.ID, u.Username})
			}
		}
		c.ok(map[string]any{"users": out})
		return
	}
	out := []userView{}
	for _, u := range us {
		out = append(out, userView{u, s.st.IdentityProviders(u.ID)})
	}
	c.ok(map[string]any{"users": out})
}

func (s *Server) cUserCreate(c *consoleCtx) {
	var in struct {
		Username, Password, Email string
		DisplayName               string `json:"display_name"`
		IsAdmin                   bool   `json:"is_admin"`
	}
	if !c.body(&in) {
		return
	}
	if in.Password != "" && len(in.Password) < 8 {
		c.fail(http.StatusBadRequest, "the password needs at least 8 characters")
		return
	}
	u := &store.User{Username: in.Username, Email: strings.TrimSpace(in.Email), DisplayName: in.DisplayName, IsAdmin: in.IsAdmin}
	if c.err(s.st.CreateUser(u, in.Password, "")) {
		return
	}
	c.ok(map[string]any{"user": u})
}

func (s *Server) pathUser(c *consoleCtx) *store.User {
	id, _ := strconv.ParseInt(c.r.PathValue("id"), 10, 64)
	u, err := s.st.UserByID(id)
	if c.err(err) {
		return nil
	}
	return u
}

func (s *Server) cUserPatch(c *consoleCtx) {
	u := s.pathUser(c)
	if u == nil {
		return
	}
	in := struct {
		Email, Note *string
		DisplayName *string `json:"display_name"`
		IsAdmin     *bool   `json:"is_admin"`
		Disabled    *bool
		ResetTOTP   bool `json:"reset_totp"`
	}{}
	if !c.body(&in) {
		return
	}
	wasAdmin := u.IsAdmin && !u.Disabled
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if in.Note != nil {
		u.Note = *in.Note
	}
	if in.DisplayName != nil {
		u.DisplayName = *in.DisplayName
	}
	if in.IsAdmin != nil {
		u.IsAdmin = *in.IsAdmin
	}
	if in.Disabled != nil {
		u.Disabled = *in.Disabled
	}
	if wasAdmin && !(u.IsAdmin && !u.Disabled) && s.st.CountAdmins() <= 1 {
		c.fail(http.StatusBadRequest, "this is the last administrator")
		return
	}
	if c.err(s.st.UpdateUser(u)) {
		return
	}
	if in.ResetTOTP && c.err(s.st.SetTOTP(u.ID, "")) {
		return
	}
	c.done()
}

func (s *Server) cUserPassword(c *consoleCtx) {
	u := s.pathUser(c)
	if u == nil {
		return
	}
	var in struct{ Password string }
	if !c.body(&in) {
		return
	}
	if len(in.Password) < 8 {
		c.fail(http.StatusBadRequest, "the password needs at least 8 characters")
		return
	}
	keep := ""
	if u.ID == c.user.ID {
		keep = c.tok
	}
	if c.err(s.st.SetPassword(u.ID, in.Password, keep)) {
		return
	}
	c.done()
}

func (s *Server) cUserDelete(c *consoleCtx) {
	u := s.pathUser(c)
	if u == nil {
		return
	}
	if u.ID == c.user.ID {
		c.fail(http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	if u.IsAdmin && !u.Disabled && s.st.CountAdmins() <= 1 {
		c.fail(http.StatusBadRequest, "this is the last administrator")
		return
	}
	if c.err(s.st.DeleteUser(u.ID)) {
		return
	}
	c.done()
}

// ---- sessions and logs ----

func (s *Server) cSessions(c *consoleCtx) {
	scope := c.user.ID
	if c.user.IsAdmin && c.r.URL.Query().Get("all") == "1" {
		scope = 0
	}
	ts, err := s.st.ListTokens(scope)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"sessions": ts})
}

func (s *Server) cSessionDelete(c *consoleCtx) {
	scope := c.user.ID
	if c.user.IsAdmin {
		scope = 0
	}
	if c.err(s.st.DeleteTokenByID(c.r.PathValue("id"), scope)) {
		return
	}
	c.done()
}

// cTokenCreate makes a named API token for scripts and for `rustdesk --assign`.
// The secret is returned once and stored only as a hash.
func (s *Server) cTokenCreate(c *consoleCtx) {
	var in struct{ Name string }
	if !c.body(&in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		c.fail(http.StatusBadRequest, "give the token a name")
		return
	}
	tok, err := s.st.NewToken(&store.Token{UserID: c.user.ID, Kind: "api", Name: in.Name, IP: s.clientIP(c.r)}, 0)
	if c.err(err) {
		return
	}
	c.ok(map[string]string{"token": tok})
}

func (s *Server) cLogLogins(c *consoleCtx) {
	limit, offset := page(c.r)
	scope := c.user.ID
	if c.user.IsAdmin {
		scope = 0
	}
	rows, total, err := s.st.LoginLogs(scope, limit, offset)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"total": total, "rows": rows})
}

func (s *Server) cLogAlarms(c *consoleCtx) {
	limit, offset := page(c.r)
	rows, total, err := s.st.Alarms(limit, offset)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"total": total, "rows": rows})
}

func page(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return
}

func (s *Server) cLogConns(c *consoleCtx) {
	limit, offset := page(c.r)
	rows, total, err := s.st.ConnAudits(limit, offset)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"total": total, "rows": rows})
}

func (s *Server) cLogFiles(c *consoleCtx) {
	limit, offset := page(c.r)
	rows, total, err := s.st.FileAudits(limit, offset)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"total": total, "rows": rows})
}

// ---- settings ----

func (s *Server) cSettings(c *consoleCtx) {
	ps, err := s.st.OIDCProviders()
	if c.err(err) {
		return
	}
	c.ok(map[string]any{
		"id_server":     s.st.Setting(keyIDServer),
		"relay_server":  s.st.Setting(keyRelayServer),
		"server_key":    s.st.Setting(keyServerKey),
		"public_url":    s.st.Setting(keyPublicURL),
		"allow_presets": s.st.Setting(keyAllowPresets) == "1",
		"log_days":      s.st.Setting(keyLogDays),
		"strategy":      strategyOptions(s.st.Setting(keyStrategy)),
		"oidc":          ps,
		"oidc_callback": s.publicURL(c.r) + oidcCallbackPath,
	})
}

func strategyOptions(raw string) map[string]string {
	opts := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &opts)
	return opts
}

func (s *Server) cSettingsPut(c *consoleCtx) {
	in := map[string]json.RawMessage{}
	if !c.body(&in) {
		return
	}
	for _, k := range []string{keyIDServer, keyRelayServer, keyServerKey, keyPublicURL} {
		if raw, ok := in[k]; ok {
			var v string
			if json.Unmarshal(raw, &v) != nil || c.err(s.st.SetSetting(k, strings.TrimSpace(v))) {
				return
			}
		}
	}
	if raw, ok := in["allow_presets"]; ok {
		v := "0"
		if string(raw) == "true" {
			v = "1"
		}
		if c.err(s.st.SetSetting(keyAllowPresets, v)) {
			return
		}
	}
	if raw, ok := in["log_days"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 0 {
			v = ""
		}
		if c.err(s.st.SetSetting(keyLogDays, strings.TrimSpace(v))) {
			return
		}
	}
	if raw, ok := in["strategy"]; ok {
		opts := map[string]string{}
		if json.Unmarshal(raw, &opts) != nil {
			c.fail(http.StatusBadRequest, "device options must be an object of text values")
			return
		}
		for k := range opts {
			if strings.TrimSpace(k) == "" {
				delete(opts, k)
			}
		}
		enc, _ := json.Marshal(opts)
		if string(enc) != s.st.Setting(keyStrategy) {
			// A new timestamp makes every device fetch the options with its next heartbeat.
			if c.err(s.st.SetSetting(keyStrategy, string(enc))) || c.err(s.st.SetSetting(keyStrategyAt, strconv.FormatInt(time.Now().Unix(), 10))) {
				return
			}
		}
	}
	c.done()
}

func (s *Server) cOIDCPut(c *consoleCtx) {
	var in struct {
		Issuer       string
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Scopes       string
		AutoRegister bool `json:"auto_register"`
	}
	if !c.body(&in) {
		return
	}
	p := &store.OIDCProvider{Name: strings.ToLower(strings.TrimSpace(c.r.PathValue("name"))), Issuer: strings.TrimRight(strings.TrimSpace(in.Issuer), "/"),
		ClientID: strings.TrimSpace(in.ClientID), ClientSecret: strings.TrimSpace(in.ClientSecret), Scopes: strings.TrimSpace(in.Scopes), AutoRegister: in.AutoRegister}
	if p.Scopes == "" && isGitHub(p) {
		p.Scopes = "read:user user:email"
	}
	if c.err(s.st.SaveOIDCProvider(p)) {
		return
	}
	s.oidc.forget(p.Name)
	c.done()
}

func (s *Server) cOIDCDelete(c *consoleCtx) {
	name := c.r.PathValue("name")
	if c.err(s.st.DeleteOIDCProvider(name)) {
		return
	}
	s.oidc.forget(name)
	c.done()
}
