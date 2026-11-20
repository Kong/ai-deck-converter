package convert

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

const (
	budgetStatusName      = "budget-status"
	budgetStatusPath      = "/budgets"
	budgetStatusWellKnown = "/.well-known/budget-status"

	minBudgetStatusTimeout = 100
	maxBudgetStatusTimeout = 10000
)

var budgetResourceURLPattern = regexp.MustCompile(`^https?://[^/\s]+`)

// convertBudgetStatus makes one Service and Route for the budget status
// endpoint. The Route authenticates with every auth strategy of a converted
// model, because the endpoint answers 401 without a credential.
func (c *Converter) convertBudgetStatus() {
	b := c.src.BudgetStatus
	if b == nil || b.Enabled == nil || !*b.Enabled {
		return
	}
	src := issueSource(kindBudgetStatus, budgetStatusName)
	c.convertEntity(src, func() error {
		if !c.supports(aimap.FeatureBudgetStatus) {
			return c.failUnsupported(src.at("enabled"), aimap.FeatureBudgetStatus, "budget_status")
		}
		if err := c.validateBudgetStatusOptions(src, b.Options); err != nil {
			return err
		}
		authPlugins, err := c.scopedAuthStrategyPlugins(src, c.budgetStatusAuthStrategies())
		if err != nil {
			return err
		}
		if needsAnonymousFallback(authPlugins) {
			c.ensureAnonymousConsumer()
		} else {
			for i := range authPlugins {
				authPlugins[i] = withoutAnonymousFallback(authPlugins[i])
			}
		}
		if len(authPlugins) == 0 && c.globalAuthPolicy() == nil {
			c.warn(src.at("enabled"), BehaviorMayDiffer,
				"no converted model has an auth strategy and no global authentication policy exists. "+
					"The budget status endpoint answers 401 to every request.")
		}
		route := kong.Route{
			Name:    budgetStatusName,
			Paths:   []string{budgetStatusPath},
			Methods: []string{http.MethodGet},
			Plugins: append(authPlugins, kong.Plugin{
				Name:   "ai-proxy-advanced",
				Config: budgetStatusPluginConfig(b.Options),
			}),
		}
		// The data plane redirects the well-known path only when the Route
		// matches it.
		if b.Options.Resource != "" {
			route.Paths = append(route.Paths, budgetStatusWellKnown)
		}
		c.out.Services = append(c.out.Services, kong.Service{
			Name:   budgetStatusName,
			Host:   placeholderHost,
			Routes: []kong.Route{route},
		})
		return nil
	})
}

// budgetStatusAuthStrategies returns the auth strategy names of the enabled
// models that converted, without duplicates and in model order.
func (c *Converter) budgetStatusAuthStrategies() []string {
	var refs []string
	for i := range c.src.Models {
		m := &c.src.Models[i]
		if _, skipped := c.skippedModels[i]; skipped || isDisabled(m) {
			continue
		}
		for _, ref := range m.Access.AuthStrategies {
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func budgetStatusPluginConfig(o aigw.BudgetStatusOptions) map[string]any {
	cfg := map[string]any{"route_type": aimap.BudgetStatusRouteType}
	options := map[string]any{}
	if o.Timeout != nil {
		options["timeout"] = *o.Timeout
	}
	setIfNotEmpty(options, "document", o.Document)
	setIfNotEmpty(options, "resource", o.Resource)
	if len(options) > 0 {
		cfg["budget_status"] = options
	}
	return cfg
}

// validateBudgetStatusOptions applies the checks of the budget_status plugin
// schema. The data plane rejects the whole configuration when one fails.
func (c *Converter) validateBudgetStatusOptions(src ConversionIssueSource, o aigw.BudgetStatusOptions) error {
	if t := o.Timeout; t != nil && (*t < minBudgetStatusTimeout || *t > maxBudgetStatusTimeout) {
		return c.fail(src.at("options.timeout"), ConfigFieldInvalid,
			"options.timeout %d must be between %d and %d", *t, minBudgetStatusTimeout, maxBudgetStatusTimeout)
	}
	if r := o.Resource; r != "" && !strings.HasPrefix(r, "/") && !budgetResourceURLPattern.MatchString(r) {
		return c.fail(src.at("options.resource"), ConfigFieldInvalid,
			`options.resource %q must start with "/" or be an http or https URL`, r)
	}
	return nil
}
