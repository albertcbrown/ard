package redact

import (
	"math/rand/v2"
	"strings"
)

const b62 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randStr returns a deterministic high-entropy string.
func randStr(seed uint64, n int) string {
	g := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(b62[g.IntN(len(b62))])
	}
	return sb.String()
}

// tokens are built at run time so no token-shaped literal is ever committed.
func plantedTokens() map[string]string {
	return map[string]string{
		"github-token":        "gh" + "s_" + randStr(1, 36),
		"github-fine-grained": "github" + "_pat_" + randStr(2, 60),
		"aws-access-key":      "AK" + "IA" + strings.ToUpper(randStr(3, 16)),
		"slack-token":         "xo" + "xb-" + randStr(4, 24),
		"slack-webhook":       "hooks.sla" + "ck.com/services/" + randStr(5, 30),
		"private-key":         "-----BEGIN " + "RSA PRIVATE" + " KEY-----",
		"anthropic-key":       "sk-" + "ant-" + randStr(6, 40),
		"openai-key":          "sk-" + "proj-" + randStr(7, 40),
		"jwt":                 "ey" + "J" + randStr(8, 20) + ".ey" + "J" + randStr(9, 20) + "." + randStr(10, 20),
		"gcp-api-key":         "AI" + "za" + randStr(11, 35),
		"stripe-live":         "sk" + "_live_" + randStr(12, 30),
		"generic-assignment":  "api_" + "key = " + randStr(13, 32),
	}
}
