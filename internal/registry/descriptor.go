package registry

import (
	"encoding/json"
	"errors"
	"reflect"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errDescriptorTooLarge = errors.New("descriptor exceeds public bounds")

type portableDescriptor struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	productDescriptor
	Generation         int64                      `json:"generation"`
	ObservedGeneration int64                      `json:"observedGeneration"`
	Health             map[string]healthDimension `json:"health"`
}

type healthDimension struct {
	State              string `json:"state"`
	Message            string `json:"message"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

// portableDescriptorFor projects public observation states, never arbitrary condition reasons or messages.
func portableDescriptorFor(product *datav1alpha1.DataProduct) portableDescriptor {
	descriptor := portableDescriptor{
		APIVersion: "data-product-descriptor/v1", Kind: "DataProduct",
		productDescriptor: descriptorFor(product), Generation: product.Generation,
		Health: map[string]healthDimension{
			"source": publicHealth(
				product,
				datav1alpha1.ConditionSourceReady,
				product.Spec.Source != nil,
			),
			"connector": publicHealth(
				product,
				datav1alpha1.ConditionConnectorReady,
				product.Spec.Connector != nil,
			),
			"contracts": publicHealth(
				product,
				datav1alpha1.ConditionContractsReady,
				len(product.Spec.ContractChecks) != 0,
			),
			"composition": publicHealth(
				product,
				datav1alpha1.ConditionCompositionReady,
				len(product.Spec.Inputs) != 0,
			),
		},
	}
	aggregate := publicHealth(product, datav1alpha1.ConditionReady, true)
	descriptor.ObservedGeneration = aggregate.ObservedGeneration
	descriptor.Ready = aggregate.State == "ready"
	descriptor.Readiness = readinessDescriptor{Reason: aggregate.State, Message: aggregate.Message}
	if descriptor.Composition != nil {
		state := descriptor.Health["composition"]
		descriptor.Composition = &readinessDescriptor{Reason: state.State, Message: state.Message}
	}
	if descriptor.Lineage != nil {
		descriptor.Lineage = append([]datav1alpha1.InputStatus(nil), descriptor.Lineage...)
		for index := range descriptor.Lineage {
			if descriptor.Lineage[index].Ready {
				descriptor.Lineage[index].Reason = "InputReady"
			} else {
				descriptor.Lineage[index].Reason = "InputNotReady"
			}
		}
	}
	return descriptor
}

// publicHealth distinguishes missing and obsolete evidence before considering success or disabled reasons.
func publicHealth(
	product *datav1alpha1.DataProduct,
	conditionType string,
	applicable bool,
) healthDimension {
	health := healthDimension{State: "unobserved", Generation: product.Generation}
	condition := meta.FindStatusCondition(product.Status.Conditions, conditionType)
	switch {
	case !applicable:
		health.State = "not-applicable"
	case condition == nil:
		// A missing independent condition is never inferred from aggregate readiness.
	case condition.ObservedGeneration != product.Generation:
		health.State = "stale"
	case condition.Status == metav1.ConditionTrue:
		health.State = "ready"
	case condition.Status == metav1.ConditionFalse:
		health.State = "not-ready"
		if disabledCondition(conditionType, condition.Reason) {
			health.State = "disabled"
		}
	}
	if applicable && condition != nil {
		health.ObservedGeneration = condition.ObservedGeneration
	}
	health.Message = healthExplanation(health.State)
	return health
}

// disabledCondition recognizes only the controller's published feature-disabled reasons for this dimension.
func disabledCondition(conditionType, reason string) bool {
	switch conditionType {
	case datav1alpha1.ConditionSourceReady:
		return reason == "SourceFeatureDisabled" || reason == "EngineProviderFeatureDisabled"
	case datav1alpha1.ConditionConnectorReady:
		return reason == "ConnectorFeatureDisabled"
	case datav1alpha1.ConditionContractsReady:
		return reason == "ContractFeatureDisabled"
	case datav1alpha1.ConditionCompositionReady:
		return reason == "CompositionFeatureDisabled"
	case datav1alpha1.ConditionReady:
		return reason == "SourceFeatureDisabled" || reason == "EngineProviderFeatureDisabled" ||
			reason == "ConnectorFeatureDisabled" ||
			reason == "ContractFeatureDisabled" ||
			reason == "CompositionFeatureDisabled"
	}
	return false
}

// healthExplanation supplies fixed public wording instead of forwarding provider or backend diagnostics.
func healthExplanation(state string) string {
	switch state {
	case "ready":
		return "The controller reported readiness for the current product generation."
	case "not-ready":
		return "The current observation is not ready. The product owner must resolve the reported condition."
	case "stale":
		return "The observation belongs to another product generation. Wait for reconciliation."
	case "disabled":
		return "The observation feature is disabled. The operator controls its rollout."
	case "not-applicable":
		return "This product does not declare this capability."
	default:
		return "No conclusive independent observation is available for this capability."
	}
}

// encodePortableDescriptor enforces field, collection and encoded bounds before publishing metadata.
func encodePortableDescriptor(product *datav1alpha1.DataProduct) ([]byte, error) {
	descriptor := portableDescriptorFor(product)
	if !withinDiscoveryBounds(reflect.ValueOf(descriptor)) {
		return nil, errDescriptorTooLarge
	}
	if !validPortableMetadata(descriptor) {
		return nil, errors.New("invalid public descriptor metadata")
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		return nil, err
	}
	if len(data) > maxDescriptorBytes {
		return nil, errDescriptorTooLarge
	}
	return data, nil
}

// withinDiscoveryBounds checks only the projected public value, excluding all source and credential references.
func withinDiscoveryBounds(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return len(value.String()) <= 16<<10
	case reflect.Pointer, reflect.Interface:
		return value.IsNil() || withinDiscoveryBounds(value.Elem())
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if !withinDiscoveryBounds(value.Field(index)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Len() > 1024 {
			return false
		}
		for index := 0; index < value.Len(); index++ {
			if !withinDiscoveryBounds(value.Index(index)) {
				return false
			}
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			if !withinDiscoveryBounds(value.MapIndex(key)) {
				return false
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return true
	case reflect.Invalid, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return false
	}
	return true
}
