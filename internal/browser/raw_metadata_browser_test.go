//go:build browser

package browser_test

import (
	"fmt"
	"strings"
	"testing"
)

// TestDescriptorNumericTokensRemainLossless exercises real local import before any navigation.
func TestDescriptorNumericTokensRemainLossless(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	wire := descriptorJSON(t, offlineDescriptor(host, publisher))
	for _, tc := range []struct {
		token string
		valid bool
	}{
		{"7.0000000000000000001", false},
		{"1e-400", false},
		{"9007199254740990.1", false},
		{"9007199254740992", false},
		{"7.0", true},
		{"7e0", true},
		{"70e-1", true},
		{"0.7e1", true},
	} {
		t.Run(tc.token, func(t *testing.T) {
			changed := strings.Replace(wire, `"generation":7`, `"generation":`+tc.token, 1)
			before := calls.Load()
			page.MustElement("#descriptor").MustSelectAllText().MustInput(changed)
			page.MustElement("#import-descriptor").MustClick()
			page.MustElement("#kit-status").
				MustWait(`()=>['ready','invalid'].includes(this.dataset.state)`)
			ready := page.MustEval(`()=>document.querySelector('#kit-status').dataset.state==='ready'`).
				Bool()
			if ready != tc.valid {
				t.Fatalf("lossless admission=%t want=%t", ready, tc.valid)
			}
			if !tc.valid && calls.Load() != before {
				t.Fatal("invalid numeric token navigated a product")
			}
		})
	}
}

// TestDescriptorHealthObservationRelations retains producer-consistent independent states.
func TestDescriptorHealthObservationRelations(t *testing.T) {
	page, host, publisher, _ := kitWorkspaceFixture(t, false, false)
	for _, tc := range []struct {
		state    string
		observed int
		valid    bool
	}{
		{"not-ready", 6, false},
		{"disabled", 6, false},
		{"stale", 7, false},
		{"not-applicable", 7, false},
		{"not-ready", 7, true},
		{"disabled", 7, true},
		{"stale", 6, true},
		{"not-applicable", 0, true},
		{"unobserved", 0, true},
		{"unobserved", 7, true},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.state, tc.observed), func(t *testing.T) {
			descriptor := offlineDescriptor(host, publisher)
			dimension := descriptorObject(t, descriptor, "health", "source")
			dimension["state"], dimension["observedGeneration"] = tc.state, tc.observed
			wire := descriptorJSON(t, descriptor)
			accepted := page.MustEval(`wire=>{try {DataProductDescriptor.validate(DataProductDescriptor.parseJSON(wire));return true;} catch{return false;}}`, wire).
				Bool()
			if accepted != tc.valid {
				t.Fatalf("health admitted=%t want=%t", accepted, tc.valid)
			}
		})
	}
}

// TestRawIntegerAndBudgetBoundaries admits exact exponent notation and preserves caller limits.
func TestRawIntegerAndBudgetBoundaries(t *testing.T) {
	page, _, _, _ := kitWorkspaceFixture(t, false, false)
	for _, tc := range []struct {
		wire  string
		valid bool
	}{
		{`{"generation":0e99999999999999999999999}`, true},
		{`{"generation":-0}`, true},
		{`{"generation":-1e-400}`, false},
		{`{"generation":1e-400}`, false},
		{`{"generation":1e400}`, false},
		{`{"generation":9007199254740991}`, true},
		{`{"generation":9007199254740991.0}`, true},
		{`{"generation":9007199254740990.1}`, false},
		{`{"generation":700e-2}`, true},
		{`{"generation":700e-3}`, false},
		{`{"message":"fraction 7.0000000000000000001 stays text"}`, true},
	} {
		if got := page.MustEval(`wire=>{try{DataProductDescriptor.parseJSON(wire);return true}catch{return false}}`, tc.wire).
			Bool(); got != tc.valid {
			t.Errorf("raw number admitted=%t want=%t for %s", got, tc.valid, tc.wire)
		}
	}
	wire := `{"message":"` + strings.Repeat("a", 65536) + `"}`
	if !page.MustEval(`wire=>{let small=false,large=false,unbounded=false;try{DataProductDescriptor.parseJSON(wire)}catch{small=true}try{DataProductDescriptor.parseJSON(wire,2097152);large=true}catch{}try{DataProductDescriptor.parseJSON(wire,2097153)}catch{unbounded=true}return small&&large&&unbounded}`, wire).
		Bool() {
		t.Fatal("caller metadata budget widened or incorrectly narrowed")
	}
}
