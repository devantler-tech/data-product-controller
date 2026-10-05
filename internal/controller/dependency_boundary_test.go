package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type dependencyBoundaryClient struct {
	client.Client
	deniedNamespace string
	foreignReads    int
	failedKey       client.ObjectKey
	readError       error
}

func (c *dependencyBoundaryClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	opts ...client.GetOption,
) error {
	if key.Namespace == c.deniedNamespace {
		c.foreignReads++
	}
	if key == c.failedKey && c.readError != nil {
		return c.readError
	}
	return c.Client.Get(ctx, key, object, opts...)
}

func TestDependencyNamespaceBoundaryWithoutComposition(t *testing.T) {
	t.Parallel()
	for _, gate := range []string{"absent", "disabled", "enabled"} {
		for _, present := range []bool{false, true} {
			t.Run(
				gate+"/"+map[bool]string{false: "missing", true: "present"}[present],
				func(t *testing.T) {
					t.Parallel()
					consumer := testProduct("consumer")
					consumer.Spec.Inputs = []data.InputPort{
						{
							Name: "source",
							ProductRef: data.ProductReference{
								Name:      "producer",
								Namespace: "other-products",
								Output:    "query",
							},
						},
					}
					producer := testProduct("producer")
					producer.Namespace = "other-products"
					producer.Status.Conditions = []metav1.Condition{
						{
							Type:               data.ConditionReady,
							Status:             metav1.ConditionTrue,
							ObservedGeneration: producer.Generation,
							Reason:             "DependenciesReady",
						},
					}
					scheme := testScheme(t)
					builder := fake.NewClientBuilder().
						WithScheme(scheme).
						WithStatusSubresource(&data.DataProduct{}).
						WithObjects(consumer)
					if present {
						builder = builder.WithObjects(producer)
					}
					store := builder.Build()
					reader := &dependencyBoundaryClient{
						Client:          store,
						deniedNamespace: "other-products",
					}
					reconciler := &DataProductReconciler{Client: reader, Scheme: scheme}
					if gate != "absent" {
						reconciler.CompositionEnabled = func(context.Context) bool { return gate == "enabled" }
					}
					if _, err := reconciler.Reconcile(
						t.Context(),
						ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)},
					); err != nil {
						t.Fatal(err)
					}
					updated := &data.DataProduct{}
					if err := store.Get(
						t.Context(),
						client.ObjectKeyFromObject(consumer),
						updated,
					); err != nil {
						t.Fatal(err)
					}
					if reader.foreignReads != 0 {
						t.Fatalf(
							"foreign producer reads=%d, want no cross-namespace access",
							reader.foreignReads,
						)
					}
					condition := readyCondition(t, updated)
					if condition.Status != metav1.ConditionFalse ||
						condition.Reason != "CrossNamespaceDependencyDenied" {
						t.Fatalf(
							"foreign dependency readiness=%s/%s",
							condition.Status,
							condition.Reason,
						)
					}
				},
			)
		}
		t.Run(gate+"/same-namespace", func(t *testing.T) {
			t.Parallel()
			consumer, producer := testProduct("consumer"), testProduct("producer")
			consumer.Spec.Inputs = []data.InputPort{
				{
					Name: "source",
					ProductRef: data.ProductReference{
						Name:      producer.Name,
						Namespace: consumer.Namespace,
						Output:    "query",
					},
				},
			}
			producer.Status.Conditions = []metav1.Condition{
				{
					Type:               data.ConditionReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: producer.Generation,
					Reason:             "DependenciesReady",
				},
			}
			scheme := testScheme(t)
			store := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&data.DataProduct{}).
				WithObjects(consumer, producer).
				Build()
			reconciler := &DataProductReconciler{Client: store, Scheme: scheme}
			if gate != "absent" {
				reconciler.CompositionEnabled = func(context.Context) bool { return gate == "enabled" }
			}
			if _, err := reconciler.Reconcile(
				t.Context(),
				ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)},
			); err != nil {
				t.Fatal(err)
			}
			updated := &data.DataProduct{}
			if err := store.Get(
				t.Context(),
				client.ObjectKeyFromObject(consumer),
				updated,
			); err != nil {
				t.Fatal(err)
			}
			if readyCondition(t, updated).Status != metav1.ConditionTrue {
				t.Fatal("healthy authorized producer was rejected")
			}
		})
	}
}

func TestOrdinaryDependencyDeletionWithdrawsReadiness(t *testing.T) {
	t.Parallel()
	consumer, producer := testProduct("consumer"), testProduct("producer")
	consumer.Spec.Inputs = []data.InputPort{
		{Name: "source", ProductRef: data.ProductReference{Name: producer.Name, Output: "query"}},
	}
	producer.Status.Conditions = []metav1.Condition{
		{
			Type:               data.ConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: producer.Generation,
			Reason:             "DependenciesReady",
		},
	}
	deletion := metav1.Unix(1791145449, 0)
	producer.DeletionTimestamp = &deletion
	producer.Finalizers = []string{"example.test/retain"}
	scheme := testScheme(t)
	store := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&data.DataProduct{}).
		WithObjects(consumer, producer).
		Build()
	reconciler := &DataProductReconciler{Client: store, Scheme: scheme}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}
	if _, err := reconciler.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	updated := &data.DataProduct{}
	if err := store.Get(t.Context(), request.NamespacedName, updated); err != nil {
		t.Fatal(err)
	}
	if readyCondition(t, updated).Status != metav1.ConditionFalse {
		t.Fatal("deleting producer retained consumer readiness")
	}
	deleting := &data.DataProduct{}
	if err := store.Get(t.Context(), client.ObjectKeyFromObject(producer), deleting); err != nil {
		t.Fatal(err)
	}
	deleting.Finalizers = nil
	if err := store.Update(t.Context(), deleting); err != nil {
		t.Fatal(err)
	}
	replacement := testProduct("producer")
	if err := store.Create(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	replacement.Status.Conditions = []metav1.Condition{
		{
			Type:               data.ConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: replacement.Generation,
			Reason:             "DependenciesReady",
		},
	}
	if err := store.Status().Update(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), request.NamespacedName, updated); err != nil {
		t.Fatal(err)
	}
	if readyCondition(t, updated).Status != metav1.ConditionTrue {
		t.Fatal("consumer did not recover with the recreated ready producer")
	}
}

func TestPlainDependencyReadErrorsPersistRevocation(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"forbidden", "unavailable"} {
		for _, priorComposition := range []bool{false, true} {
			t.Run(
				failure+"/"+map[bool]string{false: "ordinary", true: "prior-composition"}[priorComposition],
				func(t *testing.T) {
					t.Parallel()
					consumer, producer := testProduct("consumer"), testProduct("producer")
					consumer.Spec.Inputs = []data.InputPort{
						{
							Name:       "source",
							ProductRef: data.ProductReference{Name: producer.Name, Output: "query"},
						},
					}
					consumer.Status.Conditions = []metav1.Condition{
						{
							Type:               data.ConditionReady,
							Status:             metav1.ConditionTrue,
							ObservedGeneration: consumer.Generation,
							Reason:             "DependenciesReady",
						},
					}
					if priorComposition {
						consumer.Status.Conditions = append(
							consumer.Status.Conditions,
							metav1.Condition{
								Type:               data.ConditionCompositionReady,
								Status:             metav1.ConditionTrue,
								ObservedGeneration: consumer.Generation,
								Reason:             "DependenciesReady",
							},
						)
						consumer.Status.Inputs = []data.InputStatus{
							{
								Name:               "source",
								ProductRef:         consumer.Spec.Inputs[0].ProductRef,
								ProductID:          producer.Spec.ID,
								ObservedGeneration: producer.Generation,
								Ready:              true,
								Reason:             "DependenciesReady",
							},
						}
					}
					producer.Status.Conditions = []metav1.Condition{
						{
							Type:               data.ConditionReady,
							Status:             metav1.ConditionTrue,
							ObservedGeneration: producer.Generation,
							Reason:             "DependenciesReady",
						},
					}
					scheme := testScheme(t)
					store := fake.NewClientBuilder().
						WithScheme(scheme).
						WithStatusSubresource(&data.DataProduct{}).
						WithObjects(consumer, producer).
						Build()
					readError := apierrors.NewServiceUnavailable("private diagnostic")
					if failure == "forbidden" {
						readError = apierrors.NewForbidden(
							schema.GroupResource{
								Group:    data.GroupVersion.Group,
								Resource: "dataproducts",
							},
							producer.Name,
							errors.New("private diagnostic"),
						)
					}
					reader := &dependencyBoundaryClient{
						Client:    store,
						failedKey: client.ObjectKeyFromObject(producer),
						readError: readError,
					}
					reconciler := &DataProductReconciler{
						Client:             reader,
						Scheme:             scheme,
						CompositionEnabled: func(context.Context) bool { return false },
					}
					request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}
					_, err := reconciler.Reconcile(t.Context(), request)
					if failure == "forbidden" && !apierrors.IsForbidden(err) ||
						failure == "unavailable" && !apierrors.IsServiceUnavailable(err) {
						t.Fatalf("typed dependency retry error was lost: %v", err)
					}
					updated := &data.DataProduct{}
					if err := store.Get(t.Context(), request.NamespacedName, updated); err != nil {
						t.Fatal(err)
					}
					condition := readyCondition(t, updated)
					if condition.Status != metav1.ConditionFalse ||
						condition.Reason != "DependencyUnavailable" {
						t.Fatalf(
							"failed producer read retained readiness=%s/%s",
							condition.Status,
							condition.Reason,
						)
					}
					if strings.Contains(condition.Message, "private diagnostic") {
						t.Fatal("private API diagnostic reached public status")
					}
					if len(updated.Status.Inputs) != 0 ||
						meta.FindStatusCondition(
							updated.Status.Conditions,
							data.ConditionCompositionReady,
						) != nil {
						t.Fatal("disabled composition retained its previous lineage or condition")
					}
					reader.readError = nil
					if _, err := reconciler.Reconcile(t.Context(), request); err != nil {
						t.Fatal(err)
					}
					if err := store.Get(t.Context(), request.NamespacedName, updated); err != nil {
						t.Fatal(err)
					}
					if readyCondition(t, updated).Status != metav1.ConditionTrue {
						t.Fatal("consumer did not recover after the producer read succeeded")
					}
				},
			)
		}
	}
}

func TestCanonicalProductNamesResolveAsOrdinaryDependencies(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"warehouse.daily", strings.Repeat("a", 40) + "." + strings.Repeat("b", 40)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			consumer, producer := testProduct("consumer"), testProduct(name)
			consumer.Spec.Inputs = []data.InputPort{
				{Name: "source", ProductRef: data.ProductReference{Name: name, Output: "query"}},
			}
			producer.Status.Conditions = []metav1.Condition{
				{
					Type:               data.ConditionReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: producer.Generation,
					Reason:             "DependenciesReady",
				},
			}
			scheme := testScheme(t)
			store := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&data.DataProduct{}).
				WithObjects(consumer, producer).
				Build()
			reconciler := &DataProductReconciler{Client: store, Scheme: scheme}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}
			if _, err := reconciler.Reconcile(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			updated := &data.DataProduct{}
			if err := store.Get(t.Context(), request.NamespacedName, updated); err != nil {
				t.Fatal(err)
			}
			if readyCondition(t, updated).Status != metav1.ConditionTrue {
				t.Fatal("canonical catalog producer could not be selected")
			}
		})
	}
}
