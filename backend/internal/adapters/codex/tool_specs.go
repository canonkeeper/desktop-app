package codex

import (
	"encoding/json"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

func mapToolSpecs(specs []agent.ToolSpec) []protocol.DynamicToolSpec {
	result := make([]protocol.DynamicToolSpec, 0, len(specs))
	namespaces := map[string]int{}
	for _, spec := range specs {
		tool := protocol.DynamicToolNamespaceTool{
			TypeValue: "function", Name: protocol.SomeNonNull(spec.Name),
			Description: protocol.SomeNonNull(spec.Description), InputSchema: append(json.RawMessage(nil), spec.InputSchema...),
		}
		if spec.Namespace == "" {
			result = append(result, protocol.DynamicToolSpec{
				TypeValue: "function", Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
			})
			continue
		}
		index, ok := namespaces[spec.Namespace]
		if !ok {
			index = len(result)
			namespaces[spec.Namespace] = index
			result = append(result, protocol.DynamicToolSpec{
				TypeValue: "namespace", Name: protocol.SomeNonNull(spec.Namespace),
				Description: protocol.SomeNonNull(""), Tools: protocol.SomeNonNull([]protocol.DynamicToolNamespaceTool{}),
			})
		}
		tools, _ := result[index].Tools.Value()
		result[index].Tools = protocol.SomeNonNull(append(tools, tool))
	}
	return result
}
