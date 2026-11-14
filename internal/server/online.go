package server

// Online state of devices, asked from the ID server (hbbs) each of them
// registers with. It is the query the RustDesk app sends for its own address
// book: an OnlineRequest on the TCP port below the rendezvous port, answered
// with one bit per id. No key is needed for it.

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	onlineTTL      = 20 * time.Second // how long an answer is reused
	onlineBackoff  = 30 * time.Second // pause after a server did not answer
	onlineTimeout  = 4 * time.Second  // connect, and the whole exchange
	onlineMaxIDs   = 5000             // ids looked at per request
	onlineMaxHosts = 16               // ID servers asked per request
	// rendezvousPort is the ID server's default port; the query goes to the one below it.
	rendezvousPort = 21116
)

// --- wire format ---

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// pbBytes appends a length-delimited protobuf field.
func pbBytes(b []byte, field int, data []byte) []byte {
	b = pbVarint(b, uint64(field)<<3|2)
	b = pbVarint(b, uint64(len(data)))
	return append(b, data...)
}

// pbField returns the payload of the first length-delimited field numbered want.
func pbField(msg []byte, want int) ([]byte, bool) {
	for len(msg) > 0 {
		tag, n := binary.Uvarint(msg)
		if n <= 0 {
			return nil, false
		}
		msg = msg[n:]
		switch tag & 7 {
		case 0:
			_, n := binary.Uvarint(msg)
			if n <= 0 {
				return nil, false
			}
			msg = msg[n:]
		case 1:
			if len(msg) < 8 {
				return nil, false
			}
			msg = msg[8:]
		case 5:
			if len(msg) < 4 {
				return nil, false
			}
			msg = msg[4:]
		case 2:
			l, n := binary.Uvarint(msg)
			if n <= 0 || uint64(len(msg)-n) < l {
				return nil, false
			}
			body := msg[n : n+int(l)]
			if int(tag>>3) == want {
				return body, true
			}
			msg = msg[n+int(l):]
		default:
			return nil, false
		}
	}
	return nil, false
}

// Field numbers of rendezvous.proto.
const (
	pbOnlineRequest       = 23 // RendezvousMessage.online_request
	pbOnlineResponse      = 24 // RendezvousMessage.online_response
	pbOnlineRequestPeers  = 2  // OnlineRequest.peers
	pbOnlineResponseState = 1  // OnlineResponse.states
)

func onlineRequest(ids []string) []byte {
	var inner []byte
	for _, id := range ids {
		inner = pbBytes(inner, pbOnlineRequestPeers, []byte(id))
	}
	return pbBytes(nil, pbOnlineRequest, inner)
}

// hbbFrame puts the length prefix of RustDesk's stream framing in front of a
// message: the length shifted left by two, little endian, with the number of
// extra header bytes in the two low bits.
func hbbFrame(payload []byte) []byte {
	n := uint32(len(payload))
	var head []byte
	switch {
	case n <= 0x3F:
		head = []byte{byte(n << 2)}
	case n <= 0x3FFF:
		v := n<<2 | 1
		head = []byte{byte(v), byte(v >> 8)}
	case n <= 0x3FFFFF:
		v := n<<2 | 2
		head = []byte{byte(v), byte(v >> 8), byte(v >> 16)}
	default:
		v := n<<2 | 3
		head = []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	}
	return append(head, payload...)
}

func readHbbFrame(r io.Reader, max int) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:1]); err != nil {
		return nil, err
	}
	headLen := int(h[0]&3) + 1
	if _, err := io.ReadFull(r, h[1:headLen]); err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint32(h[:]) >> 2)
	if n > max {
		return nil, errors.New("answer too large")
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// askOnline sends one query over conn and returns a state per id.
func askOnline(conn net.Conn, ids []string) ([]bool, error) {
	_ = conn.SetDeadline(time.Now().Add(onlineTimeout))
	if _, err := conn.Write(hbbFrame(onlineRequest(ids))); err != nil {
		return nil, err
	}
	for range 3 { // a server may put another message first, such as a key exchange offer
		frame, err := readHbbFrame(conn, len(ids)/8+4096)
		if err != nil {
			return nil, err
		}
		body, ok := pbField(frame, pbOnlineResponse)
		if !ok {
			continue
		}
		states, _ := pbField(body, pbOnlineResponseState)
		out := make([]bool, len(ids))
		for i := range ids {
			if i/8 < len(states) {
				out[i] = states[i/8]&(1<<(7-i%8)) != 0 // bits run from the left of each byte
			}
		}
		return out, nil
	}
	return nil, errors.New("no answer to the online query")
}

// --- which server, which address ---

// splitPeerID takes an address-book id apart: "123", "123@host", "123@host:port?key=K"
// and the forced-relay spellings "123/r@host" and "123@host/r".
func splitPeerID(full string) (id, server string) {
	id = full
	if i := strings.IndexByte(full, '@'); i >= 0 {
		id, server = full[:i], full[i+1:]
		if j := strings.IndexByte(server, '?'); j >= 0 {
			server = server[:j]
		}
	}
	id = strings.TrimSuffix(strings.TrimSuffix(id, "/r"), `\r`)
	server = strings.TrimSuffix(server, "/r")
	return strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(server))
}

// onlineAddr is where an ID server answers online queries: one port below its rendezvous port.
func onlineAddr(server string) string {
	host, port := server, rendezvousPort
	if h, p, err := net.SplitHostPort(server); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 1 && n < 65536 {
			host, port = h, n
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(port-1))
}

var cgnat = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// publicIP reports whether ip is an ordinary internet address. Address-book
// entries name the servers that get asked, so everything else is refused:
// the console must not be usable to reach into the network it runs in.
func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip))
}

// dialOnline connects to an ID server. The address is checked when the
// connection is made, after name resolution, so a name cannot point somewhere
// else between the check and the connection. anyAddress lifts the check for
// the installation's own ID server, which may well sit on a private network.
func dialOnline(ctx context.Context, addr string, anyAddress bool) (net.Conn, error) {
	d := net.Dialer{Timeout: onlineTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || (!anyAddress && !publicIP(ip)) {
			return errors.New("address not allowed")
		}
		return nil
	}}
	return d.DialContext(ctx, "tcp", addr)
}

// --- cache and lookup ---

type onlineState struct {
	on bool
	at time.Time
}

type onlineServer struct {
	mu       sync.Mutex
	ids      map[string]onlineState
	failedAt time.Time
}

type onlineCache struct {
	mu      sync.Mutex
	servers map[string]*onlineServer
	dial    func(ctx context.Context, addr string, anyAddress bool) (net.Conn, error)
}

func newOnlineCache() *onlineCache {
	return &onlineCache{servers: map[string]*onlineServer{}, dial: dialOnline}
}

func (c *onlineCache) server(name string) *onlineServer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.servers) > 256 { // bound memory; the entries are only a cache
		c.servers = map[string]*onlineServer{}
	}
	sv := c.servers[name]
	if sv == nil {
		sv = &onlineServer{ids: map[string]onlineState{}}
		c.servers[name] = sv
	}
	return sv
}

// lookup returns the state of ids on one ID server; ids it could not learn
// about are left out. One query at a time goes to a server.
func (c *onlineCache) lookup(ctx context.Context, server string, ids []string, anyAddress bool) map[string]bool {
	sv := c.server(server)
	sv.mu.Lock()
	defer sv.mu.Unlock()
	now := time.Now()
	var stale []string
	seen := map[string]bool{}
	for _, id := range ids {
		if st, ok := sv.ids[id]; (!ok || now.Sub(st.at) > onlineTTL) && !seen[id] {
			seen[id] = true
			stale = append(stale, id)
		}
	}
	if len(stale) > 0 && now.Sub(sv.failedAt) > onlineBackoff {
		states, err := func() ([]bool, error) {
			conn, err := c.dial(ctx, onlineAddr(server), anyAddress)
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			return askOnline(conn, stale)
		}()
		if err != nil {
			sv.failedAt = now
		} else {
			if len(sv.ids) > 4*onlineMaxIDs {
				sv.ids = map[string]onlineState{}
			}
			for i, id := range stale {
				sv.ids[id] = onlineState{states[i], now}
			}
		}
	}
	out := map[string]bool{}
	for _, id := range ids {
		if st, ok := sv.ids[id]; ok && now.Sub(st.at) <= onlineTTL {
			out[id] = st.on
		}
	}
	return out
}

// onlineStates answers for address-book ids as the console holds them. The
// result is keyed by those same ids; an id is missing when its server could
// not be asked.
func (s *Server) onlineStates(ctx context.Context, full []string) map[string]bool {
	own := strings.ToLower(strings.TrimSpace(s.st.Setting(keyIDServer)))
	type peer struct{ full, id string }
	byServer := map[string][]peer{}
	for _, f := range full {
		id, server := splitPeerID(f)
		if server == "" {
			server = own
		}
		// "public" stands for RustDesk's own servers in the app; it is not a host name.
		if id == "" || server == "" || server == "public" {
			continue
		}
		if _, known := byServer[server]; !known && len(byServer) >= onlineMaxHosts {
			continue
		}
		byServer[server] = append(byServer[server], peer{f, id})
	}
	out := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for server, peers := range byServer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids := make([]string, len(peers))
			for i, p := range peers {
				ids[i] = p.id
			}
			states := s.online.lookup(ctx, server, ids, server == own)
			mu.Lock()
			defer mu.Unlock()
			for _, p := range peers {
				if on, ok := states[p.id]; ok {
					out[p.full] = on
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// cOnline: POST {ids: [...]} -> {online: {id: bool}}.
func (s *Server) cOnline(c *consoleCtx) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !c.body(&in) {
		return
	}
	if s.st.Setting(keyOnlineLookup) == "0" {
		c.ok(map[string]any{"online": map[string]bool{}})
		return
	}
	if len(in.IDs) > onlineMaxIDs {
		in.IDs = in.IDs[:onlineMaxIDs]
	}
	ctx, cancel := context.WithTimeout(c.r.Context(), onlineTimeout+time.Second)
	defer cancel()
	c.ok(map[string]any{"online": s.onlineStates(ctx, in.IDs)})
}
