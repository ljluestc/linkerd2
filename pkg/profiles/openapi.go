package profiles

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-openapi/spec"
	sp "github.com/linkerd/linkerd2/controller/gen/apis/serviceprofile/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	xLinkerdRetryable   = "x-linkerd-retryable"
	xLinkerdTimeout     = "x-linkerd-timeout"
	xLinkerdRouteLookup = "x-linkerd-route-lookup"
)

// RenderOpenAPI reads an OpenAPI spec file and renders the corresponding
// ServiceProfile to a buffer, given a namespace, service, and control plane
// namespace.
func RenderOpenAPI(fileName, namespace, name, clusterDomain string) (*sp.ServiceProfile, error) {

	input, err := readFile(fileName)
	if err != nil {
		return nil, err
	}

	bytes, err := io.ReadAll(input)
	if err != nil {
		return nil, fmt.Errorf("Error reading file: %w", err)
	}
	json, err := yaml.YAMLToJSON(bytes)
	if err != nil {
		return nil, fmt.Errorf("Error parsing yaml: %w", err)
	}

	swagger := spec.Swagger{}
	err = swagger.UnmarshalJSON(json)
	if err != nil {
		return nil, fmt.Errorf("Error parsing OpenAPI spec: %w", err)
	}

	profile := swaggerToServiceProfile(swagger, namespace, name, clusterDomain)

	return &profile, nil
}

func swaggerToServiceProfile(swagger spec.Swagger, namespace, name, clusterDomain string) sp.ServiceProfile {
	profile := sp.ServiceProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s.%s.svc.%s", name, namespace, clusterDomain),
			Namespace: namespace,
		},
		TypeMeta: ServiceProfileMeta,
	}

	routes := make([]*sp.RouteSpec, 0)

	// The info-level x-linkerd-route-lookup extension names an operation
	// attribute (e.g. operationId, or another vendor extension) whose value,
	// when present, overrides the generated route name.
	routeLookup := ""
	if swagger.Info != nil {
		routeLookup, _ = swagger.Info.Extensions.GetString(xLinkerdRouteLookup)
	}

	paths := make([]string, 0)
	if swagger.Paths != nil {
		for path := range swagger.Paths.Paths {
			paths = append(paths, path)
		}
		sort.Strings(paths)
	}

	base := strings.TrimRight(swagger.BasePath, "/")
	for _, relPath := range paths {
		item := swagger.Paths.Paths[relPath]
		path := base + "/" + strings.TrimLeft(relPath, "/")
		pathRegex := PathToRegex(path)
		if item.Delete != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodDelete, item.Delete, routeLookup)
			routes = append(routes, spec)
		}
		if item.Get != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodGet, item.Get, routeLookup)
			routes = append(routes, spec)
		}
		if item.Head != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodHead, item.Head, routeLookup)
			routes = append(routes, spec)
		}
		if item.Options != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodOptions, item.Options, routeLookup)
			routes = append(routes, spec)
		}
		if item.Patch != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodPatch, item.Patch, routeLookup)
			routes = append(routes, spec)
		}
		if item.Post != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodPost, item.Post, routeLookup)
			routes = append(routes, spec)
		}
		if item.Put != nil {
			spec := mkRouteSpec(path, pathRegex, http.MethodPut, item.Put, routeLookup)
			routes = append(routes, spec)
		}
	}

	profile.Spec.Routes = routes
	return profile
}

// MkRouteSpec makes a service profile route from an OpenAPI operation.
func MkRouteSpec(path, pathRegex string, method string, operation *spec.Operation) *sp.RouteSpec {
	return mkRouteSpec(path, pathRegex, method, operation, "")
}

func mkRouteSpec(path, pathRegex, method string, operation *spec.Operation, routeLookup string) *sp.RouteSpec {
	retryable := false
	timeout := ""
	var responses *spec.Responses
	name := fmt.Sprintf("%s %s", method, path)
	if operation != nil {
		retryable, _ = operation.VendorExtensible.Extensions.GetBool(xLinkerdRetryable)
		timeout, _ = operation.VendorExtensible.Extensions.GetString(xLinkerdTimeout)
		responses = operation.Responses
		if routeLookup != "" {
			if lookupName := lookupRouteName(operation, routeLookup); lookupName != "" {
				name = lookupName
			}
		}
	}
	return &sp.RouteSpec{
		Name:            name,
		Condition:       toReqMatch(pathRegex, method),
		ResponseClasses: toRspClasses(responses),
		IsRetryable:     retryable,
		Timeout:         timeout,
	}
}

// lookupRouteName resolves the operation attribute named by the
// x-linkerd-route-lookup extension to a route name. The attribute may be a
// vendor extension on the operation (e.g. x-acmecorp-route) or one of the
// operation's own string fields (e.g. operationId). Attributes that are
// missing or not strings yield "" so that the default route name is used.
func lookupRouteName(operation *spec.Operation, token string) string {
	if value, ok := operation.VendorExtensible.Extensions.GetString(token); ok {
		return value
	}
	value, err := operation.JSONLookup(token)
	if err != nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return ""
}

func toReqMatch(path string, method string) *sp.RequestMatch {
	return &sp.RequestMatch{
		PathRegex: path,
		Method:    method,
	}
}

func toRspClasses(responses *spec.Responses) []*sp.ResponseClass {
	if responses == nil {
		return nil
	}
	classes := make([]*sp.ResponseClass, 0)

	statuses := make([]int, 0)
	for status := range responses.StatusCodeResponses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)

	for _, status := range statuses {
		cond := &sp.ResponseMatch{
			Status: &sp.Range{
				Min: uint32(status),
				Max: uint32(status),
			},
		}
		classes = append(classes, &sp.ResponseClass{
			Condition: cond,
			IsFailure: status >= 500,
		})
	}
	return classes
}
