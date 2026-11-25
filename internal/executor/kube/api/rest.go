package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	defaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	maxBody          = 1 << 20
	maxMessage       = 512
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// RESTOptions configures InCluster. Zero values select the in-cluster
// defaults.
type RESTOptions struct {
	Host      string // host:port; default from KUBERNETES_SERVICE_HOST/_PORT
	TokenFile string
	CAFile    string
	Timeout   time.Duration // per request, except PodLogs; default 30s
}

type rest struct {
	base      string
	tokenFile string
	c         *http.Client // with timeout
	stream    *http.Client // no timeout, for log follow
}

// InCluster returns a Client that talks to the API server with the pod's
// projected service account token (re-read on every request) and only the
// cluster CA.
func InCluster(o RESTOptions) (Client, error) {
	host := o.Host
	if host == "" {
		h, p := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if h == "" || p == "" {
			return nil, errors.New("kube: no API host: KUBERNETES_SERVICE_HOST/_PORT unset")
		}
		host = net.JoinHostPort(h, p)
	}
	if o.TokenFile == "" {
		o.TokenFile = defaultTokenFile
	}
	if o.CAFile == "" {
		o.CAFile = defaultCAFile
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	ca, err := os.ReadFile(o.CAFile)
	if err != nil {
		return nil, fmt.Errorf("kube: read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("kube: CA file holds no certificate")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &rest{
		base:      "https://" + host,
		tokenFile: o.TokenFile,
		c:         &http.Client{Transport: tr, Timeout: o.Timeout, CheckRedirect: noRedirect},
		stream:    &http.Client{Transport: tr, CheckRedirect: noRedirect},
	}, nil
}

func checkName(kind, s string) error {
	if len(s) == 0 || len(s) > 63 || !dnsLabel.MatchString(s) {
		return fmt.Errorf("kube: invalid %s %q", kind, s)
	}
	return nil
}

func (r *rest) path(ns, res, name, sub string) (string, error) {
	if err := checkName("namespace", ns); err != nil {
		return "", err
	}
	p := "/api/v1/namespaces/" + ns + "/" + res
	if name != "" {
		if err := checkName("name", name); err != nil {
			return "", err
		}
		p += "/" + name
	}
	return p + sub, nil
}

// namedPath is path for a single object: an empty name is an error rather
// than a request for the whole collection.
func (r *rest) namedPath(ns, res, name, sub string) (string, error) {
	if err := checkName("name", name); err != nil {
		return "", err
	}
	return r.path(ns, res, name, sub)
}

// do sends a request and returns the 2xx response; the caller closes Body.
func (r *rest) do(ctx context.Context, c *http.Client, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := r.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(r.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("kube: read token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		return nil, ErrConflict
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("kube: unexpected redirect (status %d)", resp.StatusCode)
	}
	var st Status
	if json.Unmarshal(raw, &st) != nil {
		st = Status{Message: string(raw)}
	}
	if len(st.Message) > maxMessage {
		st.Message = st.Message[:maxMessage]
	}
	return nil, &StatusError{Code: resp.StatusCode, Reason: st.Reason, Message: st.Message}
}

func (r *rest) call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	resp, err := r.do(ctx, r.c, method, path, q, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8*maxBody)).Decode(out)
}

func (r *rest) CreatePod(ctx context.Context, ns string, p *Pod) (*Pod, error) {
	path, err := r.path(ns, "pods", "", "")
	if err != nil {
		return nil, err
	}
	var out Pod
	if err := r.call(ctx, http.MethodPost, path, nil, p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *rest) GetPod(ctx context.Context, ns, name string) (*Pod, error) {
	path, err := r.namedPath(ns, "pods", name, "")
	if err != nil {
		return nil, err
	}
	var out Pod
	if err := r.call(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *rest) ListPods(ctx context.Context, ns, labelSelector string) ([]Pod, error) {
	path, err := r.path(ns, "pods", "", "")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if labelSelector != "" {
		q.Set("labelSelector", labelSelector)
	}
	var out struct {
		Items []Pod `json:"items"`
	}
	if err := r.call(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (r *rest) DeletePod(ctx context.Context, ns, name string, graceSec int64) error {
	path, err := r.namedPath(ns, "pods", name, "")
	if err != nil {
		return err
	}
	body := map[string]any{"propagationPolicy": "Background", "gracePeriodSeconds": graceSec}
	err = r.call(ctx, http.MethodDelete, path, nil, body, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (r *rest) PodLogs(ctx context.Context, ns, name, container string, sinceTime time.Time) (io.ReadCloser, error) {
	path, err := r.namedPath(ns, "pods", name, "/log")
	if err != nil {
		return nil, err
	}
	q := url.Values{"follow": {"true"}, "timestamps": {"true"}}
	if container != "" {
		q.Set("container", container)
	}
	if !sinceTime.IsZero() {
		q.Set("sinceTime", sinceTime.UTC().Format(time.RFC3339Nano))
	}
	resp, err := r.do(ctx, r.stream, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (r *rest) CreateSecret(ctx context.Context, ns string, s *Secret) error {
	path, err := r.path(ns, "secrets", "", "")
	if err != nil {
		return err
	}
	return r.call(ctx, http.MethodPost, path, nil, s, nil)
}

func (r *rest) DeleteSecret(ctx context.Context, ns, name string) error {
	path, err := r.namedPath(ns, "secrets", name, "")
	if err != nil {
		return err
	}
	err = r.call(ctx, http.MethodDelete, path, nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (r *rest) ListEvents(ctx context.Context, ns, fieldSelector string) ([]Event, error) {
	path, err := r.path(ns, "events", "", "")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if fieldSelector != "" {
		q.Set("fieldSelector", fieldSelector)
	}
	var out struct {
		Items []Event `json:"items"`
	}
	if err := r.call(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
