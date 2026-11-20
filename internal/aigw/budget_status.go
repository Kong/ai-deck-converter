package aigw

// BudgetStatus turns on the budget status endpoint. The endpoint answers on the
// data plane with the Konnect budgets of the caller.
type BudgetStatus struct {
	Enabled *bool               `yaml:"enabled,omitempty"`
	Options BudgetStatusOptions `yaml:"options,omitempty"`
}

// BudgetStatusOptions are the optional settings of the budget status plugin.
type BudgetStatusOptions struct {
	// Timeout is the time in milliseconds that a Konnect data plane waits for
	// the budget read.
	Timeout *int `yaml:"timeout,omitempty"`
	// Document is the JSON budget document that a data plane serves when it is
	// not a Konnect data plane.
	Document string `yaml:"document,omitempty"`
	// Resource is the address of the endpoint. The data plane sends it in a
	// Link header and redirects /.well-known/budget-status to it.
	Resource string `yaml:"resource,omitempty"`
}
