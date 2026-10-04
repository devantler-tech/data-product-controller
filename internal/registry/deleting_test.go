package registry

import (
 "context"
 "encoding/json"
 "net/http"
 "strings"
 "testing"

 datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestDeletingProductsStayInspectableWithoutReadiness catches a finalizer-held resource retaining Ready=True.
func TestDeletingProductsStayInspectableWithoutReadiness(t *testing.T) {
 t.Parallel()
 for _, deleting := range []bool{false, true} {
  product := registryProduct()
  if deleting { now := metav1.Now(); product.DeletionTimestamp = &now }
  reader := discoveryReader{
   list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
    discoveryProductList(t, out).Items = []datav1alpha1.DataProduct{*product}; return nil
   },
   get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
    product.DeepCopyInto(discoveryProductObject(t, out)); return nil
   },
  }
  for _, path := range []string{"/api/v1/products", "/api/v2/products?limit=1", "/api/v2/products/products/customer-catalog"} {
   response := discoveryRequest(t, discoveryHandler(reader, true), path)
   if response.Code != http.StatusOK { t.Fatalf("%s status=%d", path, response.Code) }
   var value map[string]any
   if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil { t.Fatal(err) }
   if entries, ok := value["products"].([]any); ok {
    if len(entries) != 1 { t.Fatal("deleting metadata disappeared instead of becoming unready") }
    var objectOK bool
    value, objectOK = entries[0].(map[string]any); if !objectOK { t.Fatal("descriptor is not an object") }
   }
   if value["ready"] != !deleting { t.Fatalf("%s deleting=%t exposed ready=%v", path, deleting, value["ready"]) }
   if deleting && !strings.Contains(strings.ToLower(response.Body.String()), "being deleted") {
    t.Fatalf("%s did not explain pending deletion", path)
   }
  }
 }
}

