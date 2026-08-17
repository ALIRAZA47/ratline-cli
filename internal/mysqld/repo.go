package mysqld

import (
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Distro captures what differs between the MySQL-family server on Ubuntu (Oracle
// mysql-server) and on Debian (mariadb-server): the package to install, the systemd unit,
// and where the managed bind drop-in must live so its bind-address wins.
//
// The drop-in must load *after* the package's own server config, which sets
// bind-address = 127.0.0.1. On Ubuntu that default lives in
// /etc/mysql/mysql.conf.d/mysqld.cnf, so a `zz-` file in the same directory sorts last;
// on Debian it lives in /etc/mysql/mariadb.conf.d/50-server.cnf, so a `99-` file wins.
type Distro struct {
	Package    string
	Service    string
	ConfDropIn string
	Flavour    string // "mysql" or "mariadb", for messages
}

// distro resolves the MySQL-family details for this host. Distro-native: Ubuntu gets
// Oracle MySQL, Debian gets MariaDB.
func (m *Manager) distro() Distro {
	switch m.OS.ID {
	case "debian":
		return Distro{
			Package:    "mariadb-server",
			Service:    "mariadb",
			ConfDropIn: "/etc/mysql/mariadb.conf.d/99-ratline.cnf",
			Flavour:    "mariadb",
		}
	default: // ubuntu and its derivatives
		return Distro{
			Package:    "mysql-server",
			Service:    "mysql",
			ConfDropIn: "/etc/mysql/mysql.conf.d/zz-ratline.cnf",
			Flavour:    "mysql",
		}
	}
}

// Supported reports whether `db install --engine mysql` can run on this host. Exact ID
// only, like the MongoDB installer: a derivative reporting ID_LIKE=debian may not lay its
// config out where this expects.
func (m *Manager) Supported() error {
	if m.OS.ID != "ubuntu" && m.OS.ID != "debian" {
		return rlerr.Preconditionf("ratline installs MySQL from the distribution package, and this host is %q", m.OS.PrettyName).
			WithHint("install MySQL or MariaDB however this distribution does, then attach it " +
				"with 'ratline db connect --engine mysql'")
	}
	return nil
}

// ServiceName is the systemd unit for this host's MySQL-family server.
func (m *Manager) ServiceName() string { return m.distro().Service }

// DistroForPlan exposes the resolved distro details for the CLI's --dry-run plan.
func (m *Manager) DistroForPlan() Distro { return m.distro() }

// socketDefaults renders a defaults-file that authenticates as root over the local unix
// socket — how a freshly installed server lets the OS root account in before any password
// exists. No password, so it carries no secret, but it is staged 0600 all the same.
func socketDefaults() string {
	return "[client]\nuser=root\n"
}
