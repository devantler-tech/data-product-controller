// Package controller reconciles data-product desired state.
package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	connectorv1 "github.com/devantler-tech/data-product-controller/internal/connector/v1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
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
	// SourceReader bypasses the cache for external resources and Secret metadata.
	SourceReader client.Reader
	// SourcesEnabled evaluates the default-off provisioned-sources release flag.
	SourcesEnabled func(context.Context) bool
	// ConnectorReader performs uncached, scoped workload reads.
	ConnectorReader client.Reader
	// ConnectorsEnabled evaluates the default-off connector-readiness release flag.
	ConnectorsEnabled func(context.Context) bool
	// ContractsEnabled evaluates the default-off contract-readiness release flag.
	ContractsEnabled func(context.Context) bool
	// CompositionEnabled evaluates the default-off graph and compatibility release flag.
	CompositionEnabled func(context.Context) bool
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

	consumers := make(map[client.ObjectKey][]client.ObjectKey)
	for index := range products.Items {
		consumer := &products.Items[index]
		for _, input := range consumer.Spec.Inputs {
			ref := resolvedReference(consumer, input.ProductRef)
			key := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
			consumers[key] = append(consumers[key], client.ObjectKeyFromObject(consumer))
		}
	}
	transitive := r.CompositionEnabled != nil && r.CompositionEnabled(ctx)
	queue := []client.ObjectKey{client.ObjectKeyFromObject(producer)}
	seen := make(map[client.ObjectKey]bool)
	requests := make([]reconcile.Request, 0)
	for len(queue) != 0 {
		key := queue[0]
		queue = queue[1:]
		for _, consumer := range consumers[key] {
			if seen[consumer] {
				continue
			}
			seen[consumer] = true
			requests = append(requests, reconcile.Request{NamespacedName: consumer})
			if transitive {
				queue = append(queue, consumer)
			}
		}
	}

	sort.Slice(requests, func(left, right int) bool {
		return requests[left].String() < requests[right].String()
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
	result := ctrl.Result{}
	r.observeConnector(ctx, product)
	r.observeContracts(ctx, product)
	compositionError := r.observeComposition(ctx, product)
	composition := meta.FindStatusCondition(
		product.Status.Conditions,
		datav1alpha1.ConditionCompositionReady,
	)
	if composition != nil {
		result.RequeueAfter = 30 * time.Second
	}
	if compositionError != nil {
		setReadiness(product, metav1.ConditionFalse, composition.Reason, composition.Message)
		return result, errors.Join(
			compositionError,
			r.updateStatusIfChanged(ctx, product, previousStatus),
		)
	}
	if product.Spec.Connector != nil || len(product.Spec.ContractChecks) != 0 {
		result.RequeueAfter = 30 * time.Second
	}
	if product.Spec.Source != nil {
		result.RequeueAfter = 30 * time.Second
		observation := provisionerv1.Observation{
			Reason:  "SourceFeatureDisabled",
			Message: "Enable the provisioned-sources feature to observe this product's source.",
		}
		if r.SourcesEnabled != nil && r.SourcesEnabled(ctx) {
			observer := &provisionerv1.Crossplane{Reader: r.SourceReader, Mapper: r.RESTMapper()}
			observation = observer.Observe(ctx, product.Namespace, *product.Spec.Source)
		}
		if !observation.Ready {
			setReadiness(product, metav1.ConditionFalse, observation.Reason, observation.Message)
			return result, r.updateStatusIfChanged(ctx, product, previousStatus)
		}
	}

	inputs := product.Spec.Inputs
	if composition != nil {
		if composition.Status != metav1.ConditionTrue {
			setReadiness(product, metav1.ConditionFalse, composition.Reason, composition.Message)
			return result, r.updateStatusIfChanged(ctx, product, previousStatus)
		}
		inputs = nil
	}
	for _, input := range inputs {
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

				return result, r.updateStatusIfChanged(ctx, product, previousStatus)
			}

			if len(product.Spec.ContractChecks) != 0 ||
				meta.FindStatusCondition(
					previousStatus.Conditions,
					datav1alpha1.ConditionContractsReady,
				) != nil ||
				product.Spec.Connector != nil ||
				meta.FindStatusCondition(
					previousStatus.Conditions,
					datav1alpha1.ConditionConnectorReady,
				) != nil {
				setReadiness(
					product,
					metav1.ConditionFalse,
					"DependencyUnavailable",
					"A referenced data product could not be observed; check Kubernetes API availability and controller access.",
				)
				return result, errors.Join(
					err,
					r.updateStatusIfChanged(ctx, product, previousStatus),
				)
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

			return result, r.updateStatusIfChanged(ctx, product, previousStatus)
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

			return result, r.updateStatusIfChanged(ctx, product, previousStatus)
		}
	}

	if connector := meta.FindStatusCondition(
		product.Status.Conditions,
		datav1alpha1.ConditionConnectorReady,
	); connector != nil &&
		connector.Status != metav1.ConditionTrue {
		setReadiness(product, metav1.ConditionFalse, connector.Reason, connector.Message)
		return result, r.updateStatusIfChanged(ctx, product, previousStatus)
	}

	if contracts := meta.FindStatusCondition(
		product.Status.Conditions,
		datav1alpha1.ConditionContractsReady,
	); contracts != nil &&
		contracts.Status != metav1.ConditionTrue {
		setReadiness(product, metav1.ConditionFalse, contracts.Reason, contracts.Message)
		return result, r.updateStatusIfChanged(ctx, product, previousStatus)
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

	return result, nil
}

// observeConnector refreshes its independent condition even when other capabilities block aggregate readiness.
func (r *DataProductReconciler) observeConnector(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
) {
	if product.Spec.Connector == nil {
		meta.RemoveStatusCondition(&product.Status.Conditions, datav1alpha1.ConditionConnectorReady)
		return
	}
	observation := connectorv1.Observation{
		Reason:  "ConnectorFeatureDisabled",
		Message: "Enable connector-readiness to observe this product's connector Deployment.",
	}
	if r.ConnectorsEnabled != nil && r.ConnectorsEnabled(ctx) {
		observer := &connectorv1.Deployment{Reader: r.ConnectorReader}
		observation = observer.Observe(ctx, product.Namespace, *product.Spec.Connector)
	}
	status := metav1.ConditionFalse
	if observation.Ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&product.Status.Conditions, metav1.Condition{
		Type:               datav1alpha1.ConditionConnectorReady,
		Status:             status,
		ObservedGeneration: product.Generation,
		Reason:             observation.Reason,
		Message:            observation.Message,
	})
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
