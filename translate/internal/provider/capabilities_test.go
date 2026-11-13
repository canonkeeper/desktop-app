// SPDX-License-Identifier: MIT

package provider

// Profiles of the tests: a Chat Completions provider, a Responses provider and
// a GLM provider whose thinking is a request-shaping switch.

func chatCapabilities() Capabilities {
	return Capabilities{Protocol: ProtocolChatCompletions, Tools: true, ParallelToolCalls: true}
}

func responsesCapabilities() Capabilities {
	return Capabilities{Protocol: ProtocolResponses, Tools: true, ParallelToolCalls: true}
}

func glmCapabilities() Capabilities {
	c := chatCapabilities()
	c.Reasoning = true
	c.EnableGLMThinking = true
	return c
}
