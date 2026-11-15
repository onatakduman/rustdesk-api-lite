package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// kickList holds incoming sessions an administrator asked to close; a device
// picks them up with its next heartbeat (every 3 s while a session is open).
type kickList struct {
	mu sync.Mutex
	m  map[string][]int64
}

func (k *kickList) add(device string, conn int64) {
	k.mu.Lock()
	k.m[device] = append(k.m[device], conn)
	k.mu.Unlock()
}

func (k *kickList) take(device string) []int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := k.m[device]
	delete(k.m, device)
	return out
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID         string  `json:"id"`
		UUID       string  `json:"uuid"`
		Conns      []int64 `json:"conns"`
		ModifiedAt int64   `json:"modified_at"`
	}
	if err := readJSON(w, r, &in, bodySmall); err != nil || !s.reporter(in.ID, in.UUID) || !s.mayAdd(r, in.ID) {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	known, err := s.st.Heartbeat(in.ID, in.UUID, s.clientIP(r), in.Conns)
	out := map[string]any{}
	if err == nil && !known {
		out["sysinfo"] = true // ask the client to send its system information
	}
	if kicks := s.kicks.take(in.ID); len(kicks) > 0 {
		out["disconnect"] = kicks
	}
	// Options for all devices: sent only when the device's copy is out of date.
	if at, _ := strconv.ParseInt(s.st.Setting(keyStrategyAt), 10, 64); at != 0 && at != in.ModifiedAt {
		opts := map[string]string{}
		_ = json.Unmarshal([]byte(s.st.Setting(keyStrategy)), &opts)
		out["modified_at"] = at
		out["strategy"] = map[string]any{"config_options": opts}
	}
	writeJSON(w, http.StatusOK, out)
}

// enrolment is what an installation (preset-* options of a custom client) or
// an administrator (rustdesk --assign) may ask for a device.
type enrolment struct {
	User, Group, Note                        string
	Book, Tag, Alias, BookPassword, BookNote string
}

// enrol applies an enrolment. With force unset only empty fields are filled,
// so a device cannot take itself away from its owner or group.
func (s *Server) enrol(d *store.Device, e enrolment, force bool) {
	if e.User != "" && (force || d.UserID == 0) {
		if u, err := s.st.UserByUsername(e.User); err == nil && u.Active() {
			_ = s.st.SetDeviceOwner(d.ID, u.ID)
		}
	}
	if e.Group != "" && (force || d.Group == "") {
		_ = s.st.SetDeviceGroup(d.ID, e.Group)
	}
	if e.Note != "" && (force || d.Note == "") {
		_ = s.st.SetDeviceNote(d.ID, e.Note)
	}
	if e.Book == "" {
		return
	}
	guid, err := s.st.ABByName(e.Book)
	if err != nil || (!force && s.st.HasPeer(guid, d.ID)) {
		return
	}
	p := store.Peer{"id": d.ID, "hostname": d.Hostname, "username": d.Username, "platform": platformOf(d.OS), "tags": []any{}}
	if e.Alias != "" {
		p["alias"] = e.Alias
	}
	if e.BookNote != "" {
		p["note"] = e.BookNote
	}
	if e.BookPassword != "" {
		p["password"] = e.BookPassword
	}
	if e.Tag != "" {
		p["tags"] = []any{e.Tag}
		if tags, err := s.st.Tags(guid); err == nil {
			found := false
			for _, t := range tags {
				found = found || t.Name == e.Tag
			}
			if !found {
				_ = s.st.PutTag(guid, store.Tag{Name: e.Tag, Color: 0xFF0071FF})
			}
		}
	}
	if s.st.HasPeer(guid, d.ID) {
		_ = s.st.PatchPeer(guid, p)
	} else {
		_ = s.st.PutPeer(guid, p)
	}
}

// platformOf maps the "os" string of a system report to the platform names the app uses.
func platformOf(os string) string {
	first, _, _ := strings.Cut(strings.ToLower(os), " / ")
	switch {
	case first == "windows":
		return "Windows"
	case first == "macos":
		return "Mac OS"
	case first == "android":
		return "Android"
	case first == "linux" || strings.Contains(first, "linux"):
		return "Linux"
	}
	return ""
}

func (s *Server) handleSysinfo(w http.ResponseWriter, r *http.Request) {
	in := map[string]any{}
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		plain(w, "ID_NOT_FOUND")
		return
	}
	str := func(k string) string { v, _ := in[k].(string); return v }
	id, uuid := str("id"), str("uuid")
	if !s.reporter(id, uuid) || !s.mayAdd(r, id) {
		plain(w, "ID_NOT_FOUND") // the client sends its system information again later
		return
	}
	d := &store.Device{ID: id, UUID: uuid, Hostname: str("hostname"), Username: str("username"), OS: str("os"), CPU: str("cpu"), Memory: str("memory"), Version: str("version")}
	if err := s.st.SaveSysinfo(d, s.clientIP(r)); err != nil {
		plain(w, "ID_NOT_FOUND")
		return
	}
	if s.st.Setting(keyAllowPresets) == "1" {
		e := enrolment{User: str("preset-user-name"), Group: str("preset-device-group-name"), Note: str("preset-note"), Book: str("preset-address-book-name"),
			Tag: str("preset-address-book-tag"), Alias: str("preset-address-book-alias"), BookPassword: str("preset-address-book-password"), BookNote: str("preset-address-book-note")}
		if e != (enrolment{}) {
			if cur, err := s.st.Device(id); err == nil {
				s.enrol(cur, e, false)
			}
		}
	}
	plain(w, "SYSINFO_UPDATED")
}

// handleDevicesCLI serves `rustdesk --assign --token <API token> …`, run by an
// administrator on the device. An empty answer makes the client print "Done!".
func (s *Server) handleDevicesCLI(c *clientCtx) {
	if !c.user.IsAdmin {
		plain(c.w, "Administrator token required")
		return
	}
	in := map[string]any{}
	if err := readJSON(c.w, c.r, &in, bodySmall); err != nil {
		plain(c.w, err.Error())
		return
	}
	str := func(k string) string { v, _ := in[k].(string); return v }
	id, uuid := str("id"), str("uuid")
	if !s.reporter(id, uuid) {
		plain(c.w, "This device id is registered with another installation")
		return
	}
	d, err := s.st.Device(id)
	if err != nil {
		d = &store.Device{ID: id, UUID: uuid, Hostname: str("device_name"), Username: str("device_username")}
		if err := s.st.SaveSysinfo(d, s.clientIP(c.r)); err != nil {
			plain(c.w, "Could not register the device")
			return
		}
		d, _ = s.st.Device(id)
	}
	if u := str("user_name"); u != "" {
		if _, err := s.st.UserByUsername(u); err != nil {
			plain(c.w, "No such user: "+u)
			return
		}
	}
	if b := str("address_book_name"); b != "" {
		if _, err := s.st.ABByName(b); err != nil {
			plain(c.w, "No such shared address book: "+b)
			return
		}
	}
	s.enrol(d, enrolment{User: str("user_name"), Group: str("device_group_name"), Note: str("note"), Book: str("address_book_name"), Tag: str("address_book_tag"),
		Alias: str("address_book_alias"), BookPassword: str("address_book_password"), BookNote: str("address_book_note")}, true)
	c.w.WriteHeader(http.StatusOK)
}

// handleDevicesDeploy answers `rustdesk --deploy`: registering fixed ids needs
// the rendezvous server's cooperation, which the open-source hbbs does not offer.
func (s *Server) handleDevicesDeploy(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, bodySmall))
	writeJSON(w, http.StatusOK, map[string]string{"result": "NOT_ENABLED"})
}

func (s *Server) handleDeviceGroups(c *clientCtx) {
	scope := c.user.ID
	if c.user.IsAdmin {
		scope = 0
	}
	type group struct {
		Name string `json:"name"`
	}
	list := []group{}
	for _, g := range s.st.DeviceGroups(scope) {
		list = append(list, group{g})
	}
	writeJSON(c.w, http.StatusOK, map[string]any{"total": len(list), "data": list})
}

func (s *Server) handleAuditAlarm(w http.ResponseWriter, r *http.Request) {
	in, id := s.auditRecord(w, r)
	if in == nil {
		return
	}
	info, _ := in["info"].(string)
	_ = s.st.AddAlarm(id, int(jsonInt(in["typ"])), info)
}

// handleAuditActive tells the controlling app which log record belongs to the
// session it just opened, so it can attach a note to it afterwards.
func (s *Server) handleAuditActive(c *clientCtx) {
	q := c.r.URL.Query()
	row := s.st.ActiveConn(q.Get("id"), q.Get("session_id"))
	if row == 0 {
		writeJSON(c.w, http.StatusOK, "") // not recorded yet: the app retries
		return
	}
	writeJSON(c.w, http.StatusOK, strconv.FormatInt(row, 10))
}

func (s *Server) handleAuditNote(c *clientCtx) {
	var in struct {
		GUID string `json:"guid"`
		Note string `json:"note"`
	}
	if err := readJSON(c.w, c.r, &in, bodySmall); err != nil {
		c.fail(http.StatusBadRequest, err.Error())
		return
	}
	row, err := strconv.ParseInt(in.GUID, 10, 64)
	if err != nil || row <= 0 {
		c.fail(http.StatusNotFound, "Not found")
		return
	}
	_, peer, err := s.st.ConnPeer(row)
	if c.err(err) {
		return
	}
	if !c.user.IsAdmin && !s.openedBy(c, peer) {
		c.fail(http.StatusForbidden, "This session was not opened from your device")
		return
	}
	if c.err(s.st.SetConnNote(row, in.Note)) {
		return
	}
	c.ok()
}

// openedBy tells whether a recorded session was opened from the caller's side:
// from the device this app is signed in on, or from another device that is
// assigned to the caller. peer is the RustDesk id the controlled device reported.
func (s *Server) openedBy(c *clientCtx, peer string) bool {
	id, _ := splitPeerID(peer)
	if id == "" {
		return false
	}
	if own, _ := splitPeerID(c.sess.DeviceID); own != "" && own == id {
		return true
	}
	d, err := s.st.Device(id)
	return err == nil && d.UserID != 0 && d.UserID == c.user.ID
}
