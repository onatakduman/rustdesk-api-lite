// Package importer migrates data from other RustDesk API servers.
package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// parseTime reads the timestamp formats GORM writes into SQLite.
func parseTime(v any) int64 {
	switch x := v.(type) {
	case time.Time:
		return x.Unix()
	case int64:
		return x
	case []byte:
		return parseTime(string(x))
	case string:
		for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", time.RFC3339Nano, "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

func truthy(v any) bool {
	switch x := v.(type) {
	case int64:
		return x != 0
	case bool:
		return x
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	case []byte:
		return truthy(string(x))
	}
	return false
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return fmt.Sprint(v)
}

// tagPalette holds ARGB colours for tags that had none.
var tagPalette = []int64{0xFFE5484D, 0xFFF76B15, 0xFFFFB224, 0xFF46A758, 0xFF12A594, 0xFF00A2C7, 0xFF0071FF, 0xFF5B5BD6, 0xFF8E4EC6, 0xFFD6409F}

func hashString(s string) uint32 {
	var h uint32 = 7
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// rows runs a query and returns each row as a column-name map. A missing
// table yields no rows, so older schema versions import what they have.
func rows(db *sql.DB, query string) ([]map[string]any, error) {
	rs, err := db.Query(query)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out []map[string]any
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rs.Err()
}

// Lejianwen imports a lejianwen/rustdesk-api SQLite database (opened read-only).
// It can be run again: accounts are matched by username, address books are
// replaced with the source content, logs are appended from where the last run stopped.
func Lejianwen(st *store.Store, path string) (string, error) {
	src, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return "", err
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	var rep strings.Builder
	say := func(format string, a ...any) { fmt.Fprintf(&rep, format+"\n", a...) }

	// Users. Password hashes are bcrypt and are taken over as they are.
	us, err := rows(src, "SELECT * FROM users ORDER BY id")
	if err != nil {
		return "", err
	}
	if len(us) == 0 {
		return "", errors.New("the source database has no users table or no users")
	}
	userID := map[int64]int64{} // source id -> our id
	groupOf := map[int64]int64{}
	created := 0
	for _, r := range us {
		sid := r["id"].(int64)
		groupOf[sid], _ = r["group_id"].(int64)
		name := str(r["username"])
		if u, err := st.UserByUsername(name); err == nil && strings.EqualFold(u.Username, name) {
			userID[sid] = u.ID
			continue
		}
		hash := str(r["password"])
		if !strings.HasPrefix(hash, "$2") {
			hash = "" // not bcrypt: the account needs a new password
		}
		status, _ := r["status"].(int64)
		u := &store.User{Username: name, Email: str(r["email"]), DisplayName: str(r["nickname"]), IsAdmin: truthy(r["is_admin"]), Disabled: status != 1, Note: str(r["remark"])}
		if err := st.CreateUser(u, "", hash); err != nil {
			return "", fmt.Errorf("user %s: %w", name, err)
		}
		userID[sid] = u.ID
		created++
		if hash == "" {
			say("  note: %s has no usable password hash; set one with `user passwd`", name)
		}
	}
	say("users: %d found, %d created, %d already present", len(us), created, len(us)-created)

	// User groups: type 2 is a shared group (members see each other's devices).
	grows, err := rows(src, "SELECT * FROM groups ORDER BY id")
	if err != nil {
		return "", err
	}
	existing, err := st.Groups()
	if err != nil {
		return "", err
	}
	groupID := map[int64]int64{}
	for _, r := range grows {
		typ, _ := r["type"].(int64)
		g := &store.Group{Name: str(r["name"]), Shared: typ == 2}
		for _, e := range existing {
			if strings.EqualFold(e.Name, g.Name) {
				g.ID = e.ID
			}
		}
		if err := st.SaveGroup(g); err != nil {
			return "", fmt.Errorf("group %s: %w", g.Name, err)
		}
		groupID[r["id"].(int64)] = g.ID
	}
	for sid, uid := range userID {
		if u, err := st.UserByID(uid); err == nil && u.GroupID == 0 && groupID[groupOf[sid]] != 0 {
			u.GroupID = groupID[groupOf[sid]]
			if err := st.UpdateUser(u); err != nil {
				return "", err
			}
		}
	}
	say("user groups: %d", len(grows))

	// External sign-in providers and the identities linked through them.
	provs, err := rows(src, "SELECT * FROM oauths")
	if err != nil {
		return "", err
	}
	n := 0
	for _, r := range provs {
		issuer := strings.TrimRight(str(r["issuer"]), "/")
		if issuer == "" {
			say("  note: provider %q has no issuer (not OpenID Connect) and was skipped", str(r["op"]))
			continue
		}
		p := &store.OIDCProvider{Name: str(r["op"]), Issuer: issuer, ClientID: str(r["client_id"]), ClientSecret: str(r["client_secret"]),
			Scopes: strings.Join(strings.FieldsFunc(str(r["scopes"]), func(c rune) bool { return c == ',' || c == ' ' }), " "), AutoRegister: truthy(r["auto_register"])}
		if err := st.SaveOIDCProvider(p); err != nil {
			return "", fmt.Errorf("provider %s: %w", p.Name, err)
		}
		n++
	}
	ids, err := rows(src, "SELECT * FROM user_thirds")
	if err != nil {
		return "", err
	}
	linked := 0
	for _, r := range ids {
		uid, ok := userID[r["user_id"].(int64)]
		if !ok || str(r["open_id"]) == "" {
			continue
		}
		if err := st.LinkIdentity(str(r["op"]), str(r["open_id"]), uid, str(r["email"])); err != nil {
			return "", err
		}
		linked++
	}
	say("sign-in providers: %d, linked identities: %d", n, linked)

	// Sessions: keeping the tokens means signed-in RustDesk clients stay signed in.
	toks, err := rows(src, "SELECT * FROM user_tokens")
	if err != nil {
		return "", err
	}
	kept := 0
	nowTs := time.Now().Unix()
	for _, r := range toks {
		uid, ok := userID[r["user_id"].(int64)]
		exp, _ := r["expired_at"].(int64)
		if !ok || str(r["token"]) == "" || exp < nowTs {
			continue
		}
		// Carried-over sessions get no expiry, like sessions created here.
		t := &store.Token{UserID: uid, Kind: "client", DeviceID: str(r["device_id"]), DeviceUUID: str(r["device_uuid"]), CreatedAt: parseTime(r["created_at"])}
		if err := st.ImportToken(str(r["token"]), t); err != nil {
			return "", err
		}
		kept++
	}
	say("sessions: %d of %d still valid and carried over", kept, len(toks))

	// Address books. Collection 0 is the personal book; others become shared books.
	cols, err := rows(src, "SELECT * FROM address_book_collections ORDER BY id")
	if err != nil {
		return "", err
	}
	bookOf := map[string]string{} // "<user>/<collection>" -> guid
	bookFor := func(srcUser, collection int64) (string, error) {
		key := fmt.Sprintf("%d/%d", srcUser, collection)
		if g, ok := bookOf[key]; ok {
			return g, nil
		}
		uid, ok := userID[srcUser]
		if !ok {
			return "", nil
		}
		g, err := st.PersonalAB(uid)
		bookOf[key] = g
		return g, err
	}
	for _, r := range cols {
		cid, owner := r["id"].(int64), r["user_id"].(int64)
		uid, ok := userID[owner]
		if !ok {
			continue
		}
		marker := fmt.Sprintf("import:lejianwen:collection:%d", cid)
		g := st.Setting(marker)
		if _, err := st.AB(g, uid); g == "" || err != nil {
			if g, err = st.CreateAB(uid, str(r["name"]), ""); err != nil {
				return "", err
			}
			if err := st.SetSetting(marker, g); err != nil {
				return "", err
			}
		}
		bookOf[fmt.Sprintf("%d/%d", owner, cid)] = g
	}
	peers := map[string][]store.Peer{}
	tags := map[string][]store.Tag{}
	abs, err := rows(src, "SELECT * FROM address_books ORDER BY row_id")
	if err != nil {
		return "", err
	}
	for _, r := range abs {
		cid, _ := r["collection_id"].(int64)
		g, err := bookFor(r["user_id"].(int64), cid)
		if err != nil {
			return "", err
		}
		if g == "" {
			continue
		}
		p := store.Peer{"id": str(r["id"])}
		for field, col := range map[string]string{"username": "username", "hostname": "hostname", "alias": "alias", "platform": "platform", "hash": "hash",
			"rdpPort": "rdp_port", "rdpUsername": "rdp_username", "loginName": "login_name"} {
			if v := str(r[col]); v != "" {
				p[field] = v
			}
		}
		if truthy(r["force_always_relay"]) {
			p["forceAlwaysRelay"] = "true"
		}
		if truthy(r["same_server"]) {
			p["same_server"] = true
		}
		list := []any{}
		_ = json.Unmarshal([]byte(str(r["tags"])), &list)
		p["tags"] = list
		peers[g] = append(peers[g], p)
	}
	tgs, err := rows(src, "SELECT * FROM tags ORDER BY id")
	if err != nil {
		return "", err
	}
	for _, r := range tgs {
		cid, _ := r["collection_id"].(int64)
		g, err := bookFor(r["user_id"].(int64), cid)
		if err != nil {
			return "", err
		}
		if g == "" {
			continue
		}
		color, _ := r["color"].(int64)
		tags[g] = append(tags[g], store.Tag{Name: str(r["name"]), Color: color})
	}
	// The RustDesk app only offers tags that exist in the book's tag list, so
	// tags that are used on entries but were never defined get a colour here.
	total, madeTags := 0, 0
	for g, list := range peers {
		have := map[string]bool{}
		for _, t := range tags[g] {
			have[t.Name] = true
		}
		for _, p := range list {
			for _, v := range p["tags"].([]any) {
				name, _ := v.(string)
				if name == "" || have[name] {
					continue
				}
				have[name] = true
				tags[g] = append(tags[g], store.Tag{Name: name, Color: tagPalette[int(hashString(name))%len(tagPalette)]})
				madeTags++
			}
		}
	}
	for _, g := range bookOf {
		if err := st.ReplaceAB(g, peers[g], tags[g]); err != nil {
			return "", err
		}
		total += len(peers[g])
	}
	say("address books: %d (of which %d shared), entries: %d, tags given a colour: %d", len(bookOf), len(cols), total, madeTags)

	// Shares: type 1 targets a user, type 2 a user group.
	rules, err := rows(src, "SELECT * FROM address_book_collection_rules")
	if err != nil {
		return "", err
	}
	type target struct {
		group bool
		id    int64
	}
	shares := map[string]map[target]int{}
	for _, r := range rules {
		cid, owner := r["collection_id"].(int64), r["user_id"].(int64)
		g := bookOf[fmt.Sprintf("%d/%d", owner, cid)]
		rule, _ := r["rule"].(int64)
		typ, _ := r["type"].(int64)
		to, _ := r["to_id"].(int64)
		if g == "" || rule < 1 || rule > 3 {
			continue
		}
		var tg target
		if typ == 2 {
			tg = target{true, groupID[to]}
		} else if to != owner {
			tg = target{false, userID[to]}
		}
		if tg.id == 0 {
			continue
		}
		if shares[g] == nil {
			shares[g] = map[target]int{}
		}
		if int(rule) > shares[g][tg] {
			shares[g][tg] = int(rule)
		}
	}
	nshares := 0
	for g, m := range shares {
		var list []store.Share
		for tg, rule := range m {
			if tg.group {
				list = append(list, store.Share{GroupID: tg.id, Rule: rule})
			} else {
				list = append(list, store.Share{UserID: tg.id, Rule: rule})
			}
		}
		if err := st.SetShares(g, list); err != nil {
			return "", err
		}
		nshares += len(list)
	}
	say("shares: %d", nshares)

	// Devices that reported to the old server.
	db := st.DB()
	devs, err := rows(src, "SELECT * FROM peers ORDER BY row_id")
	if err != nil {
		return "", err
	}
	for _, r := range devs {
		id := str(r["id"])
		if id == "" {
			continue
		}
		var owner any
		if sid, _ := r["user_id"].(int64); sid != 0 {
			if uid, ok := userID[sid]; ok {
				owner = uid
			}
		}
		seen, _ := r["last_online_time"].(int64)
		first := parseTime(r["created_at"])
		if first == 0 {
			first = seen
		}
		if _, err := db.Exec(`INSERT INTO devices(id, uuid, hostname, username, os, cpu, memory, version, user_id, last_ip, first_seen, last_seen)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET uuid = excluded.uuid, hostname = excluded.hostname, username = excluded.username, os = excluded.os, cpu = excluded.cpu,
				memory = excluded.memory, version = excluded.version, user_id = excluded.user_id, last_ip = excluded.last_ip,
				last_seen = MAX(last_seen, excluded.last_seen)`,
			id, str(r["uuid"]), str(r["hostname"]), str(r["username"]), str(r["os"]), str(r["cpu"]), str(r["memory"]), str(r["version"]), owner, str(r["last_online_ip"]), first, seen); err != nil {
			return "", err
		}
	}
	say("devices: %d", len(devs))

	// Connection log, appended after the last imported row.
	const marker = "import:lejianwen:audit_conn_max"
	last, _ := strconv.ParseInt(st.Setting(marker), 10, 64)
	conns, err := rows(src, "SELECT * FROM audit_conns WHERE id > "+strconv.FormatInt(last, 10)+" ORDER BY id")
	if err != nil {
		return "", err
	}
	for _, r := range conns {
		typ, _ := r["type"].(int64)
		connID, _ := r["conn_id"].(int64)
		closed, _ := r["close_time"].(int64)
		if _, err := db.Exec(`INSERT INTO audit_conn(device_id, conn_id, session_id, peer_id, peer_name, ip, conn_type, started_at, ended_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			str(r["peer_id"]), connID, str(r["session_id"]), str(r["from_peer"]), str(r["from_name"]), str(r["ip"]), typ, parseTime(r["created_at"]), closed); err != nil {
			return "", err
		}
		last = r["id"].(int64)
	}
	if err := st.SetSetting(marker, strconv.FormatInt(last, 10)); err != nil {
		return "", err
	}
	say("connection log: %d rows added", len(conns))
	return rep.String(), nil
}
