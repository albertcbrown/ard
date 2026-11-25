package setupcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Conversion is the result of exchanging a manifest code. Its private key
// and secrets are redacted from every formatting verb.
type Conversion struct {
	ID   int64
	Slug string
	PEM  string
}

func (c Conversion) String() string {
	return fmt.Sprintf("Conversion{id=%d slug=%s pem=[redacted]}", c.ID, c.Slug)
}
func (c Conversion) GoString() string { return c.String() }

// Format makes %v, %+v, %#v and %s all print the redacted form.
func (c Conversion) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, c.String()) }

// LogValue keeps slog from printing the key.
func (c Conversion) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Convert exchanges a manifest code for the App's credentials. The endpoint
// needs no authentication. Errors carry only the status and GitHub's
// message, never the response body.
func Convert(ctx context.Context, apiBase string, hc *http.Client, code string) (Conversion, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	u := strings.TrimRight(apiBase, "/") + "/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return Conversion{}, errors.New("setup: build conversion request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return Conversion{}, errors.New("setup: manifest conversion request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &m)
		msg := ""
		if m.Message != "" && !strings.Contains(m.Message, "PRIVATE KEY") {
			msg = ": " + m.Message
		}
		return Conversion{}, fmt.Errorf("setup: manifest conversion failed (HTTP %d)%s", resp.StatusCode, msg)
	}
	var out struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		PEM  string `json:"pem"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == 0 || out.Slug == "" || out.PEM == "" {
		return Conversion{}, errors.New("setup: manifest conversion returned an unexpected response")
	}
	return Conversion{ID: out.ID, Slug: out.Slug, PEM: out.PEM}, nil
}
