// Package modelauth picks the one model credential agent jobs receive and
// carries its value from the controller's environment to the executor. The
// value never enters a Spec, a Handle, a log line or an error.
package modelauth

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/redact"
)

// Mode selects the credential.
type Mode string

// The supported modes.
const (
	ModeAuto       Mode = "auto" // oauth token if set, else API key
	ModeAPIKey     Mode = "apikey"
	ModeOAuthToken Mode = "oauth"
)

// EnvMode is the controller env var that overrides ModeAuto.
const EnvMode = "ARD_AGENT_AUTH"

var (
	// ErrBadMode means the mode is not one of the Mode constants.
	ErrBadMode = errors.New("modelauth: unknown mode")
	// ErrNoCredential means the selected variable is unset or empty.
	ErrNoCredential = errors.New("modelauth: credential not set")
	// ErrBadValue means the value is unusable (too short to redact, or it
	// has whitespace or control characters).
	ErrBadValue = errors.New("modelauth: credential value rejected")
)

// Credential is the selected credential. Its value is unexported and never
// printed: String, GoString and LogValue show the name only. The zero value
// means no credential was selected.
type Credential struct {
	Auth  agent.Auth
	Name  string        // e.g. CLAUDE_CODE_OAUTH_TOKEN
	value func() string // a closure, so %+v on a struct holding a Credential cannot print it
}

// IsZero reports whether no credential is selected.
func (c Credential) IsZero() bool { return c.Name == "" }

// Require returns c, or ErrNoCredential naming nothing secret when none was
// selected. Callers use it where a job needs a credential.
func (c Credential) Require() (Credential, error) {
	if c.IsZero() {
		return Credential{}, fmt.Errorf("%w: set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY", ErrNoCredential)
	}
	return c, nil
}

// Resolve picks the credential. names maps an Auth to the adapter's env var
// (claude.Adapter.CredentialEnv). mode "" means ModeAuto.
func Resolve(mode Mode, names func(agent.Auth) (string, error), getenv func(string) string) (Credential, error) {
	var order []agent.Auth
	switch mode {
	case "", ModeAuto:
		order = []agent.Auth{agent.AuthOAuthToken, agent.AuthAPIKey}
	case ModeOAuthToken:
		order = []agent.Auth{agent.AuthOAuthToken}
	case ModeAPIKey:
		order = []agent.Auth{agent.AuthAPIKey}
	default:
		return Credential{}, fmt.Errorf("%w %q (want auto, oauth or apikey)", ErrBadMode, string(mode))
	}
	var tried []string
	for _, a := range order {
		name, err := names(a)
		if err != nil {
			return Credential{}, err
		}
		tried = append(tried, name)
		v := getenv(name)
		if v == "" {
			continue
		}
		if len(v) < redact.MinSecretLen {
			return Credential{}, fmt.Errorf("%w: %s is too short to redact", ErrBadValue, name)
		}
		if v != strings.TrimSpace(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return Credential{}, fmt.Errorf("%w: %s has whitespace or a control character", ErrBadValue, name)
		}
		return Credential{Auth: a, Name: name, value: func() string { return v }}, nil
	}
	return Credential{}, fmt.Errorf("%w: set %s", ErrNoCredential, strings.Join(tried, " or "))
}

// Env returns exactly one NAME=value entry, for executor.Command.Env.
func (c Credential) Env() []string {
	if c.value == nil {
		return nil
	}
	return []string{c.Name + "=" + c.value()}
}

// AddTo registers the value with the redactor's secret set.
func (c Credential) AddTo(s *redact.Secrets) error {
	if c.value == nil {
		return nil
	}
	return s.Add(c.Name, c.value())
}

// String shows the name only.
func (c Credential) String() string { return "modelauth.Credential(" + c.Name + ")" }

// GoString shows the name only.
func (c Credential) GoString() string { return c.String() }

// LogValue shows the name only.
func (c Credential) LogValue() slog.Value { return slog.StringValue(c.String()) }
