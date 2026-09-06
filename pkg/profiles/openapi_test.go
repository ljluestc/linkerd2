package profiles

import (
	"testing"

	"github.com/go-openapi/spec"
	sp "github.com/linkerd/linkerd2/controller/gen/apis/serviceprofile/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSwaggerToServiceProfile(t *testing.T) {
	namespace := "myns"
	name := "mysvc"
	clusterDomain := "mycluster.local"

	swagger := spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Paths: &spec.Paths{
				Paths: map[string]spec.PathItem{
					"/authors/{id}": {
						PathItemProps: spec.PathItemProps{
							Post: &spec.Operation{
								OperationProps: spec.OperationProps{
									Responses: &spec.Responses{
										ResponsesProps: spec.ResponsesProps{
											StatusCodeResponses: map[int]spec.Response{
												500: {},
											},
										},
									},
								},
								VendorExtensible: spec.VendorExtensible{
									Extensions: spec.Extensions{xLinkerdRetryable: true, xLinkerdTimeout: "60s"},
								},
							},
						},
					},
					"/path/with/trailing/slash/": {
						PathItemProps: spec.PathItemProps{
							Get: &spec.Operation{},
						},
					},
				},
			},
		},
	}

	expectedServiceProfile := sp.ServiceProfile{
		TypeMeta: ServiceProfileMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "." + namespace + ".svc." + clusterDomain,
			Namespace: namespace,
		},
		Spec: sp.ServiceProfileSpec{
			Routes: []*sp.RouteSpec{
				{
					Name: "POST /authors/{id}",
					Condition: &sp.RequestMatch{
						PathRegex: "/authors/[^/]*",
						Method:    "POST",
					},
					ResponseClasses: []*sp.ResponseClass{
						{
							Condition: &sp.ResponseMatch{
								Status: &sp.Range{
									Min: 500,
									Max: 500,
								},
							},
							IsFailure: true,
						},
					},
					IsRetryable: true,
					Timeout:     "60s",
				},
				{
					Name: "GET /path/with/trailing/slash/",
					Condition: &sp.RequestMatch{
						PathRegex: "/path/with/trailing/slash/",
						Method:    "GET",
					},
				},
			},
		},
	}

	actualServiceProfile := swaggerToServiceProfile(swagger, namespace, name, clusterDomain)

	err := ServiceProfileYamlEquals(actualServiceProfile, expectedServiceProfile)
	if err != nil {
		t.Fatalf("ServiceProfiles are not equal: %v", err)
	}
}

func TestSwaggerToServiceProfileRouteLookup(t *testing.T) {
	namespace := "myns"
	name := "mysvc"
	clusterDomain := "mycluster.local"

	mkSwagger := func(routeLookup string) spec.Swagger {
		return spec.Swagger{
			SwaggerProps: spec.SwaggerProps{
				Info: &spec.Info{
					VendorExtensible: spec.VendorExtensible{
						Extensions: spec.Extensions{xLinkerdRouteLookup: routeLookup},
					},
				},
				Paths: &spec.Paths{
					Paths: map[string]spec.PathItem{
						"/pets/{id}": {
							PathItemProps: spec.PathItemProps{
								Get: &spec.Operation{
									OperationProps: spec.OperationProps{
										ID: "get_pet_by_id",
									},
									VendorExtensible: spec.VendorExtensible{
										Extensions: spec.Extensions{"x-acmecorp-route": "acme_get_pet"},
									},
								},
								Delete: &spec.Operation{},
							},
						},
					},
				},
			},
		}
	}

	mkExpected := func(getRouteName string) sp.ServiceProfile {
		return sp.ServiceProfile{
			TypeMeta: ServiceProfileMeta,
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "." + namespace + ".svc." + clusterDomain,
				Namespace: namespace,
			},
			Spec: sp.ServiceProfileSpec{
				Routes: []*sp.RouteSpec{
					{
						Name: "DELETE /pets/{id}",
						Condition: &sp.RequestMatch{
							PathRegex: "/pets/[^/]*",
							Method:    "DELETE",
						},
					},
					{
						Name: getRouteName,
						Condition: &sp.RequestMatch{
							PathRegex: "/pets/[^/]*",
							Method:    "GET",
						},
					},
				},
			},
		}
	}

	testCases := []struct {
		description  string
		routeLookup  string
		getRouteName string
	}{
		{
			description:  "operationId lookup",
			routeLookup:  "operationId",
			getRouteName: "get_pet_by_id",
		},
		{
			description:  "vendor extension lookup",
			routeLookup:  "x-acmecorp-route",
			getRouteName: "acme_get_pet",
		},
		{
			description:  "missing attribute falls back to default",
			routeLookup:  "x-does-not-exist",
			getRouteName: "GET /pets/{id}",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			actual := swaggerToServiceProfile(mkSwagger(tc.routeLookup), namespace, name, clusterDomain)
			expected := mkExpected(tc.getRouteName)
			err := ServiceProfileYamlEquals(actual, expected)
			if err != nil {
				t.Fatalf("ServiceProfiles are not equal: %v", err)
			}
		})
	}
}
