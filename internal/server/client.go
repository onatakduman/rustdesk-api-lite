package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// This file is the API the RustDesk client itself calls. Shapes follow what
// the open-source client (1.2.x – 1.5.x) sends and parses; see docs/client-api-contract.md.

// maxPeersPerBook is reported to the client as the size limit of one address book.
const maxPeersPerBook = 5000

func (s *Server) clientRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/login-options", s.handleLoginOptions)
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("POST /api/currentUser", s.client(s.handleCurrentUser))

	m.HandleFunc("POST /api/oidc/auth", s.handleOIDCAuth)
	m.HandleFunc("GET /api/oidc/auth-query", s.handleOIDCAuthQuery)

	// Address books, current protocol.
	m.HandleFunc("POST /api/ab/settings", s.client(s.handleABSettings))
	m.HandleFunc("POST /api/ab/personal", s.client(s.handleABPersonal))
	m.HandleFunc("POST /api/ab/shared/profiles", s.client(s.handleABShared))
	m.HandleFunc("POST /api/ab/peers", s.client(s.handleABPeers))
	m.HandleFunc("POST /api/ab/tags/{guid}", s.client(s.handleABTags))
	m.HandleFunc("POST /api/ab/peer/add/{guid}", s.client(s.handleABPeerAdd))
	m.HandleFunc("PUT /api/ab/peer/update/{guid}", s.client(s.handleABPeerUpdate))
	m.HandleFunc("DELETE /api/ab/peer/{guid}", s.client(s.handleABPeerDelete))
	m.HandleFunc("POST /api/ab/tag/add/{guid}", s.client(s.handleABTagAdd))
	m.HandleFunc("PUT /api/ab/tag/rename/{guid}", s.client(s.handleABTagRename))
	m.HandleFunc("PUT /api/ab/tag/update/{guid}", s.client(s.handleABTagUpdate))
	m.HandleFunc("DELETE /api/ab/tag/{guid}", s.client(s.handleABTagDelete))
	// Address book, legacy protocol (one JSON blob per user).
	m.HandleFunc("GET /api/ab", s.client(s.handleABLegacyGet))
	m.HandleFunc("POST /api/ab/get", s.client(s.handleABLegacyGet))
	m.HandleFunc("POST /api/ab", s.client(s.handleABLegacyPost))

	// "Accessible devices" tab.
	m.HandleFunc("GET /api/users", s.client(s.handleGroupUsers))
	m.HandleFunc("GET /api/peers", s.client(s.handleGroupPeers))
	m.HandleFunc("GET /api/device-group/accessible", s.client(s.handleDeviceGroups))

	// Reports from every installation, signed in or not.
	m.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	m.HandleFunc("POST /api/sysinfo", s.handleSysinfo)
	m.HandleFunc("POST /api/sysinfo_ver", s.handleSysinfoVer)
	m.HandleFunc("POST /api/audit/conn", s.handleAuditConn)
	m.HandleFunc("POST /api/audit/file", s.handleAuditFile)
	m.HandleFunc("POST /api/audit/alarm", s.handleAuditIgnored)
}

type clientCtx struct {
	w    http.ResponseWriter
	r    *http.Request
	user *store.User
	tok  string
}

// client wraps a handler that needs a signed-in RustDesk client.
func (s *Server) client(h func(c *clientCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		u, _, err := s.st.UserByToken(tok)
		if err != nil {
			fail(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		h(&clientCtx{w: w, r: r, user: u, tok: tok})
	}
}

func (c *clientCtx) fail(status int, msg string) { fail(c.w, status, msg) }

// ok answers an action the way the client expects success: 200 with an empty body.
func (c *clientCtx) ok() { c.w.WriteHeader(http.StatusOK) }

func (c *clientCtx) err(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		c.fail(http.StatusNotFound, "Not found")
	} else {
		c.fail(http.StatusBadRequest, err.Error())
	}
	return true
}

// userPayload is the account object the client parses.
func (s *Server) userPayload(u *store.User) map[string]any {
	p := map[string]any{
		"name":     u.Username,
		"email":    u.Email,
		"note":     u.Note,
		"status":   1, // 1 normal, 0 disabled, -1 unverified
		"is_admin": u.IsAdmin,
		"info":     map[string]any{},
	}
	if u.DisplayName != "" {
		p["display_name"] = u.DisplayName
	}
	return p
}

type deviceInfo struct {
	OS   string `json:"os"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func (s *Server) authBody(r *http.Request, u *store.User, id, uuid string, dev deviceInfo) (map[string]any, error) {
	tok, err := s.st.NewToken(&store.Token{UserID: u.ID, Kind: "client", DeviceID: id, DeviceUUID: uuid, DeviceName: dev.Name, DeviceOS: dev.OS, IP: s.clientIP(r)}, 0)
	if err != nil {
		return nil, err
	}
	s.st.AssignDevice(id, u.ID)
	return map[string]any{"type": "access_token", "access_token": tok, "user": s.userPayload(u)}, nil
}

// ---- sign-in ----

func (s *Server) handleLoginOptions(w http.ResponseWriter, r *http.Request) {
	out := []string{}
	if ps, err := s.st.OIDCProviders(); err == nil && len(ps) > 0 {
		type op struct {
			Name string `json:"name"`
		}
		list := []op{}
		for _, p := range ps {
			list = append(list, op{p.Name})
		}
		raw, _ := json.Marshal(list)
		out = append(out, "common-oidc/"+string(raw))
		for _, p := range ps {
			out = append(out, "oidc/"+p.Name)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username   string     `json:"username"`
		Password   string     `json:"password"`
		ID         string     `json:"id"`
		UUID       string     `json:"uuid"`
		Type       string     `json:"type"`
		DeviceInfo deviceInfo `json:"deviceInfo"`
	}
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Type != "" && in.Type != "account" {
		fail(w, http.StatusBadRequest, "This sign-in method is not supported by this server")
		return
	}
	u, err := s.checkLogin(r, in.Username, in.Password)
	if err != nil {
		// 200 with an error field: the client shows the message as is.
		writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
		return
	}
	body, err := s.authBody(r, u, in.ID, in.UUID, in.DeviceInfo)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not create a session")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := bearer(r); tok != "" {
		if _, t, err := s.st.UserByToken(tok); err == nil {
			s.st.DeleteToken(tok)
			s.st.AssignDevice(t.DeviceID, 0)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleCurrentUser(c *clientCtx) {
	writeJSON(c.w, http.StatusOK, s.userPayload(c.user))
}

func (s *Server) handleOIDCAuth(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Op         string     `json:"op"`
		ID         string     `json:"id"`
		UUID       string     `json:"uuid"`
		DeviceInfo deviceInfo `json:"deviceInfo"`
	}
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.st.OIDCProvider(in.Op); err != nil {
		fail(w, http.StatusBadRequest, "This sign-in provider is not configured")
		return
	}
	sess := &store.OIDCSession{Provider: in.Op, Purpose: "client", DeviceID: in.ID, DeviceUUID: in.UUID, DeviceName: in.DeviceInfo.Name, DeviceOS: in.DeviceInfo.OS}
	if err := s.st.NewOIDCSession(sess); err != nil {
		fail(w, http.StatusInternalServerError, "Could not start the sign-in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": sess.Code, "url": s.publicURL(r) + "/oidc/login?code=" + sess.Code})
}

func (s *Server) handleOIDCAuthQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sess, err := s.st.OIDCSessionByCode(q.Get("code"))
	if err != nil || sess.DeviceID != q.Get("id") || sess.DeviceUUID != q.Get("uuid") {
		writeJSON(w, http.StatusOK, map[string]string{"error": "No authed oidc is found"})
		return
	}
	switch {
	case sess.Result == "":
		// The exact wording matters: the client keeps polling while it sees it.
		writeJSON(w, http.StatusOK, map[string]string{"error": "No authed oidc is found"})
	case strings.HasPrefix(sess.Result, "user:"):
		s.st.DeleteOIDCSession(sess.Code)
		id, _ := strconv.ParseInt(strings.TrimPrefix(sess.Result, "user:"), 10, 64)
		u, err := s.st.UserByID(id)
		if err != nil || u.Disabled {
			writeJSON(w, http.StatusOK, map[string]string{"error": "This account is disabled"})
			return
		}
		body, err := s.authBody(r, u, sess.DeviceID, sess.DeviceUUID, deviceInfo{OS: sess.DeviceOS, Name: sess.DeviceName})
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not create a session")
			return
		}
		writeJSON(w, http.StatusOK, body)
	default:
		s.st.DeleteOIDCSession(sess.Code)
		writeJSON(w, http.StatusOK, map[string]string{"error": strings.TrimPrefix(sess.Result, "error:")})
	}
}

// ---- address books ----

// abRule returns the caller's rule on the address book in the path, or writes the error.
func (s *Server) abRule(c *clientCtx, guid string, need int) (*store.AddressBook, bool) {
	b, err := s.st.AB(guid, c.user.ID)
	if err != nil || b.Rule == store.RuleNone {
		c.fail(http.StatusNotFound, "Address book not found")
		return nil, false
	}
	if b.Rule < need {
		c.fail(http.StatusForbidden, "You do not have permission to change this address book")
		return nil, false
	}
	return b, true
}

func paging(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	current, _ := strconv.Atoi(q.Get("current"))
	limit, _ = strconv.Atoi(q.Get("pageSize"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if current < 1 {
		current = 1
	}
	return limit, (current - 1) * limit
}

func (s *Server) handleABSettings(c *clientCtx) {
	writeJSON(c.w, http.StatusOK, map[string]any{"max_peer_one_ab": maxPeersPerBook})
}

func (s *Server) handleABPersonal(c *clientCtx) {
	guid, err := s.st.PersonalAB(c.user.ID)
	if c.err(err) {
		return
	}
	writeJSON(c.w, http.StatusOK, map[string]string{"guid": guid})
}

func (s *Server) handleABShared(c *clientCtx) {
	books, err := s.st.ABsFor(c.user.ID)
	if c.err(err) {
		return
	}
	type profile struct {
		GUID  string `json:"guid"`
		Name  string `json:"name"`
		Owner string `json:"owner"`
		Note  string `json:"note"`
		Rule  int    `json:"rule"`
	}
	// The client keys address books by name: names must be unique and must not
	// collide with its built-in "My address book" / "Legacy address book".
	all := []profile{}
	used := map[string]bool{"My address book": true, "Legacy address book": true}
	for _, b := range books {
		if b.Personal {
			continue
		}
		name := b.Name
		if used[name] {
			name = b.Name + " (" + b.Owner + ")"
		}
		for i := 2; used[name]; i++ {
			name = b.Name + " (" + b.Owner + " " + strconv.Itoa(i) + ")"
		}
		used[name] = true
		all = append(all, profile{b.GUID, name, b.Owner, b.Note, b.Rule})
	}
	limit, offset := paging(c.r)
	page := []profile{}
	if offset < len(all) {
		page = all[offset:min(len(all), offset+limit)]
	}
	writeJSON(c.w, http.StatusOK, map[string]any{"total": len(all), "data": page})
}

func (s *Server) handleABPeers(c *clientCtx) {
	b, ok := s.abRule(c, c.r.URL.Query().Get("ab"), store.RuleRead)
	if !ok {
		return
	}
	limit, offset := paging(c.r)
	peers, total, err := s.st.Peers(b.GUID, limit, offset)
	if c.err(err) {
		return
	}
	writeJSON(c.w, http.StatusOK, map[string]any{"total": total, "data": peers})
}

func (s *Server) handleABTags(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleRead)
	if !ok {
		return
	}
	tags, err := s.st.Tags(b.GUID)
	if c.err(err) {
		return
	}
	writeJSON(c.w, http.StatusOK, tags)
}

func (s *Server) handleABPeerAdd(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	p := store.Peer{}
	if err := readJSON(c.w, c.r, &p, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if b.PeerCount >= maxPeersPerBook {
		c.fail(http.StatusBadRequest, "This address book is full")
		return
	}
	if c.err(s.st.PutPeer(b.GUID, p)) {
		return
	}
	c.ok()
}

func (s *Server) handleABPeerUpdate(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	p := store.Peer{}
	if err := readJSON(c.w, c.r, &p, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if c.err(s.st.PatchPeer(b.GUID, p)) {
		return
	}
	c.ok()
}

func (s *Server) handleABPeerDelete(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	var ids []string
	if err := readJSON(c.w, c.r, &ids, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if c.err(s.st.DeletePeers(b.GUID, ids)) {
		return
	}
	c.ok()
}

// tagColor accepts the colour as a number of any JSON flavour.
func tagColor(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func (s *Server) handleABTagAdd(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Color any    `json:"color"`
	}
	if err := readJSON(c.w, c.r, &in, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if c.err(s.st.PutTag(b.GUID, store.Tag{Name: in.Name, Color: tagColor(in.Color)})) {
		return
	}
	c.ok()
}

func (s *Server) handleABTagRename(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readJSON(c.w, c.r, &in, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if c.err(s.st.RenameTag(b.GUID, in.Old, in.New)) {
		return
	}
	c.ok()
}

func (s *Server) handleABTagUpdate(c *clientCtx) { s.handleABTagAdd(c) }

func (s *Server) handleABTagDelete(c *clientCtx) {
	b, ok := s.abRule(c, c.r.PathValue("guid"), store.RuleReadWrite)
	if !ok {
		return
	}
	var names []string
	if err := readJSON(c.w, c.r, &names, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	if c.err(s.st.DeleteTags(b.GUID, names)) {
		return
	}
	c.ok()
}

// The legacy protocol moves the whole personal address book as one JSON
// document, itself wrapped as a string: {"data": "{\"tags\":[…],\"peers\":[…],\"tag_colors\":\"{…}\"}"}.

func (s *Server) handleABLegacyGet(c *clientCtx) {
	guid, err := s.st.PersonalAB(c.user.ID)
	if c.err(err) {
		return
	}
	peers, _, err := s.st.Peers(guid, 0, 0)
	if c.err(err) {
		return
	}
	tags, err := s.st.Tags(guid)
	if c.err(err) {
		return
	}
	names := []string{}
	colors := map[string]int64{}
	for _, t := range tags {
		names = append(names, t.Name)
		colors[t.Name] = t.Color
	}
	colorsRaw, _ := json.Marshal(colors)
	doc, _ := json.Marshal(map[string]any{"tags": names, "peers": peers, "tag_colors": string(colorsRaw)})
	writeJSON(c.w, http.StatusOK, map[string]any{"data": string(doc)})
}

func (s *Server) handleABLegacyPost(c *clientCtx) {
	var in struct {
		Data string `json:"data"`
	}
	if err := readJSON(c.w, c.r, &in, bodyLarge); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	var doc struct {
		Tags      []string     `json:"tags"`
		Peers     []store.Peer `json:"peers"`
		TagColors string       `json:"tag_colors"`
	}
	if err := json.Unmarshal([]byte(in.Data), &doc); err != nil {
		c.fail(http.StatusBadRequest, "invalid address book data")
		return
	}
	colors := map[string]int64{}
	_ = json.Unmarshal([]byte(doc.TagColors), &colors)
	tags := make([]store.Tag, 0, len(doc.Tags))
	for _, n := range doc.Tags {
		tags = append(tags, store.Tag{Name: n, Color: colors[n]})
	}
	guid, err := s.st.PersonalAB(c.user.ID)
	if c.err(err) {
		return
	}
	if c.err(s.st.ReplaceAB(guid, doc.Peers, tags)) {
		return
	}
	c.ok()
}

// ---- "accessible devices" tab ----

func (s *Server) handleGroupUsers(c *clientCtx) {
	list := []map[string]any{}
	if c.user.IsAdmin {
		us, err := s.st.ListUsers()
		if c.err(err) {
			return
		}
		for _, u := range us {
			if !u.Disabled {
				list = append(list, s.userPayload(u))
			}
		}
	} else {
		list = append(list, s.userPayload(c.user))
	}
	limit, offset := paging(c.r)
	page := []map[string]any{}
	if offset < len(list) {
		page = list[offset:min(len(list), offset+limit)]
	}
	writeJSON(c.w, http.StatusOK, map[string]any{"total": len(list), "data": page})
}

func (s *Server) handleGroupPeers(c *clientCtx) {
	scope := c.user.ID
	if c.user.IsAdmin {
		scope = 0
	}
	ds, err := s.st.Devices(scope)
	if c.err(err) {
		return
	}
	limit, offset := paging(c.r)
	page := []map[string]any{}
	for i := offset; i < len(ds) && i < offset+limit; i++ {
		d := ds[i]
		page = append(page, map[string]any{
			"id":                d.ID,
			"info":              map[string]any{"username": d.Username, "os": d.OS, "device_name": d.Hostname},
			"user":              d.User,
			"user_name":         d.User,
			"device_group_name": "",
			"note":              d.Note,
			"status":            1,
		})
	}
	writeJSON(c.w, http.StatusOK, map[string]any{"total": len(ds), "data": page})
}

func (s *Server) handleDeviceGroups(c *clientCtx) {
	writeJSON(c.w, http.StatusOK, map[string]any{"total": 0, "data": []any{}})
}

// ---- reports from installations ----

// reporter checks the id/uuid pair of an unauthenticated report. A device id
// that is already known under another uuid is refused, so one installation
// cannot overwrite another one's record.
func (s *Server) reporter(id, uuid string) bool {
	if id == "" || len(id) > 64 || len(uuid) > 256 {
		return false
	}
	d, err := s.st.Device(id)
	return err != nil || d.UUID == "" || d.UUID == uuid
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		UUID string `json:"uuid"`
	}
	if err := readJSON(w, r, &in, bodySmall); err != nil || !s.reporter(in.ID, in.UUID) {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	known, err := s.st.Heartbeat(in.ID, in.UUID, s.clientIP(r))
	out := map[string]any{}
	if err == nil && !known {
		out["sysinfo"] = true // ask the client to send its system information
	}
	writeJSON(w, http.StatusOK, out)
}

func plain(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, s)
}

func (s *Server) handleSysinfo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		UUID     string `json:"uuid"`
		Hostname string `json:"hostname"`
		Username string `json:"username"`
		OS       string `json:"os"`
		CPU      string `json:"cpu"`
		Memory   string `json:"memory"`
		Version  string `json:"version"`
	}
	if err := readJSON(w, r, &in, bodySmall); err != nil || !s.reporter(in.ID, in.UUID) {
		plain(w, "ID_NOT_FOUND")
		return
	}
	d := &store.Device{ID: in.ID, UUID: in.UUID, Hostname: in.Hostname, Username: in.Username, OS: in.OS, CPU: in.CPU, Memory: in.Memory, Version: in.Version}
	if err := s.st.SaveSysinfo(d, s.clientIP(r)); err != nil {
		plain(w, "ID_NOT_FOUND")
		return
	}
	plain(w, "SYSINFO_UPDATED")
}

// handleSysinfoVer returns a marker; the client re-sends its system
// information whenever the marker differs from the one it remembered.
func (s *Server) handleSysinfoVer(w http.ResponseWriter, r *http.Request) {
	plain(w, "rdl-1")
}

func jsonInt(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := x.Int64()
		return n
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// auditRecord reads an audit report. The client treats only "2xx with an
// empty body" as delivered and re-sends anything else with the same nonce, so
// every audit handler answers with an empty 200 and repeats are dropped here.
// Reports are accepted from devices this server already knows (id + uuid).
func (s *Server) auditRecord(w http.ResponseWriter, r *http.Request) (map[string]any, string) {
	defer w.WriteHeader(http.StatusOK)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodySmall))
	if err != nil {
		return nil, ""
	}
	in := map[string]any{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber() // session_id is a u64 beyond float precision
	if dec.Decode(&in) != nil {
		return nil, ""
	}
	id, _ := in["id"].(string)
	uuid, _ := in["uuid"].(string)
	d, err := s.st.Device(id)
	if err != nil || d.UUID == "" || d.UUID != uuid {
		return nil, ""
	}
	if nonce, _ := in["nonce"].(string); nonce != "" && s.nonces.seen(nonce) {
		return nil, ""
	}
	return in, id
}

func (s *Server) handleAuditConn(w http.ResponseWriter, r *http.Request) {
	in, id := s.auditRecord(w, r)
	if in == nil {
		return
	}
	connID := jsonInt(in["conn_id"])
	if action, _ := in["action"].(string); action == "close" {
		_ = s.st.ConnClosed(id, connID)
		return
	}
	a := &store.ConnAudit{DeviceID: id, ConnID: connID, Type: int(jsonInt(in["type"]))}
	if n, ok := in["session_id"].(json.Number); ok && n.String() != "0" {
		a.SessionID = n.String()
	}
	a.IP, _ = in["ip"].(string)
	if peer, ok := in["peer"].([]any); ok {
		if len(peer) > 0 {
			a.PeerID, _ = peer[0].(string)
		}
		if len(peer) > 1 {
			a.PeerName, _ = peer[1].(string)
		}
	}
	_ = s.st.ConnOpened(a)
}

func (s *Server) handleAuditFile(w http.ResponseWriter, r *http.Request) {
	in, id := s.auditRecord(w, r)
	if in == nil {
		return
	}
	a := &store.FileAudit{DeviceID: id, Direction: int(jsonInt(in["type"]))}
	a.PeerID, _ = in["peer_id"].(string)
	a.Path, _ = in["path"].(string)
	a.IsFile, _ = in["is_file"].(bool)
	a.Info, _ = in["info"].(string)
	_ = s.st.AddFileAudit(a)
}

// handleAuditIgnored accepts reports this server does not keep (alarms).
func (s *Server) handleAuditIgnored(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, bodySmall))
	w.WriteHeader(http.StatusOK)
}
