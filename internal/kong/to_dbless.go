package kong

import (
	"crypto/sha1" //nolint:gosec
	"fmt"
	"net/url"
)

var dbLessNamespace = [16]byte{
	0x8f, 0x17, 0x73, 0x5c, 0x41, 0x02, 0x49, 0x6b,
	0xa3, 0x2f, 0x92, 0x0c, 0x4d, 0x18, 0x62, 0xf1,
}

type dbLessIDs struct {
	service  map[string]string
	cert     map[string]string
	route    map[string]string
	plugin   map[string]string
	model    map[string]string
	vault    map[string]string
	group    map[string]string
	consumer map[string]string
}

// ToDBLess changes the decK document into a flattened db-less document.
// Each entity without an ID gets a stable ID made from its name.
// Name references become ID references.
func (d *Document) ToDBLess() *DBLessDocument {
	out := NewDBLessDocument()
	ids := dbLessIDs{
		service:  map[string]string{},
		route:    map[string]string{},
		plugin:   map[string]string{},
		model:    map[string]string{},
		vault:    map[string]string{},
		cert:     map[string]string{},
		group:    map[string]string{},
		consumer: map[string]string{},
	}

	for _, svc := range d.Services {
		ids.service[svc.Name] = firstNonEmpty(svc.ID, StableUUID("service:"+svc.Name))
		for _, route := range svc.Routes {
			ids.route[svc.Name+"|"+route.Name] = StableUUID("route:" + svc.Name + ":" + route.Name)
		}
	}
	for _, model := range d.AIModels {
		ids.model[model.Name] = firstNonEmpty(model.ID, StableUUID("ai_model:"+model.Name))
	}
	for _, vault := range d.Vaults {
		ids.vault[vault.Prefix] = firstNonEmpty(vault.ID, StableUUID("vault:"+vault.Prefix))
	}
	for i, cert := range d.Certificates {
		ids.cert[certKey(cert, i)] = firstNonEmpty(cert.ID, StableUUID("certificate:"+certKey(cert, i)))
	}
	for _, group := range d.ConsumerGroups {
		ids.group[group.Name] = firstNonEmpty(group.ID, StableUUID("consumer_group:"+group.Name))
	}
	for _, consumer := range d.Consumers {
		ids.consumer[consumer.Username] = firstNonEmpty(consumer.ID, StableUUID("consumer:"+consumer.Username))
	}

	memberSeen := map[string]bool{}

	for _, svc := range d.Services {
		svcID := ids.service[svc.Name]
		out.Services = append(out.Services, toDBLessService(svc, svcID))

		for routeIdx, route := range svc.Routes {
			routeID := ids.route[svc.Name+"|"+route.Name]
			out.Routes = append(out.Routes, toDBLessRoute(route, routeID, svcID))

			for pluginIdx, plugin := range route.Plugins {
				id := firstNonEmpty(plugin.ID, StableUUID(fmt.Sprintf("plugin:route:%s:%s:%d", route.Name, plugin.Name, pluginIdx)))
				ids.plugin[id] = id
				out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{route: routeID}))
			}
			_ = routeIdx
		}

		for pluginIdx, plugin := range svc.Plugins {
			id := firstNonEmpty(plugin.ID, StableUUID(fmt.Sprintf("plugin:service:%s:%s:%d", svc.Name, plugin.Name, pluginIdx)))
			ids.plugin[id] = id
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{service: svcID}))
		}
	}

	for _, consumer := range d.Consumers {
		consumerID := ids.consumer[consumer.Username]
		out.Consumers = append(out.Consumers, DBLessConsumer{
			ID:       consumerID,
			Username: consumer.Username,
			CustomID: consumer.CustomID,
			Tags:     consumer.Tags,
		})
		for credIdx, cred := range consumer.KeyAuthCredentials {
			out.KeyAuthCredentials = append(out.KeyAuthCredentials, DBLessKeyAuthCredential{
				ID:       firstNonEmpty(cred.ID, StableUUID(fmt.Sprintf("keyauth:%s:%s:%d", consumer.Username, cred.Key, credIdx))),
				Key:      cred.Key,
				Consumer: consumerID,
				TTL:      cred.TTL,
				Tags:     cred.Tags,
			})
		}
		for pluginIdx, plugin := range consumer.Plugins {
			id := firstNonEmpty(plugin.ID, StableUUID(
				fmt.Sprintf("plugin:consumer:%s:%s:%d", consumer.Username, plugin.Name, pluginIdx)))
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{consumer: consumerID}))
		}
		for _, groupRef := range consumer.Groups {
			groupName := groupRef.Name
			groupID, ok := ids.group[groupName]
			if !ok {
				groupID = StableUUID("consumer_group:" + groupName)
				ids.group[groupName] = groupID
			}
			key := consumerID + "|" + groupID
			if memberSeen[key] {
				continue
			}
			memberSeen[key] = true
			out.ConsumerGroupConsumers = append(out.ConsumerGroupConsumers, DBLessConsumerGroupMember{
				Consumer:      consumerID,
				ConsumerGroup: groupID,
			})
		}
	}

	for _, group := range d.ConsumerGroups {
		groupID := ids.group[group.Name]
		out.ConsumerGroups = append(out.ConsumerGroups, DBLessConsumerGroup{
			ID:   groupID,
			Name: group.Name,
			Tags: group.Tags,
		})
		for pluginIdx, plugin := range group.Plugins {
			id := firstNonEmpty(plugin.ID, StableUUID(
				fmt.Sprintf("plugin:consumer_group:%s:%s:%d", group.Name, plugin.Name, pluginIdx)))
			out.Plugins = append(out.Plugins, toDBLessPlugin(plugin, id, scopeRef{consumerGroup: groupID}))
		}
	}

	for i, cert := range d.Certificates {
		certID := ids.cert[certKey(cert, i)]
		out.Certificates = append(out.Certificates, DBLessCertificate{
			ID:      certID,
			Cert:    cert.Cert,
			Key:     cert.Key,
			CertAlt: cert.CertAlt,
			KeyAlt:  cert.KeyAlt,
			Tags:    cert.Tags,
		})
		for _, sni := range cert.SNIs {
			out.SNIs = append(out.SNIs, DBLessSNI{
				ID:          firstNonEmpty(sni.ID, StableUUID("sni:"+certKey(cert, i)+":"+sni.Name)),
				Name:        sni.Name,
				Certificate: map[string]string{"id": certID},
				Tags:        sni.Tags,
			})
		}
	}

	for _, vault := range d.Vaults {
		out.Vaults = append(out.Vaults, DBLessVault{
			ID:          ids.vault[vault.Prefix],
			Prefix:      vault.Prefix,
			Name:        vault.Name,
			Description: vault.Description,
			Config:      vault.Config,
			Tags:        vault.Tags,
		})
	}

	for _, model := range d.AIModels {
		out.AIModels = append(out.AIModels, DBLessAIModel{
			ID:    ids.model[model.Name],
			Name:  model.Name,
			Alias: model.Alias,
			Tags:  model.Tags,
		})
	}

	for _, plugin := range d.CustomPlugins {
		out.CustomPlugins = append(out.CustomPlugins, DBLessCustomPlugin{
			ID:      firstNonEmpty(plugin.ID, StableUUID("custom_plugin:"+plugin.Name)),
			Name:    plugin.Name,
			Schema:  plugin.Schema,
			Handler: plugin.Handler,
		})
	}

	for _, cert := range d.CACertificates {
		out.CACertificates = append(out.CACertificates, DBLessCACertificate{
			ID:         firstNonEmpty(cert.ID, StableUUID("ca_certificate:"+cert.Cert)),
			Cert:       cert.Cert,
			CertDigest: cert.CertDigest,
			Tags:       cert.Tags,
		})
	}

	for pluginIdx, plugin := range d.Plugins {
		id := firstNonEmpty(plugin.ID, StableUUID(fmt.Sprintf("plugin:top:%s:%d", plugin.Name, pluginIdx)))
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

func toDBLessPlugin(plugin Plugin, id string, scope scopeRef) DBLessPlugin {
	return DBLessPlugin{
		ID:            id,
		Name:          plugin.Name,
		Enabled:       plugin.Enabled,
		Protocols:     plugin.Protocols,
		Condition:     plugin.Condition,
		Config:        plugin.Config,
		Service:       toDBLessFK(scope.service),
		Route:         toDBLessFK(scope.route),
		Consumer:      toDBLessFK(scope.consumer),
		ConsumerGroup: toDBLessFK(scope.consumerGroup),
		Model:         toDBLessFK(scope.model),
		Tags:          plugin.Tags,
		TargetSources: plugin.TargetSources,
		Source:        plugin.Source,
	}
}

func toDBLessService(service Service, id string) DBLessService {
	out := DBLessService{
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
		Source:   service.Source,
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

func toDBLessRoute(route Route, id, serviceID string) DBLessRoute {
	r := DBLessRoute{
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
		Source:                  route.Source,
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

func toDBLessCIDRPorts(in []CIDRPort) []DBLessCIDRPort {
	if len(in) == 0 {
		return nil
	}
	out := make([]DBLessCIDRPort, 0, len(in))
	for _, item := range in {
		out = append(out, DBLessCIDRPort(item))
	}
	return out
}

func lookupStringRef(ref *StringRef, ids map[string]string) string {
	if ref == nil {
		return ""
	}
	return ids[string(*ref)]
}

func lookupStringRouteRef(ref *StringRef, ids map[string]string) string {
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

// StableUUID returns a version 5 style UUID for key. The same key always gives
// the same UUID.
func StableUUID(key string) string {
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

// certKey identifies a certificate for stable db-less ID derivation. The source
// name is preferred; a hand-written decK config carries none, so the position
// keeps the derived ID stable for a given input.
func certKey(cert Certificate, idx int) string {
	if cert.SourceName != "" {
		return cert.SourceName
	}
	return fmt.Sprintf("%d", idx)
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
