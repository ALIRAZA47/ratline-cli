package validate

import (
	"strings"
	"testing"
)

// A schedule that is quietly wrong is worse than one that is refused: the job runs at the
// wrong time for months and nobody notices until the thing it was meant to do turns out
// not to have happened. Every case here is checked against systemd's own syntax by the
// integration suite, which runs `systemd-analyze calendar` on the output.
func TestCronTranslatesToTheSystemdEquivalent(t *testing.T) {
	for expr, want := range map[string]string{
		"0 3 * * *":       "*-*-* 03:00:00",
		"*/5 * * * *":     "*-*-* *:00/5:00",
		"0 */6 * * *":     "*-*-* 00/6:00:00",
		"30 2 1 * *":      "*-*-01 02:30:00",
		"0 0 1 1 *":       "*-01-01 00:00:00",
		"15 14 * * 1":     "Mon *-*-* 14:15:00",
		"0 22 * * 1-5":    "Mon..Fri *-*-* 22:00:00",
		"0 0 * * 0":       "Sun *-*-* 00:00:00",
		"0 0 * * 7":       "Sun *-*-* 00:00:00",
		"0 9,17 * * *":    "*-*-* 09,17:00:00",
		"0 3 * jan *":     "*-01-* 03:00:00",
		"0 3 * * mon,fri": "Mon,Fri *-*-* 03:00:00",
		"5 0-4 * * *":     "*-*-* 00-04:05:00",
	} {
		got, err := Schedule(expr)
		if err != nil {
			t.Errorf("Schedule(%q) errored: %v", expr, err)
			continue
		}
		if got != want {
			t.Errorf("Schedule(%q) = %q, want %q", expr, got, want)
		}
	}
}

// systemd's own syntax passes through untouched — the real tool judges it, not this.
func TestASystemdExpressionIsLeftAlone(t *testing.T) {
	for _, expr := range []string{
		"daily", "hourly", "Mon *-*-* 04:00:00", "*-*-* 02:30:00", "03:00",
		"Mon..Fri 09:00", "*:0/15",
	} {
		got, err := Schedule(expr)
		if err != nil {
			t.Errorf("Schedule(%q) errored: %v", expr, err)
		}
		if got != expr && got != strings.TrimPrefix(expr, "@") {
			t.Errorf("Schedule(%q) rewrote it to %q", expr, got)
		}
	}
}

func TestTheCronShorthandsAreAccepted(t *testing.T) {
	for expr, want := range map[string]string{
		"@daily": "daily", "@hourly": "hourly", "@weekly": "weekly",
		"@monthly": "monthly", "@yearly": "yearly", "@midnight": "midnight",
	} {
		got, err := Schedule(expr)
		if err != nil {
			t.Errorf("Schedule(%q) errored: %v", expr, err)
		}
		if got != want {
			t.Errorf("Schedule(%q) = %q, want %q", expr, got, want)
		}
	}
}

// Cron ORs day-of-month and day-of-week when both are restricted. systemd ANDs them.
// There is no OnCalendar for "the 1st, or any Monday", so translating it either way is
// wrong — and being wrong here means a backup job that runs on a schedule nobody chose.
func TestTheOneCronRuleThatCannotBeTranslatedIsRefused(t *testing.T) {
	_, err := Schedule("0 3 1 * mon")
	if err == nil {
		t.Fatal("accepted a cron expression whose meaning a timer cannot express")
	}
	if !strings.Contains(err.Error(), "either") {
		t.Errorf("the refusal does not explain why: %v", err)
	}
	// Each half on its own is fine.
	for _, ok := range []string{"0 3 1 * *", "0 3 * * mon"} {
		if _, err := Schedule(ok); err != nil {
			t.Errorf("Schedule(%q) errored: %v", ok, err)
		}
	}
}

// @reboot is not a schedule. A timer fires on a clock, and pretending otherwise would
// mean picking some interval and hoping.
func TestRebootIsRefusedWithSomethingBetterToDo(t *testing.T) {
	_, err := Schedule("@reboot")
	if err == nil {
		t.Fatal("@reboot was accepted as a schedule")
	}
	if !strings.Contains(hintOf(err), "worker") {
		t.Errorf("the refusal does not point at what does work: %v (hint %q)", err, hintOf(err))
	}
}

func TestNonsenseIsRefused(t *testing.T) {
	for _, expr := range []string{
		"", "   ",
		"99 3 * * *",  // minute out of range
		"0 25 * * *",  // hour out of range
		"0 3 * * 9",   // day out of range
		"0 3 * bad *", // month name that is not one
		"*/0 * * * *", // a zero step
		"0 5-1 * * *", // a range that counts backwards
		"x 3 * * *",   // not a number
	} {
		if _, err := Schedule(expr); err == nil {
			t.Errorf("Schedule(%q) was accepted", expr)
		}
	}
}

// A name becomes part of a unit filename. A dot would change the unit's type and a slash
// would write it somewhere else entirely.
func TestAUnitNameCannotEscapeItsFilename(t *testing.T) {
	for _, bad := range []string{
		"", "has space", "has.dot", "has/slash", "UPPER", "has@at",
		strings.Repeat("x", 49),
		"../../etc/systemd/system/sshd",
	} {
		if err := JobName(bad); err == nil {
			t.Errorf("JobName(%q) was accepted", bad)
		}
	}
	for _, good := range []string{"nightly", "send-digest", "clear_cache", "job2"} {
		if err := JobName(good); err != nil {
			t.Errorf("JobName(%q) was refused: %v", good, err)
		}
	}
}
