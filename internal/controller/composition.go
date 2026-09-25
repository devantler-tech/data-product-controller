package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxCompositionProducts = 256
	maxCompositionEdges    = 1024
	maxCompositionDepth    = 64
	maxLineageBytes        = 64 << 10
)

var stableContractVersion = regexp.MustCompile(
	`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`,
)

type compositionObservation struct {
	reason, message string
	err             error
}

// observeComposition refreshes lineage independently of source and workload readiness.
func (r *DataProductReconciler) observeComposition(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
) error {
	product.Status.Inputs = nil
	if len(product.Spec.Inputs) == 0 {
		meta.RemoveStatusCondition(
			&product.Status.Conditions,
			datav1alpha1.ConditionCompositionReady,
		)
		return nil
	}
	if r.CompositionEnabled == nil || !r.CompositionEnabled(ctx) {
		for _, input := range product.Spec.Inputs {
			if input.Contract != nil {
				setCompositionCondition(product, compositionObservation{
					reason:  "CompositionFeatureDisabled",
					message: "Enable composition to evaluate required input contracts.",
				})
				return nil
			}
		}
		meta.RemoveStatusCondition(
			&product.Status.Conditions,
			datav1alpha1.ConditionCompositionReady,
		)
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	graph := &compositionGraph{
		reader: r.Client,
		products: map[client.ObjectKey]*datav1alpha1.DataProduct{
			client.ObjectKeyFromObject(product): product,
		},
		visiting: map[client.ObjectKey]bool{},
		visited:  map[client.ObjectKey]bool{},
		heights:  map[client.ObjectKey]int{},
		missing:  map[client.ObjectKey]bool{},
	}
	observation := graph.visit(ctx, product, 0)
	// Read only the already observed snapshot. A failed/limited traversal must not
	// start a second unbounded walk to populate status.
	if len(product.Spec.Inputs) <= maxCompositionEdges {
		lineageBytes := 2 // JSON array delimiters.
		for _, input := range product.Spec.Inputs {
			ref := resolvedReference(product, input.ProductRef)
			status := datav1alpha1.InputStatus{
				Name:       input.Name,
				ProductRef: ref,
				Reason:     "DependencyUnobserved",
			}
			if producer := graph.products[client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}]; producer != nil {
				status.ProductID = producer.Spec.ID
				status.Version = producer.Spec.Version
				status.ObservedGeneration = producer.Generation
				status.Owner = &producer.Spec.Owner
				status.Output = findOutput(producer, ref.Output)
				status.Reason = inputCompatibility(input, producer)
				if status.Reason == "" && !producerReady(producer) {
					status.Reason = "DependencyNotReady"
				}
				status.Ready = status.Reason == ""
				if status.Ready {
					status.Reason = "InputReady"
				}
			} else if graph.missing[client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}] {
				status.Reason = "DependencyNotFound"
			}
			encoded, err := json.Marshal(status)
			if err != nil {
				observation = compositionObservation{
					reason:  "DependencyUnavailable",
					message: "Input lineage could not be encoded.",
					err:     err,
				}
				product.Status.Inputs = nil
				break
			}
			lineageBytes += len(encoded) + 1
			if lineageBytes > maxLineageBytes {
				observation = compositionObservation{
					reason:  "CompositionLimitExceeded",
					message: "Observed lineage exceeds 64 KiB; shorten producer metadata or split the inputs.",
				}
				product.Status.Inputs = nil
				break
			}
			product.Status.Inputs = append(product.Status.Inputs, status)
		}
		sort.Slice(
			product.Status.Inputs,
			func(i, j int) bool { return product.Status.Inputs[i].Name < product.Status.Inputs[j].Name },
		)
	}
	if observation.reason == "" {
		for _, input := range product.Status.Inputs {
			if !input.Ready {
				observation = compositionObservation{
					reason: input.Reason,
					message: fmt.Sprintf(
						"Input %q requires a ready producer with a compatible named output.",
						input.Name,
					),
				}
				break
			}
		}
	}
	setCompositionCondition(product, observation)
	return observation.err
}

type compositionGraph struct {
	reader            client.Reader
	products          map[client.ObjectKey]*datav1alpha1.DataProduct
	visiting, visited map[client.ObjectKey]bool
	heights           map[client.ObjectKey]int
	missing           map[client.ObjectKey]bool
	edges             int
}

// visit checks structure and declared contracts before trusting any producer's Ready status.
func (g *compositionGraph) visit(
	ctx context.Context,
	product *datav1alpha1.DataProduct,
	depth int,
) compositionObservation {
	if err := ctx.Err(); err != nil {
		return compositionObservation{
			reason:  "DependencyUnavailable",
			message: "Composition observation timed out; it will be retried.",
			err:     err,
		}
	}
	key := client.ObjectKeyFromObject(product)
	if g.visiting[key] {
		return compositionObservation{
			reason: "DependencyCycle",
			message: fmt.Sprintf(
				"A dependency cycle includes DataProduct %s; remove a circular input reference.",
				key,
			),
		}
	}
	if g.visited[key] {
		if depth+g.heights[key] >= maxCompositionDepth {
			return compositionLimit()
		}
		return compositionObservation{}
	}
	if depth >= maxCompositionDepth || len(product.Spec.Inputs) > maxCompositionEdges-g.edges {
		return compositionLimit()
	}
	g.edges += len(product.Spec.Inputs)
	g.visiting[key] = true
	defer delete(g.visiting, key)
	for _, input := range product.Spec.Inputs {
		ref := resolvedReference(product, input.ProductRef)
		producerKey := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
		producer := g.products[producerKey]
		if producer == nil {
			if len(g.products) >= maxCompositionProducts {
				return compositionLimit()
			}
			producer = &datav1alpha1.DataProduct{}
			if err := g.reader.Get(ctx, producerKey, producer); err != nil {
				if apierrors.IsNotFound(err) {
					g.missing[producerKey] = true
					return compositionObservation{
						reason: "DependencyNotFound",
						message: fmt.Sprintf(
							"Input %q on DataProduct %s references missing DataProduct %s.",
							input.Name,
							key,
							producerKey,
						),
					}
				}
				return compositionObservation{
					reason:  "DependencyUnavailable",
					message: "A dependency could not be observed; check Kubernetes API availability and controller access.",
					err:     err,
				}
			}
			g.products[producerKey] = producer
		}
		if observation := g.visit(ctx, producer, depth+1); observation.reason != "" {
			return observation
		}
		g.heights[key] = max(g.heights[key], 1+g.heights[producerKey])
		if reason := inputCompatibility(input, producer); reason != "" {
			return compositionObservation{
				reason: reason,
				message: fmt.Sprintf(
					"Input %q on DataProduct %s does not match output %q on %s; check its protocol and stable contract version.",
					input.Name,
					key,
					ref.Output,
					producerKey,
				),
			}
		}
	}
	g.visited[key] = true
	return compositionObservation{}
}

func compositionLimit() compositionObservation {
	return compositionObservation{
		reason:  "CompositionLimitExceeded",
		message: "Composition exceeds 256 products, 1024 inputs, or 64 levels; split the dependency graph.",
	}
}

func resolvedReference(
	product *datav1alpha1.DataProduct,
	ref datav1alpha1.ProductReference,
) datav1alpha1.ProductReference {
	if ref.Namespace == "" {
		ref.Namespace = product.Namespace
	}
	return ref
}

func findOutput(product *datav1alpha1.DataProduct, name string) *datav1alpha1.OutputPort {
	for i := range product.Spec.Outputs {
		if product.Spec.Outputs[i].Name == name {
			return &product.Spec.Outputs[i]
		}
	}
	return nil
}

func inputCompatibility(input datav1alpha1.InputPort, producer *datav1alpha1.DataProduct) string {
	output := findOutput(producer, input.ProductRef.Output)
	if output == nil {
		return "OutputNotFound"
	}
	if input.Contract == nil {
		return ""
	}
	if output.Protocol != input.Contract.Protocol ||
		!compatibleVersion(producer.Spec.Version, input.Contract.MinimumVersion) {
		return "ContractIncompatible"
	}
	return ""
}

func compatibleVersion(actual, minimum string) bool {
	if !stableContractVersion.MatchString(actual) || !stableContractVersion.MatchString(minimum) {
		return false
	}
	a, err := version.ParseSemantic(actual)
	if err != nil {
		return false
	}
	m, err := version.ParseSemantic(minimum)
	if err != nil || a.Major() != m.Major() {
		return false
	}
	if a.Major() == 0 {
		return actual == minimum
	}
	return a.AtLeast(m)
}

func producerReady(product *datav1alpha1.DataProduct) bool {
	condition := meta.FindStatusCondition(product.Status.Conditions, datav1alpha1.ConditionReady)
	return product.DeletionTimestamp.IsZero() && condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == product.Generation
}

func setCompositionCondition(
	product *datav1alpha1.DataProduct,
	observation compositionObservation,
) {
	status := metav1.ConditionFalse
	if observation.reason == "" {
		status = metav1.ConditionTrue
		observation.reason = "CompositionVerified"
		observation.message = "The dependency graph is acyclic and all inputs satisfy their declared contracts."
	}
	meta.SetStatusCondition(&product.Status.Conditions, metav1.Condition{
		Type:               datav1alpha1.ConditionCompositionReady,
		Status:             status,
		ObservedGeneration: product.Generation,
		Reason:             observation.reason,
		Message:            observation.message,
	})
}
