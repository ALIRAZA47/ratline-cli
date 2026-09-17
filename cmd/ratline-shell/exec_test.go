package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExecFlags(t *testing.T) {
	opts, err := parseExecFlags([]string{"--env-file", "/home/a/b/.env", "--log-file=/home/a/b/logs/job-x.log", "--", "/usr/bin/node", "--flag", "x"})
	if err != nil {
		t.Fatalf("parseExecFlags = %v", err)
	}
	if opts.envFile != "/home/a/b/.env" || opts.logFile != "/home/a/b/logs/job-x.log" {
		t.Errorf("paths = %+v", opts)
	}
	if strings.Join(opts.argv, " ") != "/usr/bin/node --flag x" {
		t.Errorf("argv = %v", opts.argv)
	}
	for _, bad := range [][]string{
		{},
		{"--env-file", "/x"},
		{"--", ""},
		{"--env-file", "relative/.env", "--", "/bin/true"},
		{"--unknown", "--", "/bin/true"},
		{"/bin/true"},
	} {
		if _, err := parseExecFlags(bad); err == nil {
			t.Errorf("parseExecFlags accepted %v", bad)
		}
	}
}

// The file is read the way systemd's EnvironmentFile= reads it, because that is what
// it replaces and a tenant's existing .env has to keep meaning the same thing.
func TestParseEnvFileFollowsSystemdRules(t *testing.T) {
	in := "# a comment\n" +
		"; another\n" +
		"\n" +
		"PLAIN=value with spaces   \n" +
		"  INDENTED = trimmed key\n" +
		"DQ=\"quoted \\\"inner\\\" # not a comment\"\n" +
		"SQ='single # kept'\n" +
		"ESC=a\\ b\n" +
		"CONT=first \\\n" +
		"second\n" +
		"PATH=/tenant/bin\n" +
		"not an assignment\n" +
		"9BAD=starts with a digit\n" +
		"UNTERMINATED=\"oops\n" +
		"CRLF=windows\r\n"
	pairs, warnings := parseEnvFile([]byte(in))
	got := map[string]string{}
	for _, p := range pairs {
		got[p[0]] = p[1]
	}
	want := map[string]string{
		"PLAIN":    "value with spaces",
		"INDENTED": "trimmed key",
		"DQ":       `quoted "inner" # not a comment`,
		"SQ":       "single # kept",
		"ESC":      "a b",
		"CONT":     "first second",
		"PATH":     "/tenant/bin",
		"CRLF":     "windows",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"9BAD", "UNTERMINATED", "not an assignment"} {
		if _, ok := got[k]; ok {
			t.Errorf("%q should have been skipped", k)
		}
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %v, want one per skipped line", warnings)
	}
}

func TestMergeEnvFileValuesOverrideTheInheritedEnvironment(t *testing.T) {
	env := mergeEnv([]string{"A=unit", "B=unit", "A=later"}, [][2]string{{"B", "file"}, {"C", "file"}})
	got := strings.Join(env, ",")
	if got != "A=later,B=file,C=file" {
		t.Errorf("merged = %s", got)
	}
}

func TestResolveProgram(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "prog")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProgram("prog", dir+":/nonexistent"); err != nil || got != bin {
		t.Errorf("bare name on PATH: %q, %v", got, err)
	}
	if got, err := resolveProgram(bin, ""); err != nil || got != bin {
		t.Errorf("absolute path: %q, %v", got, err)
	}
	if _, err := resolveProgram("./prog", dir); err == nil {
		t.Error("a relative path was accepted; systemd would refuse it too")
	}
	if _, err := resolveProgram("/definitely/not/here", ""); err == nil {
		t.Error("a missing program was accepted")
	}
	if _, err := resolveProgram("missing", dir); err == nil {
		t.Error("a bare name not on PATH was accepted")
	}
}

// End to end, in a child: the environment file is loaded, the log is opened, and the
// program runs in place with both. Driven through this test binary re-invoked as a
// helper, because exec replaces the process that calls it.
func TestRunExecLoadsTheEnvironmentAndRedirectsOutput(t *testing.T) {
	if os.Getenv("RATLINE_SHELL_EXEC_HELPER") == "1" {
		os.Exit(runExec(strings.Split(os.Getenv("RATLINE_SHELL_EXEC_ARGS"), "\n")))
	}
	printenv := "/usr/bin/printenv"
	if _, err := os.Stat(printenv); err != nil {
		t.Skip("printenv is not at /usr/bin/printenv on this host")
	}
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=\"hello world\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "logs", "job-x.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"--env-file", envFile, "--log-file", logFile, "--", printenv, "FROM_FILE"}
	proc, err := os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=TestRunExecLoadsTheEnvironmentAndRedirectsOutput"}, &os.ProcAttr{
		Env: append(os.Environ(),
			"RATLINE_SHELL_EXEC_HELPER=1",
			"RATLINE_SHELL_EXEC_ARGS="+strings.Join(args, "\n")),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Success() {
		t.Fatalf("the helper exited %v", state)
	}
	out, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello world" {
		t.Errorf("the program did not receive the file's value through the redirected log: %q", out)
	}

	// A missing environment file is EnvironmentFile=-PATH: not an error.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	args = []string{"--env-file", filepath.Join(dir, "absent"), "--", printenv, "PATH"}
	proc, err = os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=TestRunExecLoadsTheEnvironmentAndRedirectsOutput"}, &os.ProcAttr{
		Env: append(os.Environ(),
			"RATLINE_SHELL_EXEC_HELPER=1",
			"RATLINE_SHELL_EXEC_ARGS="+strings.Join(args, "\n")),
		Files: []*os.File{os.Stdin, devnull, os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state, err := proc.Wait(); err != nil || !state.Success() {
		t.Errorf("a missing env file should not fail the start: %v %v", state, err)
	}
}
