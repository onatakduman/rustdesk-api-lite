package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

type harness struct {
	t   *testing.T
	st  *store.Store
	srv *httptest.Server
	app *Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>console")}}
	app := New(st, Config{Web: web})
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &harness{t, st, srv, app}
}

// call sends a request and returns status, raw body and the body decoded as JSON (nil when not JSON).
func (h *harness) call(method, path, token string, body any, hdr ...string) (int, string, any) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var v any
	_ = json.Unmarshal(raw, &v)
	return res.StatusCode, string(raw), v
}

func (h *harness) user(name string, admin bool) *store.User {
	h.t.Helper()
	u := &store.User{Username: name, IsAdmin: admin, Email: name + "@example.test"}
	if err := h.st.CreateUser(u, "correct horse", ""); err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) login(name string) string {
	h.t.Helper()
	code, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": name, "password": "correct horse", "id": "100200300", "uuid": "dXVpZA==",
		"type": "account", "deviceInfo": map[string]string{"os": "macos", "type": "client", "name": "mac"}})
	m, _ := v.(map[string]any)
	tok, _ := m["access_token"].(string)
	if code != 200 || tok == "" || m["type"] != "access_token" {
		h.t.Fatalf("login: %d %s", code, raw)
	}
	if u := m["user"].(map[string]any); u["name"] != name {
		h.t.Fatalf("login user payload: %s", raw)
	}
	return tok
}

func TestLoginAndCurrentUser(t *testing.T) {
	h := newHarness(t)
	h.user("ada", true)
	if code, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "wrong"}); code != 200 || v.(map[string]any)["error"] == nil {
		t.Fatalf("wrong password: %d %s", code, raw)
	}
	tok := h.login("ada")
	code, raw, v := h.call("POST", "/api/currentUser", tok, map[string]string{"id": "100200300", "uuid": "dXVpZA=="})
	if m := v.(map[string]any); code != 200 || m["name"] != "ada" || m["is_admin"] != true || m["status"] != float64(1) {
		t.Fatalf("currentUser: %d %s", code, raw)
	}
	if code, _, _ := h.call("POST", "/api/currentUser", "nope", nil); code != 401 {
		t.Fatalf("bad token should be 401, got %d", code)
	}
	h.call("POST", "/api/logout", tok, map[string]string{"id": "100200300"})
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatalf("token should be gone after logout, got %d", code)
	}
}

func TestStaticAndUnknownPaths(t *testing.T) {
	h := newHarness(t)
	if code, raw, _ := h.call("GET", "/", "", nil); code != 200 || !strings.Contains(raw, "console") {
		t.Fatalf("index: %d %s", code, raw)
	}
	if code, raw, _ := h.call("GET", "/some/client/route", "", nil); code != 200 || !strings.Contains(raw, "console") {
		t.Fatalf("unknown paths serve the app: %d %s", code, raw)
	}
	for _, p := range []string{"/api/record", "/console/api/nope"} {
		if code, _, v := h.call("GET", p, "", nil); code != 404 || v == nil {
			t.Fatalf("%s must be a JSON 404, got %d", p, code)
		}
	}
}

func TestLoginThrottle(t *testing.T) {
	h := newHarness(t)
	h.user("ada", false)
	for i := 0; i < failMax; i++ {
		h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "wrong"})
	}
	_, raw, _ := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "correct horse"})
	if !strings.Contains(raw, "too many") {
		t.Fatalf("expected throttling, got %s", raw)
	}
}

func TestAddressBookFlow(t *testing.T) {
	h := newHarness(t)
	h.user("ada", false)
	tok := h.login("ada")

	if _, raw, v := h.call("POST", "/api/ab/settings", tok, nil); v.(map[string]any)["max_peer_one_ab"] == nil {
		t.Fatalf("settings: %s", raw)
	}
	_, _, v := h.call("POST", "/api/ab/personal", tok, nil)
	guid := v.(map[string]any)["guid"].(string)
	if guid == "" {
		t.Fatal("no personal guid")
	}
	if code, raw, _ := h.call("POST", "/api/ab/peer/add/"+guid, tok, map[string]any{"id": "111222333", "alias": "desk", "tags": []string{"work"}, "hash": "h"}); code != 200 || raw != "" {
		t.Fatalf("peer add should answer 200 with an empty body: %d %q", code, raw)
	}
	h.call("POST", "/api/ab/peer/add/"+guid, tok, map[string]any{"id": "444555666@other.example?key=K", "tags": []string{"work", "lab"}})
	if code, _, _ := h.call("PUT", "/api/ab/peer/update/"+guid, tok, map[string]any{"id": "111222333", "alias": "desk 2"}); code != 200 {
		t.Fatalf("peer update: %d", code)
	}
	_, raw, v := h.call("POST", "/api/ab/peers?current=1&pageSize=100&ab="+guid, tok, nil)
	m := v.(map[string]any)
	data := m["data"].([]any)
	if m["total"] != float64(2) || len(data) != 2 {
		t.Fatalf("peers: %s", raw)
	}
	first := data[0].(map[string]any)
	if first["alias"] != "desk 2" || first["hash"] != "h" || first["id"] != "111222333" {
		t.Fatalf("update must merge, not replace: %s", raw)
	}

	h.call("POST", "/api/ab/tag/add/"+guid, tok, map[string]any{"name": "work", "color": 4278190335})
	h.call("POST", "/api/ab/tag/add/"+guid, tok, map[string]any{"name": "lab", "color": 1})
	h.call("PUT", "/api/ab/tag/rename/"+guid, tok, map[string]string{"old": "work", "new": "office"})
	h.call("DELETE", "/api/ab/tag/"+guid, tok, []string{"lab"})
	_, raw, v = h.call("POST", "/api/ab/tags/"+guid, tok, nil)
	tags := v.([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "office" || tags[0].(map[string]any)["color"] != float64(4278190335) {
		t.Fatalf("tags: %s", raw)
	}
	_, raw, _ = h.call("POST", "/api/ab/peers?ab="+guid, tok, nil)
	if !strings.Contains(raw, `"tags":["office"]`) || strings.Contains(raw, "lab") {
		t.Fatalf("tag rename/delete must reach the peers: %s", raw)
	}

	h.call("DELETE", "/api/ab/peer/"+guid, tok, []string{"111222333"})
	_, _, v = h.call("POST", "/api/ab/peers?ab="+guid, tok, nil)
	if v.(map[string]any)["total"] != float64(1) {
		t.Fatal("peer delete failed")
	}
}

func TestSharedBookRules(t *testing.T) {
	h := newHarness(t)
	ada, bob := h.user("ada", false), h.user("bob", false)
	h.user("eve", false)
	guid, err := h.st.CreateAB(ada.ID, "Team", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetShares(guid, []store.Share{{UserID: bob.ID, Rule: store.RuleRead}}); err != nil {
		t.Fatal(err)
	}
	a, b, e := h.login("ada"), h.login("bob"), h.login("eve")

	_, raw, v := h.call("POST", "/api/ab/shared/profiles?current=1&pageSize=100", b, nil)
	data := v.(map[string]any)["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["rule"] != float64(1) || data[0].(map[string]any)["owner"] != "ada" || data[0].(map[string]any)["guid"] != guid {
		t.Fatalf("bob's shared profiles: %s", raw)
	}
	if _, _, v := h.call("POST", "/api/ab/shared/profiles", a, nil); v.(map[string]any)["data"].([]any)[0].(map[string]any)["rule"] != float64(3) {
		t.Fatal("owner must have full control")
	}
	// Two books with one name (and a reserved name) must reach the client under distinct names.
	g2, _ := h.st.CreateAB(bob.ID, "Team", "")
	g3, _ := h.st.CreateAB(bob.ID, "My address book", "")
	_ = h.st.SetShares(g2, []store.Share{{UserID: ada.ID, Rule: store.RuleReadWrite}})
	_ = h.st.SetShares(g3, []store.Share{{UserID: ada.ID, Rule: store.RuleRead}})
	_, raw, v = h.call("POST", "/api/ab/shared/profiles", a, nil)
	names := map[string]bool{}
	for _, it := range v.(map[string]any)["data"].([]any) {
		names[it.(map[string]any)["name"].(string)] = true
	}
	if len(names) != 3 || names["My address book"] || !names["Team"] || !names["Team (bob)"] {
		t.Fatalf("profile names must be unique and not reserved: %s", raw)
	}
	if _, _, v := h.call("POST", "/api/ab/shared/profiles", e, nil); len(v.(map[string]any)["data"].([]any)) != 0 {
		t.Fatal("eve must not see the book")
	}
	if code, _, _ := h.call("POST", "/api/ab/peer/add/"+guid, b, map[string]any{"id": "1"}); code != 403 {
		t.Fatalf("read-only share must not write, got %d", code)
	}
	if code, _, _ := h.call("POST", "/api/ab/peers?ab="+guid, e, nil); code != 404 {
		t.Fatalf("stranger must get 404, got %d", code)
	}
	if code, _, _ := h.call("POST", "/api/ab/peer/add/"+guid, a, map[string]any{"id": "1"}); code != 200 {
		t.Fatalf("owner add: %d", code)
	}
	if code, _, _ := h.call("POST", "/api/ab/peers?ab="+guid, b, nil); code != 200 {
		t.Fatalf("reader list: %d", code)
	}
}

func TestLegacyAddressBook(t *testing.T) {
	h := newHarness(t)
	h.user("ada", false)
	tok := h.login("ada")
	doc := `{"tags":["a"],"peers":[{"id":"9","alias":"x","tags":["a"]},{"id":"8"}],"tag_colors":"{\"a\":7}"}`
	if code, raw, _ := h.call("POST", "/api/ab", tok, map[string]string{"data": doc}); code != 200 {
		t.Fatalf("legacy post: %d %s", code, raw)
	}
	_, raw, v := h.call("GET", "/api/ab", tok, nil)
	var got struct {
		Tags      []string
		Peers     []map[string]any
		TagColors string `json:"tag_colors"`
	}
	if err := json.Unmarshal([]byte(v.(map[string]any)["data"].(string)), &got); err != nil {
		t.Fatalf("legacy get: %s", raw)
	}
	if len(got.Peers) != 2 || got.Peers[0]["id"] != "9" || got.Tags[0] != "a" || got.TagColors != `{"a":7}` {
		t.Fatalf("legacy round trip: %s", raw)
	}
}

func TestDeviceReports(t *testing.T) {
	h := newHarness(t)
	_, raw, v := h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1", "ver": 1005003, "modified_at": 0})
	if v.(map[string]any)["sysinfo"] != true {
		t.Fatalf("first heartbeat must ask for sysinfo: %s", raw)
	}
	if _, raw, _ := h.call("POST", "/api/sysinfo", "", map[string]any{"id": "777", "uuid": "u1", "hostname": "box", "os": "linux", "version": "1.5.3"}); raw != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo: %q", raw)
	}
	if _, raw, v := h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1"}); v.(map[string]any)["sysinfo"] != nil {
		t.Fatalf("known device must not be asked again: %s", raw)
	}
	// Another installation must not be able to overwrite the record.
	h.call("POST", "/api/sysinfo", "", map[string]any{"id": "777", "uuid": "other", "hostname": "evil"})
	d, err := h.st.Device("777")
	if err != nil || d.Hostname != "box" || !d.Online {
		t.Fatalf("device: %+v %v", d, err)
	}
	// The client counts an audit report as delivered only on 2xx with an empty body.
	if code, raw, _ := h.call("POST", "/api/audit/conn", "", map[string]any{"id": "777", "uuid": "u1", "conn_id": 5, "session_id": 0, "action": "new", "ip": "10.0.0.9", "nonce": "n1"}); code != 200 || raw != "" {
		t.Fatalf("audit reply must be an empty 200: %d %q", code, raw)
	}
	h.call("POST", "/api/audit/conn", "", map[string]any{"id": "777", "uuid": "u1", "conn_id": 5, "session_id": 0, "action": "new", "ip": "10.0.0.9", "nonce": "n1"}) // retry, same nonce
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/audit/conn", strings.NewReader(`{"peer":["123","ada"],"type":0,"id":"777","uuid":"u1","conn_id":5,"session_id":14172034923488123456,"nonce":"n2"}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("authorized record: %v", err)
	}
	h.call("POST", "/api/audit/conn", "", map[string]any{"id": "777", "uuid": "u1", "conn_id": 5, "action": "close", "nonce": "n3"})
	h.call("POST", "/api/audit/conn", "", map[string]any{"id": "999", "uuid": "zz", "conn_id": 1, "action": "new", "nonce": "n4"})    // unknown device
	h.call("POST", "/api/audit/conn", "", map[string]any{"id": "777", "uuid": "other", "conn_id": 2, "action": "new", "nonce": "n5"}) // wrong uuid
	rows, total, _ := h.st.ConnAudits(10, 0)
	if total != 1 || rows[0].PeerID != "123" || rows[0].PeerName != "ada" || rows[0].IP != "10.0.0.9" || rows[0].EndedAt == 0 || rows[0].SessionID != "14172034923488123456" {
		t.Fatalf("audit: total %d %+v", total, rows)
	}
	for i := 0; i < 2; i++ { // the second one is a retry
		h.call("POST", "/api/audit/file", "", map[string]any{"id": "777", "uuid": "u1", "peer_id": "123", "type": 1, "path": "/tmp", "is_file": false, "info": "{}", "nonce": "f1"})
	}
	if _, n, _ := h.st.FileAudits(10, 0); n != 1 {
		t.Fatalf("file audit must be recorded once, got %d", n)
	}
}

func TestConsoleAuth(t *testing.T) {
	h := newHarness(t)
	h.user("ada", true)
	h.user("bob", false)
	if code, _, _ := h.call("POST", "/console/api/login", "", map[string]string{"username": "ada", "password": "correct horse"}); code != 403 {
		t.Fatalf("console login without the custom header must be refused, got %d", code)
	}
	login := func(name string) string {
		raw, _ := json.Marshal(map[string]string{"username": name, "password": "correct horse"})
		req, _ := http.NewRequest("POST", h.srv.URL+"/console/api/login", bytes.NewReader(raw))
		req.Header.Set("X-RDL", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("console login %s: %v", name, err)
		}
		res.Body.Close()
		for _, c := range res.Cookies() {
			if c.Name == sessionCookie && c.HttpOnly {
				return c.Name + "=" + c.Value
			}
		}
		t.Fatal("no HttpOnly session cookie")
		return ""
	}
	ada, bob := login("ada"), login("bob")
	if code, _, _ := h.call("GET", "/console/api/users", "", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	if code, raw, _ := h.call("GET", "/console/api/users", "", nil, "Cookie", bob); code != 200 || strings.Contains(raw, "example.test") {
		t.Fatalf("non-admins must only get names: %d %s", code, raw)
	}
	if code, _, _ := h.call("GET", "/console/api/settings", "", nil, "Cookie", bob); code != 403 {
		t.Fatalf("settings for non-admin: %d", code)
	}
	if code, _, _ := h.call("POST", "/console/api/users", "", map[string]any{"username": "x", "password": "long enough"}, "Cookie", ada); code != 403 {
		t.Fatalf("cookie alone (no custom header) must not be enough for writes, got %d", code)
	}
	if code, raw, _ := h.call("POST", "/console/api/users", "", map[string]any{"username": "x", "password": "long enough"}, "Cookie", ada, "X-RDL", "1"); code != 200 {
		t.Fatalf("admin create user: %d %s", code, raw)
	}
	if code, _, _ := h.call("POST", "/console/api/users", "", map[string]any{"username": "y"}, "Cookie", ada, "X-RDL", "1", "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin must be refused, got %d", code)
	}
	// A console cookie is not a client token and vice versa.
	if code, _, _ := h.call("GET", "/console/api/overview", h.login("ada"), nil); code != 401 {
		t.Fatalf("client token on console: %d", code)
	}
	// The only administrator cannot be demoted.
	u, _ := h.st.UserByUsername("ada")
	path := "/console/api/users/" + strings.TrimSpace(strings.Split(strings.SplitN(mustString(u.ID), ".", 2)[0], " ")[0])
	if code, _, _ := h.call("PATCH", path, "", map[string]any{"is_admin": false}, "Cookie", ada, "X-RDL", "1"); code != 400 {
		t.Fatalf("demoting the last admin: %d", code)
	}
}

func mustString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestNotesAlarmsKicksAndOptions(t *testing.T) {
	h := newHarness(t)
	h.user("ada", true)
	tok := h.login("ada")
	dev := map[string]any{"id": "777", "uuid": "u1", "hostname": "box", "os": "windows / Windows 11", "version": "1.5.3"}
	h.call("POST", "/api/sysinfo", "", dev)

	// Session note: the app asks for the record, then writes the note.
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/audit/conn", strings.NewReader(`{"peer":["123","ada"],"type":0,"id":"777","uuid":"u1","conn_id":9,"session_id":14172034923488123456,"nonce":"a1"}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatal(err)
	}
	_, raw, v := h.call("GET", "/api/audit/conn/active?id=777&session_id=14172034923488123456&conn_type=0", tok, nil)
	guid, _ := v.(string)
	if guid == "" {
		t.Fatalf("active record must be a JSON string: %s", raw)
	}
	if code, raw, _ := h.call("PUT", "/api/audit", tok, map[string]string{"guid": guid, "note": "fixed the printer"}); code != 200 || raw != "" {
		t.Fatalf("note: %d %q", code, raw)
	}
	if rows, _, _ := h.st.ConnAudits(5, 0); rows[0].Note != "fixed the printer" {
		t.Fatalf("note not stored: %+v", rows[0])
	}
	if _, raw, v := h.call("GET", "/api/audit/conn/active?id=777&session_id=1", tok, nil); v != "" {
		t.Fatalf("unknown session must answer an empty string: %s", raw)
	}

	// Alarm.
	if code, raw, _ := h.call("POST", "/api/audit/alarm", "", map[string]any{"id": "777", "uuid": "u1", "typ": 1, "info": "{}", "nonce": "al1"}); code != 200 || raw != "" {
		t.Fatalf("alarm: %d %q", code, raw)
	}
	if _, n, _ := h.st.Alarms(5, 0); n != 1 {
		t.Fatal("alarm not stored")
	}

	// Live connections are kept; a requested disconnect reaches the device once.
	h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1", "conns": []int{9}})
	if d, _ := h.st.Device("777"); len(d.Conns) != 1 || d.Conns[0] != 9 {
		t.Fatalf("conns: %+v", d.Conns)
	}
	h.srvKick("777", 9)
	_, raw, v = h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1", "conns": []int{9}})
	if d, _ := v.(map[string]any)["disconnect"].([]any); len(d) != 1 || d[0] != float64(9) {
		t.Fatalf("disconnect: %s", raw)
	}
	if _, _, v := h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1"}); v.(map[string]any)["disconnect"] != nil {
		t.Fatal("disconnect must be delivered once")
	}

	// Pushed options: sent while the device's timestamp differs, then no more.
	_ = h.st.SetSetting(keyStrategy, `{"enable-lan-discovery":"N"}`)
	_ = h.st.SetSetting(keyStrategyAt, "1700000000")
	_, raw, v = h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1", "modified_at": 0})
	m := v.(map[string]any)
	if m["modified_at"] != float64(1700000000) || m["strategy"].(map[string]any)["config_options"].(map[string]any)["enable-lan-discovery"] != "N" {
		t.Fatalf("strategy: %s", raw)
	}
	if _, _, v := h.call("POST", "/api/heartbeat", "", map[string]any{"id": "777", "uuid": "u1", "modified_at": 1700000000}); v.(map[string]any)["strategy"] != nil {
		t.Fatal("up-to-date device must not get the options again")
	}

	// Device groups reach the app.
	_ = h.st.SetDeviceGroup("777", "HQ")
	h.st.AssignDevice("777", 1)
	_, raw, _ = h.call("GET", "/api/device-group/accessible?current=1&pageSize=100", tok, nil)
	if !strings.Contains(raw, `"name":"HQ"`) {
		t.Fatalf("device groups: %s", raw)
	}
	_, raw, _ = h.call("GET", "/api/peers?current=1&pageSize=100", tok, nil)
	if !strings.Contains(raw, `"device_group_name":"HQ"`) {
		t.Fatalf("group on peer: %s", raw)
	}
}

func (h *harness) srvKick(device string, conn int64) { h.app.kicks.add(device, conn) }

func TestEnrolment(t *testing.T) {
	h := newHarness(t)
	ada := h.user("ada", true)
	h.user("bob", false)
	guid, _ := h.st.CreateAB(ada.ID, "Field", "")
	dev := map[string]any{"id": "888", "uuid": "u8", "hostname": "kiosk", "os": "android / Android 14", "version": "1.5.3",
		"preset-user-name": "bob", "preset-device-group-name": "Kiosks", "preset-address-book-name": "Field", "preset-address-book-tag": "depot", "preset-address-book-alias": "Kiosk 1"}

	// Off by default: a device cannot enrol itself.
	h.call("POST", "/api/sysinfo", "", dev)
	if d, _ := h.st.Device("888"); d.UserID != 0 || d.Group != "" || h.st.HasPeer(guid, "888") {
		t.Fatalf("presets must be ignored unless enabled: %+v", d)
	}
	_ = h.st.SetSetting(keyAllowPresets, "1")
	h.call("POST", "/api/sysinfo", "", dev)
	d, _ := h.st.Device("888")
	peers, _, _ := h.st.Peers(guid, 0, 0)
	if d.User != "bob" || d.Group != "Kiosks" || len(peers) != 1 || peers[0]["alias"] != "Kiosk 1" || peers[0]["platform"] != "Android" {
		t.Fatalf("enrolment: %+v %+v", d, peers)
	}
	if tags, _ := h.st.Tags(guid); len(tags) != 1 || tags[0].Name != "depot" {
		t.Fatalf("tag: %+v", tags)
	}
	// A device cannot move itself to another owner afterwards.
	dev["preset-user-name"] = "ada"
	h.call("POST", "/api/sysinfo", "", dev)
	if d, _ := h.st.Device("888"); d.User != "bob" {
		t.Fatal("presets must not override an existing owner")
	}

	// rustdesk --assign with an administrator's API token overrides; empty body = "Done!".
	tok, _ := h.st.NewToken(&store.Token{UserID: ada.ID, Kind: "api", Name: "deploy"}, 0)
	if code, raw, _ := h.call("POST", "/api/devices/cli", tok, map[string]any{"id": "888", "uuid": "u8", "user_name": "ada", "note": "front desk"}); code != 200 || raw != "" {
		t.Fatalf("assign: %d %q", code, raw)
	}
	if d, _ := h.st.Device("888"); d.User != "ada" || d.Note != "front desk" {
		t.Fatalf("assign not applied: %+v", d)
	}
	if _, raw, _ := h.call("POST", "/api/devices/cli", tok, map[string]any{"id": "888", "uuid": "u8", "user_name": "nobody"}); !strings.Contains(raw, "No such user") {
		t.Fatalf("unknown user: %q", raw)
	}
	if _, raw, _ := h.call("POST", "/api/devices/cli", h.login("bob"), map[string]any{"id": "888", "uuid": "u8"}); !strings.Contains(raw, "Administrator") {
		t.Fatalf("non-admin: %q", raw)
	}
}

func TestTwoStepSignIn(t *testing.T) {
	h := newHarness(t)
	u := h.user("ada", false)
	secret := newTOTPSecret()
	if code, ok := totpAt("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0)); !ok || code != "287082" {
		t.Fatalf("RFC 6238 test vector: %s", code) // SHA-1, T=59 -> 94287082
	}
	_ = h.st.SetTOTP(u.ID, secret)
	now, _ := totpAt(secret, time.Now())

	// Step one: the password alone yields a challenge, not a token.
	_, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "correct horse", "id": "1", "uuid": "u", "type": "account"})
	m := v.(map[string]any)
	if m["type"] != "email_check" || m["tfa_type"] != "tfa_check" || m["access_token"] != nil || m["user"].(map[string]any)["name"] != "ada" {
		t.Fatalf("challenge: %s", raw)
	}
	ch := m["secret"].(string)
	step2 := func(code string) map[string]any {
		_, _, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "id": "1", "uuid": "u", "type": "email_code", "verificationCode": code, "tfaCode": code, "secret": ch})
		return v.(map[string]any)
	}
	if step2("000000")["error"] == nil {
		t.Fatal("wrong code accepted")
	}
	if m := step2(now); m["type"] != "access_token" || m["access_token"] == "" {
		t.Fatalf("right code refused: %v", m)
	}
	if step2(now)["error"] == nil {
		t.Fatal("a challenge must work once")
	}

	// Console: the code is asked for, then accepted.
	post := func(body map[string]string) (int, string) {
		code, raw, _ := h.call("POST", "/console/api/login", "", body, "X-RDL", "1")
		return code, raw
	}
	if code, raw := post(map[string]string{"username": "ada", "password": "correct horse"}); code != 401 || !strings.Contains(raw, `"totp":true`) {
		t.Fatalf("console must ask for the code: %d %s", code, raw)
	}
	if code, _ := post(map[string]string{"username": "ada", "password": "correct horse", "code": "111111"}); code != 401 {
		t.Fatal("console accepted a wrong code")
	}
	if code, raw := post(map[string]string{"username": "ada", "password": "correct horse", "code": now}); code != 200 {
		t.Fatalf("console refused the right code: %s", raw)
	}
}

func TestSharedGroupVisibility(t *testing.T) {
	h := newHarness(t)
	ada, bob, cy := h.user("ada", false), h.user("bob", false), h.user("cy", false)
	g := &store.Group{Name: "Support", Shared: true}
	if err := h.st.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{ada, bob} {
		u.GroupID = g.ID
		_ = h.st.UpdateUser(u)
	}
	for id, owner := range map[string]int64{"d1": ada.ID, "d2": bob.ID, "d3": cy.ID} {
		h.call("POST", "/api/sysinfo", "", map[string]any{"id": id, "uuid": "u" + id, "hostname": "h" + id, "os": "linux / x"})
		h.st.AssignDevice(id, owner)
	}
	ids := func(tok string) string {
		_, _, v := h.call("GET", "/api/peers?current=1&pageSize=100", tok, nil)
		out := []string{}
		for _, p := range v.(map[string]any)["data"].([]any) {
			if id := p.(map[string]any)["id"].(string); strings.HasPrefix(id, "d") {
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	if got := ids(h.login("ada")); got != "d1,d2" {
		t.Fatalf("shared group member must see the group's devices, got %q", got)
	}
	if got := ids(h.login("cy")); got != "d3" {
		t.Fatalf("outsider must see only their own, got %q", got)
	}
	g.Shared = false
	_ = h.st.SaveGroup(g)
	if got := ids(h.login("ada")); got != "d1" {
		t.Fatalf("regular group must not share devices, got %q", got)
	}
}

func TestGroupSharesAndIdleSessions(t *testing.T) {
	h := newHarness(t)
	ada, bob, cy := h.user("ada", false), h.user("bob", false), h.user("cy", false)
	g := &store.Group{Name: "Field"}
	_ = h.st.SaveGroup(g)
	bob.GroupID, cy.GroupID = g.ID, g.ID
	_ = h.st.UpdateUser(bob)
	_ = h.st.UpdateUser(cy)
	guid, _ := h.st.CreateAB(ada.ID, "Team", "")
	// The group may read; bob personally may write: the higher right wins.
	if err := h.st.SetShares(guid, []store.Share{{GroupID: g.ID, Rule: store.RuleRead}, {UserID: bob.ID, Rule: store.RuleReadWrite}}); err != nil {
		t.Fatal(err)
	}
	rule := func(u *store.User) int { b, _ := h.st.AB(guid, u.ID); return b.Rule }
	if rule(bob) != store.RuleReadWrite || rule(cy) != store.RuleRead || rule(ada) != store.RuleFull {
		t.Fatalf("rules: bob %d cy %d ada %d", rule(bob), rule(cy), rule(ada))
	}
	if bs, _ := h.st.ABsFor(cy.ID); len(bs) != 2 {
		t.Fatalf("a group share must list the book for members: %+v", bs)
	}
	if sh, _ := h.st.Shares(guid); len(sh) != 2 {
		t.Fatalf("shares: %+v", sh)
	}
	_ = h.st.DeleteGroup(g.ID)
	if rule(cy) != store.RuleNone || rule(bob) != store.RuleReadWrite {
		t.Fatal("deleting a group must end its shares only")
	}

	// App sessions that stay unused longer than the configured time are signed out.
	tok := h.login("ada")
	_ = h.st.SetSetting("session_idle_days", "7")
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 200 {
		t.Fatal("fresh session refused")
	}
	if _, err := h.st.DB().Exec("UPDATE tokens SET last_used_at = last_used_at - 8*86400"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatalf("idle session must be signed out, got %d", code)
	}
	// Forgetting a device signs out the app on it.
	tok = h.login("ada")
	_ = h.st.DeleteDevice("100200300")
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatalf("session of a forgotten device must end, got %d", code)
	}
}

func (h *harness) consoleLogin(name string) string {
	h.t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": name, "password": "correct horse"})
	req, _ := http.NewRequest("POST", h.srv.URL+"/console/api/login", bytes.NewReader(raw))
	req.Header.Set("X-RDL", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		h.t.Fatalf("console login %s: %v", name, err)
	}
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Name + "=" + c.Value
		}
	}
	h.t.Fatal("no session cookie")
	return ""
}

// The search index holds the books the caller may read, and nothing else.
func TestSearchIndex(t *testing.T) {
	h := newHarness(t)
	ada, bob, cy := h.user("ada", true), h.user("bob", false), h.user("cy", false)
	put := func(guid, id string) {
		t.Helper()
		if err := h.st.PutPeer(guid, store.Peer{"id": id, "alias": "dev " + id}); err != nil {
			t.Fatal(err)
		}
	}
	adaBook, _ := h.st.PersonalAB(ada.ID)
	bobBook, _ := h.st.PersonalAB(bob.ID)
	field, err := h.st.CreateAB(bob.ID, "Field", "")
	if err != nil {
		t.Fatal(err)
	}
	put(adaBook, "333")
	put(bobBook, "111")
	put(field, "222@host.example?key=a+b/c=")
	if err := h.st.SetShares(field, []store.Share{{UserID: cy.ID, Rule: store.RuleRead}}); err != nil {
		t.Fatal(err)
	}
	index := func(cookie, query string) string {
		t.Helper()
		code, raw, _ := h.call("GET", "/console/api/search-index"+query, "", nil, "Cookie", cookie)
		if code != 200 {
			t.Fatalf("search index: %d %s", code, raw)
		}
		return raw
	}
	if code, _, _ := h.call("GET", "/console/api/search-index", "", nil); code != 401 {
		t.Fatalf("anonymous search index: %d", code)
	}
	got := index(h.consoleLogin("cy"), "")
	if !strings.Contains(got, `"222@host.example?key=a+b/c="`) || !strings.Contains(got, `"name":"Field"`) || !strings.Contains(got, `"rule":1`) {
		t.Fatalf("cy must find the shared entry, key untouched: %s", got)
	}
	if strings.Contains(got, `"111"`) || strings.Contains(got, `"333"`) {
		t.Fatalf("cy must not see other people's books: %s", got)
	}
	adaCookie := h.consoleLogin("ada")
	if got := index(adaCookie, ""); !strings.Contains(got, `"333"`) || strings.Contains(got, `"111"`) {
		t.Fatalf("an admin sees only their own books by default: %s", got)
	}
	if got := index(adaCookie, "?all=1"); !strings.Contains(got, `"111"`) || !strings.Contains(got, `"222@host.example`) || !strings.Contains(got, `"333"`) {
		t.Fatalf("an admin asking for all books gets all of them: %s", got)
	}
	if got := index(h.consoleLogin("bob"), "?all=1"); strings.Contains(got, `"333"`) {
		t.Fatalf("all=1 must do nothing for a regular user: %s", got)
	}
}
