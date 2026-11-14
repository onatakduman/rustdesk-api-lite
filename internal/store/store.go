// Package store is the SQLite persistence layer.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite has a single writer; a small pool keeps readers from blocking.
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for the importer and tests.
func (s *Store) DB() *sql.DB { return s.db }

var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  email         TEXT NOT NULL DEFAULT '',
  display_name  TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  is_admin      INTEGER NOT NULL DEFAULT 0,
  disabled      INTEGER NOT NULL DEFAULT 0,
  note          TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE TABLE identities (
  provider TEXT NOT NULL,
  subject  TEXT NOT NULL,
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, subject)
);
CREATE TABLE tokens (
  hash         TEXT PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  device_id    TEXT NOT NULL DEFAULT '',
  device_uuid  TEXT NOT NULL DEFAULT '',
  device_name  TEXT NOT NULL DEFAULT '',
  device_os    TEXT NOT NULL DEFAULT '',
  ip           TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX tokens_user ON tokens(user_id);
CREATE TABLE address_books (
  guid       TEXT PRIMARY KEY,
  owner_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  personal   INTEGER NOT NULL DEFAULT 0,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX address_books_personal ON address_books(owner_id) WHERE personal = 1;
CREATE TABLE ab_shares (
  ab_guid TEXT NOT NULL REFERENCES address_books(guid) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  rule    INTEGER NOT NULL,
  PRIMARY KEY (ab_guid, user_id)
);
CREATE TABLE ab_peers (
  ab_guid    TEXT NOT NULL REFERENCES address_books(guid) ON DELETE CASCADE,
  id         TEXT NOT NULL,
  data       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (ab_guid, id)
);
CREATE TABLE ab_tags (
  ab_guid TEXT NOT NULL REFERENCES address_books(guid) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  color   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ab_guid, name)
);
CREATE TABLE devices (
  id         TEXT PRIMARY KEY,
  uuid       TEXT NOT NULL DEFAULT '',
  hostname   TEXT NOT NULL DEFAULT '',
  username   TEXT NOT NULL DEFAULT '',
  os         TEXT NOT NULL DEFAULT '',
  cpu        TEXT NOT NULL DEFAULT '',
  memory     TEXT NOT NULL DEFAULT '',
  version    TEXT NOT NULL DEFAULT '',
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  note       TEXT NOT NULL DEFAULT '',
  last_ip    TEXT NOT NULL DEFAULT '',
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL
);
CREATE TABLE audit_conn (
  rowid_     INTEGER PRIMARY KEY,
  device_id  TEXT NOT NULL,
  conn_id    INTEGER NOT NULL DEFAULT 0,
  session_id TEXT NOT NULL DEFAULT '',
  peer_id    TEXT NOT NULL DEFAULT '',
  peer_name  TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT '',
  conn_type  INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  ended_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX audit_conn_lookup ON audit_conn(device_id, conn_id);
CREATE TABLE audit_file (
  rowid_    INTEGER PRIMARY KEY,
  device_id TEXT NOT NULL,
  peer_id   TEXT NOT NULL DEFAULT '',
  direction INTEGER NOT NULL DEFAULT 0,
  path      TEXT NOT NULL DEFAULT '',
  is_file   INTEGER NOT NULL DEFAULT 0,
  info      TEXT NOT NULL DEFAULT '',
  at        INTEGER NOT NULL
);
CREATE TABLE oidc_providers (
  name          TEXT PRIMARY KEY,
  issuer        TEXT NOT NULL,
  client_id     TEXT NOT NULL,
  client_secret TEXT NOT NULL,
  scopes        TEXT NOT NULL DEFAULT 'openid email profile',
  auto_register INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE oidc_sessions (
  code        TEXT PRIMARY KEY,
  state       TEXT NOT NULL UNIQUE,
  provider    TEXT NOT NULL,
  purpose     TEXT NOT NULL,
  device_id   TEXT NOT NULL DEFAULT '',
  device_uuid TEXT NOT NULL DEFAULT '',
  device_name TEXT NOT NULL DEFAULT '',
  device_os   TEXT NOT NULL DEFAULT '',
  result      TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`,
	// 2: API tokens, sign-in log, alarms, session notes, device groups and live connections
	`
ALTER TABLE tokens ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE address_books ADD COLUMN default_password TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN conns TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_conn ADD COLUMN note TEXT NOT NULL DEFAULT '';
CREATE TABLE login_log (
  rowid_      INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL DEFAULT 0,
  username    TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT '',
  method      TEXT NOT NULL DEFAULT '',
  device_id   TEXT NOT NULL DEFAULT '',
  device_name TEXT NOT NULL DEFAULT '',
  ip          TEXT NOT NULL DEFAULT '',
  ok          INTEGER NOT NULL DEFAULT 1,
  at          INTEGER NOT NULL
);
CREATE TABLE alarms (
  rowid_    INTEGER PRIMARY KEY,
  device_id TEXT NOT NULL,
  typ       INTEGER NOT NULL DEFAULT 0,
  info      TEXT NOT NULL DEFAULT '',
  at        INTEGER NOT NULL
);
`,
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() int64 { return time.Now().Unix() }

// RandomToken returns n random bytes, URL-safe base64 encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewGUID returns a random UUIDv4-shaped identifier.
func NewGUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// Setting returns a settings value ("" when unset).
func (s *Store) Setting(key string) string {
	var v string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	return v
}

// SetSetting stores a settings value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}
