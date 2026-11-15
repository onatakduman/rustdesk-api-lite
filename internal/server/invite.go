package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// An invite link lets a person set the password of an account, or link their
// provider sign-in to it, without the administrator ever knowing a password.
// The same link serves as a reset. The administrator passes it on over a
// channel of their choice, or the server mails it. The secret sits in the
// fragment of the address, which browsers do not send to servers or log.

const (
	inviteDefaultHours = 72
	inviteMaxHours     = 24 * 30
	// A link into an administrator's account opens everything, so it is short-lived.
	inviteAdminMaxHours = 24
)

// InviteURL builds the address of an invite link.
func InviteURL(publicURL, secret string) string {
	return strings.TrimRight(publicURL, "/") + "/#/invite/" + secret
}

// InviteHours brings a requested validity into the allowed range for the account.
func InviteHours(u *store.User, hours int) int {
	if hours <= 0 {
		hours = inviteDefaultHours
	}
	if hours > inviteMaxHours {
		hours = inviteMaxHours
	}
	if u.IsAdmin && hours > inviteAdminMaxHours {
		hours = inviteAdminMaxHours
	}
	return hours
}

// change records who changed what, for the log of changes.
func (s *Server) change(c *consoleCtx, action, target, detail string) {
	s.st.AddChange(&store.Change{Actor: c.user.Username, Action: action, Target: target, Detail: detail, IP: s.clientIP(c.r)})
}

type inviteResult struct {
	URL        string `json:"url"`
	ExpiresAt  int64  `json:"expires_at"`
	Emailed    bool   `json:"emailed"`
	EmailError string `json:"email_error,omitempty"`
}

// newInvite makes a link for u and, when asked, mails it. A link that could
// not be mailed is still valid and is returned for passing on by hand.
func (s *Server) newInvite(c *consoleCtx, u *store.User, hours int, mail bool, lang string) (*inviteResult, error) {
	hours = InviteHours(u, hours)
	secret, expires, err := s.st.NewInvite(u.ID, c.user.Username, int64(hours)*3600)
	if err != nil {
		return nil, err
	}
	res := &inviteResult{URL: InviteURL(s.publicURL(c.r), secret), ExpiresAt: expires}
	detail := "valid " + strconv.Itoa(hours) + " h"
	if mail {
		switch cfg, ok := s.mailConfig(); {
		case !ok:
			res.EmailError = "sending e-mail is not set up"
		case u.Email == "":
			res.EmailError = "the account has no e-mail address"
		default:
			subject, body := inviteMail(lang, s.serverName(c.r), u, res.URL, expires)
			if err := sendMail(cfg, u.Email, subject, body); err != nil {
				res.EmailError = err.Error()
			} else {
				res.Emailed = true
				detail += ", sent to " + u.Email
			}
		}
	}
	s.change(c, "user.invite", u.Username, detail)
	return res, nil
}

// serverName is how mails and pages call this server: the host of its public address.
func (s *Server) serverName(r *http.Request) string {
	if u, err := url.Parse(s.publicURL(r)); err == nil && u.Host != "" {
		return u.Host
	}
	return r.Host
}

func inviteMail(lang, server string, u *store.User, link string, expires int64) (subject, body string) {
	until := time.Unix(expires, 0).UTC().Format("2006-01-02 15:04") + " UTC"
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	if lang == "tr" {
		return server + " hesabın",
			fmt.Sprintf("Merhaba %s,\n\n%s üzerindeki %s hesabı için bağlantın burada. Açıp kendi parolanı belirle:\n\n%s\n\nBağlantı bir kez ve %s tarihine kadar çalışır. Bu iletiyi beklemiyorsan yok say.\n",
				name, server, u.Username, link, until)
	}
	return "Your account on " + server,
		fmt.Sprintf("Hello %s,\n\nhere is your link for the account %s on %s. Open it and choose a password of your own:\n\n%s\n\nThe link works once and until %s. If you did not expect this message, ignore it.\n",
			name, u.Username, server, link, until)
}

// cUserInvite makes an invite link for an account.
func (s *Server) cUserInvite(c *consoleCtx) {
	u := s.pathUser(c)
	if u == nil {
		return
	}
	var in struct {
		Hours int
		Email bool
		Lang  string
	}
	if !c.body(&in) {
		return
	}
	if !u.Active() {
		c.fail(http.StatusBadRequest, "this account is disabled or has ended")
		return
	}
	res, err := s.newInvite(c, u, in.Hours, in.Email, in.Lang)
	if c.err(err) {
		return
	}
	c.ok(res)
}

// cUserInviteDelete withdraws the link of an account.
func (s *Server) cUserInviteDelete(c *consoleCtx) {
	u := s.pathUser(c)
	if u == nil {
		return
	}
	if c.err(s.st.DeleteInvites(u.ID)) {
		return
	}
	s.change(c, "user.invite.withdraw", u.Username, "")
	c.done()
}

// invited resolves the link secret of a request to its account, or answers
// the error. Wrong secrets count against the caller's address like wrong passwords.
func (s *Server) invited(w http.ResponseWriter, r *http.Request, secret string) (*store.User, string, bool) {
	if !s.sameOrigin(r) {
		fail(w, http.StatusForbidden, "cross-site request refused")
		return nil, "", false
	}
	ipKey := "ip:" + s.clientIP(r)
	if s.fails.blocked(ipKey) {
		fail(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return nil, "", false
	}
	hash := store.InviteHash(secret)
	u, err := s.st.InviteUser(hash)
	if secret == "" || err != nil {
		s.fails.fail(ipKey)
		fail(w, http.StatusNotFound, "this link does not work any more; ask for a new one")
		return nil, "", false
	}
	return u, hash, true
}

// handleInviteCheck tells the page behind a link which account it is for.
func (s *Server) handleInviteCheck(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	u, _, ok := s.invited(w, r, in.Token)
	if !ok {
		return
	}
	names := []string{}
	if ps, err := s.st.OIDCProviders(); err == nil {
		for _, p := range ps {
			names = append(names, p.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "display_name": u.DisplayName, "has_password": u.HasPassword, "providers": names})
}

// handleInviteAccept sets the password the person chose. The link is used up.
func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	u, _, ok := s.invited(w, r, in.Token)
	if !ok {
		return
	}
	if len(in.Password) < 8 {
		fail(w, http.StatusBadRequest, "the password needs at least 8 characters")
		return
	}
	// Setting the password also signs out whatever was signed in to the account.
	if err := s.st.SetPassword(u.ID, in.Password, ""); err != nil {
		fail(w, http.StatusInternalServerError, "could not save the password")
		return
	}
	_ = s.st.DeleteInvites(u.ID)
	s.st.AddChange(&store.Change{Actor: u.Username, Action: "user.invite.used", Target: u.Username, Detail: "password set", IP: s.clientIP(r)})
	if u.HasTOTP {
		// The link replaces the password, not the second step: sign in as usual.
		writeJSON(w, http.StatusOK, map[string]any{"sign_in": true})
		return
	}
	s.st.AddLoginLog(&store.LoginLog{UserID: u.ID, Username: u.Username, Kind: "console", Method: "invite link", DeviceName: "Web console", IP: s.clientIP(r), OK: true})
	if err := s.newConsoleSession(w, r, u); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sign_in": true})
		return
	}
	u, _ = s.st.UserByID(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": userView{u, s.st.IdentityProviders(u.ID)}})
}

// handleInviteProvider starts a provider sign-in that ends with the provider
// identity linked to the invited account. The link itself is the proof that
// the person may have the account, so this also works for administrators.
func (s *Server) handleInviteProvider(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Provider string }
	if err := readJSON(w, r, &in, bodySmall); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	_, hash, ok := s.invited(w, r, in.Token)
	if !ok {
		return
	}
	sess := &store.OIDCSession{Provider: in.Provider, Purpose: "invite:" + hash}
	if _, err := s.st.OIDCProvider(sess.Provider); err != nil {
		fail(w, http.StatusNotFound, "this sign-in provider is not configured")
		return
	}
	if err := s.st.NewOIDCSession(sess); err != nil {
		fail(w, http.StatusInternalServerError, "could not start the sign-in")
		return
	}
	to, err := s.oidcURL(r, sess)
	if err != nil {
		fail(w, http.StatusBadGateway, "the sign-in provider could not be reached")
		return
	}
	s.setOIDCCookie(w, r, sess.State)
	writeJSON(w, http.StatusOK, map[string]string{"url": to})
}

// finishInvite ends a provider sign-in that was started from an invite link:
// the identity is linked to the invited account and the link is used up.
func (s *Server) finishInvite(w http.ResponseWriter, r *http.Request, sess *store.OIDCSession, p *store.OIDCProvider, c *oidcClaims) {
	s.st.DeleteOIDCSession(sess.Code)
	u, err := s.st.InviteUser(strings.TrimPrefix(sess.Purpose, "invite:"))
	if err != nil {
		oidcPage(w, http.StatusForbidden, "Link not valid", "This link does not work any more. Ask for a new one.")
		return
	}
	if other, err := s.st.UserByIdentity(p.Name, c.Sub); err == nil && other.ID != u.ID {
		oidcPage(w, http.StatusConflict, "Sign-in not linked", "This sign-in already belongs to another account.")
		return
	}
	if err := s.st.LinkIdentity(p.Name, c.Sub, u.ID, c.Email); err != nil {
		oidcPage(w, http.StatusInternalServerError, "Sign-in not linked", "Could not save the link.")
		return
	}
	_ = s.st.DeleteInvites(u.ID)
	s.st.AddChange(&store.Change{Actor: u.Username, Action: "user.invite.used", Target: u.Username, Detail: "linked " + p.Name, IP: s.clientIP(r)})
	s.st.AddLoginLog(&store.LoginLog{UserID: u.ID, Username: u.Username, Kind: "console", Method: p.Name, DeviceName: "Web console", IP: s.clientIP(r), OK: true})
	if err := s.newConsoleSession(w, r, u); err != nil {
		oidcPage(w, http.StatusInternalServerError, "Sign-in failed", "Could not create a session.")
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// cLogChanges lists the log of changes.
func (s *Server) cLogChanges(c *consoleCtx) {
	limit, offset := page(c.r)
	rows, total, err := s.st.Changes(limit, offset)
	if c.err(err) {
		return
	}
	c.ok(map[string]any{"total": total, "rows": rows})
}

// ruleName names an address-book access level for the log.
func ruleName(rule int) string {
	switch rule {
	case store.RuleRead:
		return "view"
	case store.RuleReadWrite:
		return "edit"
	case store.RuleFull:
		return "full"
	}
	return "none"
}
