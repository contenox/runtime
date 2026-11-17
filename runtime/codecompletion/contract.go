package codecompletion

import "github.com/contenox/runtime/runtime/servicecontract"

// Contract describes editor predictions independently of the IDE and ACP adapters.
func Contract() servicecontract.Contract {
	return servicecontract.Contract{
		ID: "codecompletion", Summary: "Editor predictions", Description: "Generate insertions and reviewable edits using independently configured model operations.",
		Operations: []servicecontract.Operation{
			{ID: "autocomplete", Summary: "Complete code at the cursor", Description: "Use the selected native FIM or instructed route and supersede obsolete requests for this document.", Inputs: []servicecontract.Input{
				{ID: "prefix", Summary: "Exact source before the cursor", Type: "string", Required: true},
				{ID: "suffix", Summary: "Exact source after the cursor", Type: "string", Required: true},
			}},
			{ID: "next-edit", Summary: "Predict the next edit", Description: "Predict replacements within the current file. Review and accept the preview before applying it; accepted replacements are one undo step.", Inputs: []servicecontract.Input{
				{ID: "document", Summary: "Versioned current and previous document snapshots", Type: "object", Required: true},
				{ID: "region", Summary: "Editable source range and cursor", Type: "object", Required: true},
			}},
		},
	}
}
