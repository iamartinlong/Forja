package testgen

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Interaction represents a Pact interaction
type Interaction struct {
	Description string            `json:"description"`
	Request     Request           `json:"request"`
	Response    Response          `json:"response"`
}

type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
}

type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
}

type Pact struct {
	Consumer     string        `json:"consumer"`
	Provider     string        `json:"provider"`
	Interactions []Interaction `json:"interactions"`
}

// GeneratePact from IS OpenAPI content (JSON or YAML string)
func GeneratePact(isContent []byte, title string) (*Pact, error) {
	var data map[string]any
	// try JSON
	if err := json.Unmarshal(isContent, &data); err != nil {
		// try YAML
		if err2 := yaml.Unmarshal(isContent, &data); err2 != nil {
			return nil, fmt.Errorf("invalid IS content: %w", err)
		}
	}
	paths, _ := data["paths"].(map[string]any)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths in IS")
	}
	consumer := title
	if consumer == "" {
		consumer = "forja-consumer"
	}
	pact := &Pact{Consumer: consumer, Provider: "forja-provider"}
	for path, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for method, mv := range methods {
			upper := strings.ToUpper(method)
			if upper != "GET" && upper != "POST" && upper != "PUT" && upper != "PATCH" && upper != "DELETE" {
				continue
			}
			mm, _ := mv.(map[string]any)
			summary, _ := mm["summary"].(string)
			if summary == "" {
				summary = fmt.Sprintf("%s %s", upper, path)
			}
			// determine status: first response code or 200/201
			status := 200
			if resp, ok := mm["responses"].(map[string]any); ok {
				for code := range resp {
					if code == "201" {
						status = 201
						break
					}
					if code == "200" {
						status = 200
					}
				}
			}
			if upper == "POST" && status == 200 {
				status = 201
			}
			inter := Interaction{
				Description: summary,
				Request: Request{
					Method:  upper,
					Path:    path,
					Headers: map[string]string{"Content-Type": "application/json"},
				},
				Response: Response{
					Status:  status,
					Headers: map[string]string{"Content-Type": "application/json"},
				},
			}
			// add body from requestBody example if present
			if rb, ok := mm["requestBody"].(map[string]any); ok {
				if content, ok := rb["content"].(map[string]any); ok {
					if aj, ok := content["application/json"].(map[string]any); ok {
						if schema, ok := aj["schema"]; ok {
							inter.Request.Body = map[string]any{"example": schema}
						}
					}
				}
			}
			pact.Interactions = append(pact.Interactions, inter)
		}
	}
	if len(pact.Interactions) == 0 {
		return nil, fmt.Errorf("no interactions generated")
	}
	return pact, nil
}

func ToTestCases(pact *Pact) []map[string]any {
	cases := []map[string]any{}
	for _, inter := range pact.Interactions {
		cases = append(cases, map[string]any{
			"name":      inter.Description,
			"method":    inter.Request.Method,
			"path":      inter.Request.Path,
			"expected":  inter.Response.Status,
			"framework": "pact",
		})
	}
	return cases
}
