package validate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Schedules.
//
// What lands in a timer is always a systemd OnCalendar expression, because that is what
// `systemd-analyze calendar` can check and print the next elapse for — the same
// stage-verify-commit shape as every other generated file here. But operators think in
// cron, and telling somebody their `0 3 * * *` is not welcome here would be a bad trade
// for purity. So a five-field cron expression is translated, and the translation is then
// handed to the real tool before anything is written.
//
// The translation covers the standard five-field form: lists, ranges, steps and names.
// Anything outside that is refused rather than approximated. A schedule that is quietly
// wrong is worse than one that is rejected: the job runs at the wrong time for months and
// nobody notices until the thing it was supposed to do turns out not to have happened.

var (
	monthNames = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	dayNames = map[string]string{
		"sun": "Sun", "mon": "Mon", "tue": "Tue", "wed": "Wed",
		"thu": "Thu", "fri": "Fri", "sat": "Sat",
	}
	// systemd accepts these itself, so they are passed straight through.
	shorthand = map[string]bool{
		"@yearly": true, "@annually": true, "@monthly": true, "@weekly": true,
		"@daily": true, "@midnight": true, "@hourly": true, "@minutely": true,
		"minutely": true, "hourly": true, "daily": true, "monthly": true,
		"weekly": true, "yearly": true, "annually": true, "quarterly": true,
		"semiannually": true,
	}
)

// Schedule converts what the operator typed into an OnCalendar expression.
//
// The result still has to be checked with `systemd-analyze calendar`; this only gets it
// into the right shape. Returning the input unchanged is correct for anything already in
// systemd's own syntax — that is what the verification step is for.
func Schedule(expr string) (string, error) {
	s := strings.TrimSpace(expr)
	if s == "" {
		return "", rlerr.Usagef("the schedule is empty").
			WithHint("something like 'daily', '03:00' or '0 3 * * *'")
	}
	if shorthand[strings.ToLower(s)] {
		return strings.TrimPrefix(strings.ToLower(s), "@"), nil
	}
	// @reboot has no OnCalendar equivalent — a timer fires on a clock, not on a boot. The
	// honest answer is the directive that does mean this, rather than a schedule that
	// approximately does.
	if strings.EqualFold(s, "@reboot") {
		return "", rlerr.Usagef("@reboot is not a schedule").
			WithHint("a timer fires on the clock, not on boot. For work that must happen " +
				"when the server comes back, make it a worker instead: 'ratline site worker add'")
	}

	fields := strings.Fields(s)
	if len(fields) != 5 {
		// Not cron-shaped, so assume systemd's own syntax and let the real tool judge it.
		return s, nil
	}
	return cronToCalendar(fields)
}

// LooksLikeCron reports whether the operator wrote a five-field cron expression, so the
// caller can tell them what it became. A translation the operator cannot see is one they
// cannot check.
func LooksLikeCron(expr string) bool {
	return len(strings.Fields(strings.TrimSpace(expr))) == 5
}

func cronToCalendar(f []string) (string, error) {
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]

	// Cron's day-of-month and day-of-week are ORed together when both are restricted,
	// which is a rule almost nobody knows and systemd has no way to express. Refusing is
	// the only honest option: a schedule that means "the 1st, or any Monday" cannot be
	// written as one OnCalendar, and picking either half silently would be wrong.
	if dom != "*" && dow != "*" {
		return "", rlerr.Usagef(
			"cron treats day-of-month and day-of-week as 'either', which a timer cannot express").
			WithHint("use one or the other, or write it as a systemd calendar expression: " +
				"see 'man systemd.time'")
	}

	min, err := cronField(minute, 0, 59, nil)
	if err != nil {
		return "", fmt.Errorf("minute: %w", err)
	}
	hr, err := cronField(hour, 0, 23, nil)
	if err != nil {
		return "", fmt.Errorf("hour: %w", err)
	}
	dm, err := cronField(dom, 1, 31, nil)
	if err != nil {
		return "", fmt.Errorf("day of month: %w", err)
	}
	mo, err := cronField(month, 1, 12, monthNames)
	if err != nil {
		return "", fmt.Errorf("month: %w", err)
	}

	weekday, err := cronWeekday(dow)
	if err != nil {
		return "", err
	}

	date := fmt.Sprintf("*-%s-%s", mo, dm)
	clock := fmt.Sprintf("%s:%s:00", hr, min)
	if weekday != "" {
		return weekday + " " + date + " " + clock, nil
	}
	return date + " " + clock, nil
}

// cronField renders one cron field as its systemd equivalent.
func cronField(v string, lo, hi int, names map[string]int) (string, error) {
	if v == "*" {
		return "*", nil
	}
	var parts []string
	for _, item := range strings.Split(v, ",") {
		rendered, err := cronItem(item, lo, hi, names)
		if err != nil {
			return "", err
		}
		parts = append(parts, rendered)
	}
	return strings.Join(parts, ","), nil
}

func cronItem(item string, lo, hi int, names map[string]int) (string, error) {
	base, stepStr, hasStep := strings.Cut(item, "/")
	step := 0
	if hasStep {
		n, err := strconv.Atoi(stepStr)
		if err != nil || n <= 0 {
			return "", rlerr.Usagef("%q is not a step", stepStr)
		}
		step = n
	}

	// */n and n/m — systemd writes a step from a start as "start/step".
	if base == "*" {
		if !hasStep {
			return "*", nil
		}
		return fmt.Sprintf("%02d/%d", lo, step), nil
	}

	from, to, isRange := strings.Cut(base, "-")
	start, err := cronNumber(from, lo, hi, names)
	if err != nil {
		return "", err
	}
	if !isRange {
		if hasStep {
			return fmt.Sprintf("%02d/%d", start, step), nil
		}
		return fmt.Sprintf("%02d", start), nil
	}
	end, err := cronNumber(to, lo, hi, names)
	if err != nil {
		return "", err
	}
	if end < start {
		return "", rlerr.Usagef("%q counts backwards", base)
	}
	if hasStep {
		return fmt.Sprintf("%02d-%02d/%d", start, end, step), nil
	}
	return fmt.Sprintf("%02d-%02d", start, end), nil
}

func cronNumber(s string, lo, hi int, names map[string]int) (int, error) {
	if names != nil {
		if n, ok := names[strings.ToLower(s)]; ok {
			return n, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, rlerr.Usagef("%q is not a number", s)
	}
	if n < lo || n > hi {
		return 0, rlerr.Usagef("%d is outside %d–%d", n, lo, hi)
	}
	return n, nil
}

// cronWeekday renders the day-of-week field, which systemd puts before the date.
func cronWeekday(v string) (string, error) {
	if v == "*" {
		return "", nil
	}
	order := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	name := func(s string) (string, error) {
		if d, ok := dayNames[strings.ToLower(s)]; ok {
			return d, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return "", rlerr.Usagef("%q is not a day", s)
		}
		// Cron allows 7 for Sunday as well as 0, and a job written with 7 that silently
		// became "outside 0–6" would be a confusing refusal for a common expression.
		if n == 7 {
			n = 0
		}
		if n < 0 || n > 6 {
			return "", rlerr.Usagef("%d is not a day of the week", n)
		}
		return order[n], nil
	}

	var parts []string
	for _, item := range strings.Split(v, ",") {
		from, to, isRange := strings.Cut(item, "-")
		a, err := name(from)
		if err != nil {
			return "", err
		}
		if !isRange {
			parts = append(parts, a)
			continue
		}
		b, err := name(to)
		if err != nil {
			return "", err
		}
		parts = append(parts, a+".."+b)
	}
	return strings.Join(parts, ","), nil
}

// JobName checks the name given to a job or worker.
//
// It becomes part of a systemd unit filename, so it has to survive that without
// escaping: a name with a slash in it would create a unit in another directory, and one
// with a dot would change the unit's type.
func JobName(name string) error {
	if name == "" {
		return rlerr.Usagef("the name is empty")
	}
	if len(name) > 48 {
		return rlerr.Usagef("the name %q is longer than 48 characters", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return rlerr.Usagef("invalid name %q: use lower-case letters, digits, "+
				"hyphen and underscore", name).
				WithHint("it becomes part of a systemd unit filename, so a dot would " +
					"change the unit's type and a slash would put it somewhere else")
		}
	}
	return nil
}
