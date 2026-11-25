package roles

import (
	"strings"
	"testing"
)

func normPrompt(s string) string { return strings.Join(strings.Fields(s), " ") }

// missingPhrases returns the phrases absent from text, whitespace-normalised.
func missingPhrases(text string, phrases []string) []string {
	n := normPrompt(text)
	var out []string
	for _, p := range phrases {
		if !strings.Contains(n, p) {
			out = append(out, p)
		}
	}
	return out
}

var networkPins = []struct {
	name, path string
	phrases    []string
}{
	{"worker", "workers/engineer/prompt.txt", []string{
		"Network: available.", "Network: unavailable.", "needs-human", "`fetched:`", "CAPTURE.md",
		"regen script", "Make target", "Never invent", "checksum", "fetch only what the plan"}},
	{"triage-lead", "triage/lead/prompt.txt", []string{
		"Worker roles:", "only on the network", "not, alone, a reason for needs-human"}},
	{"triage-engineer", "triage/engineer/prompt.txt", []string{
		"### Network inputs", "exact URL", "pinned version", "landing path", "verification source",
		"the worker if", "a human"}},
	{"eng-review", "reviewers/eng-review/prompt.txt", []string{
		"`fetched:`", "CAPTURE.md", "regen", "not evidence on its own", "checksum file", "nothing was invented"}},
	{"security-review", "reviewers/security-review/prompt.txt", []string{
		"`fetched:`", "CAPTURE.md", "regen", "not evidence on its own", "checksum file", "nothing was invented"}},
}

func readStarter(t *testing.T, path string) string {
	t.Helper()
	b, err := FS.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNetworkPromptSentences(t *testing.T) {
	for _, c := range networkPins {
		t.Run(c.name, func(t *testing.T) {
			if m := missingPhrases(readStarter(t, c.path), c.phrases); len(m) > 0 {
				t.Errorf("%s lacks %q", c.path, m)
			}
		})
	}
}

func TestNetworkPromptSentencesFailOnMissing(t *testing.T) {
	text := strings.ReplaceAll(readStarter(t, "triage/engineer/prompt.txt"), "landing path", "x")
	m := missingPhrases(text, []string{"landing path", "exact URL"})
	if len(m) != 1 || m[0] != "landing path" {
		t.Fatalf("matcher reported %q", m)
	}
}
