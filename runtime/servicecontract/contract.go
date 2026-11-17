// Package servicecontract defines the human and machine readable description
// of an application service exposed through product surfaces.
package servicecontract

import "fmt"

// Flag scope values. A command flag belongs to one operation; a persistent flag
// is registered once on the root command and read by every operation below it.
const (
	FlagScopeCommand    = "command"
	FlagScopePersistent = "persistent"
)

// Contract describes one service and the operations a surface may expose.
type Contract struct {
	ID          string      `json:"id"`
	Summary     string      `json:"summary"`
	Description string      `json:"description"`
	Operations  []Operation `json:"operations"`
}

// Operation describes one callable operation.
type Operation struct {
	ID          string  `json:"id"`
	Summary     string  `json:"summary"`
	Description string  `json:"description,omitempty"`
	Inputs      []Input `json:"inputs,omitempty"`
	// Spec is the operator-facing body of the operation: what the capability
	// is, what it refuses and what it costs. It is the whole of what a surface
	// renders as long help.
	Spec string `json:"spec,omitempty"`
	// Usage is the synopsis a command surface renders after the command path,
	// carrying the positional placeholders the operation accepts.
	Usage    string    `json:"usage,omitempty"`
	Examples []Example `json:"examples,omitempty"`
	Output   Output    `json:"output,omitempty"`
	Outcomes []Outcome `json:"outcomes,omitempty"`
	// Gate names the visibility gate the operation sits behind, so a surface
	// hides it rather than offering a command that cannot run.
	Gate     string    `json:"gate,omitempty"`
	Refusals []Refusal `json:"refusals,omitempty"`
}

// Input describes one named operation input.
type Input struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	// Values enumerates the accepted values of a closed input, so a surface can
	// offer the choice instead of restating it.
	Values []string `json:"values,omitempty"`
	// Flag is the command-line spelling of this input. An empty Name means the
	// input is reached positionally or not at all.
	Flag FlagSpec `json:"flag,omitempty"`
	// Positional is the 1-based ordinal this input occupies, or zero when it has
	// no positional form.
	Positional int `json:"positional,omitempty"`
	// Variadic marks an input that consumes the remaining positionals and is
	// therefore always the last one.
	Variadic bool `json:"variadic,omitempty"`
	// Default is what the surface applies when the operator states nothing. An
	// empty Default means the input has no default.
	Default string `json:"default,omitempty"`
	// Placeholder is the metavar the synopsis renders for this input.
	Placeholder string `json:"placeholder,omitempty"`
	// Repeatable marks a flag or positional that may be given more than once.
	Repeatable bool `json:"repeatable,omitempty"`
	// Presentation marks an input the surface renders and that changes no
	// engine input, so a coverage check does not demand a request field for it.
	Presentation bool `json:"presentation,omitempty"`
	// Deprecated names the replacement or the reason a spelling is kept, so a
	// surface can warn instead of silently dropping it.
	Deprecated string `json:"deprecated,omitempty"`
}

// FlagSpec is the command-line flag an input is reached through.
type FlagSpec struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Hidden    bool   `json:"hidden,omitempty"`
}

// Example is one worked invocation of an operation.
type Example struct {
	Command     string `json:"command"`
	Explanation string `json:"explanation,omitempty"`
}

// Output describes what an operation writes where.
type Output struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Format string `json:"format,omitempty"`
}

// Outcome is one terminal status an operation reports.
type Outcome struct {
	Code    int    `json:"code"`
	When    string `json:"when"`
	Meaning string `json:"meaning,omitempty"`
}

// Refusal is one condition under which an operation declines, with what the
// surface tells the operator when it does.
type Refusal struct {
	When    string `json:"when"`
	Message string `json:"message"`
}

// Operation returns the operation with id.
func (c Contract) Operation(id string) (Operation, bool) {
	for _, operation := range c.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return Operation{}, false
}

// Validate rejects contracts whose identifiers or readable descriptions are incomplete.
func (c Contract) Validate() error {
	if c.ID == "" || c.Summary == "" || c.Description == "" {
		return fmt.Errorf("service contract id, summary, and description are required")
	}
	operations := make(map[string]struct{}, len(c.Operations))
	for _, operation := range c.Operations {
		if operation.ID == "" || operation.Summary == "" {
			return fmt.Errorf("service contract operation id and summary are required")
		}
		if _, exists := operations[operation.ID]; exists {
			return fmt.Errorf("service contract operation %q is duplicated", operation.ID)
		}
		operations[operation.ID] = struct{}{}
		if err := validateOperation(operation); err != nil {
			return err
		}
	}
	return nil
}

func validateOperation(operation Operation) error {
	inputs := make(map[string]struct{}, len(operation.Inputs))
	flags := make(map[string]string, len(operation.Inputs))
	positionals := make(map[int]string, len(operation.Inputs))
	last := len(operation.Inputs) - 1
	for index, input := range operation.Inputs {
		if input.ID == "" || input.Summary == "" || input.Type == "" {
			return fmt.Errorf("service contract operation %q has an incomplete input", operation.ID)
		}
		if _, exists := inputs[input.ID]; exists {
			return fmt.Errorf("service contract operation %q input %q is duplicated", operation.ID, input.ID)
		}
		inputs[input.ID] = struct{}{}
		if input.Values != nil && len(input.Values) == 0 {
			return fmt.Errorf("service contract operation %q input %q declares a closed set with no values", operation.ID, input.ID)
		}
		if input.Required && input.Default != "" {
			return fmt.Errorf("service contract operation %q input %q is required and states a default", operation.ID, input.ID)
		}
		if input.Variadic && input.Positional == 0 {
			return fmt.Errorf("service contract operation %q input %q is variadic without a positional ordinal", operation.ID, input.ID)
		}
		if input.Variadic && index != last {
			return fmt.Errorf("service contract operation %q input %q is variadic and not the last input", operation.ID, input.ID)
		}
		if input.Positional > 0 {
			if _, exists := positionals[input.Positional]; exists {
				return fmt.Errorf("service contract operation %q gives positional %d to more than one input", operation.ID, input.Positional)
			}
			positionals[input.Positional] = input.ID
		}
		if input.Flag.Name == "" {
			continue
		}
		if input.Flag.Scope != "" && input.Flag.Scope != FlagScopeCommand && input.Flag.Scope != FlagScopePersistent {
			return fmt.Errorf("service contract operation %q input %q states flag scope %q, which is neither %s nor %s", operation.ID, input.ID, input.Flag.Scope, FlagScopeCommand, FlagScopePersistent)
		}
		if owner, exists := flags[input.Flag.Name]; exists {
			return fmt.Errorf("service contract operation %q gives flag --%s to both %q and %q", operation.ID, input.Flag.Name, owner, input.ID)
		}
		flags[input.Flag.Name] = input.ID
	}
	for _, example := range operation.Examples {
		if example.Command == "" {
			return fmt.Errorf("service contract operation %q has an example naming no command", operation.ID)
		}
	}
	return nil
}
