package revert

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
	// mergedFKs are the model FKs of other ai-proxy-advanced plugins folded
	// into this group (see mergeableAliasFKs): distinct aliases of the one
	// underlying model produced by route.model.values fan-out. Guard plugins
	// scoped to any of these FKs belong to this model too.
	mergedFKs []string
	// capsUntied holds the capabilities that a route without a tie gives.
	// capTies holds the tie sets that each other capability came from.
	capsUntied map[string]bool
	capTies    map[string][][]string
	// statsSet, payloadsSet, and headerSet are true once a supporting
	// endpoint gave the value. A forced value never becomes a model value.
	statsSet, payloadsSet, headerSet bool
	// statsForced and payloadsForced are true when an endpoint forced the
	// value. With no supporting endpoint, the value stays unset.
	statsForced, payloadsForced bool
}

// targetFingerprint returns a stable dedup key for a reconstructed target
// model. Every field of TargetModel/TargetModelConfig is exported and
// json.Marshal sorts map keys, so structurally identical targets fingerprint
// identically. On the (unreachable in practice) marshal error the rendered
// struct is used, which at worst keeps a duplicate rather than dropping a
// distinct target.
func targetFingerprint(tm aigw.TargetModel) string {
	b, err := json.Marshal(tm)
	if err != nil {
		return fmt.Sprintf("%+v", tm)
	}
	return string(b)
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
	if hasTag(rt.Tags, aimap.VideoLifecycleRouteTag) {
		return nil
	}
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

	// The route's ai-model-selector (if any) determines which Route.Model field
	// an alias round-trips into. config.sources compacts one entry per distinct
	// selector shape, but the target does not identify its originating entry;
	// entries are therefore assigned to model groups in encounter order and
	// cycled when several models share one shape.
	selector := findPlugin(plugins, "ai-model-selector")
	entries := selectorSourceEntries(selector)
	entryIdx := 0
	nextEntry := func() map[string]any {
		if len(entries) == 0 {
			return nil
		}
		entry := entries[entryIdx%len(entries)]
		entryIdx++
		return entry
	}

	routeProxies, redirect := r.mergeableAliasFKs(findPlugins(plugins, "ai-proxy-advanced"))

	for _, proxy := range routeProxies {
		cfg := proxy.Config
		llmFormat := getStr(cfg, "llm_format")
		if llmFormat == "" {
			llmFormat = aimap.DefaultLLMFormat
		}
		if allTargetsPassthrough(cfg) {
			// llm_format still names the wire format the passthrough targets serve, but
			// the model that produced them declared the passthrough format; recovering
			// llm_format here would lower back to real route_types.
			llmFormat = aimap.PassthroughFormat
		}
		genai := getStr(cfg, "genai_category")
		fkName := ""
		if proxy.Model != nil {
			fkName = string(*proxy.Model)
		}
		// groupFK is fkName, redirected to its equivalence class's canonical FK
		// when this plugin is one of several identical-except-alias copies of
		// one route.model.values fan-out (see mergeableAliasFKs). modelGuards
		// below stays keyed by the original fkName: each copy's own guard
		// plugins must still be found and folded in (finalizeModels also visits
		// every merged FK for the same reason).
		groupFK := fkName
		if canon, ok := redirect[fkName]; ok {
			groupFK = canon
		}
		// A realtime plugin has no model FK. Its tag names the ai-models entry.
		realtimeModel, _ := aimap.DecodeRealtimeModel(proxy.Tags)
		if groupFK == "" && realtimeModel != "" {
			groupFK = realtimeModel
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
			providerType := getStr(modelMap, "provider")
			section := aimap.SectionFor(llmFormat, providerType)

			var capability string
			var bases []string
			var spec *aimap.EndpointSpec
			var ties []string
			if routeType == aimap.PassthroughRouteType {
				// A passthrough route is the model's base path itself and serves no
				// capability of its own (see aimap.PassthroughEndpoint).
				bases = rt.Paths
				spec = &aimap.PassthroughEndpoint
			} else if match, ok := resolveEndpoint(section, routeType, genai, rt.Name, path); ok {
				capability = match.capability
				spec = &match.spec
				ties = match.ties
				suffixes := []aimap.EndpointSpec{match.spec}
				if getStr(getMap(getMap(modelMap, "options"), "bedrock"), "endpoint_type") == aimap.BedrockEndpointTypeMantle {
					suffixes = suffixes[:0]
					for _, suffix := range aimap.MantlePathSuffixes(llmFormat, match.spec) {
						mantleSpec := match.spec
						mantleSpec.PathSuffix = suffix
						suffixes = append(suffixes, mantleSpec)
					}
					slices.SortStableFunc(suffixes, func(a, b aimap.EndpointSpec) int {
						return len(b.PathSuffix) - len(a.PathSuffix)
					})
				}
				for _, p := range rt.Paths {
					for _, s := range suffixes {
						if b, ok := basePathFor(p, s); ok {
							if !slices.Contains(bases, b) {
								bases = append(bases, b)
							}
							break
						}
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
			dropEndpointUpstreamURL(&d, section, spec)
			tm.Config.Options = d.options
			tm.Provider = r.providerFor(providerType, &d)

			// Mirrors convert's target dedup: key on the whole reconstructed
			// target, not just its name, so targets sharing a model name but
			// differing in synthesized provider (distinct auth/options) or
			// weight are all kept. One target reached through several
			// capabilities still fingerprints identically (capability lives on
			// the model, not the target), so those collapse as before.
			key := targetFingerprint(tm)

			// With neither model FK nor alias, groups key by route, so a
			// capability's primary and secondary routes — emitted by the forward
			// converter side by side with the same targets — would become two
			// models instead of one. Fold whichever is processed second into the
			// group the first one created; route order in the source document is
			// not guaranteed (only the forward converter emits primary first), so
			// this must not depend on which spec, primary or secondary, comes first.
			var g *modelGroup
			if groupFK == "" && alias == "" {
				g = acc.aliaslessGroupServing(capability, bases, key)
			}
			if g == nil {
				var err error
				g, err = r.modelGroupFor(acc, rt, groupFK, alias, llmFormat, bases, cfg, nextEntry, refs, acls, idpRefs)
				if err != nil {
					return err
				}
			}
			if realtimeModel != "" {
				r.mergeAliasGroupMembers(g, realtimeModel)
			}
			if !slices.ContainsFunc(g.model.Formats, func(f aigw.Format) bool { return f.Type == llmFormat }) {
				g.model.Formats = append(g.model.Formats, aigw.Format{Type: llmFormat})
			}
			liftLogging(g, getMap(target, "logging"), providerType, spec)
			if !g.headerSet && (spec == nil || aimap.SupportsModelNameHeader(*spec)) {
				g.model.Config.Model.NameHeader = getBool(cfg, "model_name_header")
				g.headerSet = true
			}
			if capability != "" {
				if len(ties) == 0 {
					g.capsUntied[capability] = true
				} else {
					g.capTies[capability] = append(g.capTies[capability], append([]string{capability}, ties...))
				}
				if !g.capsSeen[capability] {
					g.capsSeen[capability] = true
					g.caps = append(g.caps, capability)
				}
			}

			if !g.targetsSeen[key] {
				g.targetsSeen[key] = true
				g.model.TargetModels = append(g.model.TargetModels, tm)
			}
		}
	}
	return nil
}

// liftLogging lifts the logging of one target into g. Logging is per target
// in ai-proxy-advanced but one block in the AI Gateway model. A value comes
// from the first target whose endpoint supports it. A spec of nil supports
// every value.
func liftLogging(g *modelGroup, block map[string]any, providerType string, spec *aimap.EndpointSpec) {
	if g.model.Config.Logging == nil {
		g.model.Config.Logging = loggingFromBlockWithDefaults(nil, false, false)
	}
	if v, ok := block["log_statistics"].(bool); ok {
		switch {
		case spec != nil && !aimap.LogStatisticsSupported(providerType, *spec):
			g.statsForced = true
		case !g.statsSet:
			g.model.Config.Logging.Statistics = &v
			g.statsSet = true
		}
	}
	if v, ok := block["log_payloads"].(bool); ok {
		switch {
		case spec != nil && aimap.IsWebSocketEndpoint(*spec):
			g.payloadsForced = true
		case !g.payloadsSet:
			g.model.Config.Logging.Payloads = &v
			g.payloadsSet = true
		}
	}
}

// finishLogging leaves a value unset when only forcing endpoints gave it.
// The forward default then gives the same output. A statistics value equal
// to the default also stays unset next to a forcing endpoint. Set
// explicitly, it would give a CONFIG_FIELD_OVERRIDDEN warning.
func (g *modelGroup) finishLogging() {
	l := g.model.Config.Logging
	if l == nil {
		return
	}
	if g.statsForced && (!g.statsSet || *l.Statistics == aimap.DefaultLogStatistics) {
		l.Statistics = nil
	}
	if !g.payloadsSet && g.payloadsForced {
		l.Payloads = nil
	}
}

// capabilities returns the capabilities of g in pair order. It leaves out a
// capability that only tied routes give when a route without a tie gives
// another capability of the same tie.
func (g *modelGroup) capabilities() []string {
	var out []string
	for _, capability := range g.caps {
		if !g.capsUntied[capability] && slices.ContainsFunc(g.capTies[capability], func(tie []string) bool {
			return slices.ContainsFunc(tie, func(other string) bool { return other != capability && g.capsUntied[other] })
		}) {
			continue
		}
		out = append(out, capability)
	}
	return aimap.OrderCapabilities(out)
}

// formats returns the formats of g in pair order.
func (g *modelGroup) formats() []aigw.Format {
	types := make([]string, len(g.model.Formats))
	for i, f := range g.model.Formats {
		types[i] = f.Type
	}
	out := make([]aigw.Format, 0, len(types))
	for _, t := range aimap.OrderFormats(types) {
		out = append(out, aigw.Format{Type: t})
	}
	return out
}

// modelRoutePaths returns the Config.Route.Paths of a model recovered from the
// given base paths: nil for the single default base path.
func modelRoutePaths(bases []string) []string {
	if len(bases) > 1 || (len(bases) == 1 && !isDefaultBasePath(bases[0])) {
		return bases
	}
	return nil
}

// aliaslessGroupServing returns the alias-less group that already serves
// capability at the given base paths through the target fingerprinted as
// targetKey, or nil when there is none.
func (acc *modelAcc) aliaslessGroupServing(capability string, bases []string, targetKey string) *modelGroup {
	paths := modelRoutePaths(bases)
	for _, key := range acc.order {
		g := acc.groups[key]
		if g.aliasless && g.capsSeen[capability] && g.targetsSeen[targetKey] &&
			slices.Equal(g.model.Config.Route.Paths, paths) {
			return g
		}
	}
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// modelGroupFor finds or creates the model group for a plugin's model FK (or,
// FK-less, for an alias or the route), seeding model-level config from the
// plugin config. A non-empty fkName names the group directly (the type "model"
// case where ai-proxy-advanced carries an ai-model FK).
func (r *Reverter) modelGroupFor(
	acc *modelAcc, rt *kong.Route, fkName, alias, llmFormat string, bases []string,
	cfg map[string]any, nextEntry func() map[string]any, refs []string, acls aigw.ACLs, idpRefs []string,
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
		mergeAliasIntoGroup(g, alias, refs, acls, idpRefs)
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
		capsUntied:  map[string]bool{},
		capTies:     map[string][][]string{},
		aliasless:   aliasless,
		routeName:   rt.Name,
		model: aigw.Model{
			Type:     "model",
			Name:     name,
			Formats:  []aigw.Format{{Type: llmFormat}},
			Policies: refs,
			Access:   aigw.ModelAccess{ACLs: acls, AuthStrategies: idpRefs},
		},
	}
	g.model.Config.Route.Paths = modelRoutePaths(bases)
	if alias != "" {
		setAliasField(&g.model.Config.Route.Model, alias, name, nextEntry())
	}
	g.model.Config.ResponseStreaming = getStr(cfg, "response_streaming")
	g.model.Config.Proxy = proxyFromConfig(getMap(cfg, "proxy_config"))
	g.model.Config.MaxRequestBodySize = getInt(cfg, "max_request_body_size")
	g.model.Config.Balancer = balancerFromConfig(
		getMap(cfg, "balancer"), aimap.VectorDBFromPlugin(cfg["vectordb"]), cfg["embeddings"],
	)
	acc.groups[key] = g
	acc.order = append(acc.order, key)
	return g, nil
}

// mergeAliasIntoGroup folds another alias of an already-built group's
// underlying model into its Route.Model.Values, seeding the list with the
// group's own (canonical) alias first if this is the first fold, and folds in
// refs/acls/idpRefs (this alias's own route+model-scoped guard plugins,
// computed by the caller from modelGuards) — those are never visited again
// once modelGroupFor's early-return path is taken, so without this they would
// be silently dropped for every alias but the canonical one.
func mergeAliasIntoGroup(g *modelGroup, alias string, refs []string, acls aigw.ACLs, idpRefs []string) {
	for _, ref := range refs {
		if !slices.Contains(g.model.Policies, ref) {
			g.model.Policies = append(g.model.Policies, ref)
		}
	}
	mergeACLs(&g.model.Access.ACLs, acls)
	for _, ref := range idpRefs {
		if !slices.Contains(g.model.Access.AuthStrategies, ref) {
			g.model.Access.AuthStrategies = append(g.model.Access.AuthStrategies, ref)
		}
	}

	if alias == "" || alias == g.model.Name {
		return
	}
	if slices.Contains(g.model.Config.Route.Model.Values, alias) {
		return
	}
	if len(g.model.Config.Route.Model.Values) == 0 {
		g.model.Config.Route.Model.Values = append(g.model.Config.Route.Model.Values, g.model.Name)
	}
	g.model.Config.Route.Model.Values = append(g.model.Config.Route.Model.Values, alias)
	g.mergedFKs = append(g.mergedFKs, alias)
}

// mergeACLs folds src into dst, appending allow, deny, allow_when, and
// deny_when entries in first-seen order with dedup. ACL rules carried by
// several of a model's aliases or by several model-scoped acl plugins union
// instead of overwriting each other.
func mergeACLs(dst *aigw.ACLs, src aigw.ACLs) {
	for _, name := range src.Allow {
		if !slices.Contains(dst.Allow, name) {
			dst.Allow = append(dst.Allow, name)
		}
	}
	for _, name := range src.Deny {
		if !slices.Contains(dst.Deny, name) {
			dst.Deny = append(dst.Deny, name)
		}
	}
	for _, name := range src.AllowWhen {
		if !slices.Contains(dst.AllowWhen, name) {
			dst.AllowWhen = append(dst.AllowWhen, name)
		}
	}
	for _, name := range src.DenyWhen {
		if !slices.Contains(dst.DenyWhen, name) {
			dst.DenyWhen = append(dst.DenyWhen, name)
		}
	}
}

// mergeableAliasFKs partitions a route's ai-proxy-advanced plugins into
// equivalence classes: plugins carrying the same convert-written
// aimap.EncodeModelAliasGroup marker (see aiModelAliasGroup) and otherwise
// identical config (modulo their own model FK and each target's model_alias)
// are different aliases of the one model a route.model.values fan-out
// produced (see convert.convertModels). The marker is required, not just a
// tie-breaker: without it, this shape is indistinguishable from N
// independently-authored models that happen to be config-identical
// and merging those would silently corrupt them into one.
//
// A class's canonical member is the one whose FK the marker names (the
// source model's first alias, which convert tags every copy with), falling
// back to the first-seen member when none matches. Plugin order is not
// trusted: a live Admin API dump makes no ordering promise.
//
// Returns proxies reordered so each class's canonical member is processed
// first, at the position of the class's first-seen member (the group it
// creates then seeds route.model.values with the canonical alias), and a map
// from every non-canonical member's FK to its canonical member's FK; members
// outside any multi-FK class are absent from the map.
func (r *Reverter) mergeableAliasFKs(proxies []*kong.Plugin) ([]*kong.Plugin, map[string]string) {
	type class struct {
		first     int // index of the first-seen member in proxies
		canonical int // index of the canonical member in proxies
	}
	classes := map[string]*class{}
	shapeOf := make([]string, len(proxies))
	for i, proxy := range proxies {
		if proxy.Model == nil {
			continue
		}
		fkName := string(*proxy.Model)
		group := r.aiModelAliasGroup(fkName)
		if group == "" {
			continue
		}
		shape := group + "\x00" + proxyShapeFingerprint(proxy.Config)
		shapeOf[i] = shape
		c, ok := classes[shape]
		if !ok {
			classes[shape] = &class{first: i, canonical: i}
			continue
		}
		if fkName == group && string(*proxies[c.canonical].Model) != group {
			c.canonical = i
		}
	}

	ordered := make([]*kong.Plugin, 0, len(proxies))
	redirect := map[string]string{}
	for i, proxy := range proxies {
		c, ok := classes[shapeOf[i]]
		if !ok {
			ordered = append(ordered, proxy)
			continue
		}
		canon := proxies[c.canonical]
		switch i {
		case c.first:
			ordered = append(ordered, canon)
			if c.canonical != i {
				ordered = append(ordered, proxy)
			}
		case c.canonical:
			// Already emitted at the class's first-seen position.
		default:
			ordered = append(ordered, proxy)
		}
		if i != c.canonical {
			redirect[string(*proxy.Model)] = string(*canon.Model)
		}
	}
	return ordered, redirect
}

// mergeAliasGroupMembers folds into g every other ai-models entry of the alias
// group that canonical names. A realtime-only model has no FK-scoped plugin
// copies, so the ai-models entries are the only record of its other aliases.
func (r *Reverter) mergeAliasGroupMembers(g *modelGroup, canonical string) {
	for _, m := range r.src.AIModels {
		if m.Name != canonical && r.aiModelAliasGroup(m.Name) == canonical {
			mergeAliasIntoGroup(g, m.Name, nil, aigw.ACLs{}, nil)
		}
	}
}

// aiModelAliasGroup returns the aimap.EncodeModelAliasGroup marker on name's
// ai_models entry, or "" if the entry doesn't exist or carries no marker.
func (r *Reverter) aiModelAliasGroup(name string) string {
	entry, ok := r.aiModelByName[name]
	if !ok {
		return ""
	}
	group, _ := aimap.DecodeModelAliasGroup(entry.Tags)
	return group
}

// proxyShapeFingerprint fingerprints an ai-proxy-advanced config with every
// target's model_alias stripped, so two plugins differing only in their own
// model FK and their target's alias fingerprint identically.
func proxyShapeFingerprint(cfg map[string]any) string {
	clone := make(map[string]any, len(cfg))
	for k, v := range cfg {
		clone[k] = v
	}
	if rawTargets, ok := clone["targets"].([]any); ok {
		targets := make([]any, len(rawTargets))
		for i, raw := range rawTargets {
			target, ok := raw.(map[string]any)
			if !ok {
				targets[i] = raw
				continue
			}
			tclone := make(map[string]any, len(target))
			for k, v := range target {
				tclone[k] = v
			}
			if model, ok := tclone["model"].(map[string]any); ok {
				mclone := make(map[string]any, len(model))
				for k, v := range model {
					if k != "model_alias" {
						mclone[k] = v
					}
				}
				tclone["model"] = mclone
			}
			targets[i] = tclone
		}
		clone["targets"] = targets
	}
	b, err := json.Marshal(clone)
	if err != nil {
		return fmt.Sprintf("%+v", clone)
	}
	return string(b)
}

// setAliasField reconstructs the Route.Model field an alias round-trips into.
// An alias without an explicit source keeps the format-specific selector
// implementation implicit. Explicit body/header overrides retain their field names.
func setAliasField(model *aigw.ModelSelectorConfig, alias, name string, entry map[string]any) {
	if entry != nil {
		switch getStr(entry, "source") {
		case "body":
			if bodyPath := getStr(entry, "body_path"); bodyPath != "" && bodyPath != "model" {
				model.Body = aigw.ModelBodySelectorConfig{BodyParam: bodyPath}
				model.Values = []string{alias}
				return
			}
		case "header":
			if headerName := getStr(entry, "header_name"); headerName != "" {
				model.Header = aigw.ModelHeaderSelectorConfig{HeaderParam: headerName}
				model.Values = []string{alias}
				return
			}
		case "path":
			// accumulateModelRoute rejects the legacy flat schema's
			// path_pattern shape outright (errIrreversiblePathPattern), so any
			// "path" entry reaching here still carries its pcre_capture_name.
			captureName := getStr(entry, "pcre_capture_name")
			if captureName != "" {
				model.Path = aigw.ModelPathSelectorConfig{PathParam: captureName}
			}
			if alias != name {
				model.Values = []string{alias}
			}
			return
		}
	}
	if alias != name {
		model.Values = []string{alias}
	}
}

// selectorSourceEntries returns the route's selector shapes, preferring the
// current config.sources array and falling back to the legacy flat form.
func selectorSourceEntries(selector *kong.Plugin) []map[string]any {
	if selector == nil {
		return nil
	}
	if sources := getSlice(selector.Config, "sources"); len(sources) > 0 {
		entries := make([]map[string]any, 0, len(sources))
		for _, raw := range sources {
			if entry, ok := raw.(map[string]any); ok {
				entries = append(entries, entry)
			}
		}
		return entries
	}
	if getStr(selector.Config, "source") != "" {
		return []map[string]any{selector.Config}
	}
	return nil
}

// finalizeModels applies model-scoped (ai-models FK) plugins, classifies the
// model type, emits the models in first-seen order, and reports orphans.
func (r *Reverter) finalizeModels(acc *modelAcc) error {
	if err := r.nameAliaslessGroups(acc); err != nil {
		return err
	}

	built := map[string]bool{}
	for _, key := range r.orderedModelKeys(acc) {
		g := acc.groups[key]
		g.model.Capabilities = g.capabilities()
		g.model.Formats = g.formats()
		g.finishLogging()
		if isAPIOnly(g.model.Capabilities) {
			g.model.Type = "api"
		}
		if entry, ok := r.aiModelByName[g.model.Name]; ok {
			g.model.ID = entry.ID
			// The fan-out marker tag is converter bookkeeping, not a label.
			_, rest := aimap.DecodeModelAliasGroup(entry.Tags)
			g.model.Labels = r.tagsToLabels(rest)
		}

		// Guard plugins scoped to the model's own FK and to every merged
		// fan-out alias FK all belong to this model; register each policy once.
		seenPolicy := make(map[string]bool, len(g.model.Policies))
		for _, name := range g.model.Policies {
			seenPolicy[name] = true
		}
		for _, fk := range append([]string{g.model.Name}, g.mergedFKs...) {
			for _, p := range r.idx.model[fk] {
				if p.Name == "acl" {
					// Multiple model-scoped acl plugins (one per merged alias)
					// union their allow/deny lists; the last must not win.
					mergeACLs(&g.model.Access.ACLs, aclsFromBlock(p.Config))
					continue
				}
				policyName := r.registerPolicy(p, false).Name
				if seenPolicy[policyName] {
					continue
				}
				seenPolicy[policyName] = true
				g.model.Policies = append(g.model.Policies, policyName)
			}
			built[fk] = true
		}
		r.out.Models = append(r.out.Models, g.model)
	}

	// ai-models entries that no target references: emit a minimal model. An
	// entry is referenced when a target consumed its effective alias (its
	// explicit alias, or its name under the AI Gateway 2.0 convention where
	// the name carries the alias) — the built model may be named after the
	// source model (a plugin's model FK), not the alias, so match on usage.
	for _, m := range r.src.AIModels {
		if built[m.Name] || r.aiModelUsed[aiModelAlias(m)] {
			continue
		}
		if err := r.warn(
			"ai-models entry %q is not referenced by any ai-proxy-advanced target; emitting a minimal model",
			m.Name); err != nil {
			return err
		}
		model := aigw.Model{Type: "model", ID: m.ID, Name: m.Name}
		if m.Alias != "" {
			model.Config.Route.Model.Values = []string{m.Alias}
		}
		_, rest := aimap.DecodeModelAliasGroup(m.Tags)
		model.Labels = r.tagsToLabels(rest)
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

// orderedModelKeys restores the source ai-models order when it is available.
// Multiple models can now share one default selector route, so route traversal
// alone is not a stable representation of the model declaration order.
func (r *Reverter) orderedModelKeys(acc *modelAcc) []string {
	byName := make(map[string]string, len(acc.order))
	for _, key := range acc.order {
		byName[acc.groups[key].model.Name] = key
	}

	ordered := make([]string, 0, len(acc.order))
	seen := make(map[string]bool, len(acc.order))
	for _, model := range r.src.AIModels {
		if key, ok := byName[model.Name]; ok {
			ordered = append(ordered, key)
			seen[key] = true
		}
	}
	for _, key := range acc.order {
		if !seen[key] {
			ordered = append(ordered, key)
		}
	}
	return ordered
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

	free := r.freeAIModels(acc)
	if len(free) != len(unnamed) {
		// One type api model with more than one capability or format makes
		// several alias-less routes with the same targets and base paths.
		if merged := mergeAliaslessGroups(acc, unnamed, false); len(merged) == len(free) {
			unnamed = mergeAliaslessGroups(acc, unnamed, true)
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

// freeAIModels returns the names of the ai-models entries that only give
// naming help: the entry has no alias of its own, and no group with a model
// FK or alias uses it.
func (r *Reverter) freeAIModels(acc *modelAcc) []string {
	used := map[string]bool{}
	for _, key := range acc.order {
		if g := acc.groups[key]; !g.aliasless {
			used[g.model.Name] = true
			for _, fk := range g.mergedFKs {
				used[fk] = true
			}
		}
	}
	var free []string
	for _, m := range r.src.AIModels {
		if (m.Alias == "" || m.Alias == m.Name) && !used[m.Name] && !r.aiModelUsed[aiModelAlias(m)] {
			free = append(free, m.Name)
		}
	}
	return free
}

// mergeAliaslessGroups returns the alias-less groups after it merges the
// groups with equal targets and base paths, in first-seen order. It changes
// acc only when apply is true.
func mergeAliaslessGroups(acc *modelAcc, unnamed []*modelGroup, apply bool) []*modelGroup {
	byKey := map[string]*modelGroup{}
	var out []*modelGroup
	for _, g := range unnamed {
		targets := slices.Sorted(maps.Keys(g.targetsSeen))
		key := strings.Join(targets, "\x00") + "\x01" + strings.Join(g.model.Config.Route.Paths, "\x00")
		first, ok := byKey[key]
		if !ok {
			byKey[key] = g
			out = append(out, g)
			continue
		}
		if apply {
			first.absorb(g)
			acc.remove(g)
		}
	}
	return out
}

// absorb folds o into g. Both groups have the same targets and base paths.
func (g *modelGroup) absorb(o *modelGroup) {
	for _, capability := range o.caps {
		if !g.capsSeen[capability] {
			g.capsSeen[capability] = true
			g.caps = append(g.caps, capability)
		}
		if o.capsUntied[capability] {
			g.capsUntied[capability] = true
		}
		g.capTies[capability] = append(g.capTies[capability], o.capTies[capability]...)
	}
	for _, f := range o.model.Formats {
		if !slices.Contains(g.model.Formats, f) {
			g.model.Formats = append(g.model.Formats, f)
		}
	}
	mergeAliasIntoGroup(g, "", o.model.Policies, o.model.Access.ACLs, o.model.Access.AuthStrategies)
	if !g.statsSet && o.statsSet {
		g.model.Config.Logging.Statistics, g.statsSet = o.model.Config.Logging.Statistics, true
	}
	if !g.payloadsSet && o.payloadsSet {
		g.model.Config.Logging.Payloads, g.payloadsSet = o.model.Config.Logging.Payloads, true
	}
	if !g.headerSet && o.headerSet {
		g.model.Config.Model.NameHeader, g.headerSet = o.model.Config.Model.NameHeader, true
	}
	g.statsForced = g.statsForced || o.statsForced
	g.payloadsForced = g.payloadsForced || o.payloadsForced
}

// remove deletes g from acc.
func (acc *modelAcc) remove(g *modelGroup) {
	acc.order = slices.DeleteFunc(acc.order, func(key string) bool {
		if acc.groups[key] == g {
			delete(acc.groups, key)
			return true
		}
		return false
	})
}

// aiModelAlias returns the alias a target's model_alias references for an
// ai-models entry: its explicit alias when set, otherwise the entry name.
// AI Gateway 2.0 stores the alias as the ai-models name and leaves alias
// unset (see convert.convertModels), so the name is the effective alias
// there; older/hand-written configs set alias explicitly.
func aiModelAlias(m kong.AIModel) string {
	if m.Alias != "" {
		return m.Alias
	}
	return m.Name
}

// hasAIModels reports whether the source document declares any ai-models
// entries. Older gateways predate the entity, so absence is normal and the
// naming fallbacks run without warning.
func (r *Reverter) hasAIModels() bool { return len(r.src.AIModels) > 0 }

// isAPIOnly reports whether the capabilities indicate an "api" model
// (files/batches/skills lifecycle APIs rather than synchronous generation).
func isAPIOnly(caps []string) bool {
	if len(caps) == 0 {
		return false
	}
	for _, c := range caps {
		if c != "batches" && c != "files" && c != "skills" {
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

// allTargetsPassthrough reports whether every target of an ai-proxy-advanced plugin carries the
// passthrough route_type, which is the only shape the plugin accepts: it refuses to mix
// passthrough targets with any other route_type.
func allTargetsPassthrough(cfg map[string]any) bool {
	targets := getSlice(cfg, "targets")
	if len(targets) == 0 {
		return false
	}
	for _, raw := range targets {
		target, ok := raw.(map[string]any)
		if !ok || getStr(target, "route_type") != aimap.PassthroughRouteType {
			return false
		}
	}
	return true
}

// dropEndpointUpstreamURL removes the upstream_url the forward converter
// generated for a Gemini interactions route of this target. One target
// serves several interactions routes, so the value may match another
// route's URL. A per-route check splits one target in two on revert.
func dropEndpointUpstreamURL(d *defoldedTarget, section string, spec *aimap.EndpointSpec) {
	if spec == nil {
		return
	}
	stored, ok := d.options["upstream_url"]
	if !ok {
		return
	}
	for _, ce := range aimap.SectionEndpoints(section) {
		want, ok := aimap.EndpointUpstreamURL(section, ce.Spec, d.options)
		if ok && stored == want {
			delete(d.options, "upstream_url")
			if len(d.options) == 0 {
				d.options = nil
			}
			return
		}
	}
}
