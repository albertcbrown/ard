package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// graphqlURL derives the GraphQL endpoint from the REST base URL.
func graphqlURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/api/v3") {
		return strings.TrimSuffix(base, "/v3") + "/graphql"
	}
	return base + "/graphql"
}

// graphql posts one query or mutation. GraphQL-level errors become
// *HTTPError{Status: 200}. Mutations pass through the read-only guard, so they
// are refused under dry-run.
func (h *HTTP) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL(h.baseURL), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, body)
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode graphql response: %w", err)
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return &HTTPError{Status: http.StatusOK, Message: strings.Join(msgs, "; ")}
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

const pinIssueMutation = `mutation($id: ID!) { pinIssue(input: {issueId: $id}) { issue { id } } }`

const markReadyMutation = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id } } }`
