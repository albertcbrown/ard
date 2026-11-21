// Package github is ard's only door to GitHub. It defines ard's own domain
// types and the Reader, Writer and Client interfaces; no go-github type
// crosses the package boundary.
//
// Issue.Body, Comment.Body, PullRequest.Body and TimelineEvent.Body are
// untrusted text (CLAUDE.md invariant 6): treat them as data, never splice
// them into prompts.
//
// Writes happen only through the effect layer (#13), which alone is handed a
// Writer. Every other package takes a Reader or a narrower interface of its
// own. The HTTP implementation authenticates as a GitHub App installation; a
// personal token is accepted for dry-run development only.
package github
