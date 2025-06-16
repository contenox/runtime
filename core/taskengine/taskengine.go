// Package taskengine provides an engine for orchestrating chains of LLM-based tasks.
//
// taskengine enables building AI workflows where tasks are linked in sequence, supporting
// conditional branching, numeric or scored evaluation, range resolution, and optional
// integration with external systems (hooks). Each task can invoke an LLM prompt or a custom
// hook function depending on its type.
//
// Hooks are pluggable interfaces that allow tasks to perform side effects — calling APIs,
// saving data, or triggering custom business logic — outside of prompt-based processing.
//
// Supported task types include:
//   - PromptToString: executes a prompt and returns its text output
//   - PromptToNumber: parses the prompt result as an integer
//   - PromptToScore: parses the prompt result as a float
//   - PromptToRange: parses a numeric range like "3-5"
//   - PromptToCondition: resolves a boolean by matching prompt result to a condition map
//   - Hook: invokes an external system using the HookProvider interface
//
// Typical use cases:
//   - Dynamic content generation (e.g. marketing copy, reports)
//   - AI agent orchestration with branching logic
//   - Decision trees based on LLM outputs
//   - Automation pipelines involving prompts and external system calls
//
// Example usage:
//
// Define a YAML chain:
//
//	chains/article.yaml:
//	  id: article-generator
//	  description: Generates articles based on topic and length
//	  triggers:
//	    - type: manual
//	      description: Run manually via API
//	  tasks:
//	    - id: get_length
//	      type: number
//	      prompt_template: "How many words should the article be?"
//	      transition:
//	        next:
//	          - value: "default"
//	            id: generate_article
//
//	    - id: generate_article
//	      type: string
//	      prompt_template: "Write a {{ .get_length }}-word article about {{ .input }}"
//	      print: "Generated article:\n{{ .previous_output }}"
//	      transition:
//	        next:
//	          - value: "default"
//	            id: end
//
//	    - id: end
//	      type: string
//	      prompt_template: "{{ .generate_article }}"
//
// Execute it:
//
//	exec, _ := taskengine.NewExec(ctx, modelRepo, hookProvider)
//	env, _ := taskengine.NewEnv(ctx, tracker, exec)
//	output, err := env.ExecEnv(ctx, &chainDef, userInput)
package taskengine
