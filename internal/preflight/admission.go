package preflight

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/devantler-tech/data-product-controller/config/crd"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	api "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structural "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

type admission struct {
	schema  *structural.Structural
	openAPI validation.SchemaValidator
	cel     *cel.Validator
}

type finding struct{ code, path string }

var loadAdmission = sync.OnceValues(func() (*admission, error) {
	var definition api.CustomResourceDefinition
	if err := yaml.Unmarshal(crd.DataProductSchema(), &definition); err != nil {
		return nil, err
	}
	var schema apiextensions.JSONSchemaProps
	if err := api.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		definition.Spec.Versions[0].Schema.OpenAPIV3Schema, &schema, nil); err != nil {
		return nil, err
	}
	shape, err := structural.NewStructural(&schema)
	if err != nil {
		return nil, err
	}
	validator, _, err := validation.NewSchemaValidator(&schema)
	if err != nil {
		return nil, err
	}
	return &admission{
		schema:  shape,
		openAPI: validator,
		cel:     cel.NewValidator(shape, true, celconfig.PerCallLimit),
	}, nil
})

// validate uses create-time rules, not update ratcheting or a cluster's additional admission policy.
func (a *admission) validate(
	ctx context.Context,
	object map[string]any,
	budget int64,
) ([]finding, int64) {
	unknown := pruning.PruneWithOptions(
		object,
		a.schema,
		true,
		structural.UnknownFieldPathOptions{TrackUnknownFieldPaths: true},
	)
	if len(unknown) != 0 {
		result := []finding{}
		for _, path := range unknown {
			result = append(
				result,
				finding{code: "UnknownField", path: safeAdmissionPath(path, a.schema)},
			)
		}
		return result, budget
	}
	errs := validation.ValidateCustomResource(field.NewPath("manifest"), object, a.openAPI)
	errs = append(
		errs,
		listtype.ValidateListSetsAndMaps(field.NewPath("manifest"), a.schema, object)...)
	if len(errs) > 0 {
		return admissionFindings(errs, a.schema, "InvalidField"), budget
	}
	if a.cel != nil {
		errs, remaining := a.cel.Validate(
			ctx,
			field.NewPath("manifest"),
			a.schema,
			object,
			nil,
			budget,
		)
		if len(errs) != 0 {
			return admissionFindings(errs, a.schema, "CrossFieldInvalid"), remaining
		}
		budget = remaining
	}
	return nil, budget
}

func admissionFindings(
	errs field.ErrorList,
	shape *structural.Structural,
	category string,
) []finding {
	result := make([]finding, 0, len(errs))
	for _, err := range errs {
		code := category
		if err.Type == field.ErrorTypeRequired {
			code = "RequiredField"
		}
		if err.Type == field.ErrorTypeDuplicate {
			code = "DuplicateListItem"
		}
		result = append(result, finding{code: code, path: safeAdmissionPath(err.Field, shape)})
	}
	return result
}

// safeAdmissionPath walks only the embedded schema; raw names and dynamic map keys never escape.
func safeAdmissionPath(raw string, shape *structural.Structural) string {
	if raw == "manifest" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "manifest.")
	path := ""
	for len(raw) > 0 && shape != nil {
		end := strings.IndexAny(raw, ".[")
		if end < 0 {
			end = len(raw)
		}
		if end > 0 {
			property, ok := shape.Properties[raw[:end]]
			if !ok {
				return path
			}
			path += "/" + raw[:end]
			shape = &property
			raw = raw[end:]
		}
		if len(raw) == 0 {
			break
		}
		if raw[0] == '.' {
			raw = raw[1:]
			continue
		}
		if raw[0] != '[' || shape.Items == nil {
			return path
		}
		close := strings.IndexByte(raw, ']')
		if close < 2 {
			return path
		}
		index, err := strconv.Atoi(raw[1:close])
		if err != nil || index < 0 || index > 4095 || strconv.Itoa(index) != raw[1:close] {
			return path
		}
		path += "/" + strconv.Itoa(index)
		shape = shape.Items
		raw = raw[close+1:]
		raw = strings.TrimPrefix(raw, ".")
	}
	return path
}
