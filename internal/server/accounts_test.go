package server

import (
	"bufio"
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// admin calls a console endpoint as a signed-in person (cookie plus the same-origin header).
func (h *harness) admin(cookie, method, path string, body any) (int, string, map[string]any) {
	h.t.Helper()
	code, raw, v := h.call(method, path, "", body, "Cookie", cookie, "X-RDL", "1")
	m, _ := v.(map[string]any)
	return code, raw, m
}

// open calls an endpoint of the invite page: no session, only the same-origin header.
func (h *harness) open(path string, body any) (int, string, map[string]any) {
	h.t.Helper()
	code, raw, v := h.call("POST", path, "", body, "X-RDL", "1")
	m, _ := v.(map[string]any)
	return code, raw, m
}

func secretOf(t *testing.T, link string) string {
	t.Helper()
	_, secret, ok := strings.Cut(link, "/#/invite/")
	if !ok || len(secret) < 40 {
		t.Fatalf("not an invite link: %q", link)
	}
	return secret
}

func (h *harness) actions() []string {
	rows, _, _ := h.st.Changes(100, 0)
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Actor+" "+r.Action+" "+r.Target)
	}
	return out
}

func TestInviteLink(t *testing.T) {
	h := newHarness(t)
	h.user("root", true)
	h.user("bob", false)
	root := h.consoleLogin("root")

	// A new account without a password, with a link for its owner.
	code, raw, m := h.admin(root, "POST", "/console/api/users", map[string]any{"username": "ada", "email": "ada@example.test", "invite": true})
	if code != 200 || m["invite"] == nil {
		t.Fatalf("create with invite: %d %s", code, raw)
	}
	inv := m["invite"].(map[string]any)
	secret := secretOf(t, inv["url"].(string))
	if left := int64(inv["expires_at"].(float64)) - time.Now().Unix(); left < 71*3600 || left > 72*3600 {
		t.Fatalf("default validity: %d s", left)
	}
	if u, _ := h.st.UserByUsername("ada"); u.HasPassword {
		t.Fatal("the invited account must start without a password")
	}
	if strings.Contains(raw, store.InviteHash(secret)) {
		t.Fatal("the answer must not carry the stored hash")
	}
	_, raw, _ = h.admin(root, "GET", "/console/api/users", nil)
	if !strings.Contains(raw, `"invite_expires_at":`+strconv.FormatInt(int64(inv["expires_at"].(float64)), 10)) || strings.Contains(raw, secret) {
		t.Fatalf("the user list shows that a link is out, never the link: %s", raw)
	}

	// The page behind the link.
	if code, _, _ := h.call("POST", "/console/api/invite/check", "", map[string]string{"token": secret}); code != 403 {
		t.Fatalf("another site must not use the link: %d", code)
	}
	if code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": "wrong"}); code != 404 {
		t.Fatalf("wrong link: %d", code)
	}
	if code, _, m := h.open("/console/api/invite/check", map[string]string{"token": secret}); code != 200 || m["username"] != "ada" || m["has_password"] != false {
		t.Fatalf("check: %d %v", code, m)
	}
	if code, _, _ := h.open("/console/api/invite/accept", map[string]string{"token": secret, "password": "short"}); code != 400 {
		t.Fatalf("short password: %d", code)
	}
	code, raw, m = h.open("/console/api/invite/accept", map[string]string{"token": secret, "password": "a password of my own"})
	if code != 200 || m["user"].(map[string]any)["username"] != "ada" || m["user"].(map[string]any)["has_password"] != true {
		t.Fatalf("accept: %d %s", code, raw)
	}
	// It works once.
	if code, _, _ := h.open("/console/api/invite/accept", map[string]string{"token": secret, "password": "another password"}); code != 404 {
		t.Fatalf("second use: %d", code)
	}
	if _, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "a password of my own", "id": "1", "uuid": "u", "type": "account"}); v.(map[string]any)["access_token"] == nil {
		t.Fatalf("sign-in with the chosen password: %s", raw)
	}
	for _, want := range []string{"root user.create ada", "root user.invite ada", "ada user.invite.used ada"} {
		if got := h.actions(); !contains(got, want) {
			t.Fatalf("log of changes lacks %q: %v", want, got)
		}
	}

	link := func(name string, body map[string]any) (string, map[string]any) {
		u, _ := h.st.UserByUsername(name)
		code, raw, m := h.admin(root, "POST", "/console/api/users/"+strconv.FormatInt(u.ID, 10)+"/invite", body)
		if code != 200 {
			t.Fatalf("link for %s: %d %s", name, code, raw)
		}
		return secretOf(t, m["url"].(string)), m
	}
	valid := func(secret string) bool {
		code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": secret})
		return code == 200
	}
	// As a reset: it replaces the password and signs out what was signed in.
	tok := h.login("bob")
	first, _ := link("bob", map[string]any{})
	second, _ := link("bob", map[string]any{"hours": 24})
	if valid(first) || !valid(second) {
		t.Fatal("a new link must replace the one before")
	}
	if code, _, _ := h.open("/console/api/invite/accept", map[string]string{"token": second, "password": "bob's new password"}); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatalf("sessions from before the reset must end, got %d", code)
	}

	// Withdrawn, run out, or the account switched off: the link is dead.
	bob, _ := h.st.UserByUsername("bob")
	path := "/console/api/users/" + strconv.FormatInt(bob.ID, 10)
	withdrawn, _ := link("bob", map[string]any{})
	if code, _, _ := h.admin(root, "DELETE", path+"/invite", nil); code != 200 || valid(withdrawn) {
		t.Fatalf("withdraw: %d", code)
	}
	old, _ := link("bob", map[string]any{})
	if _, err := h.st.DB().Exec("UPDATE invites SET expires_at = ? WHERE user_id = ?", time.Now().Unix()-1, bob.ID); err != nil || valid(old) {
		t.Fatalf("a link that ran out must not work: %v", err)
	}
	off, _ := link("bob", map[string]any{})
	h.admin(root, "PATCH", path, map[string]any{"disabled": true})
	if valid(off) {
		t.Fatal("the link of a disabled account must not work")
	}
	if code, _, _ := h.admin(root, "POST", path+"/invite", map[string]any{}); code != 400 {
		t.Fatalf("link for a disabled account: %d", code)
	}

	// A link into an administrator's account is valid for a day at most.
	_, m = link("root", map[string]any{"hours": 168})
	if left := int64(m["expires_at"].(float64)) - time.Now().Unix(); left > 24*3600 {
		t.Fatalf("administrator link valid for %d s", left)
	}
	// Only administrators hand out links.
	ada := h.consoleLogin2("ada", "a password of my own")
	if code, _, _ := h.admin(ada, "POST", path+"/invite", map[string]any{}); code != 403 {
		t.Fatalf("link made by a non-administrator: %d", code)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// consoleLogin2 signs in with a given password and returns the session cookie.
func (h *harness) consoleLogin2(name, password string) string {
	h.t.Helper()
	cookie, status, _ := h.consoleSignIn(name, password, "")
	if status != 200 || cookie == "" {
		h.t.Fatalf("console sign-in of %s: %d", name, status)
	}
	return cookie
}

func TestInviteLinkGuessingIsThrottled(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < failMax; i++ {
		if code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": "guess-" + strconv.Itoa(i)}); code != 404 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	if code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": "one more"}); code != 429 {
		t.Fatalf("guessing must be slowed down, got %d", code)
	}
}

func TestInviteLinkKeepsTheSecondStep(t *testing.T) {
	h := newHarness(t)
	h.user("root", true)
	u := h.user("ada", false)
	_ = h.st.SetTOTP(u.ID, newTOTPSecret())
	_, _, m := h.admin(h.consoleLogin("root"), "POST", "/console/api/users/"+strconv.FormatInt(u.ID, 10)+"/invite", map[string]any{})
	code, raw, v := h.call("POST", "/console/api/invite/accept", "", map[string]string{"token": secretOf(t, m["url"].(string)), "password": "a password of my own"}, "X-RDL", "1")
	if code != 200 || v.(map[string]any)["sign_in"] != true || v.(map[string]any)["user"] != nil {
		t.Fatalf("an account with two-step sign-in is not signed in by the link: %d %s", code, raw)
	}
	if _, status, body := h.consoleSignIn("ada", "a password of my own", ""); status != 401 || body["totp"] != true {
		t.Fatalf("the new password still needs the code: %d %v", status, body)
	}
}

func TestInviteLinkWithProvider(t *testing.T) {
	h := newHarness(t)
	rootUser := h.user("root", true)
	h.user("bob", false)
	idp := fakeProvider(t, map[string]map[string]any{
		"root": {"sub": "s-root", "email": "someone@elsewhere.test", "email_verified": true},
		"bob":  {"sub": "s-bob", "email": "bob@example.test", "email_verified": true},
	})
	// The provider finds nobody by itself; only the link says who the person is.
	_ = h.st.SaveOIDCProvider(&store.OIDCProvider{Name: "corp", Issuer: idp.URL, ClientID: "id", ClientSecret: "secret"})
	bob, _ := h.st.UserByUsername("bob")
	_ = h.st.LinkIdentity("corp", "s-bob", bob.ID, "bob@example.test")
	cookie := h.consoleLogin("root")
	newLink := func() string {
		_, _, m := h.admin(cookie, "POST", "/console/api/users/"+strconv.FormatInt(rootUser.ID, 10)+"/invite", map[string]any{})
		return secretOf(t, m["url"].(string))
	}
	begin := func(secret string) (string, string) {
		code, raw, m := h.open("/console/api/invite/provider", map[string]string{"token": secret, "provider": "corp"})
		if code != 200 {
			t.Fatalf("start with a provider: %d %s", code, raw)
		}
		to, _ := url.Parse(m["url"].(string))
		if !strings.HasPrefix(m["url"].(string), idp.URL+"/authorize?") || to.Query().Get("state") == "" {
			t.Fatalf("provider address: %s", raw)
		}
		return to.Query().Get("state"), oidcCookie + "=" + to.Query().Get("state")
	}
	if code, _, _ := h.open("/console/api/invite/provider", map[string]string{"token": "wrong", "provider": "corp"}); code != 404 {
		t.Fatalf("wrong link: %d", code)
	}
	// Someone else's provider identity cannot be moved over.
	secret := newLink()
	state, oidc := begin(secret)
	if res := h.nav("GET", "/api/oidc/callback?code=bob&state="+state, nil, "Cookie", oidc); res.StatusCode != 409 {
		t.Fatalf("identity of another account: %d", res.StatusCode)
	}
	// The administrator links their own identity through the link, whatever its address.
	state, oidc = begin(secret)
	res := h.nav("GET", "/api/oidc/callback?code=root&state="+state, nil, "Cookie", oidc)
	if res.StatusCode != 302 || cookieOf(res, sessionCookie) == "" {
		t.Fatalf("sign-in through the link: %d %s", res.StatusCode, bodyOf(res))
	}
	if u, err := h.st.UserByIdentity("corp", "s-root"); err != nil || u.ID != rootUser.ID {
		t.Fatalf("identity must be linked to the invited account: %+v %v", u, err)
	}
	if code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": secret}); code != 404 {
		t.Fatalf("the link must be used up: %d", code)
	}
	// A provider sign-in that was started with a link that has gone meanwhile ends nowhere.
	other := newLink()
	state, oidc = begin(other)
	_ = h.st.DeleteInvites(rootUser.ID)
	if res := h.nav("GET", "/api/oidc/callback?code=root&state="+state, nil, "Cookie", oidc); res.StatusCode != 403 || cookieOf(res, sessionCookie) != "" {
		t.Fatalf("withdrawn link: %d", res.StatusCode)
	}
}

func TestProviderDomainsAndGroup(t *testing.T) {
	h := newHarness(t)
	h.user("root", true)
	h.user("bob", false) // bob@example.test
	g := &store.Group{Name: "Staff"}
	_ = h.st.SaveGroup(g)
	idp := fakeProvider(t, map[string]map[string]any{
		"bob":      {"sub": "s-bob", "email": "bob@example.test", "email_verified": true},
		"staff":    {"sub": "s-staff", "email": "new@Example.Test", "email_verified": true, "preferred_username": "newcomer"},
		"outsider": {"sub": "s-out", "email": "someone@elsewhere.test", "email_verified": true, "preferred_username": "outsider"},
		"unproven": {"sub": "s-fake", "email": "fake@example.test", "email_verified": false, "preferred_username": "fake"},
		"partner":  {"sub": "s-partner", "email": "pat@partner.test", "email_verified": true},
	})
	root := h.consoleLogin("root")
	save := func(body map[string]any) (int, string) {
		body["issuer"], body["client_id"], body["client_secret"] = idp.URL, "id", "secret"
		code, raw, _ := h.admin(root, "PUT", "/console/api/oidc/corp", body)
		return code, raw
	}
	if code, _ := save(map[string]any{"allowed_domains": "not a domain!"}); code != 400 {
		t.Fatalf("nonsense domain: %d", code)
	}
	if code, _ := save(map[string]any{"default_group_id": 999}); code != 400 {
		t.Fatalf("group that does not exist: %d", code)
	}
	if code, raw := save(map[string]any{"auto_register": true, "link_by_email": true, "allowed_domains": " @Example.TEST, other.test ;example.test", "default_group_id": g.ID}); code != 200 {
		t.Fatalf("save: %d %s", code, raw)
	}
	if p, _ := h.st.OIDCProvider("corp"); p.AllowedDomains != "example.test, other.test" || p.DefaultGroupID != g.ID {
		t.Fatalf("stored provider: %+v", p)
	}
	finish := func(who string) bool {
		res := h.nav("GET", "/oidc/login?provider=corp", nil)
		loc, _ := url.Parse(res.Header.Get("Location"))
		res = h.nav("GET", "/api/oidc/callback?code="+who+"&state="+loc.Query().Get("state"), nil, "Cookie", cookieOf(res, oidcCookie))
		return res.StatusCode == 302 && cookieOf(res, sessionCookie) != ""
	}
	// Outside the domains the provider creates nothing and finds nobody,
	// not even an account that has exactly that verified address.
	guest := h.user("guest", false)
	if _, err := h.st.DB().Exec("UPDATE users SET email = 'someone@elsewhere.test' WHERE id = ?", guest.ID); err != nil {
		t.Fatal(err)
	}
	if finish("outsider") || finish("unproven") {
		t.Fatal("an address outside the domains, or an unverified one, got in")
	}
	if _, err := h.st.UserByIdentity("corp", "s-out"); err == nil {
		t.Fatal("an account was found by an address outside the domains")
	}
	if _, err := h.st.UserByUsername("outsider"); err == nil {
		t.Fatal("account created for an outside address")
	}
	// Inside, a new person gets an account in the group, an existing one is found.
	if !finish("staff") {
		t.Fatal("an address inside the domains was refused")
	}
	if u, err := h.st.UserByUsername("newcomer"); err != nil || u.GroupID != g.ID || u.IsAdmin {
		t.Fatalf("registered account: %+v %v", u, err)
	}
	if !finish("bob") {
		t.Fatal("existing account at an allowed domain")
	}
	// An identity that was linked on purpose works whatever the domains say.
	pat := h.user("pat", false)
	_ = h.st.LinkIdentity("corp", "s-partner", pat.ID, "pat@partner.test")
	if !finish("partner") {
		t.Fatal("a linked identity must keep working")
	}
	// Deleting the group leaves the provider without one.
	h.admin(root, "DELETE", "/console/api/groups/"+strconv.FormatInt(g.ID, 10), nil)
	if p, _ := h.st.OIDCProvider("corp"); p.DefaultGroupID != 0 {
		t.Fatalf("group of a provider after the group is gone: %d", p.DefaultGroupID)
	}
}

func TestAccountWithEndDate(t *testing.T) {
	h := newHarness(t)
	rootUser := h.user("root", true)
	h.user("temp", false)
	h.user("bob", false)
	root := h.consoleLogin("root")
	temp, _ := h.st.UserByUsername("temp")
	path := "/console/api/users/" + strconv.FormatInt(temp.ID, 10)
	in := func(days int) int64 { return time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix() }

	if code, _, _ := h.admin(root, "PATCH", path, map[string]any{"expires_at": in(-1)}); code != 400 {
		t.Fatalf("end date in the past: %d", code)
	}
	if code, _, _ := h.admin(root, "PATCH", "/console/api/users/"+strconv.FormatInt(rootUser.ID, 10), map[string]any{"expires_at": in(7)}); code != 400 {
		t.Fatalf("end date for an administrator: %d", code)
	}
	if code, _, _ := h.admin(root, "POST", "/console/api/users", map[string]any{"username": "x", "password": "long enough", "is_admin": true, "expires_at": in(7)}); code != 400 {
		t.Fatalf("new administrator with an end date: %d", code)
	}
	if code, raw, _ := h.admin(root, "PATCH", path, map[string]any{"expires_at": in(7)}); code != 200 {
		t.Fatalf("end date: %d %s", code, raw)
	}
	if code, _, _ := h.admin(root, "PATCH", path, map[string]any{"is_admin": true}); code != 400 {
		t.Fatalf("an account with an end date must not become administrator: %d", code)
	}
	// Until the day comes the account works.
	tok := h.login("temp")
	cookie := h.consoleLogin("temp")
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 200 {
		t.Fatalf("before the end: %d", code)
	}
	// Then it is over: no sign-in, and what was signed in stops.
	if _, err := h.st.DB().Exec("UPDATE users SET expires_at = ? WHERE id = ?", time.Now().Unix()-1, temp.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 401 {
		t.Fatalf("app session after the end: %d", code)
	}
	if code, _, _ := h.call("GET", "/console/api/overview", "", nil, "Cookie", cookie); code != 401 {
		t.Fatalf("console session after the end: %d", code)
	}
	if _, raw, v := h.call("POST", "/api/login", "", map[string]any{"username": "temp", "password": "correct horse", "id": "1", "uuid": "u", "type": "account"}); v.(map[string]any)["access_token"] != nil || !strings.Contains(raw, "ended") {
		t.Fatalf("sign-in after the end: %s", raw)
	}
	if _, status, _ := h.consoleSignIn("temp", "correct horse", ""); status != 401 {
		t.Fatalf("console sign-in after the end: %d", status)
	}
	// Others no longer find it for sharing; the administrator sees that it ended.
	if _, raw, _ := h.call("GET", "/console/api/users", "", nil, "Cookie", h.consoleLogin("bob")); strings.Contains(raw, `"temp"`) {
		t.Fatalf("ended account offered for sharing: %s", raw)
	}
	if _, raw, _ := h.admin(root, "GET", "/console/api/users", nil); !strings.Contains(raw, `"expired":true`) {
		t.Fatalf("the list must tell that the account ended: %s", raw)
	}
	// Taking the date away brings it back.
	if code, _, _ := h.admin(root, "PATCH", path, map[string]any{"expires_at": 0}); code != 200 {
		t.Fatalf("removing the end date: %d", code)
	}
	if code, _, _ := h.call("POST", "/api/currentUser", tok, nil); code != 200 {
		t.Fatalf("after the date was removed: %d", code)
	}
}

func TestNewAccountWithGroupAndBooks(t *testing.T) {
	h := newHarness(t)
	rootUser := h.user("root", true)
	root := h.consoleLogin("root")
	g := &store.Group{Name: "Field"}
	_ = h.st.SaveGroup(g)
	shared, _ := h.st.CreateAB(rootUser.ID, "Kiosks", "")
	other, _ := h.st.CreateAB(rootUser.ID, "Office", "")
	personal, _ := h.st.PersonalAB(rootUser.ID)

	body := func(shares []map[string]any) map[string]any {
		return map[string]any{"username": "ada", "invite": true, "group_id": g.ID, "shares": shares}
	}
	if code, _, _ := h.admin(root, "POST", "/console/api/users", body([]map[string]any{{"guid": personal, "rule": 1}})); code != 400 {
		t.Fatalf("a personal book cannot be handed out: %d", code)
	}
	if code, _, _ := h.admin(root, "POST", "/console/api/users", body([]map[string]any{{"guid": shared, "rule": 9}})); code != 400 {
		t.Fatalf("unknown access level: %d", code)
	}
	if _, err := h.st.UserByUsername("ada"); err == nil {
		t.Fatal("a refused request must not leave an account behind")
	}
	code, raw, m := h.admin(root, "POST", "/console/api/users", body([]map[string]any{{"guid": shared, "rule": 1}, {"guid": other, "rule": 2}}))
	if code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	ada, _ := h.st.UserByUsername("ada")
	if ada.GroupID != g.ID {
		t.Fatalf("group: %+v", ada)
	}
	if b, _ := h.st.AB(shared, ada.ID); b.Rule != store.RuleRead {
		t.Fatalf("view access: %+v", b)
	}
	if b, _ := h.st.AB(other, ada.ID); b.Rule != store.RuleReadWrite {
		t.Fatalf("edit access: %+v", b)
	}
	// The person sets a password through the link and finds the books in the app.
	secret := secretOf(t, m["invite"].(map[string]any)["url"].(string))
	h.open("/console/api/invite/accept", map[string]string{"token": secret, "password": "a password of my own"})
	_, _, v := h.call("POST", "/api/login", "", map[string]any{"username": "ada", "password": "a password of my own", "id": "1", "uuid": "u", "type": "account"})
	tok := v.(map[string]any)["access_token"].(string)
	if _, raw, _ := h.call("POST", "/api/ab/shared/profiles?current=1&pageSize=100", tok, nil); !strings.Contains(raw, "Kiosks") || !strings.Contains(raw, "Office") {
		t.Fatalf("shared books in the app: %s", raw)
	}
}

func TestLogOfChanges(t *testing.T) {
	h := newHarness(t)
	h.user("root", true)
	h.user("bob", false)
	root := h.consoleLogin("root")
	bob, _ := h.st.UserByUsername("bob")
	path := "/console/api/users/" + strconv.FormatInt(bob.ID, 10)

	_, _, m := h.admin(root, "PUT", "/console/api/groups/new", map[string]any{"name": "Field", "shared": true})
	gid := int64(m["group"].(map[string]any)["id"].(float64))
	h.admin(root, "PATCH", path, map[string]any{"group_id": gid, "disabled": true})
	h.admin(root, "POST", path+"/password", map[string]string{"password": "set by the administrator"})
	_, _, m = h.admin(root, "POST", "/console/api/address-books", map[string]any{"name": "Kiosks"})
	guid := m["guid"].(string)
	h.admin(root, "PUT", "/console/api/address-books/"+guid+"/shares", map[string]any{"shares": []map[string]any{{"group_id": gid, "rule": 1}}})
	h.admin(root, "PUT", "/console/api/settings", map[string]any{"public_url": "https://rd.example.test"})
	h.admin(root, "DELETE", path, nil)

	code, raw, m := h.admin(root, "GET", "/console/api/logs/changes?limit=50", nil)
	if code != 200 {
		t.Fatalf("log: %d %s", code, raw)
	}
	want := map[string]string{ // action -> what the entry must say
		"group.save":      "shared",
		"user.update":     "disabled: yes; group: Field",
		"user.password":   "set by an administrator",
		"book.create":     "",
		"book.shares":     "group Field: view",
		"settings.update": "",
		"user.delete":     "",
	}
	seen := map[string]bool{}
	for _, r := range m["rows"].([]any) {
		row := r.(map[string]any)
		action := row["action"].(string)
		if detail, ok := want[action]; ok {
			if row["actor"] != "root" || row["ip"] == "" || !strings.Contains(row["detail"].(string), detail) {
				t.Fatalf("entry %s: %v", action, row)
			}
			seen[action] = true
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("log lacks entries: have %v in %s", seen, raw)
	}
	if strings.Contains(raw, "set by the administrator") {
		t.Fatal("a password in the log")
	}
	// Only administrators read it, and old entries go with the other logs.
	if code, _, _ := h.call("GET", "/console/api/logs/changes", "", nil, "Cookie", h.consoleLogin2("root", "correct horse")); code != 200 {
		t.Fatalf("administrator: %d", code)
	}
	h.user("eve", false)
	if code, _, _ := h.call("GET", "/console/api/logs/changes", "", nil, "Cookie", h.consoleLogin("eve")); code != 403 {
		t.Fatalf("non-administrator: %d", code)
	}
	if n := h.st.PruneLogs(time.Now().Unix() + 10); n == 0 {
		t.Fatal("pruning must take the log of changes along")
	}
	if _, total, _ := h.st.Changes(10, 0); total != 0 {
		t.Fatalf("after pruning: %d entries", total)
	}
}

// ---- e-mail ----

// fakeMailServer speaks just enough SMTP to take one message at a time, without encryption.
type fakeMailServer struct {
	addr string
	mu   sync.Mutex
	got  []string // "from|to|message"
}

func newFakeMailServer(t *testing.T) *fakeMailServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeMailServer{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeMailServer) serve(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	say("220 fake")
	var from, to string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from = strings.Trim(strings.TrimSpace(line[10:]), "<>")
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to = strings.Trim(strings.TrimSpace(line[8:]), "<>")
			if strings.HasPrefix(to, "refused@") {
				say("550 no such person")
				continue
			}
			say("250 ok")
		case cmd == "DATA":
			say("354 go on")
			var msg strings.Builder
			for {
				l, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				msg.WriteString(l)
			}
			f.mu.Lock()
			f.got = append(f.got, from+"|"+to+"|"+msg.String())
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("500 what")
		}
	}
}

func (f *fakeMailServer) last(t *testing.T) (from, to, headers, body string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("no message arrived")
	}
	parts := strings.SplitN(f.got[len(f.got)-1], "|", 3)
	headers, enc, _ := strings.Cut(parts[2], "\r\n\r\n")
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(enc, "\r\n", ""))
	if err != nil {
		t.Fatalf("body is not base64: %v", err)
	}
	return parts[0], parts[1], headers, string(raw)
}

func TestInviteLinkByMail(t *testing.T) {
	h := newHarness(t)
	h.user("root", true)
	ada := h.user("ada", false) // ada@example.test
	root := h.consoleLogin("root")
	mailbox := newFakeMailServer(t)
	host, port, _ := net.SplitHostPort(mailbox.addr)
	invite := func(u *store.User, body map[string]any) map[string]any {
		code, raw, m := h.admin(root, "POST", "/console/api/users/"+strconv.FormatInt(u.ID, 10)+"/invite", body)
		if code != 200 {
			t.Fatalf("invite: %d %s", code, raw)
		}
		return m
	}
	// Not set up: the link is made all the same, and says why it was not mailed.
	if m := invite(ada, map[string]any{"email": true}); m["emailed"] != false || m["email_error"] == "" || !strings.Contains(m["url"].(string), "/#/invite/") {
		t.Fatalf("without mail settings: %v", m)
	}
	if code, _, _ := h.admin(root, "POST", "/console/api/mail/test", map[string]string{"to": "root@example.test"}); code != 400 {
		t.Fatalf("test message without settings: %d", code)
	}

	save := func(mail map[string]any) int {
		code, _, _ := h.admin(root, "PUT", "/console/api/settings", map[string]any{"mail": mail})
		return code
	}
	if code := save(map[string]any{"host": host, "port": port, "security": "none", "from": "not an address"}); code != 400 {
		t.Fatalf("nonsense sender: %d", code)
	}
	if code := save(map[string]any{"host": "smtp://" + host, "from": "rd@example.test"}); code != 400 {
		t.Fatalf("host with a scheme: %d", code)
	}
	if code := save(map[string]any{"host": host, "port": port, "security": "none", "from": "Remote Desk <rd@example.test>", "user": "mailer", "password": "the mail password"}); code != 200 {
		t.Fatalf("save: %d", code)
	}
	_, raw, m := h.admin(root, "GET", "/console/api/settings", nil)
	if strings.Contains(raw, "the mail password") || m["mail"].(map[string]any)["password_set"] != true || m["mail"].(map[string]any)["ready"] != true {
		t.Fatalf("the settings show that a password is stored, never the password: %s", raw)
	}
	// The stored user name makes the server sign in; this one offers no sign-in, so drop it.
	if code := save(map[string]any{"host": host, "port": port, "security": "none", "from": "Remote Desk <rd@example.test>"}); code != 200 || h.st.Setting(keyMailPass) != "" {
		t.Fatalf("without a user name the password is forgotten: %d", code)
	}

	_ = h.st.SetSetting(keyPublicURL, "https://rd.example.test")
	m = invite(ada, map[string]any{"email": true, "lang": "tr"})
	if m["emailed"] != true || m["email_error"] != nil {
		t.Fatalf("mailed invite: %v", m)
	}
	from, to, headers, body := mailbox.last(t)
	if from != "rd@example.test" || to != "ada@example.test" {
		t.Fatalf("envelope: %q -> %q", from, to)
	}
	if !strings.Contains(headers, "To: <ada@example.test>") || !strings.Contains(headers, `From: "Remote Desk" <rd@example.test>`) || !strings.Contains(headers, "Subject: ") || strings.Contains(headers, "\n\n") {
		t.Fatalf("headers: %q", headers)
	}
	if !strings.Contains(body, m["url"].(string)) || !strings.Contains(body, "Merhaba") || !strings.Contains(body, "rd.example.test") {
		t.Fatalf("the message must carry the link, in the language asked for: %q", body)
	}
	if !strings.HasPrefix(m["url"].(string), "https://rd.example.test/#/invite/") {
		t.Fatalf("link: %v", m["url"])
	}
	if got := h.actions(); !contains(got, "root user.invite ada") {
		t.Fatalf("log: %v", got)
	}
	// The link from the mail works.
	if code, _, _ := h.open("/console/api/invite/check", map[string]string{"token": secretOf(t, m["url"].(string))}); code != 200 {
		t.Fatalf("mailed link: %d", code)
	}
	// A message the server refuses does not lose the link.
	refused := h.user("refused", false)
	if m := invite(refused, map[string]any{"email": true}); m["emailed"] != false || !strings.Contains(m["email_error"].(string), "recipient") || m["url"] == "" {
		t.Fatalf("refused recipient: %v", m)
	}
	// An account without an address cannot be mailed.
	_, _ = h.st.DB().Exec("UPDATE users SET email = '' WHERE id = ?", refused.ID)
	refused, _ = h.st.UserByID(refused.ID)
	if m := invite(refused, map[string]any{"email": true}); m["emailed"] != false || m["email_error"] == nil {
		t.Fatalf("no address: %v", m)
	}
	// The test message.
	if code, raw, _ := h.admin(root, "POST", "/console/api/mail/test", map[string]string{"to": "root@example.test"}); code != 200 {
		t.Fatalf("test message: %d %s", code, raw)
	}
	if _, to, _, body := mailbox.last(t); to != "root@example.test" || !strings.Contains(body, "test message") {
		t.Fatalf("test message: %q %q", to, body)
	}
	if code, _, _ := h.admin(root, "POST", "/console/api/mail/test", map[string]string{"to": "a@b.test\r\nBcc: x@y.test"}); code != 502 {
		t.Fatalf("an address with a line break must be refused: %d", code)
	}
	// Switching it off.
	if code := save(map[string]any{"host": ""}); code != 200 {
		t.Fatalf("switch off: %d", code)
	}
	if _, ok := h.app.mailConfig(); ok {
		t.Fatal("mail must be off")
	}
}
