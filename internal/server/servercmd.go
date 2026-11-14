package server

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// The open-source hbbs and hbbr accept plain-text administration commands
// ("h" lists them) from the loopback interface only: hbbs on its NAT-test
// port (21115), hbbr on its relay port (21117).

func (s *Server) cmdAddr(target string) string {
	key, def := keyHbbsCmd, "127.0.0.1:21115"
	if target == "hbbr" {
		key, def = keyHbbrCmd, "127.0.0.1:21117"
	}
	if v := s.st.Setting(key); v != "" {
		return v
	}
	return def
}

// cServerCmd sends one command to hbbs or hbbr and returns the answer.
func (s *Server) cServerCmd(c *consoleCtx) {
	var in struct{ Target, Cmd string }
	if !c.body(&in) {
		return
	}
	in.Cmd = strings.TrimSpace(in.Cmd)
	if (in.Target != "hbbs" && in.Target != "hbbr") || in.Cmd == "" || len(in.Cmd) > 1000 || strings.ContainsAny(in.Cmd, "\r\n") {
		c.fail(http.StatusBadRequest, "choose a server and type one command")
		return
	}
	conn, err := net.DialTimeout("tcp", s.cmdAddr(in.Target), 2*time.Second)
	if err != nil {
		c.fail(http.StatusBadGateway, "could not reach "+in.Target+" at "+s.cmdAddr(in.Target)+" (it must run on this machine)")
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte(in.Cmd)); err != nil {
		c.fail(http.StatusBadGateway, "could not send the command")
		return
	}
	out, _ := io.ReadAll(io.LimitReader(conn, 256<<10))
	c.ok(map[string]string{"output": string(out)})
}
