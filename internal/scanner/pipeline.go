package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// Pipeline runs one scanner end to end. Every GitHub write goes through
// Effects and every main-branch commit through Committer, so --dry-run logs
// instead of acting.
type Pipeline struct {
	Reader    Reader
	Effects   Effects
	Committer Committer
	Engine    Engine
	Stores    Stores
	Repo      string // "owner/name"; lets reconcile notice a transferred issue
	// Paused reports whether the controller is paused. It is re-checked after
	// a scan, which can outlast a pause: a paused run files and closes nothing.
	Paused func(ctx context.Context) bool
	// StatusIssue returns the pinned status issue number, 0 when not yet known.
	// Scanner failure-streak notices are posted there. nil = none posted.
	StatusIssue func() int
	// BodyLocks, shared with the costs loop, serialises body read-modify-writes
	// per issue: editIssue and join hold the issue from their fetch until the
	// body write is done. nil is a no-op.
	BodyLocks *costblock.Locks
	Log       *slog.Logger
	Now       func() time.Time
	// GuardScanners overrides the pre-filing guard's passes. Tests only. nil
	// (production) = base redact.Builtin(), optional
	// redact.Gitleaks(redact.GitleaksOptions{Path: cfg.Redact.Gitleaks.Path})
	// when cfg.Redact.Gitleaks.Enabled, else no optional pass. There is no way
	// to turn the guard off.
	GuardScanners func(cfg *config.Resolved) (base, optional redact.Scanner)
	// Secrets returns the controller's known values. nil (production) =
	// redact.FromEnviron(os.Environ()), read every run.
	Secrets func() *redact.Secrets
}

// RunResult summarises one run.
type RunResult struct {
	Outcome    string
	SHA        string
	Found      int
	New        int
	Regress    int
	Suppressed int
	Joined     int // findings added to existing issues
	Created    int // issues created
	Filed      int // findings filed, by join or create
	Edited     int // issues whose body changed for a gone, resurfaced or re-rated finding
	Closed     int
	Deferred   int // closes or edits skipped for a hold or a running job
	OverBudget int
	NearDup    int // findings skipped as near-duplicates of an open issue or an earlier finding (LLM scanners)

	// GuardRule and GuardField are the first guard hit's rule and field, or
	// "scan-error" and "" when a guard pass failed.
	GuardRule, GuardField string
}

// routeLabel maps a route to the label its issues are filed with.
func routeLabel(r config.Route) (label string, ok bool) {
	switch r {
	case config.RoutePending:
		return labels.Pending, true
	case config.RouteNone:
		return "", true
	case config.RouteNeedsHuman:
		return labels.NeedsHuman, true
	}
	return "", false
}

// guardPasses returns the guard's base and optional scanners.
func (p *Pipeline) guardPasses(cfg *config.Resolved) (base, optional redact.Scanner) {
	if p.GuardScanners != nil {
		return p.GuardScanners(cfg)
	}
	if cfg.Redact.Gitleaks.Enabled {
		optional = redact.Gitleaks(redact.GitleaksOptions{Path: cfg.Redact.Gitleaks.Path})
	}
	return redact.Builtin(), optional
}

func (p *Pipeline) secrets() *redact.Secrets {
	if p.Secrets != nil {
		return p.Secrets()
	}
	return redact.FromEnviron(os.Environ())
}

// passFailure logs a failed guard pass. Only redact errors, which carry no
// scanned text, are logged.
func passFailure(log *slog.Logger, pass string, err error, extra ...any) {
	reason := "error"
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "timeout"
	}
	args := append([]any{"rule", "scan-error", "pass", pass, "reason", reason, "error", err.Error()}, extra...)
	log.Warn("scanner.guard_failed", args...)
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// openIssues lists the open scanner issues for name, pull requests excluded.
func (p *Pipeline) openIssues(ctx context.Context, name string) ([]github.Issue, error) {
	is, err := p.Reader.ListIssues(ctx, github.ListIssuesOpts{State: github.StateOpen, Labels: []string{labels.Scanner(name)}})
	if err != nil {
		return nil, fmt.Errorf("list open %s issues: %w", name, err)
	}
	out := is[:0:0]
	for _, i := range is {
		if !i.IsPR {
			out = append(out, i)
		}
	}
	return out, nil
}

// Run executes one scan of role at the given due time. A run that is dropped
// or whose scan fails is recorded and returns a nil error; ErrNoSlot is not
// recorded (the run stays due). The error is non-nil when GitHub, the store or
// the committer failed part way, after the rest of the run was attempted.
func (p *Pipeline) Run(ctx context.Context, cfg *config.Resolved, role config.Role, due time.Time) (RunResult, error) {
	start := p.now()
	log := p.log().With("scanner", role.Name)
	if role.ScanPath == "" {
		log.Warn("scanner.skip", "reason", "no scan executable (LLM scanners arrive in phase 2)")
		return RunResult{Outcome: OutcomeSkipped}, nil
	}
	store, err := p.Stores(ctx, role.Name)
	if err != nil {
		return RunResult{Outcome: OutcomeError}, fmt.Errorf("scanner %s: open state: %w", role.Name, err)
	}
	snap, err := loadSnapshot(ctx, store)
	if err != nil {
		return RunResult{Outcome: OutcomeError}, fmt.Errorf("scanner %s: load state: %w", role.Name, err)
	}
	dry := p.Effects.DryRun()

	var res RunResult
	record := func(outcome string) {
		res.Outcome = outcome
		_, rerr := store.RecordRun(context.WithoutCancel(ctx), scannerdb.Run{
			StartedAt: start, FinishedAt: p.now(), SHA: res.SHA, Outcome: outcome,
			Found: res.Found, Filed: res.Filed, Closed: res.Closed,
		})
		if rerr != nil {
			log.Error("scanner.record_run_failed", "error", rerr)
		}
		p.noteRun(ctx, store, role, res)
		log.Info("scanner.run", "sha", res.SHA, "outcome", outcome, "found", res.Found, "new", res.New,
			"regressions", res.Regress, "suppressed", res.Suppressed, "joined", res.Joined, "created", res.Created,
			"closed", res.Closed, "edited", res.Edited, "deferred", res.Deferred, "over_budget", res.OverBudget, "near_duplicates", res.NearDup,
			"duration", p.now().Sub(start).String())
	}

	routeLbl, routeOK := routeLabel(role.Route)
	if !routeOK {
		log.Warn("scanner.bad_route", "route", string(role.Route))
		record(OutcomeError)
		return res, nil
	}

	// 1. Backpressure, before the scanner starts.
	open, err := p.openIssues(ctx, role.Name)
	if err != nil {
		return RunResult{Outcome: OutcomeError}, err
	}
	var errs []error
	views := make([]IssueView, 0, len(open))
	openNums := map[int]bool{}
	for _, is := range open {
		views = append(views, IssueView{Number: is.Number, Title: is.Title, Labels: is.Labels, CreatedAt: is.CreatedAt, Body: is.Body, Parsed: ParseBody(is.Body)})
		openNums[is.Number] = true
	}

	// 2. Reconcile.
	fates := map[int]Fate{}
	var lookups []int
	seenIssue := map[int]bool{}
	for _, r := range snap.Rows {
		if tracked(r) && r.Issue > 0 && !openNums[r.Issue] && !seenIssue[r.Issue] {
			seenIssue[r.Issue] = true
			lookups = append(lookups, r.Issue)
		}
	}
	sort.Ints(lookups)
	for _, n := range lookups {
		is, err := p.Reader.GetIssue(ctx, n)
		switch {
		case errors.Is(err, github.ErrNotFound):
			fates[n] = Fate{Kind: FateGone}
		case err != nil:
			errs = append(errs, fmt.Errorf("look up issue #%d: %w", n, err))
		default:
			fates[n] = FateOf(is, p.Repo)
		}
	}
	rec := Reconcile(snap, views, fates, time.Duration(cfg.Stale.SuppressFor), start)
	for _, fp := range rec.Duplicates {
		log.Warn("scanner.duplicate_fingerprint", "fingerprint", fp)
	}
	if err := p.apply(ctx, store, &snap, rec.Batch); err != nil {
		// A stale snapshot would re-file what reconcile was adopting.
		errs = append(errs, err)
		record(OutcomeError)
		return res, errors.Join(errs...)
	}

	if dry {
		// Before any counting: an adopted synthetic issue counts towards max_open.
		if err := p.adoptLedger(ctx, store, &snap, role, start, ""); err != nil {
			errs = append(errs, err)
			record(OutcomeError)
			return res, errors.Join(errs...)
		}
	}
	synth := snap.syntheticOpen()
	if !dry {
		synth = nil
	}
	openCount := len(open) + len(synth)
	globalOpen := openCount
	for _, other := range cfg.RolesOf(config.KindScanner) {
		if other.Name == role.Name {
			continue
		}
		is, err := p.openIssues(ctx, other.Name)
		if err != nil {
			return RunResult{Outcome: OutcomeError}, err
		}
		globalOpen += len(is)
	}
	// Backpressure comes after reconcile, which only refreshes local state, and
	// before the scanner starts: a dropped run keeps the database current.
	switch {
	case openCount >= role.MaxOpen:
		log.Info("scanner.dropped", "reason", "max_open", "open", openCount, "max_open", role.MaxOpen)
		record(OutcomeBackpressure)
		return res, errors.Join(errs...)
	case globalOpen >= cfg.GlobalMaxOpen:
		log.Info("scanner.dropped", "reason", "global_max_open", "open", globalOpen, "global_max_open", cfg.GlobalMaxOpen)
		record(OutcomeGlobalCap)
		return res, errors.Join(errs...)
	}

	// 3. Scan.
	sr, err := p.Engine.Scan(ctx, cfg, role, due)
	res.SHA = sr.SHA
	if err != nil {
		var se *ScanError
		switch {
		case errors.Is(err, ErrNoSlot):
			log.Info("scanner.no_slot", "due", due)
			res.Outcome = OutcomeNoSlot
			return res, errors.Join(errs...)
		case errors.As(err, &se):
			res.SHA = se.SHA
			log.Warn("scanner.scan_failed", "error", se.Error(), "exit", se.Exit, "timed_out", se.TimedOut)
			if se.TimedOut {
				record(OutcomeTimeout)
			} else {
				record(OutcomeScanFailed)
			}
			return res, errors.Join(errs...)
		case ctx.Err() != nil:
			return res, errors.Join(append(errs, fmt.Errorf("scanner %s: %w", role.Name, err))...)
		default:
			errs = append(errs, fmt.Errorf("scanner %s: scan: %w", role.Name, err))
			record(OutcomeError)
			return res, errors.Join(errs...)
		}
	}
	if p.Paused != nil && p.Paused(ctx) {
		log.Info("scanner.paused_after_scan")
		res.Outcome = OutcomePaused
		return res, errors.Join(errs...)
	}
	res.Found = len(sr.Findings)

	// The pre-filing guard must stay between Engine.Scan and the first use of sr.Findings, for every engine (#204).
	base, optional := p.guardPasses(cfg)
	gr, gerr := CheckFindings(ctx, sr.Findings, base, optional, p.secrets())
	closeOnly := false
	switch {
	case gerr != nil && ctx.Err() != nil:
		return res, errors.Join(append(errs, fmt.Errorf("scanner %s: guard: %w", role.Name, gerr))...)
	case gerr != nil:
		passFailure(log, "builtin", gerr)
		res.GuardRule = "scan-error"
		record(OutcomeScanFailed)
		return res, errors.Join(errs...)
	case len(gr.Hits) > 0:
		for i, h := range gr.Hits {
			if i == 20 {
				break
			}
			log.Warn("scanner.guard_flagged", "finding", h.Index, "field", h.Field, "rule", h.Rule)
		}
		log.Warn("scanner.guard_flagged_total", "hits", len(gr.Hits))
		res.GuardRule, res.GuardField = gr.Hits[0].Rule, gr.Hits[0].Field
		record(OutcomeScanFailed)
		return res, errors.Join(errs...)
	case gr.OptionalErr != nil:
		blocked := cfg.Redact.Gitleaks.BlockAutocloseWhenMissing || !role.Autoclose
		allowed := "allowed"
		if blocked {
			allowed = "blocked"
		}
		passFailure(log, "gitleaks", gr.OptionalErr, "autoclose", allowed)
		res.GuardRule = "scan-error"
		if blocked {
			record(OutcomeScanFailed)
			return res, errors.Join(errs...)
		}
		closeOnly = true
	}

	// 4. Diff.
	plan := Diff(snap, sr.Findings, role.Autoclose, start)
	res.New, res.Regress, res.Suppressed = len(plan.New), len(plan.Regress), len(plan.Suppressed)

	ex := &execution{p: p, ctx: ctx, role: role, sha: sr.SHA, now: start, store: store, snap: &snap, log: log, res: &res, dry: dry, routeLbl: routeLbl, closeOnly: closeOnly}
	ex.byNumber = map[int]IssueView{}
	for _, v := range views {
		ex.byNumber[v.Number] = v
	}

	if closeOnly {
		// Gitleaks could not run: only close issues whose lines are all gone.
		// Nothing from this run's findings is written.
		ex.edits(plan, views)
		errs = append(errs, ex.errs...)
		if len(ex.errs) > 0 {
			record(OutcomeError)
		} else {
			record(OutcomeScanFailed)
		}
		return res, errors.Join(errs...)
	}

	// 6. Execute: edits, joins, creates. (Persist is folded in: each step
	// writes its rows as soon as its GitHub effect succeeded.)
	ex.edits(plan, views)

	// 5. Group.
	budget := min(role.MaxOpen-openCount, cfg.GlobalMaxOpen-globalOpen)
	budget = max(budget, 0)
	items := make([]Item, 0, len(plan.New)+len(plan.Regress))
	for _, f := range plan.New {
		items = append(items, Item{F: f})
	}
	for _, r := range plan.Regress {
		items = append(items, Item{F: r.Finding, RegressionOf: r.Of})
	}
	if role.LLMScanner() {
		// Dry-run synthetic issues carry no title and are not compared.
		var titled []TitledIssue
		for _, v := range views {
			ti := TitledIssue{Number: v.Number, Titles: []string{v.Title}}
			for _, l := range v.Parsed.Lines {
				ti.Titles = append(ti.Titles, l.Title)
			}
			titled = append(titled, ti)
		}
		var dups []NearDup
		items, dups = SkipNearDuplicates(items, titled)
		res.NearDup = len(dups)
		for _, d := range dups {
			log.Info("scanner.near_duplicate", "fingerprint", d.Fingerprint, "issue", d.Issue)
		}
	}
	ops, over := PlanGroups(items, ex.joinable(views, synth), role.GroupSize, budget)
	res.OverBudget = over
	ex.budget = budget
	for _, op := range ops {
		ex.file(op)
	}

	// 7. Persist what was seen.
	ex.touchSeen(plan)

	errs = append(errs, ex.errs...)
	if len(errs) > 0 {
		record(OutcomeError)
		return res, errors.Join(errs...)
	}
	record(OutcomeOK)
	return res, nil
}

func (p *Pipeline) apply(ctx context.Context, store Store, snap *Snapshot, b Batch) error {
	if len(b.DeleteFindings)+len(b.DeleteSuppressions)+len(b.PutFindings)+len(b.PutSuppressions) == 0 {
		return nil
	}
	if err := store.Apply(ctx, b); err != nil {
		return fmt.Errorf("save scanner state: %w", err)
	}
	snap.Apply(b)
	return nil
}

// execution holds the state of the write phase of one run.
type execution struct {
	p      *Pipeline
	ctx    context.Context
	role   config.Role
	sha    string
	now    time.Time
	store  Store
	snap   *Snapshot
	log    *slog.Logger
	res    *RunResult
	dry    bool
	budget int

	routeLbl  string // the route's label; "" under route none
	closeOnly bool   // the guard's optional pass failed: only close, write nothing from findings
	errs      []error

	byNumber map[int]IssueView
}

func (e *execution) fail(err error) {
	e.log.Warn("scanner.step_failed", "error", err)
	e.errs = append(e.errs, err)
}

func (e *execution) save(b Batch) {
	if err := e.p.apply(e.ctx, e.store, e.snap, b); err != nil {
		e.fail(err)
	}
}

// issueNow is an issue as it is right before a write.
type issueNow struct {
	Number int
	Labels []string
	Body   string
	Parsed Parsed
}

// fetch re-reads issue n. ok is false when the issue is gone or closed.
// Synthetic (dry-run) issues have no GitHub body: it is rebuilt from rows.
func (e *execution) fetch(n int) (cur issueNow, ok bool, err error) {
	if n < 0 {
		var rows []Row
		for _, r := range e.snap.Rows {
			if r.Issue == n && tracked(r) {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			return issueNow{}, false, nil
		}
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].FirstSeen.Equal(rows[j].FirstSeen) {
				return rows[i].FirstSeen.Before(rows[j].FirstSeen)
			}
			return rows[i].Fingerprint < rows[j].Fingerprint
		})
		p := Parsed{Scanner: e.role.Name, Group: rows[0].Group, HasSpan: true}
		for _, r := range rows {
			p.Lines = append(p.Lines, Line{Checked: r.Status == StatusGone, Title: r.Fingerprint, Fingerprint: r.Fingerprint, Severity: Severity(r.Severity)})
		}
		return issueNow{Number: n, Labels: e.roleLabels(), Parsed: p}, true, nil
	}
	is, err := e.p.Reader.GetIssue(e.ctx, n)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return issueNow{}, false, nil
	case err != nil:
		return issueNow{}, false, fmt.Errorf("get issue #%d: %w", n, err)
	case is.State != github.StateOpen || is.IsPR || !hasLabel(is.Labels, labels.Scanner(e.role.Name)):
		return issueNow{}, false, nil
	}
	return issueNow{Number: n, Labels: is.Labels, Body: is.Body, Parsed: ParseBody(is.Body)}, true, nil
}

// roleLabels is the label set a new issue of this scanner is filed with.
func (e *execution) roleLabels() []string {
	l := []string{labels.Scanner(e.role.Name)}
	if e.routeLbl != "" {
		l = append(l, e.routeLbl)
	}
	return l
}

func isRunning(lbls []string) bool {
	for _, l := range lbls {
		if p, ok := labels.Parse(l); ok && p.Kind == labels.KindRole && p.State == labels.StateRunning {
			return true
		}
	}
	return false
}

// joinableIssue reports whether an issue is open, unclaimed and in group: its
// ard: labels are exactly the scanner's label plus the route's label (none
// under route none). Any other ard: label (pending under another route, hold,
// a running label) means a human or a role has claimed it.
func joinableIssue(lbls []string, scanner, routeLbl string, p Parsed, group string) bool {
	if !p.HasSpan || p.Group != group {
		return false
	}
	want := map[string]bool{strings.ToLower(labels.Scanner(scanner)): true}
	if routeLbl != "" {
		want[strings.ToLower(routeLbl)] = true
	}
	have := map[string]bool{}
	for _, l := range lbls {
		lower := strings.ToLower(l)
		if !strings.HasPrefix(lower, labels.Prefix) || labels.IsPriority(lower) {
			continue
		}
		if !want[lower] {
			return false
		}
		have[lower] = true
	}
	return len(have) == len(want)
}

func (e *execution) joinable(views []IssueView, synth map[int][]Row) []Joinable {
	var out []Joinable
	for _, v := range views {
		if v.Parsed.Group != "" && joinableIssue(v.Labels, e.role.Name, e.routeLbl, v.Parsed, v.Parsed.Group) {
			out = append(out, Joinable{Number: v.Number, Group: v.Parsed.Group, Lines: len(v.Parsed.Lines)})
		}
	}
	for n, rows := range synth {
		if g := rows[0].Group; g != "" {
			out = append(out, Joinable{Number: n, Group: g, Lines: len(rows)})
		}
	}
	return out
}

// edit is the set of changes planned for one issue.
type edit struct {
	gone      []string
	resurface []string
	rerate    map[string]Severity
}

// edits applies Gone, Resurface and severity changes, then closes issues
// whose lines are all checked. Hold issues are never touched; a close waits
// while a job runs on the issue.
func (e *execution) edits(plan Plan, views []IssueView) {
	by := map[int]*edit{}
	get := func(n int) *edit {
		if by[n] == nil {
			by[n] = &edit{rerate: map[string]Severity{}}
		}
		return by[n]
	}
	for _, fp := range plan.Gone {
		if r := e.snap.Rows[fp]; r.Issue != 0 {
			g := get(r.Issue)
			g.gone = append(g.gone, fp)
		}
	}
	for _, f := range plan.Resurface {
		if e.closeOnly {
			break
		}
		if r := e.snap.Rows[f.Fingerprint]; r.Issue != 0 {
			g := get(r.Issue)
			g.resurface = append(g.resurface, f.Fingerprint)
		}
	}
	for _, f := range plan.Seen {
		if e.closeOnly {
			break
		}
		if r := e.snap.Rows[f.Fingerprint]; r.Issue != 0 && r.Severity != string(f.Severity) {
			get(r.Issue).rerate[f.Fingerprint] = f.Severity
		}
	}
	if e.role.Autoclose { // issues already fully checked (a deferred close, or a human) are closed too
		for _, v := range views {
			if len(v.Parsed.Lines) > 0 && allChecked(v.Parsed.Lines) {
				get(v.Number)
			}
		}
		for n := range e.synthRows() {
			get(n)
		}
	}
	nums := make([]int, 0, len(by))
	for n := range by {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		e.editIssue(n, by[n])
	}
}

func (e *execution) synthRows() map[int][]Row {
	if !e.dry {
		return nil
	}
	return e.snap.syntheticOpen()
}

func allChecked(ls []Line) bool {
	for _, l := range ls {
		if !l.Checked {
			return false
		}
	}
	return len(ls) > 0
}

func (e *execution) editIssue(n int, ed *edit) {
	release := sync.OnceFunc(e.p.BodyLocks.Lock(n))
	defer release()
	cur, ok, err := e.fetch(n)
	if err != nil {
		e.fail(err)
		return
	}
	if !ok {
		return
	}
	if hasLabel(cur.Labels, labels.Hold) {
		e.res.Deferred++
		e.log.Info("scanner.deferred", "issue", n, "reason", labels.Hold)
		return
	}
	lines := append([]Line(nil), cur.Parsed.Lines...)
	idx := map[string]int{}
	for i, l := range lines {
		idx[l.Fingerprint] = i
	}
	var puts []Row
	touch := func(fp string, mut func(l *Line, r *Row)) {
		i, ok := idx[fp]
		r, has := e.snap.Rows[fp]
		if !ok || !has || r.Issue != n {
			return
		}
		mut(&lines[i], &r)
		puts = append(puts, r)
	}
	for _, fp := range ed.gone {
		touch(fp, func(l *Line, r *Row) { l.Checked = true; r.Status = StatusGone })
	}
	for _, fp := range ed.resurface {
		touch(fp, func(l *Line, r *Row) { l.Checked = false; r.Status = StatusOpen })
	}
	for fp, sev := range ed.rerate {
		touch(fp, func(l *Line, r *Row) { l.Severity = sev; r.Severity = string(sev) })
	}
	cur.Parsed.Lines = lines
	if e.closeOnly && !allChecked(lines) {
		return // an issue is only ticked when it is about to be closed
	}
	if len(puts) > 0 {
		body := RenderBody(cur.Body, cur.Parsed, nil)
		if body != cur.Body || n < 0 {
			if err := e.p.Effects.UpdateIssueBody(e.ctx, n, body); err != nil {
				e.fail(fmt.Errorf("update issue #%d: %w", n, err))
				return
			}
			e.res.Edited++
		}
		e.save(Batch{PutFindings: puts})
	}
	release()
	if !e.role.Autoclose || !allChecked(lines) {
		return
	}
	if isRunning(cur.Labels) {
		e.res.Deferred++
		e.log.Info("scanner.deferred", "issue", n, "reason", "a job is running on the issue")
		return
	}
	if _, err := e.p.Effects.CreateComment(e.ctx, n, fmt.Sprintf("No longer detected at `%s`.", e.sha)); err != nil {
		e.fail(fmt.Errorf("comment on issue #%d: %w", n, err))
		return
	}
	st, reason := github.StateClosed, github.ReasonCompleted
	if err := e.p.Effects.UpdateIssue(e.ctx, n, github.IssueUpdate{State: &st, StateReason: &reason}); err != nil {
		e.fail(fmt.Errorf("close issue #%d: %w", n, err))
		return
	}
	e.res.Closed++
	var fixed []Row
	for _, r := range e.snap.Rows {
		if r.Issue == n && tracked(r) {
			r.Status = StatusFixed
			fixed = append(fixed, r)
		}
	}
	sort.Slice(fixed, func(i, j int) bool { return fixed[i].Fingerprint < fixed[j].Fingerprint })
	e.save(Batch{PutFindings: fixed})
}

func (e *execution) rowFor(f Finding, issue int) Row {
	return Row{
		Fingerprint: f.Fingerprint, Issue: issue, Status: StatusOpen, Severity: string(f.Severity), Group: f.Group,
		FirstSeen: e.now, LastSeen: e.now, LastSHA: e.sha,
	}
}

func lineFor(it Item) Line {
	return Line{Title: it.F.Title, Fingerprint: it.F.Fingerprint, Severity: it.F.Severity, RegressionOf: it.RegressionOf}
}

func findingsOf(items []Item) []Finding {
	out := make([]Finding, len(items))
	for i, it := range items {
		out[i] = it.F
	}
	return out
}

// file executes one GroupOp.
func (e *execution) file(op GroupOp) {
	if op.Join != 0 {
		if e.join(op) {
			return
		}
		if e.budget <= 0 {
			e.res.OverBudget += len(op.Items)
			e.log.Info("scanner.join_fallback_over_budget", "issue", op.Join)
			return
		}
		op.Join = 0 // fall back to a new issue within the budget
		op.Items = e.sortedItems(op.Items)
	}
	e.create(op)
}

func (e *execution) sortedItems(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.Slice(out, func(i, j int) bool { return lessItem(out[i], out[j]) })
	return out
}

// join appends op.Items to issue op.Join. It returns false when the issue is
// no longer joinable (so the caller falls back to a new issue) and true when
// the op is done, even if it failed.
func (e *execution) join(op GroupOp) bool {
	release := sync.OnceFunc(e.p.BodyLocks.Lock(op.Join))
	defer release()
	cur, ok, err := e.fetch(op.Join)
	if err != nil {
		e.fail(err)
		return true
	}
	if !ok || !joinableIssue(cur.Labels, e.role.Name, e.routeLbl, cur.Parsed, op.Group) {
		return false
	}
	have := map[string]bool{}
	for _, l := range cur.Parsed.Lines {
		have[l.Fingerprint] = true
	}
	var items []Item
	for _, it := range op.Items {
		if !have[it.F.Fingerprint] {
			items = append(items, it)
		}
	}
	if room := e.p.groupSize(e.role) - len(cur.Parsed.Lines); room < len(items) {
		if room <= 0 {
			return false
		}
		items = items[:room]
	}
	if len(items) == 0 {
		return true
	}
	if k := fitItems(cur.Body, cur.Parsed.Lines, e.role.Name, op.Group, items); k < len(items) {
		if k == 0 {
			return false
		}
		e.res.OverBudget += len(items) - k // unrecorded: filed next run
		items = items[:k]
	}
	parsed := cur.Parsed
	parsed.Lines = append(append([]Line(nil), parsed.Lines...), linesOf(items)...)
	body := RenderBody(cur.Body, parsed, findingsOf(items))
	err = e.p.Effects.UpdateIssueBody(e.ctx, op.Join, body)
	release()
	if err != nil {
		e.fail(fmt.Errorf("join issue #%d: %w", op.Join, err))
		return true
	}
	rows := make([]Row, len(items))
	for i, it := range items {
		rows[i] = e.rowFor(it.F, op.Join)
	}
	e.save(Batch{PutFindings: rows})
	e.res.Joined += len(items)
	e.res.Filed += len(items)
	title := IssueTitle(e.role.Name, op.Group, e.p.groupSize(e.role), items)
	e.issueMD(op.Join, title, parsed, findingsOf(items), cur.Body)
	return true
}

func (p *Pipeline) groupSize(r config.Role) int { return max(r.GroupSize, 1) }

func linesOf(items []Item) []Line {
	out := make([]Line, len(items))
	for i, it := range items {
		out[i] = lineFor(it)
	}
	return out
}

// llmFooter ends the body of an issue filed by a scanner that does not route
// to triage. A later join edits the body in place, so the footer stays put.
const llmFooter = "Filed by an LLM scanner; not triaged automatically. Add `ard:pending` to send it to triage, or close it as not planned to dismiss it for 90 days."

// createBody renders the body of a new issue, with the footer unless the
// scanner routes to ard:pending.
func createBody(role config.Role, parsed Parsed, fs []Finding) string {
	body := RenderBody("", parsed, fs)
	if role.Route != config.RoutePending {
		body = strings.TrimRight(body, "\n") + "\n\n" + llmFooter + "\n"
	}
	return body
}

// create files a new issue for op.Items.
func (e *execution) create(op GroupOp) {
	if e.budget <= 0 {
		e.res.OverBudget += len(op.Items)
		return
	}
	e.budget--
	if k := fitItems("", nil, e.role.Name, op.Group, op.Items); k < len(op.Items) {
		k = max(k, 1)
		e.res.OverBudget += len(op.Items) - k
		op.Items = op.Items[:k]
	}
	parsed := Parsed{Scanner: e.role.Name, Group: op.Group, HasSpan: true, Lines: linesOf(op.Items)}
	title := IssueTitle(e.role.Name, op.Group, e.p.groupSize(e.role), op.Items)
	body := createBody(e.role, parsed, findingsOf(op.Items))
	is, err := e.p.Effects.CreateIssue(e.ctx, github.NewIssue{Title: title, Body: body, Labels: e.roleLabels()})
	if err != nil {
		e.fail(fmt.Errorf("create issue %q: %w", title, err))
		return
	}
	rows := make([]Row, len(op.Items))
	for i, it := range op.Items {
		rows[i] = e.rowFor(it.F, is.Number)
	}
	// Record the rows at once: if this fails, reconcile adopts the issue from
	// its markers on the next run instead of filing a duplicate.
	e.save(Batch{PutFindings: rows})
	e.res.Created++
	e.res.Filed += len(op.Items)
	e.issueMD(is.Number, title, parsed, findingsOf(op.Items), "")
}

// issueMD queues .ard/<n>/issue.md to the committer and waits for the push.
// Under dry-run nothing is committed (a dry-run issue has a negative number,
// which the committer's guard rejects, and a join to a real issue would be
// re-proposed on every run), so only the would-be file is logged. A committer failure does not undo the
// issue: its body already carries the checklist.
func (e *execution) issueMD(n int, title string, p Parsed, add []Finding, prevBody string) {
	md := RenderIssueMD(e.role.Name, e.sha, n, title, p, add, prevBody)
	path := fmt.Sprintf(".ard/%d/issue.md", n)
	if n < 0 || e.dry {
		e.log.Info("scanner.issue_md", "path", path, "bytes", len(md), "dry_run", e.dry)
		return
	}
	_, err := e.p.Committer.Commit(e.ctx, committer.Request{
		Issue: n, Stage: "scanner", Role: e.role.Name, Summary: title,
		Files: []committer.File{{Path: path, Data: md}},
	})
	if err != nil {
		e.log.Error("scanner.issue_md_failed", "issue", n, "path", path, "error", err)
	}
}

// touchSeen records that fingerprints still reported were seen at this sha.
func (e *execution) touchSeen(plan Plan) {
	var rows []Row
	add := func(fp string) {
		if r, ok := e.snap.Rows[fp]; ok && tracked(r) {
			r.LastSeen, r.LastSHA = e.now, e.sha
			rows = append(rows, r)
		}
	}
	for _, f := range plan.Seen {
		add(f.Fingerprint)
	}
	for _, f := range plan.Resurface {
		add(f.Fingerprint)
	}
	if len(rows) > 0 {
		e.save(Batch{PutFindings: rows})
	}
}

// ledgerIssues is implemented by the dry-run effect layer, which records
// every would-be issue in its ledger.
type ledgerIssues interface {
	Issues() ([]effects.DryIssue, error)
}

// adoptLedger is reconcile for dry-run. Synthetic issues are not on GitHub, so
// a crash or failed save between a synthetic CreateIssue and recording its
// rows would re-file the finding on the next run, and Gate 1 counts exactly
// that duplicate in the ledger. Rows are adopted from the ledger's CreateIssue
// records for this scanner that are negative, not closed later, and not
// already in the database.
func (p *Pipeline) adoptLedger(ctx context.Context, store Store, snap *Snapshot, role config.Role, now time.Time, sha string) error {
	li, ok := p.Effects.(ledgerIssues)
	if !ok {
		return nil
	}
	issues, err := li.Issues()
	if err != nil {
		return fmt.Errorf("read dry-run ledger: %w", err)
	}
	var rows []Row
	for _, is := range issues {
		if is.Closed || is.Number >= 0 || !hasLabel(is.Labels, labels.Scanner(role.Name)) {
			continue
		}
		parsed := ParseBody(is.Body)
		for _, l := range parsed.Lines {
			if _, has := snap.Rows[l.Fingerprint]; has {
				continue
			}
			r := Row{
				Fingerprint: l.Fingerprint, Issue: is.Number, Status: StatusOpen, Severity: string(l.Severity),
				Group: parsed.Group, FirstSeen: now, LastSeen: now, LastSHA: sha,
			}
			if l.Checked {
				r.Status = StatusGone
			}
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	p.log().Info("scanner.adopt_ledger", "scanner", role.Name, "rows", len(rows))
	return p.apply(ctx, store, snap, Batch{PutFindings: rows})
}

// fitItems returns how many of items can be added to a body without it
// passing MaxBodyChars. The checklist is never trimmed, so the lines are what
// is bounded: a group_size past what fits would make the create or update
// fail on every run. Details are trimmed by RenderBody and are not counted.
func fitItems(prevBody string, existing []Line, scanner, group string, items []Item) int {
	size := len(RenderBody(prevBody, Parsed{Scanner: scanner, Group: group, Lines: existing}, nil))
	n := 0
	for _, it := range items {
		size += len(FormatLine(lineFor(it))) + 1
		if size > MaxBodyChars {
			break
		}
		n++
	}
	return n
}
