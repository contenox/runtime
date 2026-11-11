package taskengine

import (
	"context"
	"fmt"
	"strings"
)

type ToolImagesKept func(ctx context.Context) int

type ExecOption func(*SimpleExec)

func WithToolImagesKept(kept ToolImagesKept) ExecOption {
	return func(exe *SimpleExec) {
		exe.toolImagesKept = kept
	}
}

func (exe *SimpleExec) imagesKept(ctx context.Context) int {
	if exe.toolImagesKept == nil {
		return 0
	}
	return exe.toolImagesKept(ctx)
}

func keepNewestToolImages(messages []Message, keep int) []Message {
	if keep <= 0 {
		return messages
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	kept := 0
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		if m.Role != "tool" || len(m.Images) == 0 {
			continue
		}
		room := keep - kept
		if room >= len(m.Images) {
			kept += len(m.Images)
			continue
		}
		if room < 0 {
			room = 0
		}
		left := len(m.Images) - room
		m.Images = m.Images[len(m.Images)-room:]
		m.Content = strings.TrimSpace(fmt.Sprintf("%s\n(%d earlier image(s) from this result are no longer attached: only the newest %d tool images are sent again)", m.Content, left, keep))
		kept += room
		out[i] = m
	}
	return out
}
