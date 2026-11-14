package store

import (
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// User is an account that can sign in from the RustDesk client and the console.
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	Disabled    bool   `json:"disabled"`
	Note        string `json:"note"`
	CreatedAt   int64  `json:"created_at"`
	HasPassword bool   `json:"has_password"`
}

const userCols = "id, username, email, display_name, is_admin, disabled, note, created_at, password_hash <> ''"

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := sc.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin, &u.Disabled, &u.Note, &u.CreatedAt, &u.HasPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equaliser"), bcrypt.DefaultCost)

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// CreateUser inserts a user. password may be empty (SSO-only account);
// passwordHash, when set, is stored as is (used by the importer).
func (s *Store) CreateUser(u *User, password, passwordHash string) error {
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" {
		return errors.New("username is required")
	}
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		passwordHash = h
	}
	res, err := s.db.Exec(`INSERT INTO users(username, email, display_name, password_hash, is_admin, disabled, note, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, u.Username, u.Email, u.DisplayName, passwordHash, u.IsAdmin, u.Disabled, u.Note, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("username already exists")
		}
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

// UserByID loads a user.
func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", id))
}

// UserByUsername loads a user by username or e-mail address.
func (s *Store) UserByUsername(name string) (*User, error) {
	name = strings.TrimSpace(name)
	u, err := scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE username = ?", name))
	if errors.Is(err, ErrNotFound) && strings.Contains(name, "@") {
		return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE email = ? COLLATE NOCASE ORDER BY id LIMIT 1", name))
	}
	return u, err
}

// CheckPassword verifies a password for username and returns the user.
// The same error is returned for unknown users and wrong passwords.
func (s *Store) CheckPassword(username, password string) (*User, error) {
	bad := errors.New("invalid username or password")
	u, err := s.UserByUsername(username)
	if err != nil {
		// Spend comparable time so that unknown users are not distinguishable.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, bad
	}
	var hash string
	if err := s.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", u.ID).Scan(&hash); err != nil {
		return nil, bad
	}
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, bad
	}
	if u.Disabled {
		return nil, errors.New("account is disabled")
	}
	return u, nil
}

// ListUsers returns all users ordered by name.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query("SELECT " + userCols + " FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of users.
func (s *Store) CountUsers() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n
}

// CountAdmins returns the number of enabled administrators.
func (s *Store) CountAdmins() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin = 1 AND disabled = 0").Scan(&n)
	return n
}

// UpdateUser saves the editable profile fields.
func (s *Store) UpdateUser(u *User) error {
	_, err := s.db.Exec(`UPDATE users SET email = ?, display_name = ?, is_admin = ?, disabled = ?, note = ? WHERE id = ?`,
		u.Email, u.DisplayName, u.IsAdmin, u.Disabled, u.Note, u.ID)
	if err == nil && u.Disabled {
		_, err = s.db.Exec("DELETE FROM tokens WHERE user_id = ?", u.ID)
	}
	return err
}

// SetPassword replaces a user's password and signs out all their sessions
// except the one identified by keepToken (may be empty).
func (s *Store) SetPassword(userID int64, password, keepToken string) error {
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", h, userID); err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM tokens WHERE user_id = ? AND hash <> ?", userID, hashToken(keepToken))
	return err
}

// DeleteUser removes a user and everything they own.
func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// LinkIdentity ties an external (OIDC) subject to a user.
func (s *Store) LinkIdentity(provider, subject string, userID int64, email string) error {
	_, err := s.db.Exec(`INSERT INTO identities(provider, subject, user_id, email) VALUES(?, ?, ?, ?)
		ON CONFLICT(provider, subject) DO UPDATE SET user_id = excluded.user_id, email = excluded.email`, provider, subject, userID, email)
	return err
}

// UserByIdentity resolves an external subject to a user.
func (s *Store) UserByIdentity(provider, subject string) (*User, error) {
	var id int64
	err := s.db.QueryRow("SELECT user_id FROM identities WHERE provider = ? AND subject = ?", provider, subject).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.UserByID(id)
}

// IdentityProviders lists the providers linked to a user.
func (s *Store) IdentityProviders(userID int64) []string {
	rows, err := s.db.Query("SELECT provider FROM identities WHERE user_id = ? ORDER BY provider", userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// Token is a signed-in session of a RustDesk client or the web console.
type Token struct {
	ID         string `json:"id"` // first 12 hex chars of the hash; enough to address a row
	Name       string `json:"name"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username,omitempty"`
	Kind       string `json:"kind"`
	DeviceID   string `json:"device_id"`
	DeviceUUID string `json:"-"`
	DeviceName string `json:"device_name"`
	DeviceOS   string `json:"device_os"`
	IP         string `json:"ip"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

// NewToken creates a session and returns the secret token (stored only as a hash).
func (s *Store) NewToken(t *Token, ttlSeconds int64) (string, error) {
	plain := RandomToken(32)
	n := now()
	var exp int64
	if ttlSeconds > 0 {
		exp = n + ttlSeconds
	}
	_, err := s.db.Exec(`INSERT INTO tokens(hash, user_id, kind, name, device_id, device_uuid, device_name, device_os, ip, created_at, last_used_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, hashToken(plain), t.UserID, t.Kind, t.Name, t.DeviceID, t.DeviceUUID, t.DeviceName, t.DeviceOS, t.IP, n, n, exp)
	return plain, err
}

// UserByToken resolves a secret token to its user and session.
func (s *Store) UserByToken(plain string) (*User, *Token, error) {
	if plain == "" {
		return nil, nil, ErrNotFound
	}
	h := hashToken(plain)
	t := &Token{ID: h[:12]}
	err := s.db.QueryRow(`SELECT user_id, kind, device_id, device_uuid, device_name, device_os, ip, created_at, last_used_at, expires_at
		FROM tokens WHERE hash = ?`, h).Scan(&t.UserID, &t.Kind, &t.DeviceID, &t.DeviceUUID, &t.DeviceName, &t.DeviceOS, &t.IP, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	n := now()
	if t.ExpiresAt > 0 && t.ExpiresAt < n {
		_, _ = s.db.Exec("DELETE FROM tokens WHERE hash = ?", h)
		return nil, nil, ErrNotFound
	}
	u, err := s.UserByID(t.UserID)
	if err != nil || u.Disabled {
		return nil, nil, ErrNotFound
	}
	if n-t.LastUsedAt > 60 {
		_, _ = s.db.Exec("UPDATE tokens SET last_used_at = ? WHERE hash = ?", n, h)
	}
	return u, t, nil
}

// DeleteToken signs out one session by its secret.
func (s *Store) DeleteToken(plain string) {
	_, _ = s.db.Exec("DELETE FROM tokens WHERE hash = ?", hashToken(plain))
}

// DeleteTokenByID signs out one session by its public id; userID 0 means any user.
func (s *Store) DeleteTokenByID(id string, userID int64) error {
	if len(id) < 12 {
		return ErrNotFound
	}
	_, err := s.db.Exec("DELETE FROM tokens WHERE substr(hash, 1, 12) = ? AND (? = 0 OR user_id = ?)", id[:12], userID, userID)
	return err
}

// ListTokens lists sessions; userID 0 lists everyone's.
func (s *Store) ListTokens(userID int64) ([]*Token, error) {
	rows, err := s.db.Query(`SELECT substr(t.hash, 1, 12), t.name, t.user_id, u.username, t.kind, t.device_id, t.device_name, t.device_os, t.ip, t.created_at, t.last_used_at, t.expires_at
		FROM tokens t JOIN users u ON u.id = t.user_id WHERE (? = 0 OR t.user_id = ?) ORDER BY t.last_used_at DESC`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Token{}
	for rows.Next() {
		t := &Token{}
		if err := rows.Scan(&t.ID, &t.Name, &t.UserID, &t.Username, &t.Kind, &t.DeviceID, &t.DeviceName, &t.DeviceOS, &t.IP, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ImportToken registers an existing secret token (migration from another
// server, so that signed-in clients stay signed in). Existing rows are kept.
func (s *Store) ImportToken(plain string, t *Token) error {
	if plain == "" {
		return nil
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = now()
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO tokens(hash, user_id, kind, device_id, device_uuid, device_name, device_os, ip, created_at, last_used_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, hashToken(plain), t.UserID, t.Kind, t.DeviceID, t.DeviceUUID, t.DeviceName, t.DeviceOS, t.IP, t.CreatedAt, t.CreatedAt, t.ExpiresAt)
	return err
}
