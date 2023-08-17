// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mapper

import (
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-codegen-openapi/internal/config"
	"github.com/hashicorp/terraform-plugin-codegen-openapi/internal/explorer"
	"github.com/hashicorp/terraform-plugin-codegen-openapi/internal/mapper/oas"
	"github.com/hashicorp/terraform-plugin-codegen-openapi/internal/mapper/util"
	"github.com/hashicorp/terraform-plugin-codegen-spec/datasource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"
)

var _ DataSourceMapper = dataSourceMapper{}

type DataSourceMapper interface {
	MapToIR() ([]datasource.DataSource, error)
}

type dataSourceMapper struct {
	dataSources map[string]explorer.DataSource
	//nolint:unused // Might be useful later!
	cfg config.Config
}

func NewDataSourceMapper(dataSources map[string]explorer.DataSource, cfg config.Config) DataSourceMapper {
	return dataSourceMapper{
		dataSources: dataSources,
		cfg:         cfg,
	}
}

func (m dataSourceMapper) MapToIR() ([]datasource.DataSource, error) {
	dataSourceSchemas := []datasource.DataSource{}

	// Guarantee the order of processing
	dataSourceNames := util.SortedKeys(m.dataSources)
	for _, name := range dataSourceNames {
		dataSource := m.dataSources[name]

		schema, err := generateDataSourceSchema(dataSource)
		if err != nil {
			log.Printf("[WARN] skipping '%s' data source schema: %s\n", name, err)
			continue
		}

		dataSourceSchemas = append(dataSourceSchemas, datasource.DataSource{
			Name:   name,
			Schema: schema,
		})
	}

	return dataSourceSchemas, nil
}

func generateDataSourceSchema(dataSource explorer.DataSource) (*datasource.Schema, error) {
	dataSourceSchema := &datasource.Schema{
		Attributes: []datasource.Attribute{},
	}

	// ********************
	// READ Response Body (required)
	// ********************
	readResponseSchema, err := oas.BuildSchemaFromResponse(dataSource.ReadOp, oas.SchemaOpts{}, oas.GlobalSchemaOpts{OverrideComputability: schema.Computed})
	if err != nil {
		return nil, err
	}
	readResponseAttributes, err := readResponseSchema.BuildDataSourceAttributes()
	if err != nil {
		return nil, err
	}

	// ****************
	// READ Parameters (optional)
	// ****************
	// TODO: Expand support for "header" and "cookie"?
	// TODO: support style + explode?
	//	- https://spec.openapis.org/oas/latest.html#style-values
	// 	- https://spec.openapis.org/oas/latest.html#style-examples
	readParameterAttributes := []datasource.Attribute{}
	if dataSource.ReadOp != nil && dataSource.ReadOp.Parameters != nil {
		for _, param := range dataSource.ReadOp.Parameters {
			if param.In != util.OAS_param_path && param.In != util.OAS_param_query {
				continue
			}

			schemaOpts := oas.SchemaOpts{
				OverrideDescription: param.Description,
			}

			s, err := oas.BuildSchema(param.Schema, schemaOpts, oas.GlobalSchemaOpts{})
			if err != nil {
				return nil, fmt.Errorf("failed to build param schema for '%s'", param.Name)
			}

			computability := schema.ComputedOptional
			if param.Required {
				computability = schema.Required
			}

			// Check for any aliases and replace the paramater name if found
			paramName := param.Name
			if matchedName, ok := dataSource.SchemaOptions.AttributeOptions.Aliases[param.Name]; ok {
				paramName = matchedName
			}

			parameterAttribute, err := s.BuildDataSourceAttribute(paramName, computability)
			if err != nil {
				log.Printf("[WARN] error mapping param attribute %s - %s", param.Name, err.Error())
			}

			readParameterAttributes = append(readParameterAttributes, *parameterAttribute)
		}
	}

	dataSourceAttributes := mergeDataSourceAttributes(
		readParameterAttributes,
		*readResponseAttributes,
	)

	dataSourceSchema.Attributes = *dataSourceAttributes
	return dataSourceSchema, nil
}

// mainSlice takes priority in the merge, will have each subsequent mergeAttributeSlice applied in sequence
// - No re-ordering of the mainSlice is done, so will append new attributes as they are encountered
func mergeDataSourceAttributes(mainSlice []datasource.Attribute, mergeAttributeSlices ...[]datasource.Attribute) *[]datasource.Attribute {
	for _, attributeSlice := range mergeAttributeSlices {

		for _, compareAttribute := range attributeSlice {
			isNewAttribute := true

			for mainIndex, mainAttribute := range mainSlice {
				if mainAttribute.Name == compareAttribute.Name {
					// Handle types that require nested merging
					if mainAttribute.SingleNested != nil && compareAttribute.SingleNested != nil {
						mergedAttributes := mergeDataSourceAttributes(mainAttribute.SingleNested.Attributes, compareAttribute.SingleNested.Attributes)
						mainSlice[mainIndex].SingleNested.Attributes = *mergedAttributes
					} else if mainAttribute.ListNested != nil && compareAttribute.ListNested != nil {
						mergedAttributes := mergeDataSourceAttributes(mainAttribute.ListNested.NestedObject.Attributes, compareAttribute.ListNested.NestedObject.Attributes)
						mainSlice[mainIndex].ListNested.NestedObject.Attributes = *mergedAttributes
					} else if mainAttribute.List != nil && compareAttribute.List != nil {
						mergedElementType := mergeElementType(mainAttribute.List.ElementType, compareAttribute.List.ElementType)
						mainSlice[mainIndex].List.ElementType = mergedElementType
					}

					isNewAttribute = false
					break
				}
			}

			if isNewAttribute {
				// Add this back to the original slice to avoid adding duplicate attributes from different mergeAttributeSlices
				mainSlice = append(mainSlice, compareAttribute)
			}
		}

	}
	return &mainSlice
}
