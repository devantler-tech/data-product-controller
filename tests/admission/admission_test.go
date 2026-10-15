//go:build admission

// Package admission_test exercises a test-only migration policy on an owned API server.
package admission_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/config/crd"
	"github.com/devantler-tech/data-product-controller/internal/preflight"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"
)

const policyMessage = "API-only products do not support spec.ui; remove it"

type fixture struct {
	t        *testing.T
	ctx      context.Context
	products dynamic.ResourceInterface
	crds     *extclient.Clientset
	original *extv1.CustomResourceDefinition
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	logf.SetLogger(zap.New())
	assets, version := os.Getenv("ADMISSION_TEST_ASSETS"), os.Getenv("ADMISSION_TEST_VERSION")
	if !filepath.IsAbs(assets) || (version != "v1.34.0" && version != "v1.37.0") {
		t.Fatal(
			"explicit absolute ADMISSION_TEST_ASSETS and supported ADMISSION_TEST_VERSION required",
		)
	}
	// envtest must never honor ambient cluster or binary overrides.
	t.Setenv("KUBEBUILDER_ASSETS", assets)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "must-not-be-used"))
	root, err := os.OpenRoot(assets)
	check(t, err)
	t.Cleanup(func() { check(t, root.Close()) })
	for _, name := range []string{"kube-apiserver", "etcd", "kubectl"} {
		path := filepath.Join(assets, name)
		info, err := root.Lstat(name)
		check(t, err)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("invalid owned binary %s", name)
		}
		t.Setenv("TEST_ASSET_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_")), path)
	}
	var original extv1.CustomResourceDefinition
	check(t, yaml.Unmarshal(crd.DataProductSchema(), &original))
	useExisting := false
	server := &envtest.Environment{
		UseExistingCluster: &useExisting, BinaryAssetsDirectory: assets,
		CRDs: []*extv1.CustomResourceDefinition{original.DeepCopy()},
		// Five retries can consume 5*(40+40+5)s; the 600s alarm also reserves recovery time.
		ControlPlaneStartTimeout: 40 * time.Second, ControlPlaneStopTimeout: 5 * time.Second,
	}
	cfg, err := server.Start()
	if err != nil {
		_ = server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() { check(t, server.Stop()) })
	cfg.Timeout = 5 * time.Second
	kc, err := kubernetes.NewForConfig(cfg)
	check(t, err)
	actual, err := kc.Discovery().ServerVersion()
	check(t, err)
	if actual.GitVersion != version {
		t.Fatalf("server version %s, want %s", actual.GitVersion, version)
	}
	deadline := time.Now().Add(150 * time.Second)
	if alarm, ok := t.Deadline(); ok && alarm.Add(-25*time.Second).Before(deadline) {
		deadline = alarm.Add(-25 * time.Second)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	_, err = kc.CoreV1().
		Namespaces().
		Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "admission-products"}}, metav1.CreateOptions{})
	check(t, err)
	dc, err := dynamic.NewForConfig(cfg)
	check(t, err)
	ec, err := extclient.NewForConfig(cfg)
	check(t, err)
	installed, err := ec.ApiextensionsV1().
		CustomResourceDefinitions().
		Get(ctx, original.Name, metav1.GetOptions{})
	check(t, err)
	f := &fixture{
		t:        t,
		ctx:      ctx,
		crds:     ec,
		original: installed,
		products: dc.Resource(schema.GroupVersionResource{Group: "data.devantler.tech", Version: "v1alpha1", Resource: "dataproducts"}).
			Namespace("admission-products"),
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		recovery := *f
		recovery.ctx = ctx
		recovery.schema(installed.Spec)
	})
	t.Logf(
		"owned API server=%s delivered-schema-sha256=%x",
		actual.GitVersion,
		sha256.Sum256(crd.DataProductSchema()),
	)
	return f
}

func (f *fixture) with(
	t *testing.T,
) *fixture {
	t.Helper()
	child := *f
	child.t = t
	return &child
}

func set(t *testing.T, p *unstructured.Unstructured, value any, path ...string) {
	t.Helper()
	check(t, unstructured.SetNestedField(p.Object, value, path...))
}

func product(name string, ui bool) *unstructured.Unstructured {
	p := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "data.devantler.tech/v1alpha1",
		"kind":       "DataProduct",
		"metadata": map[string]any{
			"name":        name,
			"namespace":   "admission-products",
			"labels":      map[string]any{"fixture": "admission"},
			"annotations": map[string]any{"example.com/public": "synthetic"},
		},
		"spec": map[string]any{
			"id":          "urn:example:" + name,
			"name":        "Admission fixture",
			"description": "Synthetic migration acceptance",
			"version":     "v1.0.0",
			"owner":       map[string]any{"name": "Fixture team"},
			"outputs": []any{
				map[string]any{
					"name":        "query",
					"protocol":    "OpenAPI",
					"url":         "https://data.example.com/query",
					"contractUrl": "https://data.example.com/openapi.json",
				},
			},
		},
	}}
	if ui {
		spec, ok := p.Object["spec"].(map[string]any)
		if !ok {
			panic("fixture specification is not an object")
		}
		spec["ui"] = map[string]any{
			"url":   "https://data.example.com/ui",
			"title": "Legacy fixture",
		}
	}
	return p
}

func (f *fixture) get(name string) *unstructured.Unstructured {
	f.t.Helper()
	p, err := f.products.Get(f.ctx, name, metav1.GetOptions{})
	check(f.t, err)
	return p
}

func (f *fixture) create(p *unstructured.Unstructured) *unstructured.Unstructured {
	f.t.Helper()
	_, err := f.products.Create(f.ctx, p, metav1.CreateOptions{FieldValidation: "Ignore"})
	check(f.t, err)
	stored := f.get(p.GetName())
	if stored.GetUID() == "" {
		f.t.Fatal("created product has no UID")
	}
	want := p.DeepCopy()
	want.SetUID(stored.GetUID())
	// Null is normalized to absence by this schema; assert that on the persisted GET.
	if value, found, err := unstructured.NestedFieldNoCopy(want.Object, "spec", "ui"); err != nil {
		f.t.Fatal(err)
	} else if found && value == nil {
		unstructured.RemoveNestedField(want.Object, "spec", "ui")
	}
	f.assertStored(want)
	return stored
}

func (f *fixture) apply(p *unstructured.Unstructured, manager string) error {
	f.t.Helper()
	data, err := json.Marshal(p.Object)
	check(f.t, err)
	force := true
	_, err = f.products.Patch(
		f.ctx,
		p.GetName(),
		types.ApplyPatchType,
		data,
		metav1.PatchOptions{FieldManager: manager, Force: &force, FieldValidation: "Ignore"},
	)
	return err
}

func (f *fixture) seedStatus(name string) {
	f.t.Helper()
	want := f.get(name)
	want.Object["status"] = map[string]any{"observedGeneration": want.GetGeneration()}
	_, err := f.products.UpdateStatus(f.ctx, want, metav1.UpdateOptions{FieldValidation: "Ignore"})
	check(f.t, err)
	f.assertStored(want)
}

func rejected(err error, field, message string, causeType ...metav1.CauseType) bool {
	if !apierrors.IsInvalid(err) {
		return false
	}
	var status apierrors.APIStatus
	if !errors.As(err, &status) || status.Status().Details == nil {
		return false
	}
	wantedType := metav1.CauseTypeFieldValueInvalid
	if len(causeType) == 1 {
		wantedType = causeType[0]
	}
	for _, cause := range status.Status().Details.Causes {
		if cause.Type == wantedType && cause.Field == field &&
			strings.Contains(cause.Message, message) {
			return true
		}
	}
	return false
}

func (f *fixture) deny(
	name string,
	action func() error,
	message string,
	causeType ...metav1.CauseType,
) {
	f.t.Helper()
	before, err := f.products.Get(f.ctx, name, metav1.GetOptions{})
	absent := apierrors.IsNotFound(err)
	if err != nil && !apierrors.IsNotFound(err) {
		f.t.Fatal(err)
	}
	if err := action(); !rejected(err, "spec.ui", message, causeType...) {
		f.t.Fatalf("wanted cause-specific UI rejection, got %v", err)
	}
	after, err := f.products.Get(f.ctx, name, metav1.GetOptions{})
	if absent {
		if !apierrors.IsNotFound(err) {
			f.t.Fatalf("rejected create persisted or read failed: %v", err)
		}
	} else {
		check(f.t, err)
		if !reflect.DeepEqual(before.Object, after.Object) {
			f.t.Fatal("rejected request changed persisted object")
		}
	}
}

// assertStored compares identity and all authored fields; server-owned versions are allowed to advance.
func (f *fixture) assertStored(want *unstructured.Unstructured) {
	f.t.Helper()
	got := f.get(want.GetName())
	view := func(p *unstructured.Unstructured) any {
		return []any{
			p.GetUID(),
			p.GetName(),
			p.GetNamespace(),
			p.GetLabels(),
			p.GetAnnotations(),
			p.Object["spec"],
			p.Object["status"],
		}
	}
	if !reflect.DeepEqual(view(want), view(got)) {
		f.t.Fatalf("persisted identity, metadata, spec or status differs: %s", want.GetName())
	}
}

func (f *fixture) uiOwners(name string, wanted ...string) {
	f.t.Helper()
	actual := map[string]bool{}
	for _, entry := range f.get(name).GetManagedFields() {
		if entry.FieldsV1 == nil {
			continue
		}
		var fields map[string]any
		check(f.t, json.Unmarshal(entry.FieldsV1.GetRawBytes(), &fields))
		if spec, ok := fields["f:spec"].(map[string]any); ok {
			if _, owns := spec["f:ui"]; owns {
				actual[entry.Manager] = true
			}
		}
	}
	expected := map[string]bool{}
	for _, manager := range wanted {
		expected[manager] = true
	}
	if !reflect.DeepEqual(actual, expected) {
		f.t.Fatalf("UI ownership for %s: %v, want %v", name, actual, expected)
	}
}

func withoutUI(p *unstructured.Unstructured) *unstructured.Unstructured {
	result := p.DeepCopy()
	unstructured.RemoveNestedField(result.Object, "spec", "ui")
	return result
}

func (f *fixture) schema(spec extv1.CustomResourceDefinitionSpec) {
	f.t.Helper()
	current, err := f.crds.ApiextensionsV1().
		CustomResourceDefinitions().
		Get(f.ctx, f.original.Name, metav1.GetOptions{})
	check(f.t, err)
	current.Spec = spec
	_, err = f.crds.ApiextensionsV1().
		CustomResourceDefinitions().
		Update(f.ctx, current, metav1.UpdateOptions{})
	check(f.t, err)
	stored, err := f.crds.ApiextensionsV1().
		CustomResourceDefinitions().
		Get(f.ctx, f.original.Name, metav1.GetOptions{})
	check(f.t, err)
	if stored.UID != f.original.UID || !reflect.DeepEqual(stored.Spec, spec) {
		f.t.Fatal("CRD identity or requested schema changed")
	}
}

func candidate(original *extv1.CustomResourceDefinition) extv1.CustomResourceDefinitionSpec {
	copy := original.DeepCopy()
	spec := copy.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	ui := spec.Properties["ui"]
	ui.XValidations = append(
		ui.XValidations,
		extv1.ValidationRule{Rule: "false", Message: policyMessage},
	)
	spec.Properties["ui"] = ui
	copy.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = spec
	return copy.Spec
}

func (f *fixture) activated(field string) {
	f.t.Helper()
	accepted := 0
	err := wait.PollUntilContextTimeout(
		f.ctx,
		100*time.Millisecond,
		10*time.Second,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := f.products.Create(
				ctx,
				product("activation-control", true),
				metav1.CreateOptions{FieldValidation: "Ignore"},
			)
			if rejected(err, field, policyMessage) {
				return true, nil
			}
			// A CRD read reflects the new spec before every admission handler switches.
			// Wait only on the exact policy being replaced; unrelated errors still fail.
			if rejected(err, "spec", policyMessage) || rejected(err, "spec.ui", policyMessage) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			accepted++
			return false, f.products.Delete(ctx, "activation-control", metav1.DeleteOptions{})
		},
	)
	if err != nil && accepted > 0 {
		f.t.Fatalf(
			"candidate accepted %d unsupported UI creates before activation expired: %v",
			accepted,
			err,
		)
	}
	check(f.t, err)
}

func TestAPIOnlyAdmissionMigration(t *testing.T) {
	f := newFixture(t)
	legacy := f.create(product("legacy", true))
	if legacy.GetUID() == "" {
		t.Fatal("original-schema control lacks UID")
	}
	for _, name := range []string{"remove-merge", "remove-json", "remove-replace", "whole-spec"} {
		f.create(product(name, true))
		f.seedStatus(name)
	}
	for _, name := range []string{"sole-owner", "non-owner", "shared-owners"} {
		check(t, f.apply(product(name, true), "original-author"))
		f.uiOwners(name, "original-author")
		f.seedStatus(name)
	}
	check(t, f.apply(product("shared-owners", true), "second-author"))
	f.uiOwners("shared-owners", "original-author", "second-author")
	f.schema(candidate(f.original))
	f.activated("spec.ui")
	t.Run("create", func(t *testing.T) {
		f := f.with(t)
		f.deny("new-ui", func() error {
			_, err := f.products.Create(
				f.ctx,
				product("new-ui", true),
				metav1.CreateOptions{FieldValidation: "Ignore"},
			)
			return err
		}, policyMessage)
		f.create(product("new-api", false))
		applied := product("apply-api", false)
		check(t, f.apply(applied, "api-author"))
		stored := f.get("apply-api")
		if stored.GetUID() == "" {
			t.Fatal("applied product has no UID")
		}
		applied.SetUID(stored.GetUID())
		f.assertStored(applied)
		if _, found, _ := unstructured.NestedFieldNoCopy(
			f.get("apply-api").Object,
			"spec",
			"ui",
		); found {
			t.Fatal("API-only apply gained UI")
		}
		f.deny(
			"apply-ui",
			func() error { return f.apply(product("apply-ui", true), "api-author") },
			policyMessage,
		)
		null := product("null-ui", false)
		set(t, null, nil, "spec", "ui")
		if _, found, _ := unstructured.NestedFieldNoCopy(
			f.create(null).Object,
			"spec",
			"ui",
		); found {
			t.Fatal("null UI was persisted")
		}
		malformed := product("malformed", false)
		set(t, malformed, "invalid", "spec", "ui")
		f.deny("malformed", func() error {
			_, err := f.products.Create(
				f.ctx,
				malformed,
				metav1.CreateOptions{FieldValidation: "Ignore"},
			)
			return err
		}, "must be of type object", metav1.CauseTypeTypeInvalid)
	})
	t.Run("unchanged-legacy-updates", func(t *testing.T) {
		f := f.with(t)
		want := f.get("legacy")
		want.SetLabels(map[string]string{"fixture": "admission", "edited": "true"})
		_, err := f.products.Update(f.ctx, want, metav1.UpdateOptions{FieldValidation: "Ignore"})
		check(t, err)
		f.assertStored(want)
		want = f.get("legacy")
		set(t, want, "Unrelated edit", "spec", "name")
		_, err = f.products.Update(f.ctx, want, metav1.UpdateOptions{FieldValidation: "Ignore"})
		check(t, err)
		f.assertStored(want)
		want = f.get("legacy")
		want.Object["status"] = map[string]any{"observedGeneration": want.GetGeneration()}
		_, err = f.products.UpdateStatus(
			f.ctx,
			want,
			metav1.UpdateOptions{FieldValidation: "Ignore"},
		)
		check(t, err)
		f.assertStored(want)
	})
	t.Run("changed-ui", func(t *testing.T) {
		f := f.with(t)
		for _, patch := range []struct {
			name string
			kind types.PatchType
			data string
		}{
			{"merge", types.MergePatchType, `{"spec":{"ui":{"title":"Changed"}}}`},
			{"json", types.JSONPatchType, `[{"op":"replace","path":"/spec/ui/title","value":"Changed"}]`},
		} {
			t.Run(patch.name, func(t *testing.T) {
				f := f.with(t)
				f.deny("legacy", func() error {
					_, err := f.products.Patch(
						f.ctx,
						"legacy",
						patch.kind,
						[]byte(patch.data),
						metav1.PatchOptions{FieldValidation: "Ignore"},
					)
					return err
				}, policyMessage)
			})
		}
		f.deny("legacy", func() error {
			p := f.get("legacy")
			set(t, p, "Changed", "spec", "ui", "title")
			_, err := f.products.Update(f.ctx, p, metav1.UpdateOptions{FieldValidation: "Ignore"})
			return err
		}, policyMessage)
		f.deny("new-api", func() error {
			p := f.get("new-api")
			set(
				t,
				p,
				map[string]any{"url": "https://data.example.com/ui", "title": "Legacy fixture"},
				"spec",
				"ui",
			)
			_, err := f.products.Update(f.ctx, p, metav1.UpdateOptions{FieldValidation: "Ignore"})
			return err
		}, policyMessage)
		f.deny("legacy", func() error {
			p := product("legacy", true)
			set(t, p, "Changed", "spec", "ui", "title")
			return f.apply(p, "changed-author")
		}, policyMessage)
	})
	t.Run("explicit-removal", func(t *testing.T) {
		f := f.with(t)
		for _, operation := range []struct {
			name string
			kind types.PatchType
			data string
		}{
			{"remove-merge", types.MergePatchType, `{"spec":{"ui":null}}`},
			{"remove-json", types.JSONPatchType, `[{"op":"remove","path":"/spec/ui"}]`},
		} {
			t.Run(operation.name, func(t *testing.T) {
				f := f.with(t)
				want := withoutUI(f.get(operation.name))
				_, err := f.products.Patch(
					f.ctx,
					operation.name,
					operation.kind,
					[]byte(operation.data),
					metav1.PatchOptions{FieldValidation: "Ignore"},
				)
				check(t, err)
				f.assertStored(want)
				f.deny(
					operation.name,
					func() error { return f.apply(product(operation.name, true), "reintroducing-author") },
					policyMessage,
				)
			})
		}
		want := withoutUI(f.get("remove-replace"))
		_, err := f.products.Update(f.ctx, want, metav1.UpdateOptions{FieldValidation: "Ignore"})
		check(t, err)
		f.assertStored(want)
		f.deny("remove-replace", func() error {
			_, err := f.products.Patch(
				f.ctx,
				"remove-replace",
				types.MergePatchType,
				[]byte(
					`{"spec":{"ui":{"url":"https://data.example.com/ui","title":"Reintroduced"}}}`,
				),
				metav1.PatchOptions{FieldValidation: "Ignore"},
			)
			return err
		}, policyMessage)
	})
	t.Run("apply-ownership", func(t *testing.T) {
		f := f.with(t)
		// An unchanged UI can still be applied by its owner; metadata must persist.
		p := product("sole-owner", true)
		p.SetAnnotations(
			map[string]string{"example.com/public": "synthetic", "example.com/edited": "true"},
		)
		want := f.get("sole-owner")
		want.SetAnnotations(p.GetAnnotations())
		check(t, f.apply(p, "original-author"))
		f.assertStored(want)
		f.uiOwners("sole-owner", "original-author")
		want = withoutUI(f.get("sole-owner"))
		p = withoutUI(p)
		check(t, f.apply(p, "original-author"))
		f.assertStored(want)
		f.uiOwners("sole-owner")
		f.deny(
			"sole-owner",
			func() error { return f.apply(product("sole-owner", true), "original-author") },
			policyMessage,
		)
		// A different manager never owned UI: its omission must retain the field.
		want = f.get("non-owner")
		check(t, f.apply(product("non-owner", false), "second-author"))
		f.assertStored(want)
		f.uiOwners("non-owner", "original-author")
		want = withoutUI(want)
		check(t, f.apply(product("non-owner", false), "original-author"))
		f.assertStored(want)
		f.uiOwners("non-owner")
		// Shared ownership drains only when the last actual UI owner omits it.
		want = f.get("shared-owners")
		check(t, f.apply(product("shared-owners", false), "original-author"))
		f.assertStored(want)
		f.uiOwners("shared-owners", "second-author")
		want = withoutUI(want)
		check(t, f.apply(product("shared-owners", false), "second-author"))
		f.assertStored(want)
		f.uiOwners("shared-owners")
	})
	t.Run("whole-spec-negative-control", func(t *testing.T) {
		f := f.with(t)
		whole := f.original.DeepCopy()
		spec := whole.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
		spec.XValidations = append(
			spec.XValidations,
			extv1.ValidationRule{Rule: "!has(self.ui)", Message: policyMessage},
		)
		whole.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = spec
		f.schema(whole.Spec)
		f.activated("spec")
		before := f.get("whole-spec")
		changed := before.DeepCopy()
		set(t, changed, "Unrelated change", "spec", "name")
		_, err := f.products.Update(f.ctx, changed, metav1.UpdateOptions{FieldValidation: "Ignore"})
		if !rejected(err, "spec", policyMessage) {
			t.Fatalf("whole-spec control must reject unrelated edit: %v", err)
		}
		if !reflect.DeepEqual(before.Object, f.get("whole-spec").Object) {
			t.Fatal("rejected whole-spec control changed stored object")
		}
		f.schema(candidate(f.original))
		f.activated("spec.ui")
		_, err = f.products.Update(f.ctx, changed, metav1.UpdateOptions{FieldValidation: "Ignore"})
		check(t, err)
		f.assertStored(changed)
	})
	t.Run("restore-original-schema", func(t *testing.T) {
		f := f.with(t)
		f.schema(f.original.Spec)
		check(
			t,
			wait.PollUntilContextTimeout(
				f.ctx,
				100*time.Millisecond,
				10*time.Second,
				true,
				func(ctx context.Context) (bool, error) {
					_, err := f.products.Create(
						ctx,
						product("restored", true),
						metav1.CreateOptions{FieldValidation: "Ignore"},
					)
					if rejected(err, "spec.ui", policyMessage) ||
						rejected(err, "spec", policyMessage) {
						return false, nil
					}
					return err == nil, err
				},
			),
		)
		want := f.get("restored")
		set(t, want, "Valid restored edit", "spec", "ui", "title")
		_, err := f.products.Update(f.ctx, want, metav1.UpdateOptions{FieldValidation: "Ignore"})
		check(t, err)
		f.assertStored(want)
	})
}

func TestOfflinePublisherFixtures(t *testing.T) {
	bundle, err := os.ReadFile("../../docs/examples/composition.yaml")
	check(t, err)
	v1 := preflight.CheckAPIOnlyFiles(t.Context(), []io.Reader{bytes.NewReader(bundle)}, "")
	v2 := preflight.CheckAPIOnlyBundle(t.Context(), []io.Reader{bytes.NewReader(bundle)}, "")
	if !v1.Valid || !v1.Complete || v1.Products != 3 || len(v1.Descriptors) != 3 ||
		!v2.Valid || !v2.Complete || v2.Products != 3 || len(v2.Descriptors) != 3 || v2.Plan == nil {
		t.Fatal("API-only publication fixture failed joint offline validation")
	}
	legacy, err := json.Marshal(product("offline-legacy", true).Object)
	check(t, err)
	v1 = preflight.CheckAPIOnlyFiles(t.Context(), []io.Reader{bytes.NewReader(legacy)}, "")
	v2 = preflight.CheckAPIOnlyBundle(t.Context(), []io.Reader{bytes.NewReader(legacy)}, "")
	if v1.Valid || v1.Complete || len(v1.Descriptors) != 0 || v2.Valid || v2.Complete ||
		len(v2.Descriptors) != 0 ||
		v2.Plan != nil {
		t.Fatal("UI-bearing fixture published an API-only preview")
	}
}
