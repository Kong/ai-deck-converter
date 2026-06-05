package revert

import (
	"sort"
	"strings"

	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/aimap"
	"github.com/gperanich/ai-deck-converter/internal/kong"
)

// modelGroup accumulates one AI Gateway Model across the routes/targets that
// share its model_alias (or, alias-less, its route).
type modelGroup struct {
	model       aigw.Model
	caps        []string
	capsSeen    map[string]bool
	targetsSeen map[string]bool
	aliasless   bool   // no model_alias on the targets; name is provisional
	routeName   string // route the group was created from (for warnings)
}

// modelAcc collects model groups in first-seen order.
type modelAcc struct {
	groups map[string]*modelGroup
	order  []string
}

// accumulateModelRoute folds one ai-proxy-advanced route into the model
// accumulator: each target resolves to a (capability, provider) and lands in
// the group keyed by its model_alias. Mirrors (in reverse) the routeGroup
// collapse in convert.convertModels.
func (r *Reverter) accumulateModelRoute(acc *modelAcc, rt *kong.Route, plugins []kong.Plugin) error {
	proxy := findPlugin(plugins, "ai-proxy-advanced")
	cfg := proxy.Config
	llmFormat := getStr(cfg, "llm_format")
	if llmFormat == "" {
		llmFormat = aimap.DefaultLLMFormat
	}
	genai := getStr(cfg, "genai_category")
	var path string
	if len(rt.Paths) > 0 {
		path = rt.Paths[0]
	}

	// Route-scoped guard plugins (other than the AI plugins) apply to every
	// model on the route.
	routeRefs, routeACLs := r.policyRefs(plugins)

	targets := getSlice(cfg, "targets")
	if len(targets) == 0 {
		return r.warn("route %q: ai-proxy-advanced has no targets; nothing to convert", rt.Name)
	}

	for _, raw := range targets {
		target, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		modelMap := getMap(target, "model")
		alias := getStr(modelMap, "model_alias")
		routeType := getStr(target, "route_type")
		providerType := detectProviderType(getStr(modelMap, "provider"), path)
		section := aimap.SectionFor(llmFormat, providerType)

		var capability, base string
		if match, ok := resolveEndpoint(section, routeType, genai, rt.Name, path); ok {
			capability = match.capability
			base, _ = basePathFor(path, match.spec)
		} else if routeType == "llm/v1/chat" {
			capability = "generate"
			if err := r.warn("route %q: cannot resolve capability for route_type %q in section %q; defaulting to generate", rt.Name, routeType, section); err != nil {
				return err
			}
		} else {
			if err := r.warn("route %q: cannot resolve capability for route_type %q in section %q; skipping target", rt.Name, routeType, section); err != nil {
				return err
			}
			continue
		}

		g, err := r.modelGroupFor(acc, rt, alias, llmFormat, base, cfg, routeRefs, routeACLs)
		if err != nil {
			return err
		}
		if !g.capsSeen[capability] {
			g.capsSeen[capability] = true
			g.caps = append(g.caps, capability)
		}

		tm := aigw.TargetModel{
			Name:         getStr(modelMap, "name"),
			Weight:       getInt(target, "weight"),
			SemanticDesc: getStr(target, "description"),
			Provider:     aigw.ProviderRef{},
			Config:       aigw.TargetModelConfig{Type: providerType},
		}
		d := defoldTarget(target, providerType)
		tm.AllowAuthOverride = d.allowOverride
		tm.Config.Options = d.options
		tm.Provider.Name = r.providerFor(providerType, &d)

		if !g.targetsSeen[tm.Name] {
			g.targetsSeen[tm.Name] = true
			g.model.TargetModels = append(g.model.TargetModels, tm)
		}
	}
	return nil
}

// modelGroupFor finds or creates the model group for an alias (or, alias-less,
// for the route), seeding model-level config from the route's plugin config.
func (r *Reverter) modelGroupFor(acc *modelAcc, rt *kong.Route, alias, llmFormat, base string, cfg map[string]any, routeRefs []string, routeACLs aigw.ACLs) (*modelGroup, error) {
	key := "alias:" + alias
	if alias == "" {
		key = "route:" + rt.Name
	}
	if g, ok := acc.groups[key]; ok {
		return g, nil
	}

	name := ""
	if alias != "" {
		if n, ok := r.aiModelByAlias[alias]; ok {
			name = n
			r.aiModelUsed[alias] = true
		} else {
			name = deriveModelName(alias)
			if err := r.warn("no ai-models entry for alias %q; deriving model name %q", alias, name); err != nil {
				return nil, err
			}
		}
	} else {
		// Provisional; finalizeModels matches alias-less groups to alias-less
		// ai-models entries by position when the counts line up.
		name = rt.Name
	}

	g := &modelGroup{
		capsSeen:    map[string]bool{},
		targetsSeen: map[string]bool{},
		aliasless:   alias == "",
		routeName:   rt.Name,
		model: aigw.Model{
			Type:     "model",
			Name:     name,
			Formats:  []aigw.Format{{Type: llmFormat}},
			Policies: routeRefs,
			ACLs:     routeACLs,
		},
	}
	g.model.Config.Model.Alias = alias
	g.model.Config.Model.NameHeader = getBool(cfg, "model_name_header")
	g.model.Config.ResponseStreaming = getStr(cfg, "response_streaming")
	g.model.Config.MaxRequestBodySize = getInt(cfg, "max_request_body_size")
	g.model.Config.Balancer = balancerFromConfig(getMap(cfg, "balancer"))
	if base != "" && base != aimap.DefaultBasePath {
		g.model.Config.Route.Paths = []string{base}
	}
	acc.groups[key] = g
	acc.order = append(acc.order, key)
	return g, nil
}

// finalizeModels applies model-scoped (ai-models FK) plugins, classifies the
// model type, emits the models in first-seen order, and reports orphans.
func (r *Reverter) finalizeModels(acc *modelAcc) error {
	if err := r.nameAliaslessGroups(acc); err != nil {
		return err
	}

	built := map[string]bool{}
	for _, key := range acc.order {
		g := acc.groups[key]
		g.model.Capabilities = g.caps
		if isAPIOnly(g.caps) {
			g.model.Type = "api"
		}

		for _, p := range r.idx.model[g.model.Name] {
			if p.Name == "acl" {
				g.model.ACLs = aclsFromBlock(p.Config)
				continue
			}
			g.model.Policies = append(g.model.Policies, r.registerPolicy(p, false).Name)
		}
		built[g.model.Name] = true
		r.out.Models = append(r.out.Models, g.model)
	}

	// ai-models entries that no target references: emit a minimal model.
	for _, m := range r.src.AIModels {
		if built[m.Name] || (m.Alias != "" && r.aiModelUsed[m.Alias]) {
			continue
		}
		if err := r.warn("ai-models entry %q is not referenced by any ai-proxy-advanced target; emitting a minimal model", m.Name); err != nil {
			return err
		}
		model := aigw.Model{Type: "model", Name: m.Name}
		model.Config.Model.Alias = m.Alias
		built[m.Name] = true
		r.out.Models = append(r.out.Models, model)
	}

	// Model-scoped plugins referencing unknown models.
	var fkNames []string
	for name := range r.idx.model {
		if !built[name] {
			fkNames = append(fkNames, name)
		}
	}
	sort.Strings(fkNames)
	for _, name := range fkNames {
		if err := r.warn("plugins scoped to unknown model %q; dropped", name); err != nil {
			return err
		}
	}
	return nil
}

// nameAliaslessGroups assigns names to model groups whose targets carry no
// model_alias. When the alias-less ai-models entries line up one-to-one with
// the alias-less groups (the shape the forward converter produces), they are
// matched by position; otherwise the route name stands in.
func (r *Reverter) nameAliaslessGroups(acc *modelAcc) error {
	var unnamed []*modelGroup
	for _, key := range acc.order {
		if g := acc.groups[key]; g.aliasless {
			unnamed = append(unnamed, g)
		}
	}
	if len(unnamed) == 0 {
		return nil
	}

	var free []string
	for _, m := range r.src.AIModels {
		if m.Alias == "" {
			free = append(free, m.Name)
		}
	}
	if len(free) == len(unnamed) {
		for i, g := range unnamed {
			g.model.Name = free[i]
		}
		return nil
	}
	for _, g := range unnamed {
		if err := r.warn("route %q: targets carry no model_alias; using route name as model name", g.routeName); err != nil {
			return err
		}
	}
	return nil
}

// isAPIOnly reports whether the capabilities indicate an "api" model
// (files/batches lifecycle APIs rather than synchronous generation).
func isAPIOnly(caps []string) bool {
	if len(caps) == 0 {
		return false
	}
	for _, c := range caps {
		if c != "batches" && c != "files" {
			return false
		}
	}
	return true
}

// balancerFromConfig reverses convert.balancerConfig, dropping the implicit
// {algorithm: round-robin} default the forward converter emits.
func balancerFromConfig(cfg map[string]any) *aigw.Balancer {
	if len(cfg) == 0 {
		return nil
	}
	algo := getStr(cfg, "algorithm")
	fields := map[string]any{}
	for k, v := range cfg {
		if k != "algorithm" {
			fields[k] = v
		}
	}
	if algo == "round-robin" && len(fields) == 0 {
		return nil
	}
	return &aigw.Balancer{Algorithm: algo, Fields: fields}
}

// deriveModelName derives a model name from an alias when no ai-models entry
// maps it: the "@scope/" prefix is stripped and non-name characters dashed
// (e.g. "@openai/gpt-5.2" -> "gpt-5-2").
func deriveModelName(alias string) string {
	s := strings.TrimPrefix(alias, "@")
	if _, after, ok := strings.Cut(s, "/"); ok && strings.HasPrefix(alias, "@") {
		s = after
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
