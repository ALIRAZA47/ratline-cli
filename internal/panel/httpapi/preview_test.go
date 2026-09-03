package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/cli"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/jobs"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/web"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// A live panel for looking at, with a fake server behind it.
//
// Skipped unless RATLINE_PANEL_PREVIEW=1, so it costs CI nothing. It exists because
// the alternative for anybody working on the interface is a Linux VM with root, a
// real ratline and a real nginx — which is a lot of setup to check that a table
// aligns. This serves the real embedded bundle against the real handler stack, with
// plausible data standing in for the ratline binary.
//
//	RATLINE_PANEL_PREVIEW=1 go test ./internal/panel/httpapi -run TestServeForPreview -v -timeout 0
//
// Then open http://127.0.0.1:8420 and claim it with any address and a long password.
func TestServeForPreview(t *testing.T) {
	if os.Getenv("RATLINE_PANEL_PREVIEW") != "1" {
		t.Skip("set RATLINE_PANEL_PREVIEW=1 to serve a live panel for looking at")
	}
	addr := os.Getenv("RATLINE_PANEL_PREVIEW_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8420"
	}

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck // a preview process exiting

	cfg := panel.Default()
	cfg.SourcePath = "preview"
	cfg.Session.SecureCookie = "never"
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen.Address = host
	if _, err := parsePort(port, cfg); err != nil {
		t.Fatal(err)
	}

	runner := &previewRunner{}
	client := &rl.Client{
		Binary: "/usr/bin/true", Runner: runner, Log: log.Discard(),
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, JobTimeout: time.Minute,
	}
	jm := jobs.New(st, client, log.Discard(), 1<<16, 50)
	if err := jm.Start(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	defer jm.Stop()

	srv, err := New(cfg, st, client, jm, log.New(log.Options{Out: os.Stderr, Level: log.LevelDebug}))
	if err != nil {
		t.Fatal(err)
	}
	ui, err := web.Handler()
	if err != nil {
		t.Fatal(err)
	}
	srv.UI = ui

	t.Logf("preview panel on http://%s — claim it with any address and a 12-character password", addr)
	//nolint:gosec // G114: a developer preview on the loopback, skipped in CI
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		t.Fatal(err)
	}
}

func parsePort(port string, cfg *panel.Config) (int, error) {
	n := 0
	for _, r := range port {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		n = n*10 + int(r-'0')
	}
	cfg.Listen.Port = n
	return n, nil
}

// previewRunner answers as a small server with three sites would.
type previewRunner struct{}

func (p *previewRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	verb := strings.Join(c.Args, " ")
	switch {
	case strings.HasPrefix(verb, "schema"):
		raw, err := json.Marshal(cli.BuildSchema(cli.NewRootCommand(&cli.Globals{})))
		if err != nil {
			return nil, err
		}
		return &system.Result{Args: c.Args, Stdout: string(raw)}, nil
	case strings.HasPrefix(verb, "status"):
		return canned(c, previewStatus)
	case strings.HasPrefix(verb, "site list"):
		return canned(c, previewSites)
	case strings.HasPrefix(verb, "site show"):
		return canned(c, `{"domain":"api.example.com","owner":"acme","runtime":"python",
			"unit":"ratline-acme-api-example-com.service","state":"active","socket":
			"/run/ratline/acme-api-example-com/app.sock","app_module":"app.main:app",
			"workers":3,"tls":"letsencrypt, 68 days","last_deploy_at":"2026-08-20T09:14:00Z"}`)
	case strings.HasPrefix(verb, "site env list"):
		return canned(c, `{"domain":"api.example.com","env":{"DATABASE_URL":"********",
			"LOG_LEVEL":"info","SENTRY_DSN":"********"},"revealed":false}`)
	case strings.HasPrefix(verb, "site logs"):
		return &system.Result{Args: c.Args, Stdout: previewLogs}, nil
	case strings.HasPrefix(verb, "user list"):
		return canned(c, `{"users":[
			{"name":"acme","home":"/home/acme","shell":"/bin/bash","disabled":false},
			{"name":"blog","home":"/home/blog","shell":"/usr/sbin/nologin","disabled":false}]}`)
	case strings.HasPrefix(verb, "cert list"):
		return canned(c, `{"certificates":[
			{"name":"api.example.com","source":"letsencrypt","not_after":"2026-11-04T00:00:00Z",
			 "attached_sites":["api.example.com"]},
			{"name":"www.example.com","source":"letsencrypt","not_after":"2026-09-09T00:00:00Z",
			 "attached_sites":["www.example.com"]}]}`)
	case strings.HasPrefix(verb, "key list"):
		return canned(c, `{"keys":[
			{"label":"dana laptop","scope":"user","user":"acme","fingerprint":"SHA256:9xK2…"},
			{"label":"ci deploy","scope":"site","site":"api.example.com","fingerprint":"SHA256:h7Q1…"}]}`)
	case strings.HasPrefix(verb, "db list"):
		return canned(c, `{"databases":[{"name":"acme_app","owner":"acme",
			"server":"mongodb://127.0.0.1:27017","users":[{"username":"acme_app_rw"}]}],"source":"server"}`)
	case strings.HasPrefix(verb, "runtime list"):
		return canned(c, `{"runtimes":[
			{"runtime":"node","version":"22.11.0","path":"/opt/ratline/runtimes/node/22.11.0","default":true},
			{"runtime":"python","version":"3.12.4","path":"/opt/ratline/runtimes/python/3.12.4","default":true}]}`)
	default:
		// Every mutation reports success and takes a moment, so a job in the
		// interface actually has something to stream.
		if c.Stderr != nil {
			for _, line := range strings.Split(previewLogs, "\n") {
				_, _ = c.Stderr.Write([]byte(line + "\n"))
				time.Sleep(120 * time.Millisecond)
			}
		}
		return canned(c, `{"ok":true,"note":"this is a preview; nothing happened"}`)
	}
}

func canned(c system.Cmd, data string) (*system.Result, error) {
	return &system.Result{Args: c.Args, Stdout: `{"ok":true,"command":"ratline",` +
		`"version":"v0.14.1","data":` + data + `}`}, nil
}

const previewStatus = `{"hostname":"vps-fra-01","version":"v0.14.1","os":"Ubuntu 24.04",
	"uptime":"18 days","users":2,"keys":4,"sites":3,"certificates":2,"jobs":1,"workers":1,
	"problems":1,
	"sites_detail":[
	  {"domain":"api.example.com","owner":"acme","runtime":"python","state":"running",
	   "tls":"letsencrypt","health":"ok","needs_attention":false},
	  {"domain":"www.example.com","owner":"acme","runtime":"static","state":"serving",
	   "tls":"letsencrypt","needs_attention":false},
	  {"domain":"edge.example.com","owner":"blog","runtime":"bun","state":"failed",
	   "detail":"the unit exited 1 four times in a minute","tls":"none",
	   "needs_attention":true}],
	"certificates_detail":[
	  {"name":"www.example.com","status":"expiring","days_remaining":12}],
	"warnings":["edge.example.com is failing to start",
	            "www.example.com renews in 12 days and its last attempt failed"]}`

const previewSites = `{"sites":[
	{"domain":"api.example.com","user":"acme","runtime":"python","enabled":true,
	 "last_deploy_at":"2026-08-27T09:14:00Z"},
	{"domain":"www.example.com","user":"acme","runtime":"static","enabled":true,
	 "last_deploy_at":"2026-08-19T16:02:00Z"},
	{"domain":"edge.example.com","user":"blog","runtime":"bun","enabled":false}]}`

const previewLogs = `staging the nginx vhost
nginx -t: configuration file /etc/nginx/nginx.conf test is successful
installing dependencies as acme
  added 412 packages in 9s
building
  vite v8.2.2 building for production...
  ✓ 214 modules transformed
restarting ratline-acme-api-example-com.service
waiting for the socket to answer
health check: 200 in 41ms
done`
