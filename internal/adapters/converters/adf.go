package converters

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/GabrielNunesIT/openapi-converter/internal/domain"
)

const adfFormat = "confluence"

// ADFConverter converts OpenAPI documents to Atlassian Document Format (ADF) for Confluence.
type ADFConverter struct{}

// NewADFConverter creates a new ADF converter.
func NewADFConverter() *ADFConverter {
	return &ADFConverter{}
}

// Format returns the output format name.
func (c *ADFConverter) Format() string {
	return adfFormat
}

// ADF node types.
type adfDocument struct {
	Version int       `json:"version"`
	Type    string    `json:"type"`
	Content []adfNode `json:"content"`
}

type adfNode struct {
	Type    string    `json:"type"`
	Attrs   *adfAttrs `json:"attrs,omitempty"`
	Content []adfNode `json:"content,omitempty"`
	Text    string    `json:"text,omitempty"`
	Marks   []adfMark `json:"marks,omitempty"`
}

type adfAttrs struct {
	Level int    `json:"level,omitempty"`
	Order int    `json:"order,omitempty"`
	URL   string `json:"url,omitempty"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

type adfEndpointRef struct {
	path      string
	method    string
	operation domain.Operation
}

// Convert transforms an OpenAPI document to ADF JSON format.
func (c *ADFConverter) Convert(doc *domain.OpenAPIDocument, output io.Writer) error {
	adf := &adfDocument{
		Version: 1,
		Type:    "doc",
		Content: []adfNode{},
	}

	// Title
	adf.Content = append(adf.Content, c.heading(doc.Title, 1))
	adf.Content = append(adf.Content, c.paragraph(fmt.Sprintf("Version: %s", doc.Version)))

	// Description
	if doc.Description != "" {
		adf.Content = append(adf.Content, c.heading("Description", 2))
		adf.Content = append(adf.Content, c.paragraph(doc.Description))
	}

	// Servers
	if len(doc.Servers) > 0 {
		adf.Content = append(adf.Content, c.heading("Servers", 2))
		adf.Content = append(adf.Content, c.serverList(doc.Servers))
	}

	// Endpoints grouped by tags
	if len(doc.Paths) > 0 {
		adf.Content = append(adf.Content, c.heading("API Endpoints", 2))

		tagPaths := c.groupPathsByTag(doc)
		tags := make([]string, 0, len(tagPaths))
		for tag := range tagPaths {
			tags = append(tags, tag)
		}
		sort.Strings(tags)

		for _, tag := range tags {
			// Tag header
			adf.Content = append(adf.Content, c.heading(tag, 3))

			// Add components used by this tag's endpoints
			tagComponents := c.collectTagComponents(tagPaths[tag])
			if len(tagComponents) > 0 {
				adf.Content = append(adf.Content, c.tagComponentNodes(tagComponents, doc.Components)...)
			}

			// Add endpoints
			for _, ep := range tagPaths[tag] {
				adf.Content = append(adf.Content, c.operationNodes(ep.path, ep.operation)...)
			}
		}
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(adf); err != nil {
		return fmt.Errorf("failed to encode ADF: %w", err)
	}

	return nil
}

// groupPathsByTag groups paths by their operation tags.
func (c *ADFConverter) groupPathsByTag(doc *domain.OpenAPIDocument) map[string][]adfEndpointRef {
	result := make(map[string][]adfEndpointRef)

	for _, path := range doc.Paths {
		for _, op := range path.Operations {
			tags := op.Tags
			if len(tags) == 0 {
				tags = []string{"Default"}
			}

			for _, tag := range tags {
				result[tag] = append(result[tag], adfEndpointRef{
					path:      path.Path,
					method:    op.Method,
					operation: op,
				})
			}
		}
	}

	// Sort endpoints within each tag by path then method
	for tag := range result {
		sort.Slice(result[tag], func(i, j int) bool {
			if result[tag][i].path == result[tag][j].path {
				return result[tag][i].method < result[tag][j].method
			}

			return result[tag][i].path < result[tag][j].path
		})
	}

	return result
}

// collectTagComponents gathers all unique component names used by endpoints in a tag.
func (c *ADFConverter) collectTagComponents(endpoints []adfEndpointRef) []string {
	componentSet := make(map[string]struct{})

	for _, ep := range endpoints {
		// Check request body
		if ep.operation.RequestBody != nil {
			for _, media := range ep.operation.RequestBody.Content {
				c.collectSchemaRefs(media.Schema, componentSet)
			}
		}

		// Check responses
		for _, resp := range ep.operation.Responses {
			for _, media := range resp.Content {
				c.collectSchemaRefs(media.Schema, componentSet)
			}
		}

		// Check parameters
		for _, param := range ep.operation.Parameters {
			c.collectSchemaRefs(param.Schema, componentSet)
		}
	}

	// Convert set to sorted slice
	components := make([]string, 0, len(componentSet))
	for name := range componentSet {
		components = append(components, name)
	}
	sort.Strings(components)

	return components
}

// collectSchemaRefs recursively collects component references from a schema.
func (c *ADFConverter) collectSchemaRefs(schema domain.Schema, refs map[string]struct{}) {
	if schema.Ref != "" {
		refs[extractRefName(schema.Ref)] = struct{}{}
	}

	for _, prop := range schema.Properties {
		c.collectSchemaRefs(prop, refs)
	}

	if schema.Items != nil {
		c.collectSchemaRefs(*schema.Items, refs)
	}
}

// tagComponentNodes generates ADF nodes for component schemas used in a tag.
func (c *ADFConverter) tagComponentNodes(componentNames []string, components map[string]domain.Schema) []adfNode {
	nodes := []adfNode{c.heading("Schemas Used", 4)}

	for _, name := range componentNames {
		schema, exists := components[name]
		if !exists {
			continue
		}

		nodes = append(nodes, c.componentSchemaNodes(name, schema)...)
	}

	return nodes
}

// componentSchemaNodes generates ADF nodes for a single component schema.
func (c *ADFConverter) componentSchemaNodes(name string, schema domain.Schema) []adfNode {
	nodes := []adfNode{}

	// Schema name as bold paragraph
	nodes = append(nodes, adfNode{
		Type: "paragraph",
		Content: []adfNode{
			c.boldText(name),
		},
	})

	// Type info
	if schema.Type != "" {
		typeStr := schema.Type
		if schema.Format != "" {
			typeStr = fmt.Sprintf("%s (%s)", schema.Type, schema.Format)
		}
		nodes = append(nodes, c.paragraph(fmt.Sprintf("Type: %s", typeStr)))
	}

	// Description
	if schema.Description != "" {
		nodes = append(nodes, c.paragraph(schema.Description))
	}

	// Properties as bullet list
	if len(schema.Properties) > 0 {
		propNames := make([]string, 0, len(schema.Properties))
		for propName := range schema.Properties {
			propNames = append(propNames, propName)
		}
		sort.Strings(propNames)

		items := make([]adfNode, 0, len(propNames))
		for _, propName := range propNames {
			prop := schema.Properties[propName]
			propType := prop.Type
			if prop.Ref != "" {
				propType = extractRefName(prop.Ref)
			} else if prop.Format != "" {
				propType = fmt.Sprintf("%s (%s)", prop.Type, prop.Format)
			}

			items = append(items, adfNode{
				Type: "listItem",
				Content: []adfNode{
					{
						Type: "paragraph",
						Content: []adfNode{
							c.codeText(propName),
							{Type: "text", Text: fmt.Sprintf(" (%s)", propType)},
						},
					},
				},
			})
		}

		nodes = append(nodes, adfNode{
			Type:    "bulletList",
			Content: items,
		})
	}

	return nodes
}

func (c *ADFConverter) heading(text string, level int) adfNode {
	return adfNode{
		Type:  "heading",
		Attrs: &adfAttrs{Level: level},
		Content: []adfNode{
			{Type: "text", Text: text},
		},
	}
}

func (c *ADFConverter) paragraph(text string) adfNode {
	return adfNode{
		Type: "paragraph",
		Content: []adfNode{
			{Type: "text", Text: text},
		},
	}
}

func (c *ADFConverter) boldText(text string) adfNode {
	return adfNode{
		Type: "text",
		Text: text,
		Marks: []adfMark{
			{Type: "strong"},
		},
	}
}

func (c *ADFConverter) codeText(text string) adfNode {
	return adfNode{
		Type: "text",
		Text: text,
		Marks: []adfMark{
			{Type: "code"},
		},
	}
}

func (c *ADFConverter) serverList(servers []domain.Server) adfNode {
	items := make([]adfNode, 0, len(servers))

	for _, server := range servers {
		text := server.URL
		if server.Description != "" {
			text = fmt.Sprintf("%s - %s", server.URL, server.Description)
		}

		items = append(items, adfNode{
			Type: "listItem",
			Content: []adfNode{
				c.paragraph(text),
			},
		})
	}

	return adfNode{
		Type:    "bulletList",
		Content: items,
	}
}

func (c *ADFConverter) operationNodes(pathStr string, operation domain.Operation) []adfNode {
	nodes := []adfNode{}

	// Endpoint heading with method and path
	endpointTitle := fmt.Sprintf("%s %s", formatMethod(operation.Method), pathStr)
	nodes = append(nodes, c.heading(endpointTitle, 5))

	// Summary (bold)
	if operation.Summary != "" {
		nodes = append(nodes, adfNode{
			Type: "paragraph",
			Content: []adfNode{
				c.boldText(operation.Summary),
			},
		})
	}

	// Description
	if operation.Description != "" {
		nodes = append(nodes, c.paragraph(operation.Description))
	}

	// Parameters
	if len(operation.Parameters) > 0 {
		nodes = append(nodes, c.heading("Parameters", 6))
		nodes = append(nodes, c.parameterList(operation.Parameters))
	}

	// Responses
	if len(operation.Responses) > 0 {
		nodes = append(nodes, c.heading("Responses", 6))
		nodes = append(nodes, c.responseList(operation.Responses))
	}

	// Divider between endpoints
	nodes = append(nodes, adfNode{Type: "rule"})

	return nodes
}

func (c *ADFConverter) parameterList(params []domain.Parameter) adfNode {
	items := make([]adfNode, 0, len(params))

	for _, param := range params {
		required := ""
		if param.Required {
			required = " (required)"
		}

		items = append(items, adfNode{
			Type: "listItem",
			Content: []adfNode{
				{
					Type: "paragraph",
					Content: []adfNode{
						c.codeText(param.Name),
						{Type: "text", Text: fmt.Sprintf(" (%s): %s%s", param.In, param.Description, required)},
					},
				},
			},
		})
	}

	return adfNode{
		Type:    "bulletList",
		Content: items,
	}
}

func (c *ADFConverter) responseList(responses []domain.Response) adfNode {
	items := make([]adfNode, 0, len(responses))

	for _, resp := range responses {
		items = append(items, adfNode{
			Type: "listItem",
			Content: []adfNode{
				{
					Type: "paragraph",
					Content: []adfNode{
						c.codeText(resp.StatusCode),
						{Type: "text", Text: fmt.Sprintf(": %s", resp.Description)},
					},
				},
			},
		})
	}

	return adfNode{
		Type:    "bulletList",
		Content: items,
	}
}
