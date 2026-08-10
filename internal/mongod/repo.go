package mongod

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// versionsByCodename is which MongoDB release series MongoDB's own apt repository
// actually publishes for each distribution release, newest first. Checked against
// repo.mongodb.org rather than the documentation, because this is the list that decides
// whether apt-get update succeeds. A codename missing here is a host `db install`
// refuses rather than pointing apt at a URL that 404s.
var versionsByCodename = map[string][]string{
	// Ubuntu
	"focal": {"8.0", "7.0"},
	"jammy": {"8.0", "7.0"},
	"noble": {"8.0"},
	// Debian
	"bullseye": {"7.0"},
	"bookworm": {"8.0", "7.0"},
}

// DefaultVersion is the newest series this host can install.
func DefaultVersion(os system.OSInfo) (string, error) {
	versions, err := supportedVersions(os)
	if err != nil {
		return "", err
	}
	return versions[0], nil
}

// ResolveVersion validates a requested series against what the repository publishes for
// this host, defaulting to the newest when none was asked for.
func ResolveVersion(os system.OSInfo, requested string) (string, error) {
	versions, err := supportedVersions(os)
	if err != nil {
		return "", err
	}
	if requested == "" {
		return versions[0], nil
	}
	for _, v := range versions {
		if v == requested {
			return v, nil
		}
	}
	return "", rlerr.Preconditionf("MongoDB %s is not published for %s %s",
		requested, os.ID, os.Codename).
		WithHint("this host can install: %s", strings.Join(versions, ", "))
}

func supportedVersions(os system.OSInfo) ([]string, error) {
	// Exact ID only: a derivative reporting ID_LIKE=debian has a codename MongoDB's
	// repository has never heard of, and the failure would be apt's, later, in worse
	// words.
	if os.ID != "ubuntu" && os.ID != "debian" {
		return nil, rlerr.Preconditionf("MongoDB's repository publishes packages for Ubuntu and Debian, and this host is %q", os.PrettyName).
			WithHint("install MongoDB however this distribution does, then attach it with 'ratline db connect'")
	}
	if os.Arch != "amd64" && os.Arch != "arm64" {
		return nil, rlerr.Preconditionf("MongoDB's repository publishes amd64 and arm64 packages, and this host is %s", os.Arch)
	}
	versions := versionsByCodename[os.Codename]
	if len(versions) == 0 {
		known := make([]string, 0, len(versionsByCodename))
		for c := range versionsByCodename {
			known = append(known, c)
		}
		sort.Strings(known)
		return nil, rlerr.Preconditionf("MongoDB's repository does not publish packages for %s %s (%q)",
			os.ID, os.VersionID, os.Codename).
			WithHint("releases it does publish for: %s", strings.Join(known, ", "))
	}
	return versions, nil
}

// KeyringPath is where the dearmored signing key for a series lives. Versioned like
// the sources file, so two series never fight over one path.
func KeyringPath(version string) string {
	return "/usr/share/keyrings/mongodb-server-" + version + ".gpg"
}

// SourcesPath is the apt sources file naming MongoDB's repository for a series.
func SourcesPath(version string) string {
	return "/etc/apt/sources.list.d/mongodb-org-" + version + ".list"
}

// SigningKey returns the binary keyring for a series, from the armored key embedded in
// the binary. Embedded rather than downloaded at install time: the key is the root of
// trust for everything apt will install, and fetching a root of trust over the network
// at the moment of use is the pattern ratline exists to avoid. A rotated key ships as a
// ratline release, verifiable against the repository like any other change.
func SigningKey(version string) ([]byte, error) {
	asc, err := templates.FS.ReadFile("mongo/server-" + version + ".asc")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "no signing key for MongoDB %s is embedded in this binary", version)
	}
	return Dearmor(asc)
}

// SourceLine is the deb line pointing apt at MongoDB's repository for this host,
// pinned to the embedded signing key.
func SourceLine(os system.OSInfo, version string) (string, error) {
	if _, err := ResolveVersion(os, version); err != nil {
		return "", err
	}
	// Ubuntu files MongoDB under multiverse, Debian under main; the repository's
	// layout, not a choice.
	component := "multiverse"
	if os.ID == "debian" {
		component = "main"
	}
	return fmt.Sprintf("deb [ arch=amd64,arm64 signed-by=%s ] https://repo.mongodb.org/apt/%s %s/mongodb-org/%s %s",
		KeyringPath(version), os.ID, os.Codename, version, component), nil
}
