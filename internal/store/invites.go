package store

import (
	"database/sql"
	"errors"
)

// An invite is a one-time link with which a person sets the password of an
// account, or links their provider sign-in to it, without the administrator
// ever knowing a password. Only the hash of the link's secret is stored.

// NewInvite makes the link secret for a user, valid for ttl seconds. Earlier
// links of the same user stop working.
func (s *Store) NewInvite(userID int64, createdBy string, ttl int64) (secret string, expiresAt int64, err error) {
	secret, expiresAt = RandomToken(32), now()+ttl
	tx, err := s.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM invites WHERE user_id = ? OR expires_at < ?", userID, now()); err != nil {
		return "", 0, err
	}
	if _, err = tx.Exec("INSERT INTO invites(hash, user_id, created_by, created_at, expires_at) VALUES(?, ?, ?, ?, ?)", hashToken(secret), userID, createdBy, now(), expiresAt); err != nil {
		return "", 0, err
	}
	return secret, expiresAt, tx.Commit()
}

// InviteHash is what is stored for a link secret.
func InviteHash(secret string) string { return hashToken(secret) }

// InviteUser returns the account a link belongs to, by the hash of its
// secret. Links that have run out, and links of accounts that may not be
// used, are not found.
func (s *Store) InviteUser(hash string) (*User, error) {
	var id, expires int64
	err := s.db.QueryRow("SELECT user_id, expires_at FROM invites WHERE hash = ?", hash).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && expires < now()) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u, err := s.UserByID(id)
	if err != nil || !u.Active() {
		return nil, ErrNotFound
	}
	return u, nil
}

// DeleteInvites withdraws the links of a user (used ones go the same way).
func (s *Store) DeleteInvites(userID int64) error {
	_, err := s.db.Exec("DELETE FROM invites WHERE user_id = ?", userID)
	return err
}

// PendingInvites maps users that have an unused link to the time it runs out.
func (s *Store) PendingInvites() map[int64]int64 {
	out := map[int64]int64{}
	rows, err := s.db.Query("SELECT user_id, expires_at FROM invites WHERE expires_at >= ?", now())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, exp int64
		if rows.Scan(&id, &exp) == nil {
			out[id] = exp
		}
	}
	return out
}

// Change is one entry of the log of changes: who did what to the accounts,
// address books and settings of the server.
type Change struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	IP     string `json:"ip"`
}

// AddChange records a change.
func (s *Store) AddChange(c *Change) {
	_, _ = s.db.Exec("INSERT INTO change_log(at, actor, action, target, detail, ip) VALUES(?, ?, ?, ?, ?, ?)", now(), c.Actor, c.Action, c.Target, c.Detail, c.IP)
}

// Changes returns the most recent changes plus the total count.
func (s *Store) Changes(limit, offset int) ([]*Change, int, error) {
	var total int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM change_log").Scan(&total)
	rows, err := s.db.Query("SELECT rowid_, at, actor, action, target, detail, ip FROM change_log ORDER BY rowid_ DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Change{}
	for rows.Next() {
		c := &Change{}
		if err := rows.Scan(&c.ID, &c.At, &c.Actor, &c.Action, &c.Target, &c.Detail, &c.IP); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// AddShare gives one user a rule on a shared address book, leaving the other shares alone.
func (s *Store) AddShare(guid string, userID int64, rule int) error {
	if rule < RuleRead || rule > RuleFull {
		return errors.New("unknown access level")
	}
	var owner int64
	var personal bool
	if err := s.db.QueryRow("SELECT owner_id, personal FROM address_books WHERE guid = ?", guid).Scan(&owner, &personal); err != nil {
		return ErrNotFound
	}
	if personal {
		return errors.New("a personal address book cannot be shared")
	}
	if userID == owner {
		return nil
	}
	_, err := s.db.Exec("INSERT OR REPLACE INTO ab_shares(ab_guid, user_id, rule) VALUES(?, ?, ?)", guid, userID, rule)
	return err
}

// GroupName returns the name of a user group ("" when there is none).
func (s *Store) GroupName(id int64) string {
	var name string
	_ = s.db.QueryRow("SELECT name FROM user_groups WHERE id = ?", id).Scan(&name)
	return name
}
