package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh 160-bit authenticator secret in base32.
func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// totpAt computes the RFC 6238 code (SHA-1, 30 s, 6 digits) for a moment.
func totpAt(secret string, at time.Time) (string, bool) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil || len(key) == 0 {
		return "", false
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000), true
}

// totpStep finds the time step a code belongs to: the current one or one of its
// two neighbours (clock drift). ok is false when the code fits none of them.
func totpStep(secret, code string) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	now := time.Now()
	for _, d := range []int{0, -1, 1} {
		at := now.Add(time.Duration(d) * 30 * time.Second)
		if c, valid := totpAt(secret, at); valid && subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			step, ok = at.Unix()/30, true
		}
	}
	return step, ok
}

// totpValid tells whether a code fits the secret right now.
func totpValid(secret, code string) bool {
	_, ok := totpStep(secret, code)
	return ok
}

// totpAccept checks a code of the user's authenticator and uses it up: the
// same code, or an older one, is refused from then on.
func (s *Server) totpAccept(userID int64, code string) bool {
	step, ok := totpStep(s.st.TOTPSecret(userID), code)
	return ok && s.st.UseTOTPStep(userID, step)
}

func totpURI(username, secret string) string {
	label := url.PathEscape("RustDesk API Lite:" + username)
	return "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + url.QueryEscape("RustDesk API Lite")
}

// challenges are sign-ins that passed the password and wait for the code.
type challenges struct {
	mu sync.Mutex
	m  map[string]*challenge
}

type challenge struct {
	userID  int64
	expires time.Time
	fails   int
}

func (c *challenges) create(userID int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	id := store.RandomToken(24)
	c.m[id] = &challenge{userID: userID, expires: now.Add(5 * time.Minute)}
	return id
}

// user returns the account a challenge belongs to (0 when unknown or expired).
func (c *challenges) user(id string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.m[id]
	if ch == nil || time.Now().After(ch.expires) {
		delete(c.m, id)
		return 0
	}
	return ch.userID
}

// settle ends a challenge after a right code, or counts a wrong one (five allowed).
func (c *challenges) settle(id string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.m[id]
	if ch == nil {
		return
	}
	if ch.fails++; ok || ch.fails >= 5 {
		delete(c.m, id)
	}
}

// ---- console: turning two-step sign-in on and off ----

func (s *Server) cTOTPNew(c *consoleCtx) {
	secret := newTOTPSecret()
	c.ok(map[string]string{"secret": secret, "uri": totpURI(c.user.Username, secret)})
}

func (s *Server) cTOTPEnable(c *consoleCtx) {
	var in struct{ Secret, Code string }
	if !c.body(&in) {
		return
	}
	step, ok := totpStep(in.Secret, in.Code)
	if !ok {
		c.fail(http.StatusBadRequest, "that code does not match; check the clock of your phone and try again")
		return
	}
	if c.err(s.st.SetTOTP(c.user.ID, strings.ToUpper(strings.ReplaceAll(in.Secret, " ", "")))) {
		return
	}
	s.st.UseTOTPStep(c.user.ID, step) // the code that turned it on is used up as well
	s.change(c, "user.totp", c.user.Username, "on")
	c.done()
}

func (s *Server) cTOTPDisable(c *consoleCtx) {
	var in struct{ Code string }
	if !c.body(&in) {
		return
	}
	if s.st.TOTPSecret(c.user.ID) != "" && !s.totpAccept(c.user.ID, in.Code) {
		c.fail(http.StatusForbidden, "that code is not right")
		return
	}
	if c.err(s.st.SetTOTP(c.user.ID, "")) {
		return
	}
	s.change(c, "user.totp", c.user.Username, "off")
	c.done()
}
