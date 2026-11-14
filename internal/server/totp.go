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

// totpValid accepts the current code and its two neighbours (clock drift).
func totpValid(secret, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	ok := false
	for _, step := range []int{0, -1, 1} {
		if c, valid := totpAt(secret, time.Now().Add(time.Duration(step)*30*time.Second)); valid && subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok
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
	if !totpValid(in.Secret, in.Code) {
		c.fail(http.StatusBadRequest, "that code does not match; check the clock of your phone and try again")
		return
	}
	if c.err(s.st.SetTOTP(c.user.ID, strings.ToUpper(strings.ReplaceAll(in.Secret, " ", "")))) {
		return
	}
	c.done()
}

func (s *Server) cTOTPDisable(c *consoleCtx) {
	var in struct{ Code string }
	if !c.body(&in) {
		return
	}
	if secret := s.st.TOTPSecret(c.user.ID); secret != "" && !totpValid(secret, in.Code) {
		c.fail(http.StatusForbidden, "that code is not right")
		return
	}
	if c.err(s.st.SetTOTP(c.user.ID, "")) {
		return
	}
	c.done()
}
