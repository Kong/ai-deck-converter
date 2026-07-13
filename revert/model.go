package revert

import (
	"sort"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
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

// accumulateModelRoute folds a route's ai-proxy-advanced plugins into the model
// accumulator. A route may carry several such plugins (one per type "model"
// entity, each scoped to the route and its ai-model); each plugin's targets land
// in the group keyed by the plugin's model FK (or, FK-less, by model_alias or
// route). Mirrors (in reverse) the per-model proxy split in convert.convertModels.
func (r *Reverter) accumulateModelRoute(acc *modelAcc, rt *kong.Route, plugins []kong.Plugin) error {
	var path string
	if len(rt.Paths) > 0 {
		path = rt.Paths[0]
	}

	// Partition guard plugins: those carrying a model FK belong to that specific
	// model; FK-less guards apply route-wide (every model on the route).
	var routeGuards []kong.Plugin
	modelGuards := map[string][]kong.Plugin{}
	for _, p := range plugins {
		if aiPluginNames[p.Name] {
			continue
		}
		if p.Model != nil {
			modelGuards[string(*p.Model)] = append(modelGuards[string(*p.Model)], p)
		} else {
			routeGuards = append(routeGuards, p)
		}
	}
	routeRefs, routeACLs, routeIDPRefs := r.modelPolicyRefs(routeGuards)

	for _, proxy := range findPlugins(plugins, "ai-proxy-advanced") {
		cfg := proxy.Config
		llmFormat := getStr(cfg, "llm_format")
		if llmFormat == "" {
			llmFormat = aimap.DefaultLLMFormat
		}
		genai := getStr(cfg, "genai_category")
		fkName := ""
		if proxy.Model != nil {
			fkName = string(*proxy.Model)
		}

		// Guard refs for this plugin's model: route-wide guards plus any scoped to
		// this model FK.
		refs, acls, idpRefs := routeRefs, routeACLs, routeIDPRefs
		if fkName != "" {
			mRefs, mACLs, mIDPRefs := r.modelPolicyRefs(modelGuards[fkName])
			if len(mRefs) > 0 || !mACLs.IsEmpty() || len(mIDPRefs) > 0 {
				refs = append(append([]string{}, routeRefs...), mRefs...)
				if !mACLs.IsEmpty() {
					acls = mACLs
				}
				idpRefs = append(append([]string{}, routeIDPRefs...), mIDPRefs...)
			}
		}

		targets := getSlice(cfg, "targets")
		if len(targets) == 0 {
			if err := r.warn("route %q: ai-proxy-advanced has no targets; nothing to convert", rt.Name); err != nil {
				return err
			}
			continue
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

			var capability string
			var bases []string
			if match, ok := resolveEndpoint(section, routeType, genai, rt.Name, path); ok {
				capability = match.capability
				for _, p := range rt.Paths {
					if b, ok := basePathFor(p, match.spec); ok {
						bases = append(bases, b)
					}
				}
			} else if routeType == "llm/v1/chat" {
				capability = "generate"
				if err := r.warn(
					"route %q: cannot resolve capability for route_type %q in section %q; defaulting to generate",
					rt.Name, routeType, section); err != nil {
					return err
				}
			} else {
				if err := r.warn(
					"route %q: cannot resolve capability for route_type %q in section %q; skipping target",
					rt.Name, routeType, section); err != nil {
					return err
				}
				continue
			}

			g, err := r.modelGroupFor(acc, rt, fkName, alias, llmFormat, bases, cfg, refs, acls, idpRefs)
			if err != nil {
				return err
			}
			// Logging is carried per target by ai-proxy-advanced but is a single
			// model-level block in the AI Gateway model; lift it back from the
			// first target that has it (all of a model's targets share the block).
			if g.model.Config.Logging == nil {
				g.model.Config.Logging = loggingFromBlock(getMap(target, "logging"))
			}
			if !g.capsSeen[capability] {
				g.capsSeen[capability] = true
				g.caps = append(g.caps, capability)
			}

			name := getStr(modelMap, "name")
			// A description equal to the model name is the forward converter's
			// default; drop it so round trips stay clean.
			semanticDesc := getStr(target, "description")
			if semanticDesc == name {
				semanticDesc = ""
			}
			tm := aigw.TargetModel{
				Name:         name,
				Weight:       getInt(target, "weight"),
				SemanticDesc: semanticDesc,
				Config:       aigw.TargetModelConfig{Type: providerType},
			}
			d := defoldTarget(target, providerType)
			tm.AllowAuthOverride = d.allowOverride
			tm.Config.Options = d.options
			tm.Provider = r.providerFor(providerType, &d)

			if !g.targetsSeen[tm.Name] {
				g.targetsSeen[tm.Name] = true
				g.model.TargetModels = append(g.model.TargetModels, tm)
			}
		}
	}
	return nil
}

// modelGroupFor finds or creates the model group for a plugin's model FK (or,
// FK-less, for an alias or the route), seeding model-level config from the
// plugin config. A non-empty fkName names the group directly (the type "model"
// case where ai-proxy-advanced carries an ai-model FK).
func (r *Reverter) modelGroupFor(
	acc *modelAcc, rt *kong.Route, fkName, alias, llmFormat string, bases []string,
	cfg map[string]any, refs []string, acls aigw.ACLs, idpRefs []string,
) (*modelGroup, error) {
	var key string
	switch {
	case fkName != "":
		key = "model:" + fkName
	case alias != "":
		key = "alias:" + alias
	default:
		key = "route:" + rt.Name
	}
	if g, ok := acc.groups[key]; ok {
		return g, nil
	}

	var name string
	aliasless := false
	switch {
	case fkName != "":
		name = fkName
		if alias != "" {
			if _, ok := r.aiModelByAlias[alias]; ok {
				r.aiModelUsed[alias] = true
			}
		}
	case alias != "":
		if n, ok := r.aiModelByAlias[alias]; ok {
			name = n
			r.aiModelUsed[alias] = true
		} else {
			name = deriveModelName(alias)
			if r.hasAIModels() {
				if err := r.warn("no ai-models entry for alias %q; deriving model name %q", alias, name); err != nil {
					return nil, err
				}
			}
		}
	default:
		// Provisional; finalizeModels matches alias-less groups to alias-less
		// ai-models entries by position when the counts line up.
		name = rt.Name
		aliasless = true
	}

	g := &modelGroup{
		capsSeen:    map[string]bool{},
		targetsSeen: map[string]bool{},
		aliasless:   aliasless,
		routeName:   rt.Name,
		model: aigw.Model{
			Type:     "model",
			Name:     name,
			Formats:  []aigw.Format{{Type: llmFormat}},
			Policies: refs,
			Access:   aigw.ModelAccess{ACLs: acls, IdentityProviders: idpRefs},
		},
	}
	g.model.Config.Model.Alias = alias
	g.model.Config.Model.NameHeader = getBool(cfg, "model_name_header")
	g.model.Config.ResponseStreaming = getStr(cfg, "response_streaming")
	g.model.Config.MaxRequestBodySize = getInt(cfg, "max_request_body_size")
	g.model.Config.Balancer = balancerFromConfig(getMap(cfg, "balancer"), cfg["vectordb"], cfg["embeddings"])
	if len(bases) > 1 || (len(bases) == 1 && bases[0] != aimap.DefaultBasePath) {
		g.model.Config.Route.Paths = bases
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
		if entry, ok := r.aiModelByName[g.model.Name]; ok {
			g.model.Labels = r.tagsToLabels(entry.Tags)
		}

		for _, p := range r.idx.model[g.model.Name] {
			if p.Name == "acl" {
				g.model.Access.ACLs = aclsFromBlock(p.Config)
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
		if err := r.warn(
			"ai-models entry %q is not referenced by any ai-proxy-advanced target; emitting a minimal model",
			m.Name); err != nil {
			return err
		}
		model := aigw.Model{Type: "model", Name: m.Name}
		model.Config.Model.Alias = m.Alias
		model.Labels = r.tagsToLabels(m.Tags)
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
// model_alias. When the alias-less groups line up one-to-one with ai-models
// entries that provide only naming help (either no alias, or the converter's
// historical alias==name default), they are matched by position; otherwise the
// route name stands in.
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
		if m.Alias == "" || m.Alias == m.Name {
			free = append(free, m.Name)
		}
	}
	if len(free) == len(unnamed) {
		for i, g := range unnamed {
			g.model.Name = free[i]
		}
		return nil
	}
	if !r.hasAIModels() {
		return nil
	}
	for _, g := range unnamed {
		if err := r.warn("route %q: targets carry no model_alias; using route name as model name", g.routeName); err != nil {
			return err
		}
	}
	return nil
}

// hasAIModels reports whether the source document declares any ai-models
// entries. Older gateways predate the entity, so absence is normal and the
// naming fallbacks run without warning.
func (r *Reverter) hasAIModels() bool { return len(r.src.AIModels) > 0 }

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
// {algorithm: round-robin} default the forward converter emits. The top-level
// vectordb/embeddings config keys (siblings of `balancer` in ai-proxy-advanced)
// are folded back into the balancer block, mirroring convert's hoisting.
func balancerFromConfig(cfg map[string]any, vectordb, embeddings any) *aigw.Balancer {
	algo := getStr(cfg, "algorithm")
	fields := map[string]any{}
	for k, v := range cfg {
		if k != "algorithm" {
			fields[k] = v
		}
	}
	if vectordb != nil {
		fields["vectordb"] = vectordb
	}
	if embeddings != nil {
		fields["embeddings"] = embeddings
	}
	if len(cfg) == 0 && len(fields) == 0 {
		return nil
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
