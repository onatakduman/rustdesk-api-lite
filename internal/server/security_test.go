package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// consoleSignIn signs in to the console and returns the session cookie and the answer.
func (h *harness) consoleSignIn(name, password, code string) (cookie string, status int, body map[string]any) {
	h.t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": name, "password": password, "code": code})
	req, _ := http.NewRequest("POST", h.srv.URL+"/console/api/login", strings.NewReader(string(raw)))
	req.Header.Set("X-RDL", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	_ = json.NewDecoder(res.Body).Decode(&body)
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	return cookie, res.StatusCode, body
}

// loginFrom signs the RustDesk app of one device in and returns its token.
func (h *harness) loginFrom(name, deviceID string) string {
	h.t.Helper()
	_, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": name, "password": "correct horse", "id": deviceID, "uuid": "uuid-" + deviceID, "type": "account"})
	tok, _ := v.(map[string]any)["access_token"].(string)
	if tok == "" {
		h.t.Fatalf("login of %s on %s: %s", name, deviceID, raw)
	}
	return tok
}

func TestCodeWorksOnce(t *testing.T) {
	h := newHarness(t)
	u := h.user("ada", false)
	secret := newTOTPSecret()
	_ = h.st.SetTOTP(u.ID, secret)
	base := time.Now()
	now, _ := totpAt(secret, base)
	next, _ := totpAt(secret, base.Add(30*time.Second))

	if _, status, _ := h.consoleSignIn("ada", "correct horse", now); status != 200 {
		t.Fatalf("first use of a code: %d", status)
	}
	if _, status, _ := h.consoleSignIn("ada", "correct horse", now); status != 401 {
		t.Fatalf("a code must not sign in twice, got %d", status)
	}
	// The app's second step shares the rule: the used code is refused, the next one works.
	step2 := func(code string) map[string]any {
		_, _, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "correct horse", "id": "1", "uuid": "u", "type": "account"})
		ch := v.(map[string]any)["secret"].(string)
		_, _, v = h.call("POST", "/api/login", "", map[string]any{"username": "ada", "id": "1", "uuid": "u", "type": "email_code", "tfaCode": code, "secret": ch})
		return v.(map[string]any)
	}
	if step2(now)["error"] == nil {
		t.Fatal("the app accepted a code that was used at the console")
	}
	if m := step2(next); m["type"] != "access_token" {
		t.Fatalf("the following code must work: %v", m)
	}
	// Turning two-step sign-in off asks for a fresh code too.
	cookie, _, _ := h.consoleSignIn("ada", "correct horse", "")
	if cookie != "" {
		t.Fatal("signed in without a code")
	}
}

func TestFirstAccountMustChangePassword(t *testing.T) {
	h := newHarness(t)
	u := h.user("admin", true)
	_ = h.st.SetMustChangePassword(u.ID, true)

	// The printed password does not sign a RustDesk app in.
	if _, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "admin", "password": "correct horse", "id": "1", "uuid": "u", "type": "account"}); v.(map[string]any)["access_token"] != nil || !strings.Contains(raw, "web console") {
		t.Fatalf("app sign-in must be refused: %s", raw)
	}
	cookie, status, body := h.consoleSignIn("admin", "correct horse", "")
	if status != 200 || body["user"].(map[string]any)["must_change_password"] != true {
		t.Fatalf("console sign-in: %d %v", status, body)
	}
	// Nothing works but the password change.
	for _, path := range []string{"/console/api/overview", "/console/api/users", "/console/api/settings", "/console/api/address-books"} {
		if code, raw, _ := h.call("GET", path, "", nil, "Cookie", cookie); code != 403 || !strings.Contains(raw, "must_change_password") {
			t.Fatalf("%s before the change: %d %s", path, code, raw)
		}
	}
	if code, _, _ := h.call("POST", "/console/api/tokens", "", map[string]string{"name": "x"}, "Cookie", cookie, "X-RDL", "1"); code != 403 {
		t.Fatalf("an API token before the change: %d", code)
	}
	if res := h.nav("GET", "/oidc/login?link=1&provider=none", nil, "Cookie", cookie); res.StatusCode != 401 {
		t.Fatalf("linking a provider before the change: %d", res.StatusCode)
	}
	change := func(newPW string) int {
		code, _, _ := h.call("POST", "/console/api/me/password", "", map[string]string{"current": "correct horse", "new": newPW}, "Cookie", cookie, "X-RDL", "1")
		return code
	}
	if code := change("correct horse"); code != 400 {
		t.Fatalf("keeping the first password: %d", code)
	}
	if code := change("a password of my own"); code != 200 {
		t.Fatalf("changing the password: %d", code)
	}
	if code, raw, _ := h.call("GET", "/console/api/overview", "", nil, "Cookie", cookie); code != 200 {
		t.Fatalf("after the change: %d %s", code, raw)
	}
	if _, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "admin", "password": "a password of my own", "id": "1", "uuid": "u", "type": "account"}); v.(map[string]any)["access_token"] == nil {
		t.Fatalf("app sign-in after the change: %s", raw)
	}
}

func TestSessionNoteBelongsToItsOpener(t *testing.T) {
	h := newHarness(t)
	h.user("ada", false)
	h.user("bob", false)
	h.user("root", true)
	ada, bob, root := h.loginFrom("ada", "100200300"), h.loginFrom("bob", "200300400"), h.loginFrom("root", "300400500")
	h.call("POST", "/api/sysinfo", "", map[string]any{"id": "777", "uuid": "u1", "hostname": "box", "os": "linux", "version": "1.5.3"})
	// The controlled device reports a session that was opened from ada's device.
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/audit/conn", strings.NewReader(`{"peer":["100200300","ada"],"type":0,"id":"777","uuid":"u1","conn_id":9,"session_id":14172034923488123456,"nonce":"a1"}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatal(err)
	}
	note := func(tok, text string) int {
		code, _, _ := h.call("PUT", "/api/audit", tok, map[string]string{"guid": "1", "note": text})
		return code
	}
	stored := func() string { rows, _, _ := h.st.ConnAudits(5, 0); return rows[0].Note }
	// Record numbers are easy to guess; knowing one must not be enough.
	if code := note(bob, "bob was here"); code != 403 || stored() != "" {
		t.Fatalf("someone else's session: %d, note %q", code, stored())
	}
	if code := note(ada, "fixed the printer"); code != 200 || stored() != "fixed the printer" {
		t.Fatalf("own session: %d, note %q", code, stored())
	}
	if code := note(root, "checked by an administrator"); code != 200 || stored() != "checked by an administrator" {
		t.Fatalf("administrator: %d, note %q", code, stored())
	}
	if code := note(ada, "x"); code != 200 {
		t.Fatalf("own session again: %d", code)
	}
	if code, _, _ := h.call("PUT", "/api/audit", ada, map[string]string{"guid": "99", "note": "x"}); code != 404 {
		t.Fatalf("unknown record: %d", code)
	}
	// A session opened towards a device on another server names the opener as id@server.
	req, _ = http.NewRequest("POST", h.srv.URL+"/api/audit/conn", strings.NewReader(`{"peer":["200300400@id.example.test","bob"],"type":0,"id":"777","uuid":"u1","conn_id":10,"session_id":2,"nonce":"a2"}`))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatal(err)
	}
	if code, _, _ := h.call("PUT", "/api/audit", bob, map[string]string{"guid": "2", "note": "mine"}); code != 200 {
		t.Fatalf("own session, opener with a server suffix: %d", code)
	}
}

func TestNewDevicesAreLimited(t *testing.T) {
	h := newHarness(t)
	h.app.newDev.perAddr = 3
	beat := func(id string) { h.call("POST", "/api/heartbeat", "", map[string]any{"id": id, "uuid": "u" + id}) }
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		beat(id)
	}
	if n := h.st.Counts().Devices; n != 3 {
		t.Fatalf("one address added %d devices, the limit is 3", n)
	}
	if _, raw, _ := h.call("POST", "/api/sysinfo", "", map[string]any{"id": "6", "uuid": "u6", "hostname": "flood"}); raw != "ID_NOT_FOUND" {
		t.Fatalf("system information of a device over the limit: %q", raw)
	}
	// Devices that are known keep reporting.
	if _, raw, _ := h.call("POST", "/api/sysinfo", "", map[string]any{"id": "2", "uuid": "u2", "hostname": "box", "os": "linux"}); raw != "SYSINFO_UPDATED" {
		t.Fatalf("known device: %q", raw)
	}
	if d, err := h.st.Device("2"); err != nil || d.Hostname != "box" || !d.Online {
		t.Fatalf("known device: %+v %v", d, err)
	}
	// The total is a limit of its own, whatever the address.
	h.app.newDev.perAddr, h.app.newDev.total = 100, 3
	beat("7")
	if n := h.st.Counts().Devices; n != 3 {
		t.Fatalf("total limit: %d devices", n)
	}
	// Once the hour has passed there is room again.
	h.app.newDev.mu.Lock()
	h.app.newDev.all, h.app.newDev.byAddr = nil, map[string][]time.Time{}
	h.app.newDev.mu.Unlock()
	beat("7")
	if n := h.st.Counts().Devices; n != 4 {
		t.Fatalf("after the window: %d devices", n)
	}
}

// ---- sign-in through a provider ----

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// nav makes a browser-like request that does not follow redirects. A non-nil form is posted.
func (h *harness) nav(method, path string, form url.Values, hdr ...string) *http.Response {
	h.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := noFollow.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	res.Body = io.NopCloser(strings.NewReader(string(raw)))
	return res
}

func bodyOf(res *http.Response) string { raw, _ := io.ReadAll(res.Body); return string(raw) }

func cookieOf(res *http.Response, name string) string {
	for _, c := range res.Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

// fakeProvider is an OpenID Connect provider: the authorization code "x"
// stands for the person whose claims are people["x"].
func fakeProvider(t *testing.T, people map[string]map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token", "userinfo_endpoint": srv.URL + "/userinfo"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token-of-" + r.PostFormValue("code")})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer token-of-")
		if people[who] == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(people[who])
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderSignIn(t *testing.T) {
	h := newHarness(t)
	h.user("root", true) // root@example.test
	h.user("bob", false) // bob@example.test
	idp := fakeProvider(t, map[string]map[string]any{
		"root":     {"sub": "s-root", "email": "root@example.test", "email_verified": true},
		"bob":      {"sub": "s-bob", "email": "BOB@example.test", "email_verified": true},
		"stranger": {"sub": "s-new", "email": "new@example.test", "email_verified": true, "preferred_username": "newcomer"},
		"unproven": {"sub": "s-fake", "email": "bob@example.test", "email_verified": false},
	})
	provider := func(link, register bool) {
		if err := h.st.SaveOIDCProvider(&store.OIDCProvider{Name: "corp", Issuer: idp.URL, ClientID: "id", ClientSecret: "secret", LinkByEmail: link, AutoRegister: register}); err != nil {
			t.Fatal(err)
		}
	}
	// start begins a console sign-in and returns the state and the cookie that binds it to this browser.
	start := func() (state, cookie string) {
		res := h.nav("GET", "/oidc/login?provider=corp", nil)
		loc, _ := url.Parse(res.Header.Get("Location"))
		state, cookie = loc.Query().Get("state"), cookieOf(res, oidcCookie)
		if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), idp.URL+"/authorize?") || state == "" || cookie == "" {
			t.Fatalf("start of a sign-in: %d %q cookie %q", res.StatusCode, res.Header.Get("Location"), cookie)
		}
		return state, cookie
	}
	// finish plays the provider's answer for a person and returns the outcome.
	finish := func(who string) *http.Response {
		state, cookie := start()
		return h.nav("GET", "/api/oidc/callback?code="+who+"&state="+state, nil, "Cookie", cookie)
	}
	signedIn := func(res *http.Response) bool { return res.StatusCode == 302 && cookieOf(res, sessionCookie) != "" }
	linked := func(sub string) bool { _, err := h.st.UserByIdentity("corp", sub); return err == nil }

	// A provider that is not trusted with it links nobody by e-mail.
	provider(false, false)
	if res := finish("bob"); signedIn(res) || res.StatusCode != 403 || linked("s-bob") {
		t.Fatalf("linking by e-mail is off: %d", res.StatusCode)
	}
	// Self-registration must not make a second account for an address that has one.
	provider(false, true)
	if res := finish("bob"); signedIn(res) || h.st.CountUsers() != 2 {
		t.Fatalf("existing address with self-registration: %d, %d users", res.StatusCode, h.st.CountUsers())
	}

	provider(true, false)
	// An address the provider did not verify proves nothing.
	if res := finish("unproven"); signedIn(res) || linked("s-fake") {
		t.Fatalf("unverified address: %d", res.StatusCode)
	}
	// Administrators are never found by e-mail, whatever the provider says.
	if res := finish("root"); signedIn(res) || res.StatusCode != 403 || linked("s-root") {
		t.Fatalf("administrator by e-mail: %d", res.StatusCode)
	}
	if res := finish("stranger"); signedIn(res) {
		t.Fatal("a stranger got in without self-registration")
	}

	// The answer has to come back to the browser that asked.
	state, cookie := start()
	if res := h.nav("GET", "/api/oidc/callback?code=bob&state="+state, nil); res.StatusCode != 400 || signedIn(res) {
		t.Fatalf("answer in a browser that did not start the sign-in: %d", res.StatusCode)
	}
	if res := h.nav("GET", "/api/oidc/callback?code=bob&state="+state, nil, "Cookie", oidcCookie+"=something-else"); res.StatusCode != 400 {
		t.Fatalf("answer with another sign-in's cookie: %d", res.StatusCode)
	}
	// ... and still works there afterwards: bob is linked by his verified address.
	res := h.nav("GET", "/api/oidc/callback?code=bob&state="+state, nil, "Cookie", cookie)
	if !signedIn(res) || !linked("s-bob") {
		t.Fatalf("sign-in by verified e-mail: %d %s", res.StatusCode, bodyOf(res))
	}
	if again := h.nav("GET", "/api/oidc/callback?code=bob&state="+state, nil, "Cookie", cookie); signedIn(again) {
		t.Fatal("a sign-in must work once")
	}
	// Once linked, the identity signs in whatever the options say.
	provider(false, false)
	if res := finish("bob"); !signedIn(res) {
		t.Fatalf("linked identity: %d", res.StatusCode)
	}

	// An administrator links the sign-in from Settings, signed in; after that it works.
	rootCookie, _, _ := h.consoleSignIn("root", "correct horse", "")
	res = h.nav("GET", "/oidc/login?link=1&provider=corp", nil, "Cookie", rootCookie)
	loc, _ := url.Parse(res.Header.Get("Location"))
	res = h.nav("GET", "/api/oidc/callback?code=root&state="+loc.Query().Get("state"), nil, "Cookie", rootCookie+"; "+cookieOf(res, oidcCookie))
	if res.StatusCode != 302 || !linked("s-root") {
		t.Fatalf("linking from Settings: %d %s", res.StatusCode, bodyOf(res))
	}
	if res := finish("root"); !signedIn(res) {
		t.Fatalf("administrator with a linked identity: %d", res.StatusCode)
	}

	// Self-registration makes an account for someone new.
	provider(false, true)
	if res := finish("stranger"); !signedIn(res) {
		t.Fatalf("self-registration: %d %s", res.StatusCode, bodyOf(res))
	}
	if u, err := h.st.UserByUsername("newcomer"); err != nil || u.IsAdmin || u.Email != "new@example.test" {
		t.Fatalf("registered account: %+v %v", u, err)
	}
	// Someone who registers with an address the provider did not verify does not get
	// that address on the account, where bob's later sign-in could find it.
	if res := finish("unproven"); !signedIn(res) {
		t.Fatalf("self-registration with an unverified address: %d", res.StatusCode)
	}
	if u, err := h.st.UserByIdentity("corp", "s-fake"); err != nil || u.Email != "" || u.Username == "bob" {
		t.Fatalf("account of the unverified address: %+v %v", u, err)
	}
}

func TestAppSignInThroughProviderIsConfirmed(t *testing.T) {
	h := newHarness(t)
	h.user("bob", false)
	idp := fakeProvider(t, map[string]map[string]any{"bob": {"sub": "s-bob", "email": "bob@example.test", "email_verified": true}})
	_ = h.st.SaveOIDCProvider(&store.OIDCProvider{Name: "corp", Issuer: idp.URL, ClientID: "id", ClientSecret: "secret", LinkByEmail: true})

	_, raw, v := h.call("POST", "/api/oidc/auth", "", map[string]any{"op": "corp", "id": "100200300", "uuid": "u1", "deviceInfo": map[string]string{"os": "windows", "type": "client", "name": "<b>front desk</b>"}})
	code, _ := v.(map[string]any)["code"].(string)
	if code == "" || !strings.Contains(raw, "/oidc/login?code=") {
		t.Fatalf("start from the app: %s", raw)
	}
	poll := func() map[string]any {
		_, _, v := h.call("GET", "/api/oidc/auth-query?code="+code+"&id=100200300&uuid=u1", "", nil)
		return v.(map[string]any)
	}
	// The link alone leads nowhere: it shows who is asking and waits for a click.
	res := h.nav("GET", "/oidc/login?code="+code, nil)
	page := bodyOf(res)
	if res.StatusCode != 200 || res.Header.Get("Location") != "" || cookieOf(res, oidcCookie) != "" {
		t.Fatalf("the link must not go to the provider by itself: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if !strings.Contains(page, "&lt;b&gt;front desk&lt;/b&gt; (ID 100200300)") || strings.Contains(page, "<b>front desk</b>") || !strings.Contains(page, `method="post"`) {
		t.Fatalf("the page must name the device, escaped, and ask: %s", page)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("the question must not be shown in a frame")
	}
	// Another site cannot press the button.
	form := url.Values{"code": {code}}
	if res := h.nav("POST", "/oidc/login", form, "Origin", "https://evil.example"); res.StatusCode != 403 {
		t.Fatalf("form sent by another site: %d", res.StatusCode)
	}
	if res := h.nav("POST", "/oidc/login", form, "Sec-Fetch-Site", "cross-site"); res.StatusCode != 403 {
		t.Fatalf("cross-site form: %d", res.StatusCode)
	}
	if poll()["access_token"] != nil {
		t.Fatal("signed in before anyone agreed")
	}
	// The person agrees on the page itself.
	res = h.nav("POST", "/oidc/login", form, "Origin", h.srv.URL, "Sec-Fetch-Site", "same-origin")
	loc, _ := url.Parse(res.Header.Get("Location"))
	cookie := cookieOf(res, oidcCookie)
	if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), idp.URL+"/authorize?") || cookie == "" {
		t.Fatalf("after the click: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res := h.nav("GET", "/api/oidc/callback?code=bob&state="+loc.Query().Get("state"), nil, "Cookie", cookie); res.StatusCode != 200 || !strings.Contains(bodyOf(res), "return to RustDesk") {
		t.Fatalf("provider's answer: %d", res.StatusCode)
	}
	m := poll()
	if m["type"] != "access_token" || m["user"].(map[string]any)["name"] != "bob" {
		t.Fatalf("the app must be signed in now: %v", m)
	}
	// A console sign-in cannot be continued through the app's form.
	res = h.nav("GET", "/oidc/login?provider=corp", nil)
	loc, _ = url.Parse(res.Header.Get("Location"))
	sess, err := h.st.OIDCSessionByState(loc.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	if res := h.nav("POST", "/oidc/login", url.Values{"code": {sess.Code}}); res.StatusCode != 400 {
		t.Fatalf("console sign-in through the app's form: %d", res.StatusCode)
	}
}
