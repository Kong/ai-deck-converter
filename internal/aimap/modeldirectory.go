package aimap

// ModelDirectoryRouteType is the ai-proxy-advanced route_type that serves the
// configured model catalog on its Route instead of inference.
const ModelDirectoryRouteType = "llm/v1/models"

// ModelDirectorySpec is the endpoint that serves a model list under a model
// base path.
var ModelDirectorySpec = EndpointSpec{
	RouteLabel: "models",
	PathSuffix: "/v1/models",
	Methods:    []string{"GET"},
	RouteType:  ModelDirectoryRouteType,
}
