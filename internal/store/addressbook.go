package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Share rules, as understood by the RustDesk client.
const (
	RuleNone      = 0
	RuleRead      = 1
	RuleReadWrite = 2
	RuleFull      = 3
)

// AddressBook is a personal or shared list of peers.
type AddressBook struct {
	GUID      string `json:"guid"`
	OwnerID   int64  `json:"owner_id"`
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	Personal  bool   `json:"personal"`
	Note      string `json:"note"`
	Rule      int    `json:"rule"`
	PeerCount int    `json:"peer_count"`
	CreatedAt int64  `json:"created_at"`
}

// Peer is one address-book entry. It is kept as the JSON object the client
// sent, so fields added by newer clients survive a round trip.
type Peer = map[string]any

// Tag is a coloured label inside one address book.
type Tag struct {
	Name  string `json:"name"`
	Color int64  `json:"color"`
}

// Share grants a user access to someone else's address book.
type Share struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Rule     int    `json:"rule"`
}

// PersonalAB returns the guid of the user's personal address book, creating it on first use.
func (s *Store) PersonalAB(userID int64) (string, error) {
	var guid string
	err := s.db.QueryRow("SELECT guid FROM address_books WHERE owner_id = ? AND personal = 1", userID).Scan(&guid)
	if err == nil {
		return guid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	guid = NewGUID()
	_, err = s.db.Exec("INSERT INTO address_books(guid, owner_id, name, personal, created_at) VALUES(?, ?, 'My address book', 1, ?)", guid, userID, now())
	if err != nil {
		// Lost a race with a concurrent request: read what the other one created.
		if e := s.db.QueryRow("SELECT guid FROM address_books WHERE owner_id = ? AND personal = 1", userID).Scan(&guid); e == nil {
			return guid, nil
		}
		return "", err
	}
	return guid, nil
}

const abSelect = `SELECT b.guid, b.owner_id, u.username, b.name, b.personal, b.note, b.created_at,
	(SELECT COUNT(*) FROM ab_peers p WHERE p.ab_guid = b.guid),
	CASE WHEN b.owner_id = ?1 THEN 3 ELSE COALESCE((SELECT rule FROM ab_shares sh WHERE sh.ab_guid = b.guid AND sh.user_id = ?1), 0) END
	FROM address_books b JOIN users u ON u.id = b.owner_id`

func scanAB(sc interface{ Scan(...any) error }) (*AddressBook, error) {
	b := &AddressBook{}
	err := sc.Scan(&b.GUID, &b.OwnerID, &b.Owner, &b.Name, &b.Personal, &b.Note, &b.CreatedAt, &b.PeerCount, &b.Rule)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) queryABs(q string, args ...any) ([]*AddressBook, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AddressBook{}
	for rows.Next() {
		b, err := scanAB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ABsFor lists the address books a user can see: personal first, then the
// shared ones they own, then the ones shared with them.
func (s *Store) ABsFor(userID int64) ([]*AddressBook, error) {
	if _, err := s.PersonalAB(userID); err != nil {
		return nil, err
	}
	return s.queryABs(abSelect+` WHERE b.owner_id = ?1 OR EXISTS (SELECT 1 FROM ab_shares sh WHERE sh.ab_guid = b.guid AND sh.user_id = ?1)
		ORDER BY b.personal DESC, (b.owner_id = ?1) DESC, b.name COLLATE NOCASE`, userID)
}

// AllABs lists every address book (console, administrators). Rule is relative to userID.
func (s *Store) AllABs(userID int64) ([]*AddressBook, error) {
	return s.queryABs(abSelect+` ORDER BY (b.owner_id = ?1) DESC, u.username, b.personal DESC, b.name COLLATE NOCASE`, userID)
}

// AB loads one address book; Rule is relative to userID.
func (s *Store) AB(guid string, userID int64) (*AddressBook, error) {
	return scanAB(s.db.QueryRow(abSelect+" WHERE b.guid = ?2", userID, guid))
}

// CreateAB creates a shared (non-personal) address book.
func (s *Store) CreateAB(ownerID int64, name, note string) (string, error) {
	if name == "" {
		return "", errors.New("name is required")
	}
	guid := NewGUID()
	_, err := s.db.Exec("INSERT INTO address_books(guid, owner_id, name, personal, note, created_at) VALUES(?, ?, ?, 0, ?, ?)", guid, ownerID, name, note, now())
	return guid, err
}

// UpdateAB renames an address book or changes its note.
func (s *Store) UpdateAB(guid, name, note string) error {
	if name == "" {
		return errors.New("name is required")
	}
	_, err := s.db.Exec("UPDATE address_books SET name = ?, note = ? WHERE guid = ?", name, note, guid)
	return err
}

// DeleteAB removes a shared address book. Personal books are removed with their owner only.
func (s *Store) DeleteAB(guid string) error {
	res, err := s.db.Exec("DELETE FROM address_books WHERE guid = ? AND personal = 0", guid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("address book not found or is a personal one")
	}
	return nil
}

// Peers returns one page of an address book plus the total count. limit <= 0 returns everything.
func (s *Store) Peers(guid string, limit, offset int) ([]Peer, int, error) {
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ab_peers WHERE ab_guid = ?", guid).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query("SELECT data FROM ab_peers WHERE ab_guid = ? ORDER BY rowid LIMIT ? OFFSET ?", guid, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Peer{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		p := Peer{}
		if json.Unmarshal([]byte(raw), &p) == nil {
			out = append(out, p)
		}
	}
	return out, total, rows.Err()
}

// PeerID extracts the id of a peer object.
func PeerID(p Peer) string {
	switch v := p["id"].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case json.Number:
		return v.String()
	}
	return ""
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func putPeer(x execer, guid string, p Peer, merge bool) error {
	id := PeerID(p)
	if id == "" {
		return errors.New("peer id is required")
	}
	p["id"] = id
	n := now()
	if merge {
		var raw string
		err := x.QueryRow("SELECT data FROM ab_peers WHERE ab_guid = ? AND id = ?", guid, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cur := Peer{}
		_ = json.Unmarshal([]byte(raw), &cur)
		for k, v := range p {
			cur[k] = v
		}
		p = cur
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = x.Exec(`INSERT INTO ab_peers(ab_guid, id, data, created_at, updated_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(ab_guid, id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, guid, id, string(data), n, n)
	return err
}

// PutPeer inserts a peer or replaces the stored object with the same id.
func (s *Store) PutPeer(guid string, p Peer) error { return putPeer(s.db, guid, p, false) }

// PatchPeer merges the given fields into an existing peer.
func (s *Store) PatchPeer(guid string, fields Peer) error { return putPeer(s.db, guid, fields, true) }

// DeletePeers removes peers by id.
func (s *Store) DeletePeers(guid string, ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec("DELETE FROM ab_peers WHERE ab_guid = ? AND id = ?", guid, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceAB swaps the whole content of an address book (legacy sync, import).
func (s *Store) ReplaceAB(guid string, peers []Peer, tags []Tag) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Rows are re-inserted in the given order (listing is by rowid); creation
	// times of entries that survive are kept.
	created := map[string]int64{}
	rows, err := tx.Query("SELECT id, created_at FROM ab_peers WHERE ab_guid = ?", guid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var c int64
		if rows.Scan(&id, &c) == nil {
			created[id] = c
		}
	}
	rows.Close()
	if _, err := tx.Exec("DELETE FROM ab_peers WHERE ab_guid = ?", guid); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM ab_tags WHERE ab_guid = ?", guid); err != nil {
		return err
	}
	n := now()
	for _, p := range peers {
		id := PeerID(p)
		if id == "" {
			continue
		}
		p["id"] = id
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		c, ok := created[id]
		if !ok {
			c = n
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO ab_peers(ab_guid, id, data, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`, guid, id, string(data), c, n); err != nil {
			return err
		}
	}
	for _, t := range tags {
		if t.Name == "" {
			continue
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO ab_tags(ab_guid, name, color) VALUES(?, ?, ?)", guid, t.Name, t.Color); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Tags lists the tags of an address book.
func (s *Store) Tags(guid string) ([]Tag, error) {
	rows, err := s.db.Query("SELECT name, color FROM ab_tags WHERE ab_guid = ? ORDER BY name COLLATE NOCASE", guid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Name, &t.Color); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PutTag adds a tag or changes its colour.
func (s *Store) PutTag(guid string, t Tag) error {
	if t.Name == "" {
		return errors.New("tag name is required")
	}
	_, err := s.db.Exec("INSERT INTO ab_tags(ab_guid, name, color) VALUES(?, ?, ?) ON CONFLICT(ab_guid, name) DO UPDATE SET color = excluded.color", guid, t.Name, t.Color)
	return err
}

// rewriteTags applies fn to the tag list of every peer in an address book.
func rewriteTags(tx *sql.Tx, guid string, fn func([]any) []any) error {
	rows, err := tx.Query("SELECT id, data FROM ab_peers WHERE ab_guid = ?", guid)
	if err != nil {
		return err
	}
	type row struct{ id, data string }
	var all []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.data) == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	for _, r := range all {
		p := Peer{}
		if json.Unmarshal([]byte(r.data), &p) != nil {
			continue
		}
		tags, ok := p["tags"].([]any)
		if !ok || len(tags) == 0 {
			continue
		}
		p["tags"] = fn(tags)
		data, _ := json.Marshal(p)
		if string(data) == r.data {
			continue
		}
		if _, err := tx.Exec("UPDATE ab_peers SET data = ?, updated_at = ? WHERE ab_guid = ? AND id = ?", string(data), now(), guid, r.id); err != nil {
			return err
		}
	}
	return nil
}

// RenameTag renames a tag and updates every peer carrying it.
func (s *Store) RenameTag(guid, oldName, newName string) error {
	if newName == "" {
		return errors.New("tag name is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE OR REPLACE ab_tags SET name = ? WHERE ab_guid = ? AND name = ?", newName, guid, oldName); err != nil {
		return err
	}
	err = rewriteTags(tx, guid, func(tags []any) []any {
		out := make([]any, 0, len(tags))
		seen := map[any]bool{}
		for _, t := range tags {
			if t == oldName {
				t = newName
			}
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		return out
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTags removes tags and strips them from every peer.
func (s *Store) DeleteTags(guid string, names []string) error {
	drop := map[any]bool{}
	for _, n := range names {
		drop[n] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range names {
		if _, err := tx.Exec("DELETE FROM ab_tags WHERE ab_guid = ? AND name = ?", guid, n); err != nil {
			return err
		}
	}
	err = rewriteTags(tx, guid, func(tags []any) []any {
		out := make([]any, 0, len(tags))
		for _, t := range tags {
			if !drop[t] {
				out = append(out, t)
			}
		}
		return out
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Shares lists who an address book is shared with.
func (s *Store) Shares(guid string) ([]Share, error) {
	rows, err := s.db.Query(`SELECT sh.user_id, u.username, sh.rule FROM ab_shares sh JOIN users u ON u.id = sh.user_id
		WHERE sh.ab_guid = ? ORDER BY u.username`, guid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.UserID, &sh.Username, &sh.Rule); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// SetShares replaces the share list of an address book.
func (s *Store) SetShares(guid string, shares []Share) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner int64
	var personal bool
	if err := tx.QueryRow("SELECT owner_id, personal FROM address_books WHERE guid = ?", guid).Scan(&owner, &personal); err != nil {
		return ErrNotFound
	}
	if personal && len(shares) > 0 {
		return errors.New("a personal address book cannot be shared")
	}
	if _, err := tx.Exec("DELETE FROM ab_shares WHERE ab_guid = ?", guid); err != nil {
		return err
	}
	for _, sh := range shares {
		if sh.UserID == owner || sh.Rule < RuleRead || sh.Rule > RuleFull {
			continue
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO ab_shares(ab_guid, user_id, rule) VALUES(?, ?, ?)", guid, sh.UserID, sh.Rule); err != nil {
			return err
		}
	}
	return tx.Commit()
}
