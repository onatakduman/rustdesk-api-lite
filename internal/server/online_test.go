package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"slices"
	"strconv"
	"testing"
)

// fakeHbbs answers online queries the way hbbs does; the ids in up are online.
func fakeHbbs(t *testing.T, up ...string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				frame, err := readHbbFrame(conn, 1<<20)
				if err != nil {
					return
				}
				body, ok := pbField(frame, pbOnlineRequest)
				if !ok {
					return
				}
				var ids []string
				for rest := body; len(rest) > 0; {
					tag, n := binary.Uvarint(rest)
					rest = rest[n:]
					l, n := binary.Uvarint(rest)
					rest = rest[n:]
					if tag>>3 == pbOnlineRequestPeers {
						ids = append(ids, string(rest[:l]))
					}
					rest = rest[l:]
				}
				states := make([]byte, (len(ids)+7)/8)
				for i, id := range ids {
					if slices.Contains(up, id) {
						states[i/8] |= 1 << (7 - i%8)
					}
				}
				// A key exchange offer comes first on some servers; it has to be skipped.
				conn.Write(hbbFrame(pbBytes(nil, 25, []byte("offer"))))
				conn.Write(hbbFrame(pbBytes(nil, pbOnlineResponse, pbBytes(nil, pbOnlineResponseState, states))))
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port
}

func TestOnlineLookup(t *testing.T) {
	h := newHarness(t)
	h.user("ada", true)
	cookie := h.consoleLogin("ada")
	host, port := fakeHbbs(t, "111", "333", "n0", "n9", "n19")
	// The query goes one port below the rendezvous port, so the ID server is "one above" the fake.
	own := fmt.Sprintf("%s:%d", host, port+1)
	if err := h.st.SetSetting(keyIDServer, own); err != nil {
		t.Fatal(err)
	}
	ask := func(ids ...string) map[string]any {
		t.Helper()
		code, raw, v := h.call("POST", "/console/api/online", "", map[string]any{"ids": ids}, "Cookie", cookie, "X-RDL", "1")
		if code != 200 {
			t.Fatalf("online: %d %s", code, raw)
		}
		return v.(map[string]any)["online"].(map[string]any)
	}
	pinned := "333@" + own + "?key=a+b/c="
	got := ask("111", "222", pinned, "444/r", "555@10.1.2.3", "666@public")
	want := map[string]any{"111": true, "222": false, pinned: true, "444/r": false}
	if len(got) != len(want) {
		t.Fatalf("ids on a private address or on \"public\" must be left out: %v", got)
	}
	for id, on := range want {
		if got[id] != on {
			t.Fatalf("%s: got %v, want %v (all: %v)", id, got[id], on, got)
		}
	}
	// Bits run from the left of each byte, over more than two bytes.
	var many []string
	for i := range 20 {
		many = append(many, "n"+strconv.Itoa(i))
	}
	got = ask(many...)
	for i, id := range many {
		if on := i == 0 || i == 9 || i == 19; got[id] != on {
			t.Fatalf("%s: got %v, want %v", id, got[id], on)
		}
	}
	if code, _, _ := h.call("POST", "/console/api/online", "", map[string]any{"ids": []string{"111"}}, "X-RDL", "1"); code != 401 {
		t.Fatalf("anonymous online query: %d", code)
	}
	if err := h.st.SetSetting(keyOnlineLookup, "0"); err != nil {
		t.Fatal(err)
	}
	if got := ask("111"); len(got) != 0 {
		t.Fatalf("switched off, nothing may be asked: %v", got)
	}
}

func TestHbbFraming(t *testing.T) {
	for _, n := range []int{0, 1, 63, 64, 16383, 16384, 70000} {
		payload := bytes.Repeat([]byte{0xA5}, n)
		got, err := readHbbFrame(bytes.NewReader(hbbFrame(payload)), 1<<20)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("frame of %d bytes: %v, %d bytes back", n, err, len(got))
		}
	}
	if _, err := readHbbFrame(bytes.NewReader(hbbFrame(make([]byte, 5000))), 4096); err == nil {
		t.Fatal("a frame over the limit must be refused")
	}
}

func TestPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.0.10": false, "169.254.169.254": false,
		"100.100.1.1": false, "::1": false, "fe80::1": false, "fd00::1": false, "0.0.0.0": false, "::ffff:10.0.0.1": false,
	} {
		if got := publicIP(net.ParseIP(ip)); got != want {
			t.Errorf("publicIP(%s) = %v, want %v", ip, got, want)
		}
	}
	if id, server := splitPeerID("123/r@Host.Example:21116?key=K="); id != "123" || server != "host.example:21116" {
		t.Errorf("splitPeerID: %q %q", id, server)
	}
	if got := onlineAddr("host.example:21200"); got != "host.example:21199" {
		t.Errorf("onlineAddr with a port: %s", got)
	}
	if got := onlineAddr("host.example"); got != "host.example:21115" {
		t.Errorf("onlineAddr without a port: %s", got)
	}
}
