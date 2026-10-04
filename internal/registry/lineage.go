package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxTraceProducts = 256
	maxTraceEdges    = 1024
	maxTraceDepth    = 64
	maxTraceMetadata = 1 << 20
)

type lineageTrace struct {
	APIVersion string        `json:"apiVersion"`
	Root       string        `json:"root"`
	Complete   bool          `json:"complete"`
	Issues     []string      `json:"issues"`
	Nodes      []lineageNode `json:"nodes"`
	Edges      []lineageEdge `json:"edges"`
}

type lineageNode struct {
	Key                string                     `json:"key"`
	State              string                     `json:"state"`
	ID                 string                     `json:"id,omitempty"`
	DisplayName        string                     `json:"displayName,omitempty"`
	Version            string                     `json:"version,omitempty"`
	Owner              *datav1alpha1.ProductOwner `json:"owner,omitempty"`
	Generation         int64                      `json:"generation"`
	ObservedGeneration int64                      `json:"observedGeneration"`
	Health             map[string]healthDimension `json:"health,omitempty"`
}

type lineageEdge struct {
	From          string                      `json:"from"`
	To            string                      `json:"to"`
	Input         string                      `json:"input"`
	Output        string                      `json:"output"`
	Depth         int                         `json:"depth"`
	State         string                      `json:"state"`
	Compatibility string                      `json:"compatibility"`
	Requirement   *datav1alpha1.InputContract `json:"requirement,omitempty"`
}

type lineageWalk struct {
	reader            client.Reader
	compatibility     func(datav1alpha1.InputPort, *datav1alpha1.DataProduct) string
	trace             lineageTrace
	products          map[string]*datav1alpha1.DataProduct
	nodes             map[string]lineageNode
	visiting, visited map[string]bool
	heights           map[string]int
	issues            map[string]bool
	retained          int
}

// lineageAvailable requires both release grants and the canonical compatibility evaluator.
func (s *server) lineageAvailable(ctx context.Context) bool {
	return s.discoveryEnabled != nil && s.discoveryEnabled(ctx) && s.lineageEnabled != nil &&
		s.lineageEnabled(ctx) &&
		s.inputCompatibility != nil
}

// productLineage observes declared metadata only; references never authorize external reads.
func (s *server) productLineage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.lineageAvailable(r.Context()) {
		http.NotFound(w, r)
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) != 0 || r.URL.RawQuery != "" ||
		!validPublicLabel(r.PathValue("namespace")) || !validProductName(r.PathValue("name")) {
		discoveryFailure(
			w,
			http.StatusBadRequest,
			"invalid-trace",
			"Use an exact namespace and product name without a body or query.",
		)
		return
	}
	select {
	case s.lineageSlot <- struct{}{}:
		defer func() { <-s.lineageSlot }()
	default:
		discoveryFailure(
			w,
			http.StatusTooManyRequests,
			"trace-busy",
			"A trace is in progress. Try again.",
		)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	key := client.ObjectKey{Namespace: r.PathValue("namespace"), Name: r.PathValue("name")}
	walk := lineageWalk{
		reader:        s.reader,
		compatibility: s.inputCompatibility,
		trace: lineageTrace{
			APIVersion: "data-product-lineage/v1",
			Root:       key.String(),
			Complete:   true,
			Issues:     []string{},
			Nodes:      []lineageNode{},
			Edges:      []lineageEdge{},
		},
		products: map[string]*datav1alpha1.DataProduct{},
		nodes:    map[string]lineageNode{},
		visiting: map[string]bool{},
		visited:  map[string]bool{},
		heights:  map[string]int{},
		issues:   map[string]bool{},
	}
	root, state := walk.read(ctx, key)
	if root == nil {
		code := http.StatusBadGateway
		if state == "missing" {
			code = http.StatusNotFound
		}
		if state == "invalid" {
			code = http.StatusUnprocessableEntity
		}
		discoveryFailure(
			w,
			code,
			"trace-root-unavailable",
			"The selected product cannot be traced. Refresh its descriptor or ask its owner to correct public metadata.",
		)
		return
	}
	walk.visit(ctx, root, 0)
	for _, node := range walk.nodes {
		walk.trace.Nodes = append(walk.trace.Nodes, node)
	}
	for issue := range walk.issues {
		walk.trace.Issues = append(walk.trace.Issues, issue)
	}
	sort.Strings(walk.trace.Issues)
	sort.Slice(
		walk.trace.Nodes,
		func(i, j int) bool { return walk.trace.Nodes[i].Key < walk.trace.Nodes[j].Key },
	)
	sort.Slice(walk.trace.Edges, func(i, j int) bool {
		a, b := walk.trace.Edges[i], walk.trace.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.Input < b.Input
	})
	data, err := json.Marshal(walk.trace)
	if err != nil || len(data) > maxDiscoveryResponseBytes {
		discoveryFailure(
			w,
			http.StatusRequestEntityTooLarge,
			"trace-too-large",
			"The trace exceeds the response bound. Split the product inputs.",
		)
		return
	}
	writeDiscoveryJSON(w, data)
}

func (g *lineageWalk) incomplete(
	reason string,
) {
	g.trace.Complete = false
	g.issues[reason] = true
}

// read caches failures as well as public projections; every unique attempt consumes the same budget.
func (g *lineageWalk) read(
	ctx context.Context,
	key client.ObjectKey,
) (*datav1alpha1.DataProduct, string) {
	id := key.String()
	if node, ok := g.nodes[id]; ok {
		return g.products[id], node.State
	}
	if ctx.Err() != nil {
		g.incomplete("timeout")
		return nil, "timeout"
	}
	if len(g.nodes) >= maxTraceProducts {
		g.incomplete("product-limit")
		return nil, "product-limit"
	}
	node := lineageNode{Key: id, State: "unavailable"}
	p := &datav1alpha1.DataProduct{}
	err := g.reader.Get(ctx, key, p)
	switch {
	case ctx.Err() != nil:
		node.State = "timeout"
	case apierrors.IsNotFound(err):
		node.State = "missing"
	case err != nil:
	case p.Namespace != key.Namespace || p.Name != key.Name:
		node.State = "identity-mismatch"
	default:
		// Historical lineage is not part of the trace and cannot select a destination.
		p.Status.Inputs = nil
		data, encodeErr := encodePortableDescriptor(p)
		if encodeErr != nil || !distinctTracePorts(p) {
			node.State = "invalid"
			break
		}
		if len(data) > maxTraceMetadata-g.retained {
			node.State = "metadata-limit"
			break
		}
		g.retained += len(data)
		d := portableDescriptorFor(p)
		node = lineageNode{
			Key:                id,
			State:              d.Readiness.Reason,
			ID:                 d.ID,
			DisplayName:        d.DisplayName,
			Version:            d.Version,
			Owner:              &d.Owner,
			Generation:         d.Generation,
			ObservedGeneration: d.ObservedGeneration,
			Health:             d.Health,
		}
		if !p.DeletionTimestamp.IsZero() {
			node.State = "deleting"
		}
		// Retain only the public declarations needed by traversal and compatibility.
		g.products[id] = &datav1alpha1.DataProduct{
			ObjectMeta: metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name},
			Spec: datav1alpha1.DataProductSpec{
				Version: p.Spec.Version,
				Inputs:  p.Spec.Inputs,
				Outputs: p.Spec.Outputs,
			},
		}
	}
	g.nodes[id] = node
	if g.products[id] == nil {
		g.incomplete(node.State)
	}
	return g.products[id], node.State
}

func distinctTracePorts(p *datav1alpha1.DataProduct) bool {
	names := map[string]bool{}
	for _, input := range p.Spec.Inputs {
		if names[input.Name] {
			return false
		}
		names[input.Name] = true
	}
	names = map[string]bool{}
	for _, output := range p.Spec.Outputs {
		if names[output.Name] {
			return false
		}
		names[output.Name] = true
	}
	return true
}

// visit evaluates every inspected edge independently of producer readiness.
func (g *lineageWalk) visit(ctx context.Context, p *datav1alpha1.DataProduct, depth int) string {
	key := p.Namespace + "/" + p.Name
	if ctx.Err() != nil {
		g.incomplete("timeout")
		return "timeout"
	}
	if g.visiting[key] {
		g.incomplete("cycle")
		return "cycle"
	}
	if depth >= maxTraceDepth || (g.visited[key] && depth+g.heights[key] >= maxTraceDepth) {
		g.incomplete("depth-limit")
		return "depth-limit"
	}
	if g.visited[key] {
		return "resolved"
	}
	g.visiting[key] = true
	defer delete(g.visiting, key)
	inputs := append([]datav1alpha1.InputPort(nil), p.Spec.Inputs...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	for _, input := range inputs {
		if len(g.trace.Edges) >= maxTraceEdges {
			g.incomplete("edge-limit")
			break
		}
		if ctx.Err() != nil {
			g.incomplete("timeout")
			break
		}
		ref := input.ProductRef
		if ref.Namespace == "" {
			ref.Namespace = p.Namespace
		}
		edge := lineageEdge{
			From:          key,
			To:            ref.Namespace + "/" + ref.Name,
			Input:         input.Name,
			Output:        ref.Output,
			Depth:         depth + 1,
			State:         "resolved",
			Compatibility: "not-evaluated",
			Requirement:   input.Contract,
		}
		index := len(g.trace.Edges)
		g.trace.Edges = append(g.trace.Edges, edge)
		switch {
		case ref.Namespace != p.Namespace:
			edge.State = "cross-namespace"
			g.incomplete(edge.State)
		case depth+1 >= maxTraceDepth:
			edge.State = "depth-limit"
			g.incomplete(edge.State)
		default:
			producer, state := g.read(
				ctx,
				client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name},
			)
			if producer == nil {
				edge.State = state
			} else {
				switch g.compatibility(input, producer) {
				case "":
					edge.Compatibility = "compatible"
				case "OutputNotFound":
					edge.Compatibility = "output-missing"
				case "ContractIncompatible":
					edge.Compatibility = "contract-incompatible"
				default:
					edge.State = "unavailable"
					g.incomplete(edge.State)
				}
				if edge.State == "resolved" {
					edge.State = g.visit(ctx, producer, depth+1)
				}
				g.heights[key] = max(g.heights[key], 1+g.heights[edge.To])
			}
		}
		g.trace.Edges[index] = edge
	}
	g.visited[key] = true
	return "resolved"
}
