package imagescan

import (
	"archive/tar"
	"bytes"
	"context"
	"strings"
	"testing"
)

// Secret-shaped values are assembled at run time so this file never holds one.
var (
	fakeGitHub = "gh" + "s_" + strings.Repeat("Ab3", 12)
	fakeAnth   = "sk-" + "ant-" + "api03-" + strings.Repeat("Zq9_", 10)
	pemHeader  = "-----BEGIN " + "RSA PRIVATE" + " KEY-----"
	fakeKey    = pemHeader + "\n" + strings.Repeat("QUJD", 16) + "\n-----END RSA PRIVATE KEY-----\n"
)

type entry struct {
	name string
	body string
	typ  byte
}

func tarOf(t *testing.T, es ...entry) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range es {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if typ == tar.TypeSymlink {
			h.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &b
}

func TestScan(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		want    []string
	}{
		{"clean", []entry{{name: "etc/", typ: tar.TypeDir}, {name: "etc/hostname", body: "x\n"}, {name: "usr/bin/tool", body: "\x7fELF token: abc"}}, nil},
		{"github token in a file", []entry{{name: "home/agent/notes.txt", body: "a\nGH=" + fakeGitHub + "\n"}}, []string{"/home/agent/notes.txt:2: github-token"}},
		{"anthropic key in a binary", []entry{{name: "usr/local/bin/x", body: "\x00\x01" + fakeAnth + "\x00"}}, []string{"/usr/local/bin/x:1: anthropic-key"}},
		{"pem key with body", []entry{{name: "opt/k.pem", body: "x\n" + fakeKey}}, []string{"/opt/k.pem:2: private-key"}},
		{"pem key with headers", []entry{{name: "opt/k.pem", body: pemHeader + "\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00\n\n" + strings.Repeat("QUJD", 16) + "\n"}}, []string{"/opt/k.pem:1: private-key"}},
		{"pem header alone is a parser constant", []entry{{name: "usr/lib/libssl.so", body: "\x00" + pemHeader + "\x00unsupported\x00"}}, nil},
		{"ssh key by name, even empty", []entry{{name: "root/.ssh/id_ed25519", body: ""}}, []string{"/root/.ssh/id_ed25519: name:ssh-dir", "/root/.ssh/id_ed25519: name:ssh-private-key"}},
		{"git credentials", []entry{{name: "tmp/.git-credentials", body: "x"}}, []string{"/tmp/.git-credentials: name:git-credentials"}},
		{"netrc", []entry{{name: "root/.netrc", body: "x"}}, []string{"/root/.netrc: name:netrc"}},
		{"docker config", []entry{{name: "home/a/.docker/config.json", body: "{}"}}, []string{"/home/a/.docker/config.json: name:docker-config"}},
		{"claude credentials", []entry{{name: "tmp/.claude/.credentials.json", body: "{}"}}, []string{"/tmp/.claude/.credentials.json: name:claude-config", "/tmp/.claude/.credentials.json: name:claude-credentials"}},
		{"gh hosts", []entry{{name: "tmp/.config/gh/hosts.yml", body: "x"}}, []string{"/tmp/.config/gh/hosts.yml: name:gh-hosts"}},
		{"kube config symlink", []entry{{name: "root/.kube/config", typ: tar.TypeSymlink}}, []string{"/root/.kube/config: name:kube-config"}},
		{"key store", []entry{{name: "opt/app/keys.p12", body: "x"}}, []string{"/opt/app/keys.p12: name:key-store"}},
		{"dotenv", []entry{{name: "app/.env.production", body: "A=b"}}, []string{"/app/.env.production: name:dotenv"}},
		{"go testdata is allowed", []entry{{name: "usr/local/go/src/crypto/tls/testdata/k.pem", body: fakeKey}, {name: "usr/local/go/src/net/http/x_test.go", body: fakeGitHub}}, nil},
		{"allow never covers a name hit", []entry{{name: "usr/local/go/src/x/testdata/id_rsa", body: ""}}, []string{"/usr/local/go/src/x/testdata/id_rsa: name:ssh-private-key"}},
		{"allow is anchored", []entry{{name: "opt/usr/local/go/src/a/testdata/k.pem", body: fakeKey}}, []string{"/opt/usr/local/go/src/a/testdata/k.pem:1: private-key"}},
		{"absolute and dotted names are cleaned", []entry{{name: "./etc/../root/.netrc", body: "x"}}, []string{"/root/.netrc: name:netrc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, err := Scan(context.Background(), tarOf(t, tc.entries...), Options{})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, f := range fs {
				got = append(got, f.String())
				if strings.Contains(f.String(), fakeGitHub) || strings.Contains(f.String(), fakeAnth) {
					t.Errorf("finding carries the secret: %s", f)
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanFailsClosed(t *testing.T) {
	ctx := context.Background()
	if _, err := Scan(ctx, &bytes.Buffer{}, Options{}); err == nil {
		t.Error("empty stream: want an error")
	}
	if _, err := Scan(ctx, strings.NewReader(strings.Repeat("not a tar archive ", 64)), Options{}); err == nil {
		t.Error("garbage: want an error")
	}
	full := tarOf(t, entry{name: "a", body: strings.Repeat("x", 4096)}).Bytes()
	if _, err := Scan(ctx, bytes.NewReader(full[:1024]), Options{}); err == nil {
		t.Error("truncated: want an error")
	}
	if _, err := Scan(ctx, tarOf(t, entry{name: "big", body: "0123456789"}), Options{MaxFile: 4}); err == nil {
		t.Error("over the per-file limit: want an error")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Scan(cctx, tarOf(t, entry{name: "a", body: "x"}), Options{}); err == nil {
		t.Error("cancelled: want an error")
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		glob, path string
		want       bool
	}{
		{"usr/local/go/src/**/testdata/**", "usr/local/go/src/crypto/tls/testdata/a.pem", true},
		{"usr/local/go/src/**/testdata/**", "usr/local/go/src/testdata/a", true},
		{"usr/local/go/src/**/testdata/**", "usr/local/go/src/crypto/testdata", false},
		{"usr/local/go/src/**/*_test.go", "usr/local/go/src/a/b/c_test.go", true},
		{"usr/local/go/src/**/*_test.go", "usr/local/go/src/a/b/c.go", false},
		{"usr/lib/*-linux-gnu/libgnutls.so.*", "usr/lib/aarch64-linux-gnu/libgnutls.so.30.40.3", true},
		{"usr/lib/*-linux-gnu/libgnutls.so.*", "usr/lib/aarch64-linux-gnu/sub/libgnutls.so.30", false},
	}
	for _, tc := range tests {
		if got := allowed(tc.path, []Allow{{Glob: tc.glob}}); got != tc.want {
			t.Errorf("allowed(%q, %q) = %v, want %v", tc.path, tc.glob, got, tc.want)
		}
	}
}
