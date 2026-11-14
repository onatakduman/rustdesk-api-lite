package store

import (
	"database/sql"
	"errors"
)

// SetABPassword sets the default password of a shared address book.
func (s *Store) SetABPassword(guid, password string) error {
	_, err := s.db.Exec("UPDATE address_books SET default_password = ? WHERE guid = ? AND personal = 0", password, guid)
	return err
}

// ABByName finds a shared address book by its name (oldest first when names repeat).
func (s *Store) ABByName(name string) (string, error) {
	var guid string
	err := s.db.QueryRow("SELECT guid FROM address_books WHERE personal = 0 AND name = ? COLLATE NOCASE ORDER BY created_at LIMIT 1", name).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return guid, err
}

// HasPeer reports whether an address book already holds an id.
func (s *Store) HasPeer(guid, id string) bool {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM ab_peers WHERE ab_guid = ? AND id = ?", guid, id).Scan(&n)
	return n > 0
}

// SetDeviceGroup puts a device into a named group ("" removes it).
func (s *Store) SetDeviceGroup(id, group string) error {
	_, err := s.db.Exec("UPDATE devices SET group_name = ? WHERE id = ?", group, id)
	return err
}

// SetDeviceOwner assigns a known device to a user (0 clears the owner).
func (s *Store) SetDeviceOwner(id string, userID int64) error {
	var uid any
	if userID > 0 {
		uid = userID
	}
	_, err := s.db.Exec("UPDATE devices SET user_id = ? WHERE id = ?", uid, id)
	return err
}

// DeviceGroups lists the group names in use among the devices a user may see (0 = all).
func (s *Store) DeviceGroups(userID int64) []string {
	rows, err := s.db.Query("SELECT DISTINCT group_name FROM devices WHERE group_name <> '' AND (? = 0 OR user_id = ?) ORDER BY group_name COLLATE NOCASE", userID, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			out = append(out, g)
		}
	}
	return out
}

// LoginLog is one sign-in attempt.
type LoginLog struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	Kind       string `json:"kind"`   // client | console
	Method     string `json:"method"` // password | provider name
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	IP         string `json:"ip"`
	OK         bool   `json:"ok"`
	At         int64  `json:"at"`
}

// AddLoginLog records a sign-in attempt.
func (s *Store) AddLoginLog(l *LoginLog) {
	_, _ = s.db.Exec(`INSERT INTO login_log(user_id, username, kind, method, device_id, device_name, ip, ok, at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.UserID, l.Username, l.Kind, l.Method, l.DeviceID, l.DeviceName, l.IP, l.OK, now())
}

// LoginLogs returns recent sign-ins (userID 0 = everyone) plus the total count.
func (s *Store) LoginLogs(userID int64, limit, offset int) ([]*LoginLog, int, error) {
	var total int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM login_log WHERE (? = 0 OR user_id = ?)", userID, userID).Scan(&total)
	rows, err := s.db.Query(`SELECT rowid_, user_id, username, kind, method, device_id, device_name, ip, ok, at FROM login_log
		WHERE (? = 0 OR user_id = ?) ORDER BY rowid_ DESC LIMIT ? OFFSET ?`, userID, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*LoginLog{}
	for rows.Next() {
		l := &LoginLog{}
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Kind, &l.Method, &l.DeviceID, &l.DeviceName, &l.IP, &l.OK, &l.At); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// Alarm is a security event a device reported (blocked address, repeated failed attempts …).
type Alarm struct {
	ID       int64  `json:"id"`
	DeviceID string `json:"device_id"`
	Device   string `json:"device"`
	Type     int    `json:"type"`
	Info     string `json:"info"`
	At       int64  `json:"at"`
}

// AddAlarm records a security event.
func (s *Store) AddAlarm(deviceID string, typ int, info string) error {
	_, err := s.db.Exec("INSERT INTO alarms(device_id, typ, info, at) VALUES(?, ?, ?, ?)", deviceID, typ, info, now())
	return err
}

// Alarms returns recent security events plus the total count.
func (s *Store) Alarms(limit, offset int) ([]*Alarm, int, error) {
	var total int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM alarms").Scan(&total)
	rows, err := s.db.Query(`SELECT a.rowid_, a.device_id, COALESCE(d.hostname, ''), a.typ, a.info, a.at
		FROM alarms a LEFT JOIN devices d ON d.id = a.device_id ORDER BY a.rowid_ DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Alarm{}
	for rows.Next() {
		a := &Alarm{}
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.Device, &a.Type, &a.Info, &a.At); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// ActiveConn finds the newest session into a device by its session id (0 when none).
func (s *Store) ActiveConn(deviceID, sessionID string) int64 {
	var id int64
	_ = s.db.QueryRow("SELECT rowid_ FROM audit_conn WHERE device_id = ? AND session_id = ? AND session_id <> '' ORDER BY rowid_ DESC LIMIT 1", deviceID, sessionID).Scan(&id)
	return id
}

// ConnPeer returns the device a recorded session went into and the RustDesk id
// it was opened from.
func (s *Store) ConnPeer(id int64) (deviceID, peerID string, err error) {
	err = s.db.QueryRow("SELECT device_id, peer_id FROM audit_conn WHERE rowid_ = ?", id).Scan(&deviceID, &peerID)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return deviceID, peerID, err
}

// SetConnNote stores the note written for a session.
func (s *Store) SetConnNote(id int64, note string) error {
	if len(note) > 1024 {
		note = note[:1024]
	}
	_, err := s.db.Exec("UPDATE audit_conn SET note = ? WHERE rowid_ = ?", note, id)
	return err
}

// PruneLogs deletes log rows older than the given unix time and returns how many went.
func (s *Store) PruneLogs(before int64) int64 {
	var n int64
	for _, q := range []string{"DELETE FROM audit_conn WHERE started_at < ?", "DELETE FROM audit_file WHERE at < ?", "DELETE FROM login_log WHERE at < ?", "DELETE FROM alarms WHERE at < ?"} {
		if res, err := s.db.Exec(q, before); err == nil {
			c, _ := res.RowsAffected()
			n += c
		}
	}
	return n
}
