package unit

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

func testManager() *Manager {
	return &Manager{Cfg: config.Default(), Log: log.Discard()}
}

func pythonSite() *state.Site {
	return &state.Site{
		Domain: "api.example.com", Owner: "alice", Runtime: "python", Slug: "alice-api_example_com",
		Enabled: true, AppModule: "app.main:app", ASGI: true, Workers: 3,
		Listen: "socket", AppServer: "gunicorn", Instances: 1,
	}
}

func render(t *testing.T, site *state.Site, exec string, opts RenderOptions) string {
	t.Helper()
	body, err := testManager().Render(site, exec, opts)
	if err != nil {
		t.Fatalf("Render = %v", err)
	}
	return string(body)
}

func TestUnitInvariants(t *testing.T) {
	out := render(t, pythonSite(), "/home/alice/api.example.com/venv/bin/gunicorn app.main:app", RenderOptions{})

	for _, want := range []string{
		"# managed-by: ratline",
		"Description=ratline site api.example.com (python) owned by alice",
		// PartOf is what makes `systemctl stop ratline.target` reach every site.
		"PartOf=ratline.target",
		"User=alice",
		"Group=alice",
		"WorkingDirectory=/home/alice/api.example.com/app",
		// The tenant's secrets reach the application without nginx ever being
		// able to serve them.
		"EnvironmentFile=-/home/alice/api.example.com/.env",
		"RuntimeDirectory=ratline/alice-api_example_com",
		"RuntimeDirectoryMode=0750",
		"Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the unit is missing %q:\n%s", want, out)
		}
	}
}

func TestUnitUsesTypeExecRatherThanNotify(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{})
	// Neither gunicorn nor a plain Node server implements sd_notify, so
	// Type=notify would hang until the start timeout and then report a failure.
	// Matched at the start of a line, since the template explains the choice in a
	// comment that names the rejected value.
	if strings.Contains(out, "\nType=notify") {
		t.Error("Type=notify would never receive a readiness notification from gunicorn")
	}
	if !strings.Contains(out, "Type=exec") {
		t.Error("expected Type=exec")
	}
}

// A socket at 0640 is the classic silent 502: connect(2) needs write permission
// on the socket inode, and nginx is only in the tenant's group.
func TestSocketSitesUseAGroupWritableUmask(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{})
	if !strings.Contains(out, "UMask=0007") {
		t.Errorf("a socket site must use UMask=0007 so the socket lands at 0660:\n%s", out)
	}

	// A port site has no socket, so the tighter umask applies.
	portSite := pythonSite()
	portSite.Listen = "port"
	portSite.Port = 20001
	out = render(t, portSite, "/venv/bin/gunicorn app:app", RenderOptions{})
	if !strings.Contains(out, "UMask=0027") {
		t.Errorf("a port site should use the default umask:\n%s", out)
	}
}

func TestUnitResourceLimits(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{})
	for _, want := range []string{
		"MemoryMax=512M",
		// MemoryHigh throttles before MemoryMax kills, turning a hard OOM into
		// back pressure the application may survive.
		"MemoryHigh=448M",
		"MemoryAccounting=true",
		"CPUQuota=100%",
		"TasksMax=256",
		"LimitNOFILE=8192",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the unit is missing the limit %q:\n%s", want, out)
		}
	}
}

func TestUnitPerSiteLimitsOverrideTheDefaults(t *testing.T) {
	site := pythonSite()
	site.MemoryMax = "1G"
	site.CPUQuota = "200%"
	out := render(t, site, "/venv/bin/gunicorn app:app", RenderOptions{})
	if !strings.Contains(out, "MemoryMax=1G") || !strings.Contains(out, "CPUQuota=200%") {
		t.Errorf("per-site limits were not applied:\n%s", out)
	}
	if !strings.Contains(out, "MemoryHigh=896M") {
		t.Error("MemoryHigh was not recomputed from the new ceiling")
	}
}

func TestUnitHardening(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{})
	for _, want := range []string{
		"NoNewPrivileges=true",
		"PrivateTmp=true",
		"PrivateDevices=true",
		"ProtectSystem=strict",
		"ProtectHome=tmpfs",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectControlGroups=true",
		"RestrictNamespaces=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"SystemCallFilter=@system-service",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the hardening directive %q is missing:\n%s", want, out)
		}
	}
	// ProtectHome=tmpfs replaces /home in the namespace, so the site directory
	// has to be mounted back in or the application cannot see its own code.
	if !strings.Contains(out, "BindPaths=/home/alice/api.example.com") {
		t.Error("the site directory is not bound back in under ProtectHome=tmpfs")
	}
	if !strings.Contains(out, "ReadWritePaths=/home/alice/api.example.com/logs /home/alice/api.example.com/tmp") {
		t.Error("the writable paths are missing")
	}
	// A private /tmp is pointless if the application still writes to the shared one.
	if !strings.Contains(out, "TMPDIR=/home/alice/api.example.com/tmp") {
		t.Error("TMPDIR does not point at the site's own directory")
	}
}

func TestNodeRelaxesMemoryDenyWriteExecuteByDefault(t *testing.T) {
	site := &state.Site{
		Domain: "app.example.com", Owner: "bob", Runtime: "node", Slug: "bob-app_example_com",
		Enabled: true, Entry: "server.js", Listen: "socket", Instances: 1,
	}
	out := render(t, site, "/opt/ratline/runtimes/node/22/bin/node /home/bob/app.example.com/app/server.js", RenderOptions{})
	// V8 needs writable-executable memory for its JIT, so shipping this
	// directive enabled would mean every Node site failed on first start.
	if strings.Contains(out, "\nMemoryDenyWriteExecute=true") {
		t.Error("MemoryDenyWriteExecute is enabled for a Node site, which breaks V8's JIT")
	}
	if !strings.Contains(out, "# MemoryDenyWriteExecute=true — relaxed for this site") {
		t.Errorf("the relaxed directive is not documented in the unit:\n%s", out)
	}
	// Everything else stays on.
	if !strings.Contains(out, "ProtectSystem=strict") {
		t.Error("relaxing one directive dropped the rest of the sandbox")
	}
}

func TestExplicitRelaxIsRecordedInTheUnit(t *testing.T) {
	site := pythonSite()
	site.Relaxed = []string{"SystemCallFilter"}
	out := render(t, site, "/venv/bin/gunicorn app:app", RenderOptions{})
	if strings.Contains(out, "\nSystemCallFilter=@system-service") {
		t.Error("the relaxed directive is still active")
	}
	if !strings.Contains(out, "Relaxed for this site: SystemCallFilter") {
		t.Errorf("the unit does not record what was relaxed:\n%s", out)
	}
}

func TestExecReloadAndStartPost(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{
		ExecReload:    "/bin/kill -HUP $MAINPID",
		ExecStartPost: []string{"+/bin/sh -c 'chmod 0660 /run/x.sock'"},
	})
	if !strings.Contains(out, "ExecReload=/bin/kill -HUP $MAINPID") {
		t.Error("ExecReload is missing, so a graceful reload is impossible")
	}
	if !strings.Contains(out, "ExecStartPost=+/bin/sh -c 'chmod 0660 /run/x.sock'") {
		t.Error("ExecStartPost is missing")
	}
}

func TestRenderRejectsABadMemoryLimit(t *testing.T) {
	site := pythonSite()
	site.MemoryMax = "512Q"
	if _, err := testManager().Render(site, "/venv/bin/gunicorn app:app", RenderOptions{}); err == nil {
		t.Fatal("Render accepted an invalid memory size")
	}
}

func TestUnitDefaultsToTypeExecWithNoPIDFile(t *testing.T) {
	out := render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{})
	if !strings.Contains(out, "Type=exec") {
		t.Errorf("want Type=exec by default:\n%s", out)
	}
	// A PIDFile on a Type=exec unit makes systemd wait for a file that never
	// appears, so it must be absent unless something actually forks.
	if strings.Contains(out, "PIDFile=") {
		t.Errorf("Type=exec must not carry a PIDFile:\n%s", out)
	}
}

func TestUnitRendersAForkingPM2Service(t *testing.T) {
	site := pythonSite()
	site.Runtime, site.Domain = "node", "app.example.com"
	out := render(t, site, "/opt/ratline/runtimes/node/22/bin/pm2 start /home/alice/app.example.com/.ratline/ecosystem.config.json",
		RenderOptions{
			Type:        "forking",
			PIDFile:     "/home/alice/app.example.com/.pm2/pm2.pid",
			ExecReload:  "/opt/ratline/runtimes/node/22/bin/pm2 reload /home/alice/app.example.com/.ratline/ecosystem.config.json --update-env",
			ExecStop:    "/opt/ratline/runtimes/node/22/bin/pm2 kill",
			Environment: []string{"PM2_HOME=/home/alice/app.example.com/.pm2"},
		})

	for _, want := range []string{
		// PM2 daemonises, so systemd has to be told to expect the fork and where
		// to read the surviving process's pid.
		"Type=forking",
		"PIDFile=/home/alice/app.example.com/.pm2/pm2.pid",
		// The reload is the whole reason PM2 is the default supervisor.
		"ExecReload=/opt/ratline/runtimes/node/22/bin/pm2 reload",
		// Without ExecStop the daemon survives `systemctl stop`.
		"ExecStop=/opt/ratline/runtimes/node/22/bin/pm2 kill",
		"Environment=PM2_HOME=/home/alice/app.example.com/.pm2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the unit is missing %q:\n%s", want, out)
		}
	}

	// The extra supervision layer must not cost the kernel-enforced ceiling: a
	// cgroup contains every descendant, so the limits still cover PM2's workers.
	for _, want := range []string{"MemoryMax=", "TasksMax=", "CPUQuota="} {
		if !strings.Contains(out, want) {
			t.Errorf("PM2 supervision must keep %s enforced:\n%s", want, out)
		}
	}
}
