package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Group is a set of users. In a shared group the members see each other's
// devices in the RustDesk app; in a regular one everybody sees only their own.
type Group struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Shared  bool   `json:"shared"`
	Members int    `json:"members"`
}

// Groups lists the user groups.
func (s *Store) Groups() ([]*Group, error) {
	rows, err := s.db.Query(`SELECT g.id, g.name, g.shared, (SELECT COUNT(*) FROM users u WHERE u.group_id = g.id) FROM user_groups g ORDER BY g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Group{}
	for rows.Next() {
		g := &Group{}
		if err := rows.Scan(&g.ID, &g.Name, &g.Shared, &g.Members); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGroup creates (ID 0) or updates a group.
func (s *Store) SaveGroup(g *Group) error {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		return errors.New("name is required")
	}
	var err error
	if g.ID == 0 {
		var res sql.Result
		if res, err = s.db.Exec("INSERT INTO user_groups(name, shared) VALUES(?, ?)", g.Name, g.Shared); err == nil {
			g.ID, _ = res.LastInsertId()
		}
	} else {
		_, err = s.db.Exec("UPDATE user_groups SET name = ?, shared = ? WHERE id = ?", g.Name, g.Shared, g.ID)
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("a group with this name already exists")
	}
	return err
}

// DeleteGroup removes a group; its members end up in no group.
func (s *Store) DeleteGroup(id int64) error {
	if _, err := s.db.Exec("UPDATE users SET group_id = 0 WHERE group_id = ?", id); err != nil {
		return err
	}
	if _, err := s.db.Exec("DELETE FROM ab_group_shares WHERE group_id = ?", id); err != nil {
		return err
	}
	if _, err := s.db.Exec("UPDATE oidc_providers SET default_group_id = 0 WHERE default_group_id = ?", id); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM user_groups WHERE id = ?", id)
	return err
}

// VisibleUsers returns the ids of the users whose devices u may see: nil
// means everybody (administrators), otherwise u and, in a shared group, its members.
func (s *Store) VisibleUsers(u *User) []int64 {
	if u.IsAdmin {
		return nil
	}
	ids := []int64{u.ID}
	if u.GroupID == 0 {
		return ids
	}
	var shared bool
	if s.db.QueryRow("SELECT shared FROM user_groups WHERE id = ?", u.GroupID).Scan(&shared) != nil || !shared {
		return ids
	}
	rows, err := s.db.Query("SELECT id FROM users WHERE group_id = ? AND disabled = 0 AND id <> ?", u.GroupID, u.ID)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// DevicesFor lists the devices owned by any of the given users (nil = all devices).
func (s *Store) DevicesFor(userIDs []int64) ([]*Device, error) {
	all, err := s.Devices(0)
	if err != nil || userIDs == nil {
		return all, err
	}
	want := map[int64]bool{}
	for _, id := range userIDs {
		want[id] = true
	}
	out := []*Device{}
	for _, d := range all {
		if want[d.UserID] {
			out = append(out, d)
		}
	}
	return out, nil
}
