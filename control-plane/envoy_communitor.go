package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	"istio.io/client-go/pkg/apis/networking/v1alpha3"
	_struct "github.com/golang/protobuf/ptypes/struct"
	istioclient "istio.io/client-go/pkg/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type EnvoyCommunicator struct {
	k8sClient   *kubernetes.Clientset
	istioClient *istioclient.Clientset
	namespace   string
}

type FreezeFilterConfig struct {
	TraceID   string `json:"trace_id"`
	State     string `json:"state"` // PREPARE, FREEZE, UNFREEZE
	TimeoutMs int64  `json:"timeout_ms"`
}

func NewEnvoyCommunicator(namespace string) (*EnvoyCommunicator, error) {
	// Try in-cluster config first, then fall back to kubeconfig
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config: %v", err)
		}
	}

	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %v", err)
	}

	istioClient, err := istioclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Istio client: %v", err)
	}

	return &EnvoyCommunicator{
		k8sClient:   k8sClient,
		istioClient: istioClient,
		namespace:   namespace,
	}, nil
}

// CreateFreezeFilter creates an EnvoyFilter to control trace freezing
func (ec *EnvoyCommunicator) CreateFreezeFilter(filterName, traceID, serviceName, state string) error {
	log.Printf("[EnvoyCommunicator] Creating filter %s for trace %s, service %s, state %s",
		filterName, traceID, serviceName, state)

	// Create configuration for the WASM filter
	config := FreezeFilterConfig{
		TraceID:   traceID,
		State:     state,
		TimeoutMs: 30000, // 30 second timeout
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	// Create EnvoyFilter targeting the specific service
	envoyFilter := &v1alpha3.EnvoyFilter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      filterName,
			Namespace: ec.namespace,
		},
		Spec: networkingv1alpha3.EnvoyFilter{
			WorkloadSelector: &networkingv1alpha3.WorkloadSelector{
				Labels: map[string]string{
					"app": serviceName,
				},
			},
			ConfigPatches: []*networkingv1alpha3.EnvoyFilter_EnvoyConfigObjectPatch{
				{
					ApplyTo: networkingv1alpha3.EnvoyFilter_HTTP_FILTER,
					Match: &networkingv1alpha3.EnvoyFilter_EnvoyConfigObjectMatch{
						Context: networkingv1alpha3.EnvoyFilter_SIDECAR_INBOUND,
						ObjectTypes: &networkingv1alpha3.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
							Listener: &networkingv1alpha3.EnvoyFilter_ListenerMatch{
								FilterChain: &networkingv1alpha3.EnvoyFilter_ListenerMatch_FilterChainMatch{
									Filter: &networkingv1alpha3.EnvoyFilter_ListenerMatch_FilterMatch{
										Name: "envoy.filters.network.http_connection_manager",
										SubFilter: &networkingv1alpha3.EnvoyFilter_ListenerMatch_SubFilterMatch{
											Name: "envoy.filters.http.router",
										},
									},
								},
							},
						},
					},
					Patch: &networkingv1alpha3.EnvoyFilter_Patch{
						Operation: networkingv1alpha3.EnvoyFilter_Patch_INSERT_BEFORE,
						Value: &_struct.Struct{
							Fields: map[string]*_struct.Value{
								"name": {
									Kind: &_struct.Value_StringValue{
										StringValue: "envoy.filters.http.wasm",
									},
								},
								"typed_config": {
									Kind: &_struct.Value_StructValue{
										StructValue: &_struct.Struct{
											Fields: map[string]*_struct.Value{
												"@type": {
													Kind: &_struct.Value_StringValue{
														StringValue: "type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm",
													},
												},
												"config": {
													Kind: &_struct.Value_StructValue{
														StructValue: &_struct.Struct{
															Fields: map[string]*_struct.Value{
																"vm_config": {
																	Kind: &_struct.Value_StructValue{
																		StructValue: &_struct.Struct{
																			Fields: map[string]*_struct.Value{
																				"runtime": {
																					Kind: &_struct.Value_StringValue{
																						StringValue: "envoy.wasm.runtime.v8",
																					},
																				},
																				"code": {
																					Kind: &_struct.Value_StructValue{
																						StructValue: &_struct.Struct{
																							Fields: map[string]*_struct.Value{
																								"local": {
																									Kind: &_struct.Value_StructValue{
																										StructValue: &_struct.Struct{
																											Fields: map[string]*_struct.Value{
																												"filename": {
																													Kind: &_struct.Value_StringValue{
																														StringValue: "/etc/istio/extensions/freeze-filter.wasm",
																													},
																												},
																											},
																										},
																									},
																								},
																							},
																						},
																					},
																				},
																			},
																		},
																	},
																},
																"configuration": {
																	Kind: &_struct.Value_StructValue{
																		StructValue: &_struct.Struct{
																			Fields: map[string]*_struct.Value{
																				"@type": {
																					Kind: &_struct.Value_StringValue{
																						StringValue: "type.googleapis.com/google.protobuf.StringValue",
																					},
																				},
																				"value": {
																					Kind: &_struct.Value_StringValue{
																						StringValue: string(configJSON),
																					},
																				},
																			},
																		},
																	},
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create or update the EnvoyFilter
	ctx := context.Background()
	_, err = ec.istioClient.NetworkingV1alpha3().EnvoyFilters(ec.namespace).Create(
		ctx, envoyFilter, metav1.CreateOptions{})

	if err != nil {
		// If already exists, try update
		_, err = ec.istioClient.NetworkingV1alpha3().EnvoyFilters(ec.namespace).Update(
			ctx, envoyFilter, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create/update EnvoyFilter: %v", err)
		}
	}

	log.Printf("[EnvoyCommunicator] ✅ Successfully created/updated filter %s", filterName)
	return nil
}

// DeleteFreezeFilter removes an EnvoyFilter
func (ec *EnvoyCommunicator) DeleteFreezeFilter(filterName string) error {
	log.Printf("[EnvoyCommunicator] Deleting filter %s", filterName)

	ctx := context.Background()
	err := ec.istioClient.NetworkingV1alpha3().EnvoyFilters(ec.namespace).Delete(
		ctx, filterName, metav1.DeleteOptions{})

	if err != nil {
		log.Printf("[EnvoyCommunicator] Warning: Failed to delete filter %s: %v", filterName, err)
		return err
	}

	log.Printf("[EnvoyCommunicator] ✅ Deleted filter %s", filterName)
	return nil
}

// UpdateFreezeState updates an existing freeze filter's state
func (ec *EnvoyCommunicator) UpdateFreezeState(filterName, traceID, state string) error {
	log.Printf("[EnvoyCommunicator] Updating filter %s to state %s", filterName, state)

	// Get existing filter
	ctx := context.Background()
	filter, err := ec.istioClient.NetworkingV1alpha3().EnvoyFilters(ec.namespace).Get(
		ctx, filterName, metav1.GetOptions{})

	if err != nil {
		return fmt.Errorf("failed to get existing filter: %v", err)
	}

	// Update configuration
	config := FreezeFilterConfig{
		TraceID:   traceID,
		State:     state,
		TimeoutMs: 30000,
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	// Update the configuration field
	if len(filter.Spec.ConfigPatches) > 0 {
		patch := filter.Spec.ConfigPatches[0]
		if patch.Patch != nil && patch.Patch.Value != nil {
			if typedConfig, ok := patch.Patch.Value.Fields["typed_config"]; ok {
				if structVal := typedConfig.GetStructValue(); structVal != nil {
					if configField, ok := structVal.Fields["config"]; ok {
						if configStruct := configField.GetStructValue(); configStruct != nil {
							if confField, ok := configStruct.Fields["configuration"]; ok {
								if confStruct := confField.GetStructValue(); confStruct != nil {
									confStruct.Fields["value"] = &_struct.Value{
										Kind: &_struct.Value_StringValue{
											StringValue: string(configJSON),
										},
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Update the filter
	_, err = ec.istioClient.NetworkingV1alpha3().EnvoyFilters(ec.namespace).Update(
		ctx, filter, metav1.UpdateOptions{})

	if err != nil {
		return fmt.Errorf("failed to update filter: %v", err)
	}

	log.Printf("[EnvoyCommunicator] ✅ Updated filter %s to state %s", filterName, state)
	return nil
}
