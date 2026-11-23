package config

// SelectReviewers returns the reviewer roles that review a PR by default, in
// config order. The candidates are worker.Reviewers (only roles that exist)
// when it is non-empty, else every reviewer role; a candidate stays when its
// When matches. A worker's list narrows the candidates and never bypasses
// when:. Lead or worker next requests are added by the caller.
func (r *Resolved) SelectReviewers(worker Role, changed []string, triageRan func(string) bool) []string {
	var allowed map[string]bool
	if len(worker.Reviewers) > 0 {
		allowed = map[string]bool{}
		for _, n := range worker.Reviewers {
			allowed[n] = true
		}
	}
	var out []string
	for _, role := range r.RolesOf(KindReviewer) {
		if allowed != nil && !allowed[role.Name] {
			continue
		}
		if role.When.Matches(changed, triageRan) {
			out = append(out, role.Name)
		}
	}
	return out
}
