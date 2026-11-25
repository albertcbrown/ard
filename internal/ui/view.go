package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	minWidth, minHeight     = 20, 8
	largeWidth, largeHeight = 100, 40
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	headStyle  = lipgloss.NewStyle().Bold(true).Underline(true)
	selStyle   = lipgloss.NewStyle().Reverse(true)
	warnStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
)

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	if d < 0 {
		d = 0
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func usd(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", *p)
}

// Render draws the model into a string of height lines, each at most width
// cells wide. It is pure: the clock comes from the snapshot. Every string that
// came from outside the process goes through Clean or Trunc.
func Render(m Model) string {
	w, h := m.width, m.height
	if w < minWidth || h < minHeight {
		return "terminal too small"
	}
	s := m.snap
	var lines []string
	add := func(l string) { lines = append(lines, Trunc(l, w)) }
	section := func(t string) { lines = append(lines, headStyle.Render(Trunc(t, w))) }

	add(titleStyle.Render("ard serve") + "  " + Clean(m.Repo))
	hdr := fmt.Sprintf("%s  pool %d/%d  spend $%.2f", s.TakenAt.UTC().Format("2006-01-02 15:04:05Z"), s.Pool.Used, s.Pool.Max, s.Spend.RepoUSD)
	if s.Spend.RepoCap > 0 {
		hdr += fmt.Sprintf(" of $%.2f", s.Spend.RepoCap)
	}
	add(hdr)
	if m.LogPath != "" {
		add(dimStyle.Render("logs: " + Clean(m.LogPath)))
	}

	section("Needs you")
	need := 0
	for _, p := range s.Pause {
		need++
		add(warnStyle.Render("paused") + fmt.Sprintf(" %s: %s %s", Clean(p.Loop), Clean(string(p.Reason)), Clean(p.Err)))
	}
	for _, c := range s.Spend.CappedNow {
		need++
		add(warnStyle.Render("capped") + fmt.Sprintf(" %v", c))
	}
	for _, b := range s.Limits.Blocks {
		need++
		fam := Clean(b.Family)
		if fam == "" {
			fam = "unknown"
		}
		add(warnStyle.Render("limit") + fmt.Sprintf(" %s until %s", fam, b.Until.UTC().Format("15:04Z")))
	}
	for _, e := range s.Errors {
		need++
		add(warnStyle.Render("error") + " " + Clean(e))
	}
	if need == 0 {
		add(dimStyle.Render("nothing"))
	}

	section(fmt.Sprintf("Running (%d)", len(s.Running)))
	for _, r := range s.Running {
		ref := "scan"
		if r.Issue > 0 {
			ref = fmt.Sprintf("#%d", r.Issue)
		}
		t := dur(r.Elapsed)
		if r.Timeout > 0 {
			t += "/" + dur(r.Timeout)
		}
		add(fmt.Sprintf("%-6s %s:%s %s %s", ref, Clean(r.Stage), Clean(r.Role), t, usd(r.FinishedCostUSD)))
	}

	section("Waiting")
	if len(s.Pool.Waiting) == 0 {
		add(dimStyle.Render("nothing"))
	}
	for _, x := range s.Pool.Waiting {
		l := fmt.Sprintf("%s:%s %d %s", Clean(x.Class), Clean(x.Role), x.Count, Clean(x.Reason))
		if x.Oldest > 0 {
			l += " oldest " + dur(x.Oldest)
		}
		add(l)
	}

	section("Limits")
	if len(s.Limits.Windows) == 0 {
		add(dimStyle.Render("no readings"))
	}
	for _, x := range s.Limits.Windows {
		add(fmt.Sprintf("%s %3.0f%% %s resets %s", Clean(x.Window), x.Utilization*100, Clean(x.Status), x.ResetsAt.UTC().Format("15:04Z")))
	}

	var tail []string
	if w >= largeWidth && h >= largeHeight {
		tail = append(tail, headStyle.Render("Next tick"))
		for _, t := range s.Ticks {
			l := fmt.Sprintf("%-10s ticks %d last %s", Clean(t.Loop), t.Count, t.End.UTC().Format("15:04:05Z"))
			if t.Err != "" {
				l += " err: " + Clean(t.Err)
			}
			tail = append(tail, Trunc(l, w))
		}
		tail = append(tail, headStyle.Render("Recent events"))
		ev := s.Events
		if len(ev) > 8 {
			ev = ev[len(ev)-8:]
		}
		for _, e := range ev {
			tail = append(tail, Trunc(fmt.Sprintf("%s %s %s %s", e.Time.UTC().Format("15:04:05"), e.Level, Clean(e.Msg), Clean(e.Attrs)), w))
		}
	}

	if m.help {
		lines = append(lines, headStyle.Render("Help"),
			"?  toggle help", "j/k, arrows  select issue", "o  open selected issue in the browser", "q  quit (asks first)", "ctrl+c  stop now")
	}

	room := h - len(lines) - len(tail) - 2 // issues heading and footer
	if room < 1 {
		room = 1
	}
	pages := (len(s.Issues) + room - 1) / room
	if pages < 1 {
		pages = 1
	}
	page := m.selected / room
	title := "Issues"
	if pages > 1 {
		title = fmt.Sprintf("Issues (page %d/%d)", page+1, pages)
	}
	section(title)
	if len(s.Issues) == 0 {
		lines = append(lines, dimStyle.Render("none today"))
	}
	for i := page * room; i < len(s.Issues) && i < (page+1)*room; i++ {
		x := s.Issues[i]
		l := Trunc(fmt.Sprintf("#%-5d %s  jobs %d  %s", x.Issue, Clean(x.State), x.Jobs, usd(x.CostUSD)), w)
		if i == m.selected {
			l = selStyle.Render(l)
		}
		lines = append(lines, l)
	}
	lines = append(lines, tail...)

	footer := "? help  j/k select  o open  q quit"
	if m.confirmQuit {
		footer = "quit? y/n"
	}
	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, dimStyle.Render(Trunc(footer, w)))
	return strings.Join(lines, "\n")
}
