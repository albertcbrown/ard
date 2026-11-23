package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
)

const (
	maxLine    = 4 << 20
	maxSummary = 2000
)

type head struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type initEvent struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Version   string `json:"claude_code_version"`
}

type resultEvent struct {
	Subtype        string            `json:"subtype"`
	IsError        bool              `json:"is_error"`
	TerminalReason string            `json:"terminal_reason"`
	APIErrorStatus *int              `json:"api_error_status"`
	NumTurns       *int              `json:"num_turns"`
	TotalCostUSD   *float64          `json:"total_cost_usd"`
	DurationMS     float64           `json:"duration_ms"`
	SessionID      string            `json:"session_id"`
	Result         json.RawMessage   `json:"result"`
	Denials        []json.RawMessage `json:"permission_denials"`
	// Usage and ModelUsage stay raw so a malformed one never drops the result.
	Usage      json.RawMessage `json:"usage"`
	ModelUsage json.RawMessage `json:"modelUsage"`
}

type apiRetryEvent struct {
	Error string `json:"error"`
}

type assistantEvent struct {
	Error   string `json:"error"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type rateLimitEvent struct {
	Info struct {
		Status        string   `json:"status"`
		ResetsAt      *float64 `json:"resetsAt"`
		RateLimitType string   `json:"rateLimitType"` // undocumented; used when present
		ErrorCode     string   `json:"errorCode"`
		// UnifiedWindows stays raw so a malformed value never drops the event.
		UnifiedWindows json.RawMessage `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

type usageJSON struct {
	Input      *float64 `json:"input_tokens"`
	Output     *float64 `json:"output_tokens"`
	CacheWrite *float64 `json:"cache_creation_input_tokens"`
	CacheRead  *float64 `json:"cache_read_input_tokens"`
}

type modelUsageJSON struct {
	Input      *float64 `json:"inputTokens"`
	Output     *float64 `json:"outputTokens"`
	CacheRead  *float64 `json:"cacheReadInputTokens"`
	CacheWrite *float64 `json:"cacheCreationInputTokens"`
}

func tokens(v *float64) int64 {
	if v == nil || *v < 0 {
		return 0
	}
	return int64(*v)
}

// limitRE matches the CLI's own error text for a usage limit, for example
// "You've hit your session limit · resets 3:45pm". Apostrophe is straight or
// curly. It is applied ONLY to text the CLI synthesised (see limitAcc).
var limitRE = regexp.MustCompile(`(?i)you(?:'|’)ve hit your (session|weekly|opus|sonnet) limit`)

// limitAcc collects limit evidence in one pass.
//
// Untrusted text (invariant 6): the limit message is matched only on paths the
// CLI itself writes, never in agent output. An assistant event carrying a
// top-level error field is CLI-synthesised (agent text cannot set it), and so
// is the result of a run that ended is_error with HTTP 429 or terminal_reason
// api_error. An agent can print "You've hit your session limit" after reading
// a hostile issue body; if that text counted, a prompt-injected job would
// never fail, would be re-claimed every few minutes and would pause the whole
// account. So the phrase in a normal assistant message or in a successful
// result never sets a LimitHit.
//
// Observed on 2.1.328 (TestCLIRateLimitStream, against a loopback fake): a
// plain HTTP 429 throttle also emits api_retry with error rate_limit and a
// rate_limit_event with status "rejected" (no resetsAt, no rateLimitType), and
// ends with the CLI text "Server is temporarily limiting requests (not your
// usage limit)". So those signals alone cannot tell a usage limit from a
// throttle. That marker text is read on the same CLI-synthesised paths; with it
// and no positive evidence of a usage limit (limit message, reset time, scope,
// credits_required) there is no LimitHit, and the run stays an ordinary
// transient rate_limit failure.
type limitAcc struct {
	signal      agent.LimitSignal
	typed, text scopeFam // from rateLimitType; from the message text
	resetsAt    time.Time
	throttle    bool // CLI text says this is a throttle, not the usage limit
	positive    bool // evidence that only a usage limit produces
}

type scopeFam struct {
	scope  agent.LimitScope
	family string
}

func signalRank(s agent.LimitSignal) int {
	switch s {
	case agent.SignalRateLimitEvent:
		return 3
	case agent.SignalLimitMessage:
		return 2
	case agent.SignalRateLimitError:
		return 1
	}
	return 0
}

func (a *limitAcc) note(s agent.LimitSignal) {
	if signalRank(s) > signalRank(a.signal) {
		a.signal = s
	}
}

// rateLimitError reports whether an error code is a limit or billing stop.
func rateLimitError(code string) bool { return code == "rate_limit" || code == "billing_error" }

// matchText looks for the limit message in CLI-synthesised text.
func (a *limitAcc) matchText(s string) {
	if strings.Contains(strings.ToLower(s), "not your usage limit") {
		a.throttle = true
	}
	m := limitRE.FindStringSubmatch(s)
	if m == nil {
		return
	}
	a.note(agent.SignalLimitMessage)
	a.positive = true
	switch strings.ToLower(m[1]) {
	case "session":
		a.text = scopeFam{agent.ScopeSession, ""}
	case "weekly":
		a.text = scopeFam{agent.ScopeWeekly, ""}
	default:
		a.text = scopeFam{agent.ScopeModel, strings.ToLower(m[1])}
	}
}

func (a *limitAcc) rateLimitEvent(e rateLimitEvent) {
	i := e.Info
	if i.Status != "rejected" && i.ErrorCode != "credits_required" {
		return // allowed and allowed_warning belong to #63
	}
	if i.ErrorCode == "credits_required" {
		a.positive = true
	}
	if i.ResetsAt != nil {
		if t := resetTime(*i.ResetsAt); !t.IsZero() {
			a.resetsAt = t
			a.positive = true
		}
	}
	switch i.RateLimitType {
	case "five_hour":
		a.typed = scopeFam{agent.ScopeSession, ""}
	case "seven_day":
		a.typed = scopeFam{agent.ScopeWeekly, ""}
	case "seven_day_opus":
		a.typed = scopeFam{agent.ScopeModel, "opus"}
	case "seven_day_sonnet":
		a.typed = scopeFam{agent.ScopeModel, "sonnet"}
	}
	if a.typed.scope != "" {
		a.positive = true
	}
	// A bare rejected event (no reset, no scope) is what a plain 429 throttle
	// emits too, so it is only as strong as an api_retry rate_limit; the
	// strongest signal needs evidence only a usage limit carries. This keeps an
	// API-key throttle, killed before the CLI's closing text, from looking like
	// a subscription limit.
	if i.ErrorCode == "credits_required" || a.resetsAt.IsZero() == false || a.typed.scope != "" {
		a.note(agent.SignalRateLimitEvent)
	} else {
		a.note(agent.SignalRateLimitError)
	}
}

// resetTime converts the event's resetsAt. The unit is undocumented: a value
// above 1e11 is read as milliseconds, anything else as Unix seconds.
func resetTime(v float64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	if v > 1e11 {
		v /= 1000
	}
	return time.Unix(int64(v), 0).UTC()
}

func (a *limitAcc) hit() *agent.LimitHit {
	if a.signal == "" || (a.throttle && !a.positive) {
		return nil
	}
	sf := a.typed
	if sf.scope == "" {
		sf = a.text
	}
	return &agent.LimitHit{Scope: sf.scope, Family: sf.family, ResetsAt: a.resetsAt, Signal: a.signal}
}

// textOf joins the text blocks of an assistant message's content.
func textOf(content json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		var s string
		if json.Unmarshal(content, &s) == nil {
			return s
		}
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Parse reads the stream-json output. The last result event wins; unknown
// event types and fields are ignored.
func (*Adapter) Parse(stdout io.Reader, exitCode int) (agent.Result, error) {
	res := agent.Result{ExitCode: exitCode}
	var last *resultEvent
	var lim limitAcc
	windows := map[string]agent.Window{}
	br := bufio.NewReaderSize(stdout, 64<<10)
	var readErr error
	for {
		line, tooLong, err := readLine(br)
		if len(line) > 0 || tooLong {
			if tooLong {
				res.SkippedLines++
			} else if line = bytes.TrimSpace(line); len(line) == 0 {
				res.SkippedLines++
			} else {
				var h head
				if json.Unmarshal(line, &h) != nil {
					res.SkippedLines++
					// A damaged result line must not let an earlier result stand.
					if bytes.HasPrefix(line, []byte(`{"type":"result"`)) {
						last = nil
					}
				} else {
					switch {
					case h.Type == "system" && h.Subtype == "init":
						var e initEvent
						if json.Unmarshal(line, &e) == nil {
							res.SessionID, res.Model, res.CLIVersion = e.SessionID, e.Model, e.Version
						}
					case h.Type == "system" && h.Subtype == "api_retry":
						var e apiRetryEvent
						if json.Unmarshal(line, &e) != nil {
							res.SkippedLines++
						} else if rateLimitError(e.Error) {
							lim.note(agent.SignalRateLimitError)
						}
					case h.Type == "rate_limit_event":
						var e rateLimitEvent
						if json.Unmarshal(line, &e) != nil {
							res.SkippedLines++
						} else {
							lim.rateLimitEvent(e)
							for _, w := range readWindows(e.Info.UnifiedWindows) {
								windows[w.Type] = w
							}
						}
					case h.Type == "assistant":
						var e assistantEvent
						if json.Unmarshal(line, &e) != nil {
							res.SkippedLines++
						} else if e.Error != "" {
							if rateLimitError(e.Error) {
								lim.note(agent.SignalRateLimitError)
							}
							lim.matchText(textOf(e.Message.Content))
						}
					case h.Type == "result":
						var e resultEvent
						if json.Unmarshal(line, &e) != nil {
							res.SkippedLines++
							last = nil
						} else {
							last = &e
						}
					}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	if last != nil {
		res.Subtype, res.IsError, res.TerminalReason = last.Subtype, last.IsError, last.TerminalReason
		if last.APIErrorStatus != nil {
			res.APIErrorStatus = *last.APIErrorStatus
		}
		res.Turns, res.CostUSD = last.NumTurns, last.TotalCostUSD
		res.DurationMS = int64(last.DurationMS)
		if last.SessionID != "" {
			res.SessionID = last.SessionID
		}
		res.PermissionDenials = len(last.Denials)
		var s string
		if json.Unmarshal(last.Result, &s) == nil {
			if r := []rune(s); len(r) > maxSummary {
				s = string(r[:maxSummary])
			}
			res.Summary = s
		}
		res.OK = exitCode == 0 && !last.IsError && last.Subtype == "success"
		res.Usage, res.ModelUsage = parseUsage(last)
		if last.IsError {
			if res.APIErrorStatus == 429 {
				lim.note(agent.SignalRateLimitError)
			}
			if res.APIErrorStatus == 429 || last.TerminalReason == "api_error" {
				lim.matchText(res.Summary)
			}
		}
	}
	res.LimitHit = lim.hit()
	for _, w := range windows {
		res.Windows = append(res.Windows, w)
	}
	sort.Slice(res.Windows, func(i, j int) bool { return res.Windows[i].Type < res.Windows[j].Type })
	switch {
	case readErr != nil:
		return res, fmt.Errorf("read agent stdout: %w", readErr)
	case last == nil:
		return res, agent.ErrNoResult
	}
	return res, nil
}

// parseUsage reads result.usage and result.modelUsage. Usage prefers the sum
// of modelUsage (the whole run); result.usage covers the main loop only.
func parseUsage(e *resultEvent) (*agent.Usage, map[string]agent.Usage) {
	var model map[string]agent.Usage
	var raw map[string]modelUsageJSON
	if len(e.ModelUsage) > 0 && json.Unmarshal(e.ModelUsage, &raw) == nil && len(raw) > 0 {
		model = make(map[string]agent.Usage, len(raw))
		for k, m := range raw {
			model[k] = agent.Usage{Input: tokens(m.Input), Output: tokens(m.Output), CacheWrite: tokens(m.CacheWrite), CacheRead: tokens(m.CacheRead)}
		}
	}
	if model != nil {
		var sum agent.Usage
		for _, u := range model {
			sum = sum.Add(u)
		}
		return &sum, model
	}
	var u usageJSON
	if len(e.Usage) > 0 && json.Unmarshal(e.Usage, &u) == nil && (u != usageJSON{}) {
		return &agent.Usage{Input: tokens(u.Input), Output: tokens(u.Output), CacheWrite: tokens(u.CacheWrite), CacheRead: tokens(u.CacheRead)}, nil
	}
	return nil, nil
}

// readLine returns one line without its newline. A line over maxLine is
// discarded to its end and reported as tooLong.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxLine {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		return line, tooLong, e
	}
}

type windowJSON struct {
	Type        string   `json:"type"`
	Utilization *float64 `json:"utilization"`
	Status      string   `json:"status"`
	ResetsAt    *float64 `json:"resetsAt"`
}

// readWindows reads rate_limit_info.unifiedWindows: an object keyed by window
// name, or an array of objects with a type field. Anything else, and any entry
// with a missing, negative or non-finite utilization, yields no window.
func readWindows(raw json.RawMessage) []agent.Window {
	if len(raw) == 0 {
		return nil
	}
	byName := map[string]windowJSON{}
	var arr []windowJSON
	if json.Unmarshal(raw, &byName) == nil {
		for k, w := range byName {
			w.Type = k
			arr = append(arr, w)
		}
	} else if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	var out []agent.Window
	for _, w := range arr {
		if w.Type == "" || w.Utilization == nil {
			continue
		}
		u := *w.Utilization
		if math.IsNaN(u) || math.IsInf(u, 0) || u < 0 {
			continue
		}
		win := agent.Window{Type: w.Type, Utilization: u, Status: w.Status}
		if w.ResetsAt != nil {
			win.ResetsAt = resetTime(*w.ResetsAt)
		}
		out = append(out, win)
	}
	return out
}
