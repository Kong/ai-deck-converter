package convert

// IssuesSafeToDeploy reports whether a converted configuration should be
// applied without manual review of the issue list. Any ERROR blocks deployment.
// ROUTING_CONFLICT also blocks deployment because Kong picks one matching route
// without a user-controlled rule.
func IssuesSafeToDeploy(issues []ConversionIssue) bool {
	for _, issue := range issues {
		if issue.Severity == Error {
			return false
		}
		if issue.Code == RoutingConflict {
			return false
		}
	}
	return true
}

// SafeToDeploy reports whether the conversion output should be applied without
// manual review. The Output field may still be populated when this returns
// false; callers must not treat stdout alone as deploy-ready.
func (r *ConversionResult) SafeToDeploy() bool {
	if r == nil {
		return true
	}
	return IssuesSafeToDeploy(r.Issues)
}
