package httpapi

import (
	"net/http"
	"strings"

	"canonkeeper/backend/internal/observability"
)

func RequestRouteInfo(r *http.Request) observability.RouteInfo {
	route := registeredRouteForRequest(r)
	if route.OperationID == "" {
		return observability.RouteInfo{}
	}
	return observability.RouteInfo{
		OperationID:  route.OperationID,
		PathTemplate: route.Path,
		PathValues:   routePathValues(route.Path, normalizedRoutePath(r.URL.Path)),
	}
}

// routePathValues называет сегменты пути по параметрам шаблона: {jobId} → значение из пути.
func routePathValues(template string, path string) map[string]string {
	templateSegments := pathSegments(template)
	pathSegments := pathSegments(path)
	values := map[string]string{}
	for index, templateSegment := range templateSegments {
		if isPathParameter(templateSegment) && index < len(pathSegments) {
			values[strings.Trim(templateSegment, "{}")] = pathSegments[index]
		}
	}
	return values
}

func registeredRouteForRequest(r *http.Request) RouteSpec {
	if r == nil || r.URL == nil {
		return RouteSpec{}
	}

	method := strings.ToLower(r.Method)
	path := normalizedRoutePath(r.URL.Path)
	for _, route := range RouteCatalog() {
		if route.Method == method && pathMatchesTemplate(route.Path, path) {
			return route
		}
	}
	return RouteSpec{}
}

func normalizedRoutePath(path string) string {
	if path == "/api/v1" {
		return "/"
	}
	return strings.TrimPrefix(path, "/api/v1")
}

func pathMatchesTemplate(template string, path string) bool {
	templateSegments := pathSegments(template)
	pathSegments := pathSegments(path)
	if len(templateSegments) != len(pathSegments) {
		return false
	}

	for index, templateSegment := range templateSegments {
		if isPathParameter(templateSegment) {
			if pathSegments[index] == "" {
				return false
			}
			continue
		}
		if templateSegment != pathSegments[index] {
			return false
		}
	}

	return true
}

func routePathParameter(template string, path string, name string) string {
	templateSegments := pathSegments(template)
	pathSegments := pathSegments(path)
	if len(templateSegments) != len(pathSegments) {
		return ""
	}

	want := "{" + name + "}"
	for index, templateSegment := range templateSegments {
		if templateSegment == want {
			return pathSegments[index]
		}
	}
	return ""
}

func pathSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func isPathParameter(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}
