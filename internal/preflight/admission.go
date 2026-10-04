package preflight

import (
	"context"
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
) (string, int64) {
	unknown := pruning.PruneWithOptions(
		object,
		a.schema,
		true,
		structural.UnknownFieldPathOptions{TrackUnknownFieldPaths: true},
	)
	if len(unknown) != 0 {
		return "UnknownField", budget
	}
	if len(validation.ValidateCustomResource(field.NewPath("manifest"), object, a.openAPI)) != 0 ||
		len(listtype.ValidateListSetsAndMaps(field.NewPath("manifest"), a.schema, object)) != 0 {
		return "AdmissionInvalid", budget
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
			return "AdmissionInvalid", remaining
		}
		budget = remaining
	}
	return "", budget
}
