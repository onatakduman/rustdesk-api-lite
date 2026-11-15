package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
		if u.MustChangePassword && r.URL.Path != passwordPath {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "change the password of this account first", "must_change_password": true})
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
	m.HandleFunc("POST "+passwordPath, s.authed(false, s.cOwnPassword))
	m.HandleFunc("DELETE "+p+"/me/identities/{provider}", s.authed(false, s.cOwnUnlink))
	m.HandleFunc("POST "+p+"/me/totp/new", s.authed(false, s.cTOTPNew))
	m.HandleFunc("POST "+p+"/me/totp", s.authed(false, s.cTOTPEnable))
	m.HandleFunc("POST "+p+"/me/totp/off", s.authed(false, s.cTOTPDisable))

	m.HandleFunc("GET "+p+"/devices", s.authed(false, s.cDevices))
	m.HandleFunc("PATCH "+p+"/devices/{id}", s.authed(true, s.cDevicePatch))
	m.HandleFunc("POST "+p+"/devices/{id}/disconnect", s.authed(true, s.cDeviceDisconnect))
	m.HandleFunc("DELETE "+p+"/devices/{id}", s.authed(true, s.cDeviceDelete))

	m.HandleFunc("GET "+p+"/address-books", s.authed(false, s.cBooks))
	m.HandleFunc("GET "+p+"/search-index", s.authed(false, s.cSearchIndex))
	m.HandleFunc("POST "+p+"/online", s.authed(false, s.cOnline))
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
	m.HandleFunc("POST "+p+"/users/{id}/invite", s.authed(true, s.cUserInvite))
	m.HandleFunc("DELETE "+p+"/users/{id}/invite", s.authed(true, s.cUserInviteDelete))
	// What the page behind an invite link calls; the link's secret is the credential.
	m.HandleFunc("POST "+p+"/invite/check", s.handleInviteCheck)
	m.HandleFunc("POST "+p+"/invite/accept", s.handleInviteAccept)
	m.HandleFunc("POST "+p+"/invite/provider", s.handleInviteProvider)
	m.HandleFunc("DELETE "+p+"/users/{id}", s.authed(true, s.cUserDelete))

	m.HandleFunc("GET "+p+"/groups", s.authed(false, s.cGroups))
	m.HandleFunc("PUT "+p+"/groups/{id}", s.authed(true, s.cGroupPut))
	m.HandleFunc("DELETE "+p+"/groups/{id}", s.authed(true, s.cGroupDelete))

	m.HandleFunc("GET "+p+"/sessions", s.authed(false, s.cSessions))
	m.HandleFunc("DELETE "+p+"/sessions/{id}", s.authed(false, s.cSessionDelete))
	m.HandleFunc("POST "+p+"/tokens", s.authed(false, s.cTokenCreate))

	m.HandleFunc("GET "+p+"/logs/connections", s.authed(true, s.cLogConns))
	m.HandleFunc("GET "+p+"/logs/files", s.authed(true, s.cLogFiles))
	m.HandleFunc("GET "+p+"/logs/logins", s.authed(false, s.cLogLogins))
	m.HandleFunc("GET "+p+"/logs/alarms", s.authed(true, s.cLogAlarms))
	m.HandleFunc("GET "+p+"/logs/changes", s.authed(true, s.cLogChanges))

	m.HandleFunc("GET "+p+"/settings", s.authed(true, s.cSettings))
	m.HandleFunc("PUT "+p+"/settings", s.authed(true, s.cSettingsPut))
	m.HandleFunc("POST "+p+"/server-cmd", s.authed(true, s.cServerCmd))
	m.HandleFunc("POST "+p+"/mail/test", s.authed(true, s.cMailTest))
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

// passwordPath is the one console call an account that still has to change
// its password may make.
const passwordPath = "/console/api/me/password"

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
		if !s.totpAccept(u.ID, in.Code) {
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
	if c.user.MustChangePassword && in.New == in.Current {
		c.fail(http.StatusBadRequest, "choose a password of your own, not the one the account was created with")
		return
	}
	if c.err(s.st.SetPassword(c.user.ID, in.New, c.tok)) {
		return
	}
	s.change(c, "user.password", c.user.Username, "own password")
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
	s.change(c, "user.provider", c.user.Username, "unlinked "+c.r.PathValue("provider"))
	c.done()
}

// ---- devices ----

func (s *Server) cDevices(c *consoleCtx) {
	ds, err := s.st.DevicesFor(s.st.VisibleUsers(c.user))
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
	var what []string
	if in.Note != nil {
		what = append(what, "note")
	}
	if in.Group != nil {
		what = append(what, "group: "+strings.TrimSpace(*in.Group))
	}
	if in.OwnerID != nil {
		owner := "nobody"
		if u, err := s.st.UserByID(*in.OwnerID); err == nil {
			owner = u.Username
		}
		what = append(what, "owner: "+owner)
	}
	s.change(c, "device.update", id, strings.Join(what, "; "))
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
	s.change(c, "device.disconnect", d.ID, "session "+strconv.FormatInt(in.ConnID, 10))
	c.done()
}

func (s *Server) cDeviceDelete(c *consoleCtx) {
	if c.err(s.st.DeleteDevice(c.r.PathValue("id"))) {
		return
	}
	s.change(c, "device.forget", c.r.PathValue("id"), "")
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

// cSearchIndex returns every address book the caller may read together with its
// entries and tags, so the console can search across all of them at once.
func (s *Server) cSearchIndex(c *consoleCtx) {
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
	type entry struct {
		Book  *store.AddressBook `json:"book"`
		Peers []store.Peer       `json:"peers"`
		Tags  []store.Tag        `json:"tags"`
	}
	out := make([]entry, 0, len(bs))
	for _, b := range bs {
		peers, _, err := s.st.Peers(b.GUID, 0, 0)
		if c.err(err) {
			return
		}
		tags, err := s.st.Tags(b.GUID)
		if c.err(err) {
			return
		}
		out = append(out, entry{b, peers, tags})
	}
	c.ok(map[string]any{"books": out})
}

func (s *Server) cBookCreate(c *consoleCtx) {
	var in struct {
		Name, Note string
		OwnerID    int64 `json:"owner_id"`
	}
	if !c.body(&in) {
		return
	}
	owner := c.user.ID
	if in.OwnerID != 0 && in.OwnerID != owner {
		// Administrators may set up a book for somebody else.
		if !c.user.IsAdmin {
			c.fail(http.StatusForbidden, "administrator rights required")
			return
		}
		if _, err := s.st.UserByID(in.OwnerID); c.err(err) {
			return
		}
		owner = in.OwnerID
	}
	guid, err := s.st.CreateAB(owner, strings.TrimSpace(in.Name), in.Note)
	if c.err(err) {
		return
	}
	s.change(c, "book.create", strings.TrimSpace(in.Name), "")
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
	var what []string
	if in.Name != nil {
		what = append(what, "name")
	}
	if in.Note != nil {
		what = append(what, "note")
	}
	if in.DefaultPassword != nil {
		what = append(what, "default password")
	}
	s.change(c, "book.update", b.Name, strings.Join(what, "; "))
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
	s.change(c, "book.delete", b.Name, "")
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
	var who []string
	if shares, err := s.st.Shares(b.GUID); err == nil {
		for _, sh := range shares {
			if sh.GroupID != 0 {
				who = append(who, "group "+sh.Group+": "+ruleName(sh.Rule))
			} else {
				who = append(who, sh.Username+": "+ruleName(sh.Rule))
			}
		}
	}
	if len(who) == 0 {
		who = []string{"nobody"}
	}
	s.change(c, "book.shares", b.Name, strings.Join(who, "; "))
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
			if u.Active() {
				out = append(out, brief{u.ID, u.Username})
			}
		}
		c.ok(map[string]any{"users": out})
		return
	}
	type row struct {
		userView
		// InviteExpiresAt is set while the account has an unused invite link.
		InviteExpiresAt int64 `json:"invite_expires_at"`
	}
	pending := s.st.PendingInvites()
	out := []row{}
	for _, u := range us {
		out = append(out, row{userView{u, s.st.IdentityProviders(u.ID)}, pending[u.ID]})
	}
	_, mailReady := s.mailConfig()
	c.ok(map[string]any{"users": out, "mail": mailReady})
}

func (s *Server) cUserCreate(c *consoleCtx) {
	var in struct {
		Username, Password, Email string
		DisplayName               string `json:"display_name"`
		IsAdmin                   bool   `json:"is_admin"`
		GroupID                   int64  `json:"group_id"`
		ExpiresAt                 int64  `json:"expires_at"`
		// Address books the new account can use from the start.
		Shares []struct {
			GUID string
			Rule int
		}
		// Invite makes a link with which the person sets their own password.
		Invite      bool
		InviteHours int  `json:"invite_hours"`
		InviteEmail bool `json:"invite_email"`
		Lang        string
	}
	if !c.body(&in) {
		return
	}
	if in.Password != "" && len(in.Password) < 8 {
		c.fail(http.StatusBadRequest, "the password needs at least 8 characters")
		return
	}
	if msg := endDateProblem(in.IsAdmin, in.ExpiresAt); msg != "" {
		c.fail(http.StatusBadRequest, msg)
		return
	}
	if in.GroupID != 0 && s.st.GroupName(in.GroupID) == "" {
		c.fail(http.StatusBadRequest, "that group does not exist")
		return
	}
	for _, sh := range in.Shares {
		if b, err := s.st.AB(sh.GUID, c.user.ID); err != nil || b.Personal || sh.Rule < store.RuleRead || sh.Rule > store.RuleFull {
			c.fail(http.StatusBadRequest, "one of the address books cannot be shared")
			return
		}
	}
	u := &store.User{Username: in.Username, Email: strings.TrimSpace(in.Email), DisplayName: in.DisplayName, IsAdmin: in.IsAdmin, GroupID: in.GroupID, ExpiresAt: in.ExpiresAt}
	if c.err(s.st.CreateUser(u, in.Password, "")) {
		return
	}
	what := []string{}
	if u.IsAdmin {
		what = append(what, "administrator")
	}
	if u.GroupID != 0 {
		what = append(what, "group: "+s.st.GroupName(u.GroupID))
	}
	if u.ExpiresAt != 0 {
		what = append(what, "ends "+time.Unix(u.ExpiresAt, 0).UTC().Format("2006-01-02"))
	}
	for _, sh := range in.Shares {
		if c.err(s.st.AddShare(sh.GUID, u.ID, sh.Rule)) {
			return
		}
		if b, err := s.st.AB(sh.GUID, c.user.ID); err == nil {
			what = append(what, "book "+b.Name+": "+ruleName(sh.Rule))
		}
	}
	s.change(c, "user.create", u.Username, strings.Join(what, "; "))
	out := map[string]any{"user": u}
	if in.Invite {
		res, err := s.newInvite(c, u, in.InviteHours, in.InviteEmail, in.Lang)
		if c.err(err) {
			return
		}
		out["invite"] = res
	}
	c.ok(out)
}

// endDateProblem says why an account may not get this end date ("" when it may).
func endDateProblem(admin bool, expiresAt int64) string {
	switch {
	case expiresAt == 0:
		return ""
	case admin:
		// An administrator that ends could leave the server without one.
		return "an administrator cannot have an end date"
	case expiresAt <= time.Now().Unix():
		return "the end date has to be in the future"
	}
	return ""
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
		GroupID     *int64 `json:"group_id"`
		ExpiresAt   *int64 `json:"expires_at"`
		ResetTOTP   bool   `json:"reset_totp"`
	}{}
	if !c.body(&in) {
		return
	}
	before := *u
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
	if in.GroupID != nil {
		u.GroupID = *in.GroupID
	}
	if in.ExpiresAt != nil {
		u.ExpiresAt = *in.ExpiresAt
	}
	if wasAdmin && !(u.IsAdmin && !u.Disabled) && s.st.CountAdmins() <= 1 {
		c.fail(http.StatusBadRequest, "this is the last administrator")
		return
	}
	if u.ExpiresAt != before.ExpiresAt || (u.IsAdmin && u.ExpiresAt != 0) {
		if msg := endDateProblem(u.IsAdmin, u.ExpiresAt); msg != "" {
			c.fail(http.StatusBadRequest, msg)
			return
		}
	}
	if u.GroupID != 0 && u.GroupID != before.GroupID && s.st.GroupName(u.GroupID) == "" {
		c.fail(http.StatusBadRequest, "that group does not exist")
		return
	}
	if c.err(s.st.UpdateUser(u)) {
		return
	}
	if in.ResetTOTP && c.err(s.st.SetTOTP(u.ID, "")) {
		return
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	what := []string{}
	if u.Email != before.Email {
		what = append(what, "e-mail")
	}
	if u.DisplayName != before.DisplayName {
		what = append(what, "display name")
	}
	if u.Note != before.Note {
		what = append(what, "note")
	}
	if u.IsAdmin != before.IsAdmin {
		what = append(what, "administrator: "+yes(u.IsAdmin))
	}
	if u.Disabled != before.Disabled {
		what = append(what, "disabled: "+yes(u.Disabled))
	}
	if u.GroupID != before.GroupID {
		name := s.st.GroupName(u.GroupID)
		if name == "" {
			name = "none"
		}
		what = append(what, "group: "+name)
	}
	if u.ExpiresAt != before.ExpiresAt {
		if u.ExpiresAt == 0 {
			what = append(what, "no end date")
		} else {
			what = append(what, "ends "+time.Unix(u.ExpiresAt, 0).UTC().Format("2006-01-02"))
		}
	}
	if in.ResetTOTP {
		what = append(what, "two-step sign-in reset")
	}
	s.change(c, "user.update", u.Username, strings.Join(what, "; "))
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
	s.change(c, "user.password", u.Username, "set by an administrator")
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
	s.change(c, "user.delete", u.Username, "")
	c.done()
}

// ---- user groups ----

func (s *Server) cGroups(c *consoleCtx) {
	gs, err := s.st.Groups()
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"groups": gs})
}

// cGroupPut creates (id "new") or updates a group.
func (s *Server) cGroupPut(c *consoleCtx) {
	var in struct {
		Name   string
		Shared bool
	}
	if !c.body(&in) {
		return
	}
	g := &store.Group{Name: in.Name, Shared: in.Shared}
	if id := c.r.PathValue("id"); id != "new" {
		g.ID, _ = strconv.ParseInt(id, 10, 64)
		if g.ID <= 0 {
			c.fail(http.StatusNotFound, "not found")
			return
		}
	}
	if c.err(s.st.SaveGroup(g)) {
		return
	}
	kind := "regular"
	if g.Shared {
		kind = "shared"
	}
	s.change(c, "group.save", g.Name, kind)
	c.ok(map[string]any{"group": g})
}

func (s *Server) cGroupDelete(c *consoleCtx) {
	id, _ := strconv.ParseInt(c.r.PathValue("id"), 10, 64)
	name := s.st.GroupName(id)
	if c.err(s.st.DeleteGroup(id)) {
		return
	}
	s.change(c, "group.delete", name, "")
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
	s.change(c, "session.end", c.r.PathValue("id"), "")
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
	s.change(c, "token.create", in.Name, "API token of "+c.user.Username)
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
		"online_lookup": s.st.Setting(keyOnlineLookup) != "0",
		"log_days":      s.st.Setting(keyLogDays),
		"idle_days":     s.st.Setting(keyIdleDays),
		"hbbs_cmd":      s.cmdAddr("hbbs"),
		"hbbr_cmd":      s.cmdAddr("hbbr"),
		"strategy":      strategyOptions(s.st.Setting(keyStrategy)),
		"oidc":          ps,
		"oidc_callback": s.publicURL(c.r) + oidcCallbackPath,
		"mail":          s.mailSettings(),
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
	if raw, ok := in["online_lookup"]; ok {
		v := "1"
		if string(raw) == "false" {
			v = "0"
		}
		if c.err(s.st.SetSetting(keyOnlineLookup, v)) {
			return
		}
	}
	for field, key := range map[string]string{"log_days": keyLogDays, "idle_days": keyIdleDays} {
		if raw, ok := in[field]; ok {
			var v string
			_ = json.Unmarshal(raw, &v)
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n <= 0 {
				v = ""
			}
			if c.err(s.st.SetSetting(key, strings.TrimSpace(v))) {
				return
			}
		}
	}
	for field, key := range map[string]string{"hbbs_cmd": keyHbbsCmd, "hbbr_cmd": keyHbbrCmd} {
		if raw, ok := in[field]; ok {
			var v string
			if json.Unmarshal(raw, &v) != nil || c.err(s.st.SetSetting(key, strings.TrimSpace(v))) {
				return
			}
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
	if raw, ok := in["mail"]; ok {
		var m struct {
			Host, Port, Security, User, From string
			Password                         string
		}
		if json.Unmarshal(raw, &m) != nil {
			c.fail(http.StatusBadRequest, "the mail settings are not readable")
			return
		}
		if err := s.saveMailSettings(m); err != nil {
			c.fail(http.StatusBadRequest, err.Error())
			return
		}
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	s.change(c, "settings.update", strings.Join(keys, ", "), "")
	c.done()
}

// cleanDomains reads a list of e-mail domains as typed (commas, spaces, with
// or without @) into lower-case names.
func cleanDomains(raw string) ([]string, error) {
	out := []string{}
	for _, d := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if d == "" {
			continue
		}
		if !domainName.MatchString(d) {
			return nil, errors.New("not an e-mail domain: " + d)
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out, nil
}

var domainName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func (s *Server) cOIDCPut(c *consoleCtx) {
	var in struct {
		Issuer         string
		ClientID       string `json:"client_id"`
		ClientSecret   string `json:"client_secret"`
		Scopes         string
		AutoRegister   bool   `json:"auto_register"`
		LinkByEmail    bool   `json:"link_by_email"`
		AllowedDomains string `json:"allowed_domains"`
		DefaultGroupID int64  `json:"default_group_id"`
	}
	if !c.body(&in) {
		return
	}
	p := &store.OIDCProvider{Name: strings.ToLower(strings.TrimSpace(c.r.PathValue("name"))), Issuer: strings.TrimRight(strings.TrimSpace(in.Issuer), "/"),
		ClientID: strings.TrimSpace(in.ClientID), ClientSecret: strings.TrimSpace(in.ClientSecret), Scopes: strings.TrimSpace(in.Scopes), AutoRegister: in.AutoRegister, LinkByEmail: in.LinkByEmail, DefaultGroupID: in.DefaultGroupID}
	domains, err := cleanDomains(in.AllowedDomains)
	if err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	p.AllowedDomains = strings.Join(domains, ", ")
	if p.DefaultGroupID != 0 && s.st.GroupName(p.DefaultGroupID) == "" {
		c.fail(http.StatusBadRequest, "the group for new accounts does not exist")
		return
	}
	if p.Scopes == "" && isGitHub(p) {
		p.Scopes = "read:user user:email"
	}
	if c.err(s.st.SaveOIDCProvider(p)) {
		return
	}
	s.oidc.forget(p.Name)
	opts := []string{p.Issuer}
	if p.AutoRegister {
		opts = append(opts, "creates accounts")
	}
	if p.LinkByEmail {
		opts = append(opts, "finds accounts by e-mail")
	}
	if p.AllowedDomains != "" {
		opts = append(opts, "domains: "+p.AllowedDomains)
	}
	if p.DefaultGroupID != 0 {
		opts = append(opts, "group: "+s.st.GroupName(p.DefaultGroupID))
	}
	s.change(c, "provider.save", p.Name, strings.Join(opts, "; "))
	c.done()
}

func (s *Server) cOIDCDelete(c *consoleCtx) {
	name := c.r.PathValue("name")
	if c.err(s.st.DeleteOIDCProvider(name)) {
		return
	}
	s.oidc.forget(name)
	s.change(c, "provider.delete", name, "")
	c.done()
}
