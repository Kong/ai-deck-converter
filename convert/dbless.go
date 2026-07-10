package convert

import (
	"crypto/sha1" //nolint:gosec
	"fmt"
	"net/url"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

var dbLessNamespace = [16]byte{
	0x8f, 0x17, 0x73, 0x5c, 0x41, 0x02, 0x49, 0x6b,
	0xa3, 0x2f, 0x92, 0x0c, 0x4d, 0x18, 0x62, 0xf1,
}

type dbLessIDs struct {
	service  map[string]string
	route    map[string]string
	plugin   map[string]string
	model    map[string]string
	vault    map[string]string
	group    map[string]string
	consumer map[string]string
}

// projectDBLess reshapes the already-converted Kong document into a flattened
// db-less DP payload. It assigns stable IDs to every emitted entity, moves
// nested entities into top-level collections, converts name-based foreign keys
// into ID references, and lifts nested credentials/group memberships into the
// top-level entities the DP expects.
func (c *Converter) projectDBLess() *kong.DBLessDocument {
	out := kong.NewDBLessDocument()
	ids := dbLessIDs{
		service:  map[string]string{},
		route:    map[string]string{},
		plugin:   map[string]string{},
		model:    map[string]string{},
		vault:    map[string]string{},
		group:    map[string]string{},
		consumer: map[string]string{},
	}

	for _, svc := range c.out.Services {
		ids.service[svc.Name] = stableUUID("service:" + svc.Name)
		for _, route := range svc.Routes {
			ids.route[svc.Name+"|"+route.Name] = stableUUID("route:" + svc.Name + ":" + route.Name)
		}
	}
	for _, model := range c.out.AIModels {
		ids.model[model.Name] = firstNonEmpty(model.ID, stableUUID("ai_model:"+model.Name))
	}
	for _, vault := range c.out.Vaults {
		ids.vault[vault.Prefix] = firstNonEmpty(vault.ID, stableUUID("vault:"+vault.Prefix))
	}
	for _, group := range c.out.ConsumerGroups {
		ids.group[group.Name] = firstNonEmpty(group.ID, stableUUID("consumer_group:"+group.Name))
	}
	for _, consumer := range c.out.Consumers {
		ids.consumer[consumer.Username] = firstNonEmpty(consumer.ID, stableUUID("consumer:"+consumer.Username))
	}

	memberSeen := map[string]bool{}

	for _, svc := range c.out.Services {
		svcID := ids.service[svc.Name]
		out.Services = append(out.Services, toDBLessService(svc, svcID))

		for routeIdx, route := range svc.Routes {
			routeID := ids.route[svc.Name+"|"+route.Name]
			out.Routes = append(out.Routes, toDBLessRoute(route, routeID, svcID))

			for pluginIdx, plugin := range route.Plugins {
				id := firstNonEmpty(plugin.ID, stableUUID(fmt.Sprintf("plugin:route:%s:%s:%d", route.Name, plugin.Name, pluginIdx)))
				ids.plugin[id] = id
				out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{route: routeID}))
			}
			_ = routeIdx
		}

		for pluginIdx, plugin := range svc.Plugins {
			id := firstNonEmpty(plugin.ID, stableUUID(fmt.Sprintf("plugin:service:%s:%s:%d", svc.Name, plugin.Name, pluginIdx)))
			ids.plugin[id] = id
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{service: svcID}))
		}
	}

	for _, consumer := range c.out.Consumers {
		consumerID := ids.consumer[consumer.Username]
		out.Consumers = append(out.Consumers, kong.DBLessConsumer{
			ID:       consumerID,
			Username: consumer.Username,
			CustomID: consumer.CustomID,
			Tags:     consumer.Tags,
		})
		for credIdx, cred := range consumer.KeyAuthCredentials {
			out.KeyAuthCredentials = append(out.KeyAuthCredentials, kong.DBLessKeyAuthCredential{
				ID:       firstNonEmpty(cred.ID, stableUUID(fmt.Sprintf("keyauth:%s:%s:%d", consumer.Username, cred.Key, credIdx))),
				Key:      cred.Key,
				Consumer: consumerID,
				TTL:      cred.TTL,
				Tags:     cred.Tags,
			})
		}
		for pluginIdx, plugin := range consumer.Plugins {
			id := firstNonEmpty(plugin.ID, stableUUID(
				fmt.Sprintf("plugin:consumer:%s:%s:%d", consumer.Username, plugin.Name, pluginIdx)))
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{consumer: consumerID}))
		}
		for _, groupRef := range consumer.Groups {
			groupName := groupRef.Name
			groupID, ok := ids.group[groupName]
			if !ok {
				groupID = stableUUID("consumer_group:" + groupName)
				ids.group[groupName] = groupID
			}
			key := consumerID + "|" + groupID
			if memberSeen[key] {
				continue
			}
			memberSeen[key] = true
			out.ConsumerGroupConsumers = append(out.ConsumerGroupConsumers, kong.DBLessConsumerGroupMember{
				Consumer:      consumerID,
				ConsumerGroup: groupID,
			})
		}
	}

	for _, group := range c.out.ConsumerGroups {
		groupID := ids.group[group.Name]
		out.ConsumerGroups = append(out.ConsumerGroups, kong.DBLessConsumerGroup{
			ID:   groupID,
			Name: group.Name,
			Tags: group.Tags,
		})
		for pluginIdx, plugin := range group.Plugins {
			id := firstNonEmpty(plugin.ID, stableUUID(
				fmt.Sprintf("plugin:consumer_group:%s:%s:%d", group.Name, plugin.Name, pluginIdx)))
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{consumerGroup: groupID}))
		}
	}

	for _, vault := range c.out.Vaults {
		out.Vaults = append(out.Vaults, kong.DBLessVault{
			ID:          ids.vault[vault.Prefix],
			Prefix:      vault.Prefix,
			Name:        vault.Name,
			Description: vault.Description,
			Config:      vault.Config,
			Tags:        vault.Tags,
		})
	}

	for _, model := range c.out.AIModels {
		out.AIModels = append(out.AIModels, kong.DBLessAIModel{
			ID:    ids.model[model.Name],
			Name:  model.Name,
			Alias: model.Alias,
			Tags:  model.Tags,
		})
	}

	for pluginIdx, plugin := range c.out.Plugins {
		id := firstNonEmpty(plugin.ID, stableUUID(fmt.Sprintf("plugin:top:%s:%d", plugin.Name, pluginIdx)))
		out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{
			service:       lookupStringRef(plugin.Service, ids.service),
			route:         lookupStringRouteRef(plugin.Route, ids.route),
			consumer:      lookupStringRef(plugin.Consumer, ids.consumer),
			consumerGroup: lookupStringRef(plugin.ConsumerGroup, ids.group),
			model:         lookupStringRef(plugin.Model, ids.model),
		}))
	}

	return out
}

type scopeRef struct {
	service       string
	route         string
	consumer      string
	consumerGroup string
	model         string
}

func toDBLessPlugin(plugin kong.Plugin, id string, scope scopeRef) kong.DBLessPlugin {
	return kong.DBLessPlugin{
		ID:            id,
		Name:          plugin.Name,
		Enabled:       plugin.Enabled,
		Config:        plugin.Config,
		Service:       toDBLessFK(scope.service),
		Route:         toDBLessFK(scope.route),
		Consumer:      toDBLessFK(scope.consumer),
		ConsumerGroup: toDBLessFK(scope.consumerGroup),
		Model:         toDBLessFK(scope.model),
		Tags:          plugin.Tags,
	}
}

func toDBLessService(service kong.Service, id string) kong.DBLessService {
	out := kong.DBLessService{
		ID:       id,
		Name:     service.Name,
		URL:      service.URL,
		Host:     service.Host,
		Port:     service.Port,
		Protocol: service.Protocol,
		Path:     service.Path,
		Enabled:  service.Enabled,
		Retries:  service.Retries,
		Tags:     service.Tags,
	}
	if service.URL != "" {
		if parsed, err := url.Parse(service.URL); err == nil {
			if out.Protocol == "" {
				out.Protocol = parsed.Scheme
			}
			if out.Host == "" {
				out.Host = parsed.Hostname()
			}
			if out.Port == nil {
				port := defaultPort(parsed)
				if port != 0 {
					out.Port = &port
				}
			}
			if out.Path == "" && parsed.Path != "" {
				out.Path = parsed.Path
			}
		}
	}
	return out
}

func toDBLessRoute(route kong.Route, id, serviceID string) kong.DBLessRoute {
	r := kong.DBLessRoute{
		ID:                      id,
		Name:                    route.Name,
		Service:                 toDBLessFK(serviceID),
		Paths:                   route.Paths,
		Hosts:                   route.Hosts,
		Methods:                 route.Methods,
		Protocols:               route.Protocols,
		Headers:                 route.Headers,
		SNIs:                    route.SNIs,
		Sources:                 toDBLessCIDRPorts(route.Sources),
		Destinations:            toDBLessCIDRPorts(route.Destinations),
		StripPath:               route.StripPath,
		PreserveHost:            route.PreserveHost,
		HTTPSRedirectStatusCode: route.HTTPSRedirectStatusCode,
		RegexPriority:           route.RegexPriority,
		PathHandling:            route.PathHandling,
		RequestBuffering:        route.RequestBuffering,
		ResponseBuffering:       route.ResponseBuffering,
		Tags:                    route.Tags,
	}
	if len(r.Protocols) == 0 {
		r.Protocols = []string{"http", "https"}
	}
	return r
}

func toDBLessFK(id string) map[string]string {
	if id == "" {
		return nil
	}
	return map[string]string{
		"id": id,
	}
}

func toDBLessCIDRPorts(in []kong.CIDRPort) []kong.DBLessCIDRPort {
	if len(in) == 0 {
		return nil
	}
	out := make([]kong.DBLessCIDRPort, 0, len(in))
	for _, item := range in {
		out = append(out, kong.DBLessCIDRPort(item))
	}
	return out
}

func lookupRouteRef(ref *kong.Ref, ids map[string]string) string {
	if ref == nil {
		return ""
	}
	for key, id := range ids {
		if len(key) > len(ref.Name)+1 && key[len(key)-len(ref.Name)-1:] == "|"+ref.Name {
			return id
		}
	}
	return ""
}

func lookupStringRef(ref *kong.StringRef, ids map[string]string) string {
	if ref == nil {
		return ""
	}
	return ids[string(*ref)]
}

func lookupStringRouteRef(ref *kong.StringRef, ids map[string]string) string {
	if ref == nil {
		return ""
	}
	refName := string(*ref)
	for key, id := range ids {
		if len(key) > len(refName)+1 && key[len(key)-len(refName)-1:] == "|"+refName {
			return id
		}
	}
	return ""
}

func defaultPort(parsed *url.URL) int {
	if parsed.Port() != "" {
		var port int
		_, _ = fmt.Sscanf(parsed.Port(), "%d", &port)
		return port
	}
	switch parsed.Scheme {
	case "http", "ws":
		return 80 //nolint:mnd
	case "https", "wss":
		return 443 //nolint:mnd
	default:
		return 0
	}
}

func stableUUID(key string) string {
	sum := sha1.Sum(append(dbLessNamespace[:], []byte(key)...)) //nolint:gosec
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50 //nolint:mnd
	b[8] = (b[8] & 0x3f) | 0x80 //nolint:mnd
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
