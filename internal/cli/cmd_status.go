package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
)

// `status` answers the question an operator has on connecting to a server they have
// not touched in a month: what is on here, and is any of it broken.
//
// It is not `doctor`. doctor runs every check and prints what is wrong, which means
// a healthy server produces no output — correct, but it does not say what the server
// *is*. status inverts that: it always prints the inventory and marks the entries
// that need attention. doctor is what a cron job runs; status is what a human runs
// first.

// ServerStatus is the whole picture, in a shape worth serialising.
type ServerStatus struct {
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version"`
	OS       string `json:"os,omitempty"`
	Uptime   string `json:"uptime,omitempty"`

	Users        int `json:"users"`
	Keys         int `json:"keys"`
	Sites        int `json:"sites"`
	Certificates int `json:"certificates"`
	Problems     int `json:"problems"`

	SiteRows []SiteStatusRow `json:"sites_detail"`
	CertRows []CertStatusRow `json:"certificates_detail,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

// SiteStatusRow is one site's line.
type SiteStatusRow struct {
	Domain         string `json:"domain"`
	Owner          string `json:"owner"`
	Runtime        string `json:"runtime"`
	State          string `json:"state"`
	Detail         string `json:"detail,omitempty"`
	TLS            string `json:"tls"`
	NeedsAttention bool   `json:"needs_attention"`
}

// CertStatusRow is one certificate worth mentioning on a summary screen.
type CertStatusRow struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Days   int    `json:"days_remaining"`
}

func newStatusCommand(g *Globals) *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Show everything on this server on one screen",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		Long: "The inventory and the health of it: tenants, sites and what state each one is\n" +
			"in, certificates that need attention, and a count of anything 'ratline doctor'\n" +
			"would report.\n\n" +
			"Unlike doctor, this always prints. doctor says what is wrong; status says what\n" +
			"is here.",
		Example: "  ratline status\n" +
			"  ratline status --json | jq '.sites_detail[] | select(.needs_attention)'",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := g.collectStatus(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(st)
			}
			return g.printStatus(st, quiet)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Only the summary counts, without the per-site table")
	return cmd
}

// collectStatus gathers the inventory.
func (g *Globals) collectStatus(ctx context.Context) (*ServerStatus, error) {
	store, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	users, err := store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := store.ListSites(ctx, state.SiteFilter{})
	if err != nil {
		return nil, err
	}
	keys, err := store.ListKeys(ctx, state.KeyFilter{})
	if err != nil {
		return nil, err
	}
	mgr, err := g.siteManager(ctx)
	if err != nil {
		return nil, err
	}

	out := &ServerStatus{
		Hostname: g.Cfg.Server.Hostname,
		Version:  buildinfo.Version,
		OS:       g.OS.PrettyName,
		Uptime:   uptimeHuman(),
		Users:    len(users),
		Keys:     len(keys),
		Sites:    len(sites),
	}
	if out.Hostname == "" {
		out.Hostname, _ = store.GetServerValue(ctx, "hostname")
	}

	// Which domains have a certificate attached, so the table can say so without
	// one query per site.
	secured := map[string]tls.Status{}
	if certMgr, err := g.certManager(ctx); err == nil {
		if rows, err := certMgr.List(ctx, 0, false); err == nil {
			out.Certificates = len(rows)
			for _, row := range rows {
				for _, domain := range row.Attached {
					secured[domain] = row.Status
				}
				// Anything not simply valid belongs on a summary screen: an expiry
				// nobody noticed is the most common way a working site goes down.
				if row.Status != tls.StatusValid {
					out.CertRows = append(out.CertRows, CertStatusRow{
						Name: row.Name, Status: string(row.Status), Days: row.Days,
					})
				}
			}
		}
	}
	sort.Slice(out.CertRows, func(i, j int) bool { return out.CertRows[i].Days < out.CertRows[j].Days })

	for _, s := range sites {
		row := SiteStatusRow{Domain: s.Domain, Owner: s.Owner, Runtime: s.Runtime, TLS: "http"}
		if status, ok := secured[s.Domain]; ok {
			row.TLS = "https"
			if status != tls.StatusValid {
				row.TLS = "https (" + string(status) + ")"
				row.NeedsAttention = true
			}
		}
		fillSiteState(ctx, mgr, s, &row)
		out.SiteRows = append(out.SiteRows, row)
	}

	// The problem count comes from doctor itself rather than a second
	// implementation, so the two commands can never disagree about whether this
	// server is healthy.
	if findings, err := g.diagnose(ctx, doctorOptions{}); err == nil {
		for _, f := range findings {
			if f.Severity == "problem" {
				out.Problems++
			}
		}
	} else {
		out.Warnings = append(out.Warnings, "the health checks could not run: "+firstLine(err.Error()))
	}
	return out, nil
}

// fillSiteState works out what a site is actually doing.
func fillSiteState(ctx context.Context, mgr *site.Manager, s *state.Site, row *SiteStatusRow) {
	if !s.Enabled {
		row.State = "disabled"
		row.NeedsAttention = true
		return
	}
	if !s.Dynamic() {
		// A static site has no process, so there is nothing further to ask: nginx
		// having accepted the configuration is the whole of its health.
		row.State = "serving"
		return
	}
	status, err := mgr.Unit.Status(ctx, s)
	if err != nil || status == nil {
		row.State = "unknown"
		row.Detail = "the unit could not be queried"
		row.NeedsAttention = true
		return
	}
	switch status.Active {
	case "active":
		row.State = "running"
	case "failed":
		row.State = "failed"
		row.NeedsAttention = true
	default:
		row.State = status.Active
		row.NeedsAttention = true
	}

	// PM2's restart counter, because systemd's stays at zero on a PM2 site: a
	// crash-looping application would otherwise read as healthy here.
	if report, err := mgr.ProcessReport(ctx, s); err == nil && report != nil {
		switch {
		case report.Restarts >= 10:
			row.Detail = fmt.Sprintf("pm2 has restarted a worker %d times", report.Restarts)
			row.NeedsAttention = true
		case report.Instances > 0 && report.Online < report.Instances:
			row.Detail = fmt.Sprintf("%d of %d workers online", report.Online, report.Instances)
			row.NeedsAttention = true
		case report.Online > 1:
			row.Detail = fmt.Sprintf("%d workers", report.Online)
		}
		return
	}
	if status.NRestarts != "" && status.NRestarts != "0" {
		row.Detail = "restarted " + status.NRestarts + " time(s)"
		row.NeedsAttention = true
	}
}

// printStatus renders the screen.
func (g *Globals) printStatus(st *ServerStatus, quiet bool) error {
	header := st.Hostname
	if header == "" {
		header = "this server"
	}
	g.Printf("%s — ratline %s\n", header, st.Version)
	var context []string
	if st.OS != "" {
		context = append(context, st.OS)
	}
	if st.Uptime != "" {
		context = append(context, "up "+st.Uptime)
	}
	if len(context) > 0 {
		g.Printf("%s\n", strings.Join(context, ", "))
	}
	g.Printf("\n%s, %s, %s, %s\n",
		plural(st.Users, "tenant"), plural(st.Sites, "site"),
		plural(st.Keys, "SSH key"), plural(st.Certificates, "certificate"))

	if len(st.SiteRows) == 0 {
		g.Printf("\nNo sites yet. Create one:\n" +
			"  ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub\n" +
			"  ratline site add app.example.com --user acme --runtime static\n")
	} else if !quiet {
		g.Printf("\n")
		table := g.Table("", "DOMAIN", "OWNER", "RUNTIME", "STATE", "TLS", "NOTE")
		for _, r := range st.SiteRows {
			marker := " "
			if r.NeedsAttention {
				marker = "!"
			}
			table.Row(marker, r.Domain, r.Owner, r.Runtime, r.State, r.TLS, r.Detail)
		}
		if err := table.Render(); err != nil {
			return err
		}
	}

	if len(st.CertRows) > 0 {
		g.Printf("\nCertificates needing attention:\n")
		for _, c := range st.CertRows {
			g.Printf("  %-40s %s, %s left\n", c.Name, c.Status, plural(c.Days, "day"))
		}
	}
	for _, w := range st.Warnings {
		g.Printf("\n%s\n", w)
	}

	g.Printf("\n")
	switch st.Problems {
	case 0:
		g.Printf("No problems found.\n")
	case 1:
		g.Printf("1 problem found. See it with 'ratline doctor'.\n")
	default:
		g.Printf("%d problems found. See them with 'ratline doctor'.\n", st.Problems)
	}
	return nil
}

// plural formats a count with its noun, because "1 sites" reads as a bug in the
// tool rather than as a count of one.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// uptimeHuman reads /proc/uptime, which is the only place it is available without
// shelling out. Absent or unreadable — a container, a non-Linux host — is not worth
// reporting as an error on a summary screen, so it comes back empty.
func uptimeHuman() string {
	body, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || secs <= 0 {
		return ""
	}
	d := time.Duration(secs) * time.Second
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
