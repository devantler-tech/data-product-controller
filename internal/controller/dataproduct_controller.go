// Package controller reconciles data-product desired state.
package controller

import (
	"context"
	"fmt"
	"sort"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// DataProductReconciler reports whether a product's composed inputs are ready.
type DataProductReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *DataProductReconciler) requestsForDependency(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	producer, ok := object.(*datav1alpha1.DataProduct)
	if !ok {
		return nil
	}

	products := &datav1alpha1.DataProductList{}
	if err := r.List(ctx, products); err != nil {
		logf.FromContext(ctx).Error(err, "list data products while mapping dependency event")

		return nil
	}

	requests := make([]reconcile.Request, 0)
	for index := range products.Items {
		consumer := &products.Items[index]
		for _, input := range consumer.Spec.Inputs {
			namespace := input.ProductRef.Namespace
			if namespace == "" {
				namespace = consumer.Namespace
			}

			if input.ProductRef.Name == producer.Name && namespace == producer.Namespace {
				requests = append(
					requests,
					reconcile.Request{NamespacedName: client.ObjectKeyFromObject(consumer)},
				)

				break
			}
		}
	}

	sort.Slice(requests, func(left, right int) bool {
		return requests[left].NamespacedName.String() < requests[right].NamespacedName.String()
	})

	return requests
}

// SetupWithManager registers the product and dependency event watches.
func (r *DataProductReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&datav1alpha1.DataProduct{}).
		Watches(&datav1alpha1.DataProduct{}, handler.EnqueueRequestsFromMapFunc(r.requestsForDependency)).
		Named("data-product").
		Complete(r)
}

// +kubebuilder:rbac:groups=data.devantler.tech,resources=dataproducts,verbs=get;list;watch
// +kubebuilder:rbac:groups=data.devantler.tech,resources=dataproducts/status,verbs=get;update;patch

// Reconcile evaluates one DataProduct.
func (r *DataProductReconciler) Reconcile(
	ctx context.Context,
	request ctrl.Request,
) (ctrl.Result, error) {
	product := &datav1alpha1.DataProduct{}
	if err := r.Get(ctx, request.NamespacedName, product); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}
	previousStatus := product.DeepCopy().Status

	for _, input := range product.Spec.Inputs {
		namespace := input.ProductRef.Namespace
		if namespace == "" {
			namespace = product.Namespace
		}

		dependency := &datav1alpha1.DataProduct{}
		dependencyKey := client.ObjectKey{Name: input.ProductRef.Name, Namespace: namespace}
		if err := r.Get(ctx, dependencyKey, dependency); err != nil {
			if apierrors.IsNotFound(err) {
				setReadiness(
					product,
					metav1.ConditionFalse,
					"DependencyNotFound",
					fmt.Sprintf(
						"Input %q references missing DataProduct %s/%s.",
						input.Name,
						namespace,
						input.ProductRef.Name,
					),
				)

				return ctrl.Result{}, r.updateStatusIfChanged(ctx, product, previousStatus)
			}

			return ctrl.Result{}, err
		}

		readyCondition := meta.FindStatusCondition(
			dependency.Status.Conditions,
			datav1alpha1.ConditionReady,
		)
		if readyCondition == nil ||
			readyCondition.Status != metav1.ConditionTrue ||
			readyCondition.ObservedGeneration != dependency.Generation {
			setReadiness(
				product,
				metav1.ConditionFalse,
				"DependencyNotReady",
				fmt.Sprintf(
					"Input %q references DataProduct %s/%s, which is not Ready.",
					input.Name,
					namespace,
					input.ProductRef.Name,
				),
			)

			return ctrl.Result{}, r.updateStatusIfChanged(ctx, product, previousStatus)
		}

		if !hasOutput(dependency, input.ProductRef.Output) {
			setReadiness(
				product,
				metav1.ConditionFalse,
				"OutputNotFound",
				fmt.Sprintf(
					"Input %q references missing output %q on DataProduct %s/%s.",
					input.Name,
					input.ProductRef.Output,
					namespace,
					input.ProductRef.Name,
				),
			)

			return ctrl.Result{}, r.updateStatusIfChanged(ctx, product, previousStatus)
		}
	}

	setReadiness(
		product,
		metav1.ConditionTrue,
		"DependenciesReady",
		"All referenced data products and output ports are ready.",
	)

	if err := r.updateStatusIfChanged(ctx, product, previousStatus); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DataProductReconciler) updateStatusIfChanged(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
	previous datav1alpha1.DataProductStatus,
) error {
	if apiequality.Semantic.DeepEqual(previous, product.Status) {
		return nil
	}

	return r.Status().Update(ctx, product)
}

func hasOutput(product *datav1alpha1.DataProduct, outputName string) bool {
	for _, output := range product.Spec.Outputs {
		if output.Name == outputName {
			return true
		}
	}

	return false
}

func setReadiness(
	product *datav1alpha1.DataProduct,
	status metav1.ConditionStatus,
	reason, message string,
) {
	product.Status.ObservedGeneration = product.Generation
	meta.SetStatusCondition(&product.Status.Conditions, metav1.Condition{
		Type:               datav1alpha1.ConditionReady,
		Status:             status,
		ObservedGeneration: product.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
}
