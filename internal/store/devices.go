package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// OnlineWindow is how long after its last heartbeat a device counts as online.
const OnlineWindow = 45

// Device is a RustDesk installation that reports to this server.
type Device struct {
	ID        string `json:"id"`
	UUID      string `json:"-"`
	Hostname  string `json:"hostname"`
	Username  string `json:"username"`
	OS        string `json:"os"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	Version   string `json:"version"`
	UserID    int64  `json:"user_id"`
	User      string `json:"user"`
	Note      string `json:"note"`
	LastIP    string `json:"last_ip"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Online    bool   `json:"online"`
	Group     string `json:"group"`
	// Conns are the ids of the incoming sessions the device reported in its last heartbeat.
	Conns []int64 `json:"conns"`
}

// Heartbeat records that a device is alive. It reports whether the device is
// known with system information (false asks the client to send it).
func (s *Store) Heartbeat(id, uuid, ip string, conns []int64) (known bool, err error) {
	connsJSON := ""
	if len(conns) > 0 {
		raw, _ := json.Marshal(conns)
		connsJSON = string(raw)
	}
	n := now()
	res, err := s.db.Exec("UPDATE devices SET last_seen = ?, last_ip = ?, conns = ? WHERE id = ?", n, ip, connsJSON, id)
	if err != nil {
		return false, err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		_, err = s.db.Exec("INSERT OR IGNORE INTO devices(id, uuid, last_ip, first_seen, last_seen) VALUES(?, ?, ?, ?, ?)", id, uuid, ip, n, n)
		return false, err
	}
	var host string
	_ = s.db.QueryRow("SELECT hostname FROM devices WHERE id = ?", id).Scan(&host)
	return host != "", nil
}

// SaveSysinfo stores the system information a device reported.
func (s *Store) SaveSysinfo(d *Device, ip string) error {
	n := now()
	_, err := s.db.Exec(`INSERT INTO devices(id, uuid, hostname, username, os, cpu, memory, version, last_ip, first_seen, last_seen)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET uuid = excluded.uuid, hostname = excluded.hostname, username = excluded.username, os = excluded.os,
			cpu = excluded.cpu, memory = excluded.memory, version = excluded.version, last_ip = excluded.last_ip, last_seen = excluded.last_seen`,
		d.ID, d.UUID, d.Hostname, d.Username, d.OS, d.CPU, d.Memory, d.Version, ip, n, n)
	return err
}

// AssignDevice ties a device to the user signed in on it (userID 0 clears it).
func (s *Store) AssignDevice(id string, userID int64) {
	if id == "" {
		return
	}
	var uid any
	if userID > 0 {
		uid = userID
	}
	n := now()
	_, _ = s.db.Exec(`INSERT INTO devices(id, user_id, first_seen, last_seen) VALUES(?, ?, ?, 0)
		ON CONFLICT(id) DO UPDATE SET user_id = excluded.user_id`, id, uid, n)
}

const deviceCols = `d.id, d.uuid, d.hostname, d.username, d.os, d.cpu, d.memory, d.version, COALESCE(d.user_id, 0), COALESCE(u.username, ''),
	d.note, d.last_ip, d.first_seen, d.last_seen, d.group_name, d.conns`

func scanDevice(sc interface{ Scan(...any) error }) (*Device, error) {
	d := &Device{Conns: []int64{}}
	var conns string
	err := sc.Scan(&d.ID, &d.UUID, &d.Hostname, &d.Username, &d.OS, &d.CPU, &d.Memory, &d.Version, &d.UserID, &d.User, &d.Note, &d.LastIP, &d.FirstSeen, &d.LastSeen, &d.Group, &conns)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err == nil {
		d.Online = now()-d.LastSeen <= OnlineWindow
		if d.Online && conns != "" {
			_ = json.Unmarshal([]byte(conns), &d.Conns)
		}
	}
	return d, err
}

// Devices lists devices; userID 0 lists all, otherwise only that user's.
func (s *Store) Devices(userID int64) ([]*Device, error) {
	rows, err := s.db.Query("SELECT "+deviceCols+` FROM devices d LEFT JOIN users u ON u.id = d.user_id
		WHERE (? = 0 OR d.user_id = ?) ORDER BY d.last_seen DESC, d.id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Device loads one device.
func (s *Store) Device(id string) (*Device, error) {
	return scanDevice(s.db.QueryRow("SELECT "+deviceCols+" FROM devices d LEFT JOIN users u ON u.id = d.user_id WHERE d.id = ?", id))
}

// SetDeviceNote changes the note shown next to a device.
func (s *Store) SetDeviceNote(id, note string) error {
	_, err := s.db.Exec("UPDATE devices SET note = ? WHERE id = ?", note, id)
	return err
}

// DeleteDevice forgets a device and signs out the RustDesk app on it (the
// device reappears, without an owner, on its next heartbeat).
func (s *Store) DeleteDevice(id string) error {
	if _, err := s.db.Exec("DELETE FROM tokens WHERE device_id = ? AND kind = 'client'", id); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM devices WHERE id = ?", id)
	return err
}

// ConnAudit is one remote session into a device.
type ConnAudit struct {
	ID        int64  `json:"id"`
	Note      string `json:"note"`
	DeviceID  string `json:"device_id"`
	Device    string `json:"device"`
	ConnID    int64  `json:"conn_id"`
	SessionID string `json:"session_id"`
	PeerID    string `json:"peer_id"`
	PeerName  string `json:"peer_name"`
	IP        string `json:"ip"`
	Type      int    `json:"type"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
}

// ConnOpened records a new incoming session, or fills in the remote peer of
// one that was already recorded for the same connection id.
func (s *Store) ConnOpened(a *ConnAudit) error {
	n := now()
	var rowid int64
	err := s.db.QueryRow(`SELECT rowid_ FROM audit_conn WHERE device_id = ? AND conn_id = ? AND ended_at = 0 AND started_at > ?
		ORDER BY rowid_ DESC LIMIT 1`, a.DeviceID, a.ConnID, n-86400).Scan(&rowid)
	if err == nil {
		_, err = s.db.Exec(`UPDATE audit_conn SET
			session_id = CASE WHEN ?1 <> '' THEN ?1 ELSE session_id END,
			peer_id    = CASE WHEN ?2 <> '' THEN ?2 ELSE peer_id END,
			peer_name  = CASE WHEN ?3 <> '' THEN ?3 ELSE peer_name END,
			ip         = CASE WHEN ?4 <> '' THEN ?4 ELSE ip END,
			conn_type  = CASE WHEN ?5 <> 0 THEN ?5 ELSE conn_type END
			WHERE rowid_ = ?6`, a.SessionID, a.PeerID, a.PeerName, a.IP, a.Type, rowid)
		return err
	}
	_, err = s.db.Exec(`INSERT INTO audit_conn(device_id, conn_id, session_id, peer_id, peer_name, ip, conn_type, started_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, a.DeviceID, a.ConnID, a.SessionID, a.PeerID, a.PeerName, a.IP, a.Type, n)
	return err
}

// ConnClosed marks a session as ended.
func (s *Store) ConnClosed(deviceID string, connID int64) error {
	_, err := s.db.Exec("UPDATE audit_conn SET ended_at = ? WHERE device_id = ? AND conn_id = ? AND ended_at = 0", now(), deviceID, connID)
	return err
}

// ConnAudits returns the most recent sessions plus the total count.
func (s *Store) ConnAudits(limit, offset int) ([]*ConnAudit, int, error) {
	var total int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM audit_conn").Scan(&total)
	rows, err := s.db.Query(`SELECT a.rowid_, a.device_id, COALESCE(d.hostname, ''), a.conn_id, a.session_id, a.peer_id, a.peer_name, a.ip, a.conn_type, a.started_at, a.ended_at, a.note
		FROM audit_conn a LEFT JOIN devices d ON d.id = a.device_id ORDER BY a.rowid_ DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*ConnAudit{}
	for rows.Next() {
		a := &ConnAudit{}
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.Device, &a.ConnID, &a.SessionID, &a.PeerID, &a.PeerName, &a.IP, &a.Type, &a.StartedAt, &a.EndedAt, &a.Note); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// FileAudit is one file transfer event.
type FileAudit struct {
	ID        int64  `json:"id"`
	DeviceID  string `json:"device_id"`
	PeerID    string `json:"peer_id"`
	Direction int    `json:"direction"`
	Path      string `json:"path"`
	IsFile    bool   `json:"is_file"`
	Info      string `json:"info"`
	At        int64  `json:"at"`
}

// AddFileAudit records a file transfer.
func (s *Store) AddFileAudit(a *FileAudit) error {
	_, err := s.db.Exec("INSERT INTO audit_file(device_id, peer_id, direction, path, is_file, info, at) VALUES(?, ?, ?, ?, ?, ?, ?)",
		a.DeviceID, a.PeerID, a.Direction, a.Path, a.IsFile, a.Info, now())
	return err
}

// FileAudits returns the most recent file transfers plus the total count.
func (s *Store) FileAudits(limit, offset int) ([]*FileAudit, int, error) {
	var total int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM audit_file").Scan(&total)
	rows, err := s.db.Query("SELECT rowid_, device_id, peer_id, direction, path, is_file, info, at FROM audit_file ORDER BY rowid_ DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*FileAudit{}
	for rows.Next() {
		a := &FileAudit{}
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.PeerID, &a.Direction, &a.Path, &a.IsFile, &a.Info, &a.At); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// OIDCProvider is an external sign-in provider.
type OIDCProvider struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"-"`
	Scopes       string `json:"scopes"`
	AutoRegister bool   `json:"auto_register"`
	// LinkByEmail signs a person in to the existing account that has the same
	// verified e-mail address. Only for providers trusted to verify addresses.
	LinkByEmail bool `json:"link_by_email"`
	// AllowedDomains limits what the provider does by itself (finding an account
	// by e-mail, creating one) to verified addresses at these domains: a
	// comma-separated list, empty for no limit. DefaultGroupID is the user
	// group of the accounts it creates (0 = none).
	AllowedDomains string `json:"allowed_domains"`
	DefaultGroupID int64  `json:"default_group_id"`
}

const providerCols = "name, issuer, client_id, client_secret, scopes, auto_register, link_by_email, allowed_domains, default_group_id"

func scanProvider(sc interface{ Scan(...any) error }) (*OIDCProvider, error) {
	p := &OIDCProvider{}
	err := sc.Scan(&p.Name, &p.Issuer, &p.ClientID, &p.ClientSecret, &p.Scopes, &p.AutoRegister, &p.LinkByEmail, &p.AllowedDomains, &p.DefaultGroupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// OIDCProviders lists the configured providers.
func (s *Store) OIDCProviders() ([]*OIDCProvider, error) {
	rows, err := s.db.Query("SELECT " + providerCols + " FROM oidc_providers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OIDCProvider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OIDCProvider loads one provider.
func (s *Store) OIDCProvider(name string) (*OIDCProvider, error) {
	return scanProvider(s.db.QueryRow("SELECT "+providerCols+" FROM oidc_providers WHERE name = ?", name))
}

// SaveOIDCProvider inserts or updates a provider. An empty secret keeps the stored one.
func (s *Store) SaveOIDCProvider(p *OIDCProvider) error {
	if p.Name == "" || p.Issuer == "" || p.ClientID == "" {
		return errors.New("name, issuer and client id are required")
	}
	if p.Scopes == "" {
		p.Scopes = "openid email profile"
	}
	_, err := s.db.Exec(`INSERT INTO oidc_providers(`+providerCols+`) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET issuer = excluded.issuer, client_id = excluded.client_id,
			client_secret = CASE WHEN excluded.client_secret <> '' THEN excluded.client_secret ELSE client_secret END,
			scopes = excluded.scopes, auto_register = excluded.auto_register, link_by_email = excluded.link_by_email,
			allowed_domains = excluded.allowed_domains, default_group_id = excluded.default_group_id`,
		p.Name, p.Issuer, p.ClientID, p.ClientSecret, p.Scopes, p.AutoRegister, p.LinkByEmail, p.AllowedDomains, p.DefaultGroupID)
	return err
}

// DeleteOIDCProvider removes a provider and the identities linked through it.
func (s *Store) DeleteOIDCProvider(name string) error {
	if _, err := s.db.Exec("DELETE FROM identities WHERE provider = ?", name); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM oidc_providers WHERE name = ?", name)
	return err
}

// OIDCSession is one in-flight browser sign-in.
type OIDCSession struct {
	Code       string
	State      string
	Provider   string
	Purpose    string // "client" (RustDesk app waits and polls) or "console"
	DeviceID   string
	DeviceUUID string
	DeviceName string
	DeviceOS   string
	Result     string // "" while pending; otherwise "ok:<token>" or "error:<message>"
	CreatedAt  int64
}

// OIDCSessionTTL is how long a browser sign-in may take, in seconds.
const OIDCSessionTTL = 600

// NewOIDCSession stores a pending sign-in.
func (s *Store) NewOIDCSession(o *OIDCSession) error {
	n := now()
	_, _ = s.db.Exec("DELETE FROM oidc_sessions WHERE created_at < ?", n-OIDCSessionTTL)
	o.Code, o.State, o.CreatedAt = RandomToken(24), RandomToken(24), n
	_, err := s.db.Exec(`INSERT INTO oidc_sessions(code, state, provider, purpose, device_id, device_uuid, device_name, device_os, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, o.Code, o.State, o.Provider, o.Purpose, o.DeviceID, o.DeviceUUID, o.DeviceName, o.DeviceOS, n)
	return err
}

func (s *Store) oidcSession(where, arg string) (*OIDCSession, error) {
	o := &OIDCSession{}
	err := s.db.QueryRow(`SELECT code, state, provider, purpose, device_id, device_uuid, device_name, device_os, result, created_at
		FROM oidc_sessions WHERE `+where+` = ? AND created_at >= ?`, arg, now()-OIDCSessionTTL).
		Scan(&o.Code, &o.State, &o.Provider, &o.Purpose, &o.DeviceID, &o.DeviceUUID, &o.DeviceName, &o.DeviceOS, &o.Result, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// OIDCSessionByState finds a pending sign-in from the provider's callback.
func (s *Store) OIDCSessionByState(state string) (*OIDCSession, error) {
	return s.oidcSession("state", state)
}

// OIDCSessionByCode finds a sign-in by the code handed to the client.
func (s *Store) OIDCSessionByCode(code string) (*OIDCSession, error) {
	return s.oidcSession("code", code)
}

// SetOIDCResult stores the outcome of a sign-in.
func (s *Store) SetOIDCResult(code, result string) error {
	_, err := s.db.Exec("UPDATE oidc_sessions SET result = ? WHERE code = ?", result, code)
	return err
}

// DeleteOIDCSession removes a finished sign-in.
func (s *Store) DeleteOIDCSession(code string) {
	_, _ = s.db.Exec("DELETE FROM oidc_sessions WHERE code = ?", code)
}

// Counts are the headline numbers for the console.
type Counts struct {
	Users         int `json:"users"`
	Devices       int `json:"devices"`
	DevicesOnline int `json:"devices_online"`
	AddressBooks  int `json:"address_books"`
	Peers         int `json:"peers"`
	Sessions      int `json:"sessions"`
}

// Counts returns the headline numbers.
func (s *Store) Counts() Counts {
	var c Counts
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&c.Users)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&c.Devices)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM devices WHERE last_seen >= ?", now()-OnlineWindow).Scan(&c.DevicesOnline)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM address_books").Scan(&c.AddressBooks)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM ab_peers").Scan(&c.Peers)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM audit_conn").Scan(&c.Sessions)
	return c
}
