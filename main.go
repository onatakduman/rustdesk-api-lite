// rustdesk-api-lite is a small account, address-book and console server for
// self-hosted RustDesk installations.
package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/importer"
	"github.com/onatakduman/rustdesk-api-lite/internal/server"
	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

//go:embed web
var webFiles embed.FS

const usage = `rustdesk-api-lite — accounts, address books and a console for self-hosted RustDesk

Usage:
  rustdesk-api-lite serve                     run the server
  rustdesk-api-lite user add <name> [-admin]  create an account (password is read from stdin)
  rustdesk-api-lite user passwd <name>        set a password (read from stdin)
  rustdesk-api-lite user list                 list accounts
  rustdesk-api-lite token add <user> <name>   create an API token for an account (printed once)
  rustdesk-api-lite share <book> <user> <view|edit|full|none>
                                              share an address book with an account
  rustdesk-api-lite config                    show the server settings and the string that
                                              configures a RustDesk app for this server
  rustdesk-api-lite config set <name> <value> name: public-url, id-server, relay-server, key
  rustdesk-api-lite import-lejianwen <file>   import users, address books and devices from a
                                              lejianwen/rustdesk-api SQLite database
  rustdesk-api-lite version

Options (flag or environment variable):
  -db <path>         RDL_DB           database file (default ./data/rustdesk-api-lite.db)
  -listen <addr>     RDL_LISTEN       listen address (default 127.0.0.1:21114)
  -trust-proxy       RDL_TRUST_PROXY  trust X-Forwarded-* headers (set behind a reverse proxy)

Seeded into the settings on first start, editable in the console afterwards:
  RDL_PUBLIC_URL, RDL_ID_SERVER, RDL_RELAY_SERVER, RDL_KEY
`

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	fl.Usage = func() { fmt.Print(usage) }
	dbPath := fl.String("db", env("RDL_DB", "./data/rustdesk-api-lite.db"), "")
	listen := fl.String("listen", env("RDL_LISTEN", "127.0.0.1:21114"), "")
	trust := fl.Bool("trust-proxy", env("RDL_TRUST_PROXY", "") != "", "")
	admin := fl.Bool("admin", false, "")

	// Flags may come before or after the positional arguments.
	var pos []string
	for len(args) > 0 {
		_ = fl.Parse(args)
		args = fl.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}

	open := func() *store.Store {
		if dir := filepath.Dir(*dbPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				log.Fatalf("create %s: %v", dir, err)
			}
		}
		st, err := store.Open(*dbPath)
		if err != nil {
			log.Fatalf("open database: %v", err)
		}
		return st
	}

	switch cmd {
	case "serve":
		serve(open(), *listen, *trust)
	case "user":
		userCmd(open(), pos, *admin)
	case "token":
		tokenCmd(open(), pos)
	case "share":
		shareCmd(open(), pos)
	case "config":
		configCmd(open(), pos)
	case "import-lejianwen":
		if len(pos) != 1 {
			log.Fatal("usage: rustdesk-api-lite import-lejianwen <rustdeskapi.db>")
		}
		st := open()
		defer st.Close()
		rep, err := importer.Lejianwen(st, pos[0])
		if err != nil {
			log.Fatalf("import failed: %v", err)
		}
		fmt.Print(rep)
	case "version", "-v", "--version":
		fmt.Println(server.Version)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func serve(st *store.Store, listen string, trust bool) {
	defer st.Close()
	for key, envKey := range map[string]string{"public_url": "RDL_PUBLIC_URL", "id_server": "RDL_ID_SERVER", "relay_server": "RDL_RELAY_SERVER", "server_key": "RDL_KEY"} {
		if v := os.Getenv(envKey); v != "" && st.Setting(key) == "" {
			if err := st.SetSetting(key, strings.TrimSpace(v)); err != nil {
				log.Fatalf("store setting %s: %v", key, err)
			}
		}
	}
	if st.CountUsers() == 0 {
		pw := store.RandomToken(12)
		if err := st.CreateUser(&store.User{Username: "admin", IsAdmin: true}, pw, ""); err != nil {
			log.Fatalf("create first account: %v", err)
		}
		fmt.Printf("\n  First start: created the account \"admin\" with password  %s\n  Sign in and change it. This is shown only once.\n\n", pw)
	}
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	app := server.New(st, server.Config{TrustProxy: trust, Web: web})
	go app.Maintain()
	srv := &http.Server{
		Addr:              listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("rustdesk-api-lite %s listening on http://%s", server.Version, listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// tokenCmd creates an API token, for scripts on machines where nobody signs in to the console.
func tokenCmd(st *store.Store, pos []string) {
	defer st.Close()
	if len(pos) != 3 || pos[0] != "add" || strings.TrimSpace(pos[2]) == "" {
		log.Fatal("usage: rustdesk-api-lite token add <user> <name>")
	}
	u, err := st.UserByUsername(pos[1])
	if err != nil {
		log.Fatalf("no such user: %s", pos[1])
	}
	tok, err := st.NewToken(&store.Token{UserID: u.ID, Kind: "api", Name: strings.TrimSpace(pos[2])}, 0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(tok)
}

// shareCmd gives an account access to a shared address book, or takes it away.
func shareCmd(st *store.Store, pos []string) {
	defer st.Close()
	const usage = "usage: rustdesk-api-lite share <book> <user> <view|edit|full|none>"
	if len(pos) != 3 {
		log.Fatal(usage)
	}
	rule, ok := map[string]int{"none": 0, "view": store.RuleRead, "edit": store.RuleReadWrite, "full": store.RuleFull}[strings.ToLower(pos[2])]
	if !ok {
		log.Fatal(usage)
	}
	guid, err := st.ABByName(pos[0])
	if err != nil {
		log.Fatalf("no shared address book named %q", pos[0])
	}
	u, err := st.UserByUsername(pos[1])
	if err != nil {
		log.Fatalf("no such user: %s", pos[1])
	}
	shares, err := st.Shares(guid)
	if err != nil {
		log.Fatal(err)
	}
	kept := shares[:0]
	for _, sh := range shares {
		if sh.UserID != u.ID {
			kept = append(kept, sh)
		}
	}
	if rule > 0 {
		kept = append(kept, store.Share{UserID: u.ID, Rule: rule})
	}
	if err := st.SetShares(guid, kept); err != nil {
		log.Fatal(err)
	}
	if rule == 0 {
		fmt.Printf("%s: %s no longer has access\n", pos[0], u.Username)
		return
	}
	fmt.Printf("%s: %s now has %s access\n", pos[0], u.Username, strings.ToLower(pos[2]))
}

// The settings a RustDesk app needs to use this server, by their command-line names.
var configNames = []struct{ name, key string }{
	{"public-url", "public_url"}, {"id-server", "id_server"}, {"relay-server", "relay_server"}, {"key", "server_key"},
}

// configCmd shows or changes those settings without the console.
func configCmd(st *store.Store, pos []string) {
	defer st.Close()
	switch {
	case len(pos) == 0:
		for _, c := range configNames {
			fmt.Printf("%s=%s\n", c.name, st.Setting(c.key))
		}
		// One line that sets up an app: paste it into the app, or run `rustdesk --config <string>`.
		fmt.Printf("client-config=%s\n", server.EncodeClientConfig(st.Setting("id_server"), st.Setting("relay_server"), st.Setting("public_url"), st.Setting("server_key")))
	case len(pos) == 3 && pos[0] == "set":
		for _, c := range configNames {
			if c.name == pos[1] {
				if err := st.SetSetting(c.key, strings.TrimSpace(pos[2])); err != nil {
					log.Fatal(err)
				}
				fmt.Printf("%s=%s\n", c.name, strings.TrimSpace(pos[2]))
				return
			}
		}
		log.Fatalf("unknown setting %q; use public-url, id-server, relay-server or key", pos[1])
	default:
		log.Fatal("usage: rustdesk-api-lite config   |   config set <public-url|id-server|relay-server|key> <value>")
	}
}

func readPassword() string {
	if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Password (input is visible): ")
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 8 {
		log.Fatal("the password needs at least 8 characters")
	}
	return pw
}

func userCmd(st *store.Store, pos []string, admin bool) {
	defer st.Close()
	if len(pos) == 1 && pos[0] == "list" {
		us, err := st.ListUsers()
		if err != nil {
			log.Fatal(err)
		}
		for _, u := range us {
			role := "user"
			if u.IsAdmin {
				role = "admin"
			}
			state := ""
			if u.Disabled {
				state = " (disabled)"
			}
			fmt.Printf("%-24s %-6s %s%s\n", u.Username, role, u.Email, state)
		}
		return
	}
	if len(pos) != 2 {
		log.Fatal("usage: rustdesk-api-lite user add|passwd <name>   |   user list")
	}
	switch pos[0] {
	case "add":
		if err := st.CreateUser(&store.User{Username: pos[1], IsAdmin: admin}, readPassword(), ""); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("created %s\n", pos[1])
	case "passwd":
		u, err := st.UserByUsername(pos[1])
		if err != nil {
			log.Fatalf("no such user: %s", pos[1])
		}
		if err := st.SetPassword(u.ID, readPassword(), ""); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("password of %s changed; all their sessions were signed out\n", u.Username)
	default:
		log.Fatal("usage: rustdesk-api-lite user add|passwd <name>   |   user list")
	}
}
