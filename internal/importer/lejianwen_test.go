package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

const fixture = `
INSERT INTO users(id, username, email, password, nickname, group_id, is_admin, status) VALUES
 (1, 'ada', 'ada@example.test', '{{HASH}}', 'Ada', 1, 1, 1),
 (2, 'bob', 'bob@example.test', 'not-a-bcrypt-hash', '', 2, 0, 1),
 (3, 'cy', '', '{{HASH}}', '', 2, 0, 2);
INSERT INTO oauths(op, oauth_type, client_id, client_secret, auto_register, scopes, issuer) VALUES ('google', 'google', 'cid', 'sec', 1, '', 'https://accounts.google.com');
INSERT INTO user_thirds(user_id, open_id, email, op) VALUES (1, 'sub-1', 'ada@example.test', 'google');
INSERT INTO user_tokens(user_id, device_uuid, device_id, token, expired_at, created_at) VALUES
 (1, 'uu', '100', 'live-token', 4102444800, '2026-08-28 20:00:12.123456789+03:00'),
 (1, 'uu', '100', 'dead-token', 1000, '2026-08-28 20:00:12+03:00');
INSERT INTO address_book_collections(id, user_id, name) VALUES (7, 1, 'Team');
INSERT INTO address_book_collection_rules(user_id, collection_id, rule, type, to_id) VALUES (1, 7, 2, 1, 2), (1, 7, 1, 2, 2);
INSERT INTO address_books(id, alias, platform, tags, user_id, collection_id, force_always_relay) VALUES
 ('111', 'home', 'Windows', '[]', 1, 0, 0),
 ('222@other.example?key=K', '', '', '["field"]', 1, 7, 1),
 ('333', '', 'Android', '["loose"]', 1, 7, 0);
INSERT INTO tags(name, user_id, color, collection_id) VALUES ('field', 1, 4278190335, 7);
INSERT INTO peers(id, hostname, os, uuid, version, user_id, last_online_time, last_online_ip, created_at) VALUES ('100', 'mac', 'macos', 'uu', '1.5.3', 1, 1790000000, '10.0.0.1', '2026-08-28 20:00:12+03:00');
INSERT INTO audit_conns(action, conn_id, peer_id, from_peer, from_name, ip, type, close_time, created_at) VALUES ('new', 3, '100', '555', 'ada', '10.0.0.2', 0, 1790000100, '2026-10-01 10:00:00+03:00');
`

func TestLejianwen(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rustdeskapi.db")
	schema, err := os.ReadFile("testdata/lejianwen-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	src, err := sql.Open("sqlite", "file:"+srcPath)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := store.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(string(schema) + strings.ReplaceAll(fixture, "{{HASH}}", hash)); err != nil {
		t.Fatal(err)
	}
	src.Close()

	st, err := store.Open(filepath.Join(dir, "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Running twice must not duplicate anything.
	for i := 0; i < 2; i++ {
		rep, err := Lejianwen(st, srcPath)
		if err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		t.Log("\n" + rep)
	}

	if _, err := st.CheckPassword("ada", "password"); err != nil {
		t.Fatalf("bcrypt hash must keep working: %v", err)
	}
	ada, _ := st.UserByUsername("ada")
	bob, _ := st.UserByUsername("bob")
	if cy, _ := st.UserByUsername("cy"); !cy.Disabled || bob.HasPassword || !ada.IsAdmin || ada.DisplayName != "Ada" {
		t.Fatal("user fields not carried over")
	}
	if u, err := st.UserByIdentity("google", "sub-1"); err != nil || u.ID != ada.ID {
		t.Fatalf("identity: %v", err)
	}
	if p, err := st.OIDCProvider("google"); err != nil || p.ClientSecret != "sec" || !p.AutoRegister || p.Scopes != "openid email profile" {
		t.Fatalf("provider: %+v %v", p, err)
	}
	if u, tok, err := st.UserByToken("live-token"); err != nil || u.ID != ada.ID || tok.DeviceID != "100" {
		t.Fatalf("live token must be carried over: %v", err)
	}
	if _, _, err := st.UserByToken("dead-token"); err == nil {
		t.Fatal("expired token must not be carried over")
	}

	books, _ := st.ABsFor(ada.ID)
	if len(books) != 2 || !books[0].Personal || books[0].PeerCount != 1 || books[1].Name != "Team" || books[1].PeerCount != 2 {
		t.Fatalf("ada's books: %+v", books)
	}
	peers, _, _ := st.Peers(books[1].GUID, 0, 0)
	if peers[0]["id"] != "222@other.example?key=K" || peers[0]["forceAlwaysRelay"] != "true" || len(peers[0]["tags"].([]any)) != 1 || peers[1]["platform"] != "Android" {
		t.Fatalf("peers: %+v", peers)
	}
	// "field" keeps its colour; "loose" is used on an entry but was never defined and gets one.
	if tags, _ := st.Tags(books[1].GUID); len(tags) != 2 || tags[0].Color != 4278190335 || tags[1].Name != "loose" || tags[1].Color == 0 {
		t.Fatalf("tags: %+v", tags)
	}
	// bob: direct share (rule 2) wins over the group share (rule 1); cy gets the group share.
	bb, _ := st.ABsFor(bob.ID)
	if len(bb) != 2 || bb[1].Rule != store.RuleReadWrite {
		t.Fatalf("bob's books: %+v", bb)
	}
	if d, err := st.Device("100"); err != nil || d.Hostname != "mac" || d.User != "ada" || d.LastSeen != 1790000000 {
		t.Fatalf("device: %+v %v", d, err)
	}
	rows, total, _ := st.ConnAudits(10, 0)
	if total != 1 || rows[0].PeerID != "555" || rows[0].EndedAt != 1790000100 || rows[0].StartedAt == 0 {
		t.Fatalf("audit: %+v total %d", rows, total)
	}
	if n := st.CountUsers(); n != 3 {
		t.Fatalf("users duplicated: %d", n)
	}
}
