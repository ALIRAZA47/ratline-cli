package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/jobs"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request, c *Caller) {
	cat, err := s.Client.Catalogue(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	actions := rl.Actions(cat, c.Account.Role)
	if group := r.URL.Query().Get("group"); group != "" {
		filtered := actions[:0]
		for _, a := range actions {
			if a.Group == group {
				filtered = append(filtered, a)
			}
		}
		actions = filtered
	}
	ok(w, actions)
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, c *Caller) {
	cat, err := s.Client.Catalogue(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	action, _, found := rl.Lookup(cat, r.PathValue("id"), c.Account.Role)
	if !found {
		s.notAvailable(w)
		return
	}
	ok(w, action)
}

// runRequest is what a form sends.
type runRequest struct {
	Args  []string       `json:"args,omitempty"`
	Flags map[string]any `json:"flags,omitempty"`
	// Secret is the one value that never becomes a flag. It reaches ratline on
	// stdin, so it is absent from /proc/PID/cmdline while the command runs.
	Secret string `json:"secret,omitempty"`
	// SecretKey is the name that goes with it, where stdin is an assignment
	// rather than a bare value.
	SecretKey string `json:"secret_key,omitempty"`
	// Confirm is the target's name, typed back, for a destructive action. It is
	// compared against the first positional argument.
	Confirm string `json:"confirm,omitempty"`
}

// handlePreview runs the action with --dry-run and returns the plan.
//
// This is the feature the panel gets almost free and that a hand-written web
// provisioner cannot have: ratline implements --dry-run at the Runner, so nothing is
// written at any layer, and the plan an operator reads in the browser is produced by
// the same code path that would have done the work.
//
// One caveat, and it is ratline's own: a command that composes other commands cannot
// rehearse itself this way, because each step preconditions on the previous one
// having really happened. Those commands resolve and print a plan instead, which is
// what arrives here.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.execute(w, r, c, true)
}

// handleRun does it for real.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.execute(w, r, c, false)
}

// runResult is what a synchronous action returns.
type runResult struct {
	Action   string          `json:"action"`
	Argv     []string        `json:"argv"`
	DryRun   bool            `json:"dry_run"`
	OK       bool            `json:"ok"`
	ExitCode int             `json:"exit_code"`
	Data     json.RawMessage `json:"data,omitempty"`
	Logs     string          `json:"logs,omitempty"`
	Error    *errorPayload   `json:"error,omitempty"`
	// JobID is set instead of the rest when the action was queued.
	JobID string `json:"job_id,omitempty"`
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request, c *Caller, dryRun bool) {
	var body runRequest
	if err := decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	ctx := r.Context()
	cat, err := s.Client.Catalogue(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	id := r.PathValue("id")
	action, policy, found := rl.Lookup(cat, id, c.Account.Role)
	if !found {
		s.notAvailable(w)
		return
	}

	req := rl.Request{
		Verb:      action.Verb,
		Args:      body.Args,
		Flags:     body.Flags,
		Secret:    body.Secret,
		SecretKey: body.SecretKey,
		DryRun:    dryRun,
	}
	// Composed and validated here rather than at the point of writing, so that a
	// name with an "=" in it or a value with a newline is a 400 with a sentence
	// rather than a second environment variable nobody asked for.
	payload, err := rl.StdinPayload(policy, req)
	if err != nil {
		s.fail(w, err)
		return
	}
	req.Secret = payload

	// The typed confirmation. Compared against the first positional argument,
	// which for every destructive command in ratline is the thing being destroyed
	// — the domain, the username, the database. Constant-time because there is no
	// reason for it not to be.
	if policy.Destructive && !dryRun {
		target := ""
		if len(body.Args) > 0 {
			target = body.Args[0]
		}
		if target == "" || !auth.ConstantTimeEqualString(strings.TrimSpace(body.Confirm), target) {
			failStatus(w, http.StatusBadRequest, "confirmation_required",
				"type "+quote(target)+" to confirm this",
				"this cannot be undone by running another command")
			return
		}
		req.Confirmed = true
	}

	target := ""
	if len(body.Args) > 0 {
		target = body.Args[0]
	}

	// Long-running work becomes a job. Not for a preview: a dry run of a deploy
	// finishes in a second and the plan is the answer, so making somebody watch a
	// job for it would be ceremony.
	if policy.Long && !dryRun {
		jobID, err := auth.NewID()
		if err != nil {
			s.fail(w, err)
			return
		}
		spec := jobs.Spec{
			ID: jobID, Action: action.Verb, Target: target,
			ActorID: c.Account.ID, Actor: c.Account.Email,
			Policy: policy, Request: req,
		}
		if err := s.Jobs.Submit(ctx, spec); err != nil {
			s.fail(w, err)
			return
		}
		s.record(ctx, c, action.Verb, target, "queued as job "+jobID, dryRun, true, 0, nil, 0)
		writeJSON(w, http.StatusAccepted, reply{OK: true, Data: runResult{
			Action: action.ID, JobID: jobID, OK: true,
		}})
		return
	}

	start := time.Now()
	out, err := s.Client.Run(ctx, cat, policy, req)
	if err != nil {
		s.record(ctx, c, action.Verb, target, "", dryRun, false, 0, err, time.Since(start))
		s.fail(w, err)
		return
	}
	cmdErr := out.Err()
	s.record(ctx, c, action.Verb, target, strings.Join(out.Argv, " "), dryRun,
		cmdErr == nil, out.ExitCode, cmdErr, out.Duration)

	// Any mutation may have changed what a listing would say, and the catalogue
	// itself changes when ratline is updated.
	if action.Mutates && !dryRun {
		s.invalidateReads()
		if action.Verb == "update" {
			s.Client.InvalidateCatalogue()
		}
	}

	res := runResult{
		Action:   action.ID,
		Argv:     out.Argv,
		DryRun:   dryRun,
		OK:       cmdErr == nil,
		ExitCode: out.ExitCode,
		Logs:     out.Logs,
	}
	if out.Envelope != nil {
		res.Data = out.Envelope.Data
	}
	if cmdErr != nil {
		code := rlerr.CodeOf(cmdErr)
		res.Error = &errorPayload{
			Code: int(code), Name: code.Name(), Message: cmdErr.Error(),
			Hint: rlerr.Hint(cmdErr), Fields: rlerr.Fields(cmdErr),
		}
		// 200 with ok:false, not a 5xx. The panel did its job — it ran the command
		// and read the answer — and the answer was that the command failed. A
		// browser needs the exit code, the hint and the log to show, and burying
		// those in an HTTP error would throw away everything useful.
		writeJSON(w, http.StatusOK, reply{OK: false, Data: res, Error: res.Error})
		return
	}
	ok(w, res)
}

// notAvailable is the single answer for "no such action", "denied in the panel" and
// "above your role". Distinguishing them would map out the surface above the caller.
func (s *Server) notAvailable(w http.ResponseWriter) {
	failStatus(w, http.StatusNotFound, "no_such_action",
		"no such action is available to you", "")
}

// record writes the panel's half of the audit trail.
//
// ratline writes its own entry for the command; this one carries the person, because
// every invocation reaches ratline as root and it cannot know who asked.
func (s *Server) record(ctx context.Context, c *Caller, action, target, argv string,
	dryRun, okResult bool, exit int, err error, took time.Duration) {
	rec := &store.ActionRecord{
		At: s.now(), ActorID: c.Account.ID, Actor: c.Account.Email,
		Action: action, Target: target, Argv: argv, DryRun: dryRun,
		OK: okResult, ExitCode: exit, DurationMS: took.Milliseconds(), IP: c.IP,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	if rerr := s.Store.RecordAction(ctx, rec); rerr != nil {
		// Losing the trail must not stop somebody managing the server, exactly as
		// ratline treats its own audit log.
		s.Log.Error("could not record the action", "action", action, "err", rerr)
	}
	if s.Cfg.Jobs.Retain > 0 {
		if _, perr := s.Store.PurgeActions(ctx, s.Cfg.Jobs.Retain*10); perr != nil {
			s.Log.Debug("could not trim the action log", "err", perr)
		}
	}
}

func quote(s string) string {
	if s == "" {
		return "the target's name"
	}
	return "\"" + s + "\""
}
