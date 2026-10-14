package controller

import (
	"context"
	"fmt"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	connectorv1 "github.com/devantler-tech/data-product-controller/internal/connector/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// observeContracts refreshes the independent condition before any other capability can return early.
func (r *DataProductReconciler) observeContracts(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
) {
	if len(product.Spec.ContractChecks) == 0 {
		meta.RemoveStatusCondition(&product.Status.Conditions, datav1alpha1.ConditionContractsReady)
		return
	}
	observation := connectorv1.Observation{
		Reason:  "ContractFeatureDisabled",
		Message: "Enable contract-readiness to observe the selected contract probes.",
	}
	if r.ContractsEnabled != nil && r.ContractsEnabled(ctx) {
		observation = r.checkContracts(ctx, product)
	}
	status := metav1.ConditionFalse
	if observation.Ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(
		&product.Status.Conditions,
		metav1.Condition{
			Type:               datav1alpha1.ConditionContractsReady,
			Status:             status,
			Reason:             observation.Reason,
			Message:            observation.Message,
			ObservedGeneration: product.Generation,
		},
	)
}

// checkContracts bounds all exact-name API reads together and reports the first failing selection.
func (r *DataProductReconciler) checkContracts(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
) connectorv1.Observation {
	if len(product.Spec.ContractChecks) > 8 {
		return connectorv1.Observation{
			Reason:  "ContractChecksInvalid",
			Message: "Select at most eight distinct outputs for contract checks.",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seen := make(map[string]bool)
	for _, check := range product.Spec.ContractChecks {
		if seen[check.Output] {
			return connectorv1.Observation{
				Reason:  "ContractChecksInvalid",
				Message: "Select each output only once for contract checks.",
			}
		}
		seen[check.Output] = true
		target := ""
		for _, output := range product.Spec.Outputs {
			if output.Name == check.Output {
				target = output.ContractURL
				break
			}
		}
		if target == "" {
			return connectorv1.Observation{
				Reason:  "ContractOutputNotFound",
				Message: "Select an existing output with a contract URL.",
			}
		}
		observer := &connectorv1.Deployment{Reader: r.ConnectorReader}
		observation := observer.ObserveContract(ctx, product.Namespace, check.ResourceRef, target)
		if !observation.Ready {
			observation.Message = fmt.Sprintf("Output %q: %s", check.Output, observation.Message)
			return observation
		}
	}
	return connectorv1.Observation{
		Ready:   true,
		Reason:  "ContractsReady",
		Message: "All selected contract probes match the current URLs and are available.",
	}
}
