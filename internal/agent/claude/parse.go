package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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
}

// Parse reads the stream-json output. The last result event wins; unknown
// event types and fields are ignored.
func (*Adapter) Parse(stdout io.Reader, exitCode int) (agent.Result, error) {
	res := agent.Result{ExitCode: exitCode}
	var last *resultEvent
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
				} else {
					switch {
					case h.Type == "system" && h.Subtype == "init":
						var e initEvent
						if json.Unmarshal(line, &e) == nil {
							res.SessionID, res.Model, res.CLIVersion = e.SessionID, e.Model, e.Version
						}
					case h.Type == "result":
						var e resultEvent
						if json.Unmarshal(line, &e) != nil {
							res.SkippedLines++
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
	}
	switch {
	case readErr != nil:
		return res, fmt.Errorf("read agent stdout: %w", readErr)
	case last == nil:
		return res, agent.ErrNoResult
	}
	return res, nil
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
