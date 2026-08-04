package validate

import (
	"strings"
	"testing"
)

func TestAppModule(t *testing.T) {
	for _, ok := range []string{
		"app:app", "app.main:app", "myproject.wsgi:application", "a.b.c.d:handler",
		"_private.mod:app", "app.main:App", "app2.main3:app_4",
	} {
		if err := AppModule(ok); err != nil {
			t.Errorf("AppModule(%q) = %v, want nil", ok, err)
		}
	}

	// This string lands on a gunicorn command line and inside a unit file.
	invalid := map[string]string{
		"":                                "empty",
		"app":                             "no callable",
		"app:":                            "empty callable",
		":app":                            "empty module",
		"app.main:app:extra":              "two colons",
		"app main:app":                    "space",
		"app.main:app app":                "space in the callable",
		"app-main:app":                    "hyphen",
		"app/main:app":                    "slash",
		"../app:app":                      "path traversal",
		".app:app":                        "leading dot",
		"app.:app":                        "trailing dot",
		"app..main:app":                   "empty segment",
		"app.main:app;reboot":             "command separator",
		"$(id):app":                       "command substitution",
		"app.main:app\nExecStart=/bin/sh": "unit-file injection",
		"app.main:app\x00":                "NUL byte",
		"1app:app":                        "leading digit",
		"app.main:1app":                   "callable starting with a digit",
		strings.Repeat("a", 300) + ":app": "too long",
	}
	for in, why := range invalid {
		if err := AppModule(in); err == nil {
			t.Errorf("AppModule(%q) = nil, want an error (%s)", in, why)
		}
	}
}

func TestNodeEntry(t *testing.T) {
	for _, ok := range []string{"server.js", "dist/main.js", "src/index.mjs", "app.cjs", "build/server.ts"} {
		if err := NodeEntry(ok); err != nil {
			t.Errorf("NodeEntry(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "server", "server.py", "/abs/server.js", "../server.js",
		"dist/../../server.js", "server.js;reboot", "$(id).js", "ser ver.js", "server.js\x00",
	} {
		if err := NodeEntry(bad); err == nil {
			t.Errorf("NodeEntry(%q) = nil, want an error", bad)
		}
	}
}

func TestVersions(t *testing.T) {
	for _, ok := range []string{"22", "22.11.0", "v20", "18.19"} {
		if err := NodeVersion(ok); err != nil {
			t.Errorf("NodeVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "latest", "22.x", "22;rm", "../22", "3.12.4.5"} {
		if err := NodeVersion(bad); err == nil {
			t.Errorf("NodeVersion(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"3.12", "3.11.9", "3.9"} {
		if err := PythonVersion(ok); err != nil {
			t.Errorf("PythonVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "2.7", "3", "3.12.4.5", "3.x", "python3.12"} {
		if err := PythonVersion(bad); err == nil {
			t.Errorf("PythonVersion(%q) = nil, want an error", bad)
		}
	}
}

func TestRuntimeAndPackageManager(t *testing.T) {
	for _, ok := range []string{"static", "node", "python"} {
		if err := RuntimeName(ok); err != nil {
			t.Errorf("RuntimeName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "php", "go", "Static"} {
		if err := RuntimeName(bad); err == nil {
			t.Errorf("RuntimeName(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"npm", "pnpm", "yarn", "bun"} {
		if err := PackageManager(ok); err != nil {
			t.Errorf("PackageManager(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "NPM", "pip", "npm install"} {
		if err := PackageManager(bad); err == nil {
			t.Errorf("PackageManager(%q) = nil, want an error", bad)
		}
	}
}

func TestSystemdUnitName(t *testing.T) {
	for _, ok := range []string{"ratline-alice-example_com.service", "ratline.target", "x@1.service", "a.timer", "b.socket"} {
		if err := SystemdUnitName(ok); err != nil {
			t.Errorf("SystemdUnitName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "ratline-x", "ratline x.service", "ratline/x.service", "x.service\nExecStart=", strings.Repeat("a", 300) + ".service"} {
		if err := SystemdUnitName(bad); err == nil {
			t.Errorf("SystemdUnitName(%q) = nil, want an error", bad)
		}
	}
}

func FuzzAppModule(f *testing.F) {
	for _, seed := range []string{"app.main:app", "", ":", "a:b", "a..b:c", "app:app\n", "$(id):x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if err := AppModule(in); err != nil {
			return
		}
		// Anything accepted must be safe on a command line and in a unit file.
		if strings.ContainsAny(in, " \t\n\r\x00;&|$`'\"\\/*?<>()[]{}#!") {
			t.Fatalf("accepted %q, which contains a shell- or unit-significant character", in)
		}
		if strings.Count(in, ":") != 1 {
			t.Fatalf("accepted %q, which does not have exactly one colon", in)
		}
		if strings.Contains(in, "..") {
			t.Fatalf("accepted %q, which contains %q", in, "..")
		}
	})
}
