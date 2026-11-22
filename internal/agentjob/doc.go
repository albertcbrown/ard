// Package agentjob holds the building blocks the agent loops (triage, worker,
// later reviewer) share: the untrusted-input snapshot, the agent-to-executor
// command mapping, the handle save, the shutdown-aware wait and the
// post-once comment. Everything here is a small function; the loops own the
// policy.
package agentjob
