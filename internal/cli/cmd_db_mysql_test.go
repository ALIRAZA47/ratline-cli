package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestDBEngineChoice(t *testing.T) {
	g := NewGlobals()
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("engine", "mongo", "")

	if got, err := g.dbEngineChoice(cmd); err != nil || got != engineMongo {
		t.Errorf("default = %q, %v; want mongo, nil", got, err)
	}
	_ = cmd.Flags().Set("engine", "mysql")
	if got, err := g.dbEngineChoice(cmd); err != nil || got != engineMySQL {
		t.Errorf("mysql = %q, %v", got, err)
	}
	_ = cmd.Flags().Set("engine", "redis")
	if got, err := g.dbEngineChoice(cmd); err != nil || got != engineRedis {
		t.Errorf("redis = %q, %v", got, err)
	}
	// An engine ratline does not support is a clear refusal, not a silent mongo run.
	_ = cmd.Flags().Set("engine", "postgres")
	if _, err := g.dbEngineChoice(cmd); err == nil {
		t.Error("an unknown engine was accepted")
	}
}

// The engine flag routes each verb without disturbing the MongoDB default.
func TestDBRolesDispatchByEngine(t *testing.T) {
	// MySQL roles map to privilege sets.
	code, out, _ := harness(t, "db", "roles", "--engine", "mysql")
	if code != 0 {
		t.Fatalf("db roles --engine mysql exit = %d", code)
	}
	if !strings.Contains(out.String(), "readWrite") || !strings.Contains(out.String(), "dbOwner") {
		t.Errorf("mysql roles output missing entries:\n%s", out.String())
	}
	if strings.Contains(out.String(), "dbAdmin") {
		t.Errorf("mysql roles listed a MongoDB-only role:\n%s", out.String())
	}

	// The default is still MongoDB, unchanged.
	code, out, _ = harness(t, "db", "roles")
	if code != 0 {
		t.Fatalf("db roles exit = %d", code)
	}
	if !strings.Contains(out.String(), "dbAdmin") {
		t.Errorf("default (mongo) roles output changed:\n%s", out.String())
	}
}

func TestDBPingMySQLRefusesWhenProvisioningOff(t *testing.T) {
	// With the default config (provisioning off), the MySQL ping refuses with a hint
	// naming the MySQL setup command — it does not silently run the MongoDB path.
	code, _, errOut := harness(t, "db", "ping", "--engine", "mysql")
	if code == 0 {
		t.Fatal("db ping --engine mysql succeeded with provisioning off")
	}
	if !strings.Contains(errOut.String(), "mysql") && !strings.Contains(errOut.String(), "provisioning") {
		t.Errorf("refusal did not mention mysql/provisioning:\n%s", errOut.String())
	}
}
