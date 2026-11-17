package codecompletion

import (
	"errors"
	"strings"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelcontract"
)

func selectNextEditInput(request NextEditRequest) (modelcontract.NextEditInput, error) {
	profile, err := modelcontract.ResolveNextEdit(request.Format)
	if err != nil {
		return modelcontract.NextEditInput{}, err
	}
	if request.MaxTokens > profile.MaxTokens {
		return modelcontract.NextEditInput{}, errors.New("next-edit output limit exceeds its declared profile")
	}
	input := request.Input
	start := input.Cursor
	for line := 0; line <= 10 && start > input.Start; line++ {
		previous := strings.LastIndexByte(input.Current[input.Start:start], '\n')
		if previous < 0 {
			start = input.Start
			break
		}
		start = input.Start + previous
	}
	if start > input.Start && input.Current[start] == '\n' {
		start++
	}
	end := input.Cursor
	for line := 0; line <= 10 && end < input.End; line++ {
		next := strings.IndexByte(input.Current[end:input.End], '\n')
		if next < 0 {
			end = input.End
			break
		}
		end += next + 1
	}
	maxBytes := min(2048, profile.MaxTokens*2)
	for end-start > maxBytes {
		left := strings.IndexByte(input.Current[start:input.Cursor], '\n')
		right := strings.LastIndexByte(input.Current[input.Cursor:end], '\n')
		if right >= 0 && input.Cursor+right+1 == end {
			right = strings.LastIndexByte(input.Current[input.Cursor:end-1], '\n')
		}
		if left < 0 && right < 0 {
			return modelcontract.NextEditInput{}, errors.New("cursor line exceeds the next-edit source window; use Autocomplete for this line")
		}
		if left >= 0 && (right < 0 || input.Cursor-start >= end-input.Cursor) {
			start += left + 1
		} else {
			end = input.Cursor + right + 1
		}
	}
	input.Start, input.End = start, end
	input.History = append([]modelcontract.EditHistory{}, input.History[max(0, len(input.History)-4):]...)
	input.Context = append([]modelcontract.ContextFile{}, input.Context...)
	budget := profile.ContextTokens - request.MaxTokens - 512
	for {
		prepared, err := modelcontract.PrepareNextEdit(request.Format, input)
		if err != nil {
			return modelcontract.NextEditInput{}, err
		}
		if len(prepared.Render()) <= budget {
			return input, nil
		}
		if len(input.Context) > 0 {
			input.Context = input.Context[:len(input.Context)-1]
		} else if len(input.History) > 0 {
			input.History = input.History[1:]
		} else {
			return modelcontract.NextEditInput{}, errors.New("the current change exceeds the next-edit context budget; make a smaller change, or choose a model whose next-edit format has a larger window: " + remedy.NextEditRole)
		}
	}
}
